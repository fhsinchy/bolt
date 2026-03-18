#include "daemonclient.h"

#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QStandardPaths>
#include <unistd.h>

DaemonClient::DaemonClient(QObject *parent)
    : QObject(parent)
    , m_socket(new QLocalSocket(this))
    , m_pollTimer(new QTimer(this))
    , m_reconnectTimer(new QTimer(this))
{
    m_pollTimer->setInterval(1000);
    m_reconnectTimer->setInterval(3000);
    m_reconnectTimer->setSingleShot(true);

    connect(m_socket, &QLocalSocket::connected, this, &DaemonClient::onSocketConnected);
    connect(m_socket, &QLocalSocket::disconnected, this, &DaemonClient::onSocketDisconnected);
    connect(m_socket, &QLocalSocket::errorOccurred, this, &DaemonClient::onSocketError);
    connect(m_socket, &QLocalSocket::readyRead, this, &DaemonClient::onReadyRead);
    connect(m_pollTimer, &QTimer::timeout, this, &DaemonClient::poll);
    connect(m_reconnectTimer, &QTimer::timeout, this, &DaemonClient::tryConnect);

    // Defer initial connection to the event loop so consumers can
    // connect to our signals before we potentially emit connected().
    QTimer::singleShot(0, this, &DaemonClient::tryConnect);
}

QString DaemonClient::socketPath() {
    QString runtimeDir = QStandardPaths::writableLocation(QStandardPaths::RuntimeLocation);
    if (!runtimeDir.isEmpty())
        return runtimeDir + "/bolt/bolt.sock";
    return QString("/tmp/bolt-%1/bolt.sock").arg(getuid());
}

void DaemonClient::tryConnect() {
    if (m_socket->state() != QLocalSocket::UnconnectedState)
        return;
    QString path = socketPath();
    // Ensure we connect to the filesystem socket, not abstract namespace
    m_socket->setSocketOptions(QLocalSocket::NoOptions);
    m_socket->setServerName(path);
    m_socket->connectToServer(QIODevice::ReadWrite);
}

void DaemonClient::onSocketConnected() {
    m_connected = true;
    m_reconnectTimer->stop();
    m_pollTimer->start();
    resetParserState();
    emit connected();
    fetchDownloads();
}

void DaemonClient::onSocketDisconnected() {
    m_connected = false;
    m_pollTimer->stop();
    m_requestInFlight = false;
    m_pollInFlight = false;
    m_queue.clear();
    resetParserState();
    m_reconnectTimer->start();
    emit disconnected();
}

void DaemonClient::onSocketError(QLocalSocket::LocalSocketError) {
    if (!m_connected && !m_reconnectTimer->isActive())
        m_reconnectTimer->start();
}

void DaemonClient::resetParserState() {
    m_headerBuffer.clear();
    m_bodyBuffer.clear();
    m_contentLength = -1;
    m_responseStatusCode = 0;
    m_headersComplete = false;
}

void DaemonClient::sendRequest(const QByteArray &method, const QByteArray &path,
                                const QByteArray &body, const QString &tag) {
    PendingRequest req;
    req.method = method;
    req.path = path;
    req.body = body;
    req.tag = tag;
    m_queue.enqueue(req);
    processQueue();
}

void DaemonClient::processQueue() {
    if (m_requestInFlight || m_queue.isEmpty() || !m_connected)
        return;

    m_currentRequest = m_queue.dequeue();
    m_requestInFlight = true;
    resetParserState();

    QByteArray request;
    request.append(m_currentRequest.method);
    request.append(" ");
    request.append(m_currentRequest.path);
    request.append(" HTTP/1.1\r\n");
    request.append("Host: localhost\r\n");

    if (!m_currentRequest.body.isEmpty()) {
        request.append("Content-Type: application/json\r\n");
        request.append("Content-Length: ");
        request.append(QByteArray::number(m_currentRequest.body.size()));
        request.append("\r\n");
    }

    request.append("\r\n");
    request.append(m_currentRequest.body);

    m_socket->write(request);
    m_socket->flush();
}

void DaemonClient::onReadyRead() {
    while (m_socket->bytesAvailable() > 0) {
        if (!m_headersComplete) {
            m_headerBuffer.append(m_socket->readAll());
            int headerEnd = m_headerBuffer.indexOf("\r\n\r\n");
            if (headerEnd < 0)
                return;

            // Parse status line
            int firstLineEnd = m_headerBuffer.indexOf("\r\n");
            QByteArray statusLine = m_headerBuffer.left(firstLineEnd);
            // "HTTP/1.1 200 OK"
            int spaceIdx = statusLine.indexOf(' ');
            if (spaceIdx >= 0) {
                int secondSpace = statusLine.indexOf(' ', spaceIdx + 1);
                QByteArray codeStr = (secondSpace >= 0)
                    ? statusLine.mid(spaceIdx + 1, secondSpace - spaceIdx - 1)
                    : statusLine.mid(spaceIdx + 1);
                m_responseStatusCode = codeStr.toInt();
            }

            // Parse Content-Length
            QByteArray headers = m_headerBuffer.left(headerEnd);
            m_contentLength = 0;
            for (const QByteArray &line : headers.split('\n')) {
                QByteArray trimmed = line.trimmed();
                if (trimmed.toLower().startsWith("content-length:")) {
                    m_contentLength = trimmed.mid(15).trimmed().toInt();
                    break;
                }
            }

            // Move remaining data to body buffer
            m_bodyBuffer = m_headerBuffer.mid(headerEnd + 4);
            m_headerBuffer.clear();
            m_headersComplete = true;
        } else {
            m_bodyBuffer.append(m_socket->readAll());
        }

        if (m_headersComplete && m_bodyBuffer.size() >= m_contentLength) {
            QByteArray body = m_bodyBuffer.left(m_contentLength);
            QByteArray remaining = m_bodyBuffer.mid(m_contentLength);

            QString tag = m_currentRequest.tag;
            int statusCode = m_responseStatusCode;

            m_requestInFlight = false;
            resetParserState();

            // If there's leftover data, keep it for next response
            if (!remaining.isEmpty())
                m_headerBuffer = remaining;

            handleResponse(statusCode, body, tag);
            processQueue();
        }
    }
}

void DaemonClient::handleResponse(int statusCode, const QByteArray &body, const QString &tag) {
    QJsonDocument doc = QJsonDocument::fromJson(body);
    QJsonObject obj = doc.object();

    // Handle error responses
    if (statusCode >= 400) {
        QString errorMsg = obj["error"].toString();
        QString errorCode = obj["code"].toString();

        // Special case: probe failure
        if (tag == "probe") {
            emit probeFailed(errorMsg);
            return;
        }

        // Special case: add download duplicate
        if (tag == "addDownload" && errorCode == "DUPLICATE_FILENAME") {
            emit requestFailed(tag, statusCode, errorCode, errorMsg);
            return;
        }

        emit requestFailed(tag, statusCode, errorCode, errorMsg);

        // Clear poll flag if this was a poll request
        if (tag == "poll")
            m_pollInFlight = false;
        return;
    }

    // Route by tag
    if (tag == "poll" || tag == "fetchDownloads") {
        QJsonArray arr = obj["downloads"].toArray();
        QVector<Download> downloads;
        downloads.reserve(arr.size());
        for (const QJsonValue &v : arr)
            downloads.append(Download::fromJson(v.toObject()));
        if (tag == "poll")
            m_pollInFlight = false;
        emit downloadsFetched(downloads);
    } else if (tag == "addDownload") {
        Download dl = Download::fromJson(obj["download"].toObject());
        emit downloadAdded(dl);
        fetchDownloads();
    } else if (tag == "probe") {
        ProbeResult result = ProbeResult::fromJson(obj);
        emit probeCompleted(result);
    } else if (tag == "fetchConfig") {
        Config cfg = Config::fromJson(obj);
        emit configFetched(cfg);
    } else if (tag == "updateConfig") {
        emit configUpdated();
    } else if (tag == "fetchStats") {
        Stats stats = Stats::fromJson(obj);
        emit statsFetched(stats);
    } else if (tag == "pause" || tag == "resume" || tag == "retry" || tag == "delete") {
        // Action succeeded — refresh download list
        if (!m_pollInFlight)
            fetchDownloads();
    }
}

void DaemonClient::poll() {
    if (m_pollInFlight)
        return;
    m_pollInFlight = true;
    sendRequest("GET", "/api/downloads", {}, "poll");
}

// --- Public API methods ---

void DaemonClient::fetchDownloads() {
    sendRequest("GET", "/api/downloads", {}, "fetchDownloads");
}

void DaemonClient::addDownload(const AddRequest &req) {
    QJsonDocument doc(req.toJson());
    sendRequest("POST", "/api/downloads", doc.toJson(QJsonDocument::Compact), "addDownload");
}

void DaemonClient::deleteDownload(const QString &id, bool deleteFile) {
    QByteArray path = "/api/downloads/" + id.toUtf8();
    if (deleteFile)
        path.append("?delete_file=true");
    sendRequest("DELETE", path, {}, "delete");
}

void DaemonClient::pauseDownload(const QString &id) {
    sendRequest("POST", "/api/downloads/" + id.toUtf8() + "/pause", {}, "pause");
}

void DaemonClient::resumeDownload(const QString &id) {
    sendRequest("POST", "/api/downloads/" + id.toUtf8() + "/resume", {}, "resume");
}

void DaemonClient::retryDownload(const QString &id) {
    sendRequest("POST", "/api/downloads/" + id.toUtf8() + "/retry", {}, "retry");
}

void DaemonClient::probeUrl(const QString &url) {
    QJsonObject obj;
    obj["url"] = url;
    QJsonDocument doc(obj);
    sendRequest("POST", "/api/probe", doc.toJson(QJsonDocument::Compact), "probe");
}

void DaemonClient::fetchConfig() {
    sendRequest("GET", "/api/config", {}, "fetchConfig");
}

void DaemonClient::updateConfig(const QJsonObject &partial) {
    QJsonDocument doc(partial);
    sendRequest("PUT", "/api/config", doc.toJson(QJsonDocument::Compact), "updateConfig");
}

void DaemonClient::fetchStats() {
    sendRequest("GET", "/api/stats", {}, "fetchStats");
}
