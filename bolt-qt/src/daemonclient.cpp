#include "daemonclient.h"

#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
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
    // Match the daemon's Go logic exactly: os.Getenv("XDG_RUNTIME_DIR")
    // with fallback to /tmp/bolt-<uid>
    QString runtimeDir = qEnvironmentVariable("XDG_RUNTIME_DIR");
    if (runtimeDir.isEmpty())
        runtimeDir = QString("/tmp/bolt-%1").arg(getuid());
    return runtimeDir + "/bolt/bolt.sock";
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
    failAbandonedRequests();
    resetParserState();
    m_reconnectTimer->start();
    emit disconnected();
}

void DaemonClient::onSocketError(QLocalSocket::LocalSocketError) {
    if (!m_connected) {
        if (!m_reconnectTimer->isActive())
            m_reconnectTimer->start();
        // Let the UI know we failed to connect (shows "Disconnected" instead of "Connecting...")
        emit disconnected();
    }
}

void DaemonClient::failAbandonedRequests() {
    // Fail the in-flight request
    if (m_requestInFlight) {
        QString tag = m_currentRequest.tag;
        m_requestInFlight = false;
        if (tag == "probe")
            emit probeFailed("Connection lost");
        else if (tag != "poll" && tag != "fetchDownloads" && tag != "fetchStats")
            emit requestFailed(tag, 0, "CONNECTION_LOST", "Connection lost");
    }
    // Fail all queued requests
    while (!m_queue.isEmpty()) {
        PendingRequest req = m_queue.dequeue();
        if (req.tag == "probe")
            emit probeFailed("Connection lost");
        else if (req.tag != "poll" && req.tag != "fetchDownloads"
                 && req.tag != "fetchStats" && req.tag != "fetchConfig")
            emit requestFailed(req.tag, 0, "CONNECTION_LOST", "Connection lost");
    }
    m_pollInFlight = false;
}

void DaemonClient::resetParserState() {
    m_headerBuffer.clear();
    m_bodyBuffer.clear();
    m_chunkedBody.clear();
    m_contentLength = -1;
    m_responseStatusCode = 0;
    m_headersComplete = false;
    m_chunked = false;
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
    request.append("Connection: keep-alive\r\n");

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
    while (m_socket->bytesAvailable() > 0 || (m_headersComplete && !m_bodyBuffer.isEmpty())) {
        if (!m_headersComplete) {
            m_headerBuffer.append(m_socket->readAll());
            int headerEnd = m_headerBuffer.indexOf("\r\n\r\n");
            if (headerEnd < 0)
                return;

            // Parse status line: "HTTP/1.1 200 OK"
            int firstLineEnd = m_headerBuffer.indexOf("\r\n");
            QByteArray statusLine = m_headerBuffer.left(firstLineEnd);
            int spaceIdx = statusLine.indexOf(' ');
            if (spaceIdx >= 0) {
                int secondSpace = statusLine.indexOf(' ', spaceIdx + 1);
                QByteArray codeStr = (secondSpace >= 0)
                    ? statusLine.mid(spaceIdx + 1, secondSpace - spaceIdx - 1)
                    : statusLine.mid(spaceIdx + 1);
                m_responseStatusCode = codeStr.toInt();
            }

            // Parse headers
            QByteArray headers = m_headerBuffer.left(headerEnd);
            m_contentLength = -1;
            m_chunked = false;
            for (const QByteArray &line : headers.split('\n')) {
                QByteArray lower = line.trimmed().toLower();
                if (lower.startsWith("content-length:"))
                    m_contentLength = lower.mid(15).trimmed().toInt();
                else if (lower.startsWith("transfer-encoding:") && lower.contains("chunked"))
                    m_chunked = true;
            }

            // Default content-length for non-chunked, non-204 responses
            if (!m_chunked && m_contentLength < 0)
                m_contentLength = (m_responseStatusCode == 204) ? 0 : 0;

            // Move remaining data to body buffer
            m_bodyBuffer = m_headerBuffer.mid(headerEnd + 4);
            m_headerBuffer.clear();
            m_headersComplete = true;
        } else if (m_socket->bytesAvailable() > 0) {
            m_bodyBuffer.append(m_socket->readAll());
        }

        if (!m_headersComplete)
            return;

        if (m_chunked) {
            // Parse chunked transfer encoding
            // Format: <hex-size>\r\n<data>\r\n ... 0\r\n\r\n
            while (true) {
                int lineEnd = m_bodyBuffer.indexOf("\r\n");
                if (lineEnd < 0)
                    return; // need more data for chunk size line

                bool ok;
                int chunkSize = m_bodyBuffer.left(lineEnd).trimmed().toInt(&ok, 16);
                if (!ok) {
                    // Malformed chunk — disconnect to resync
                    m_requestInFlight = false;
                    resetParserState();
                    m_socket->disconnectFromServer();
                    return;
                }

                if (chunkSize == 0) {
                    // Terminal chunk — need trailing \r\n
                    if (m_bodyBuffer.size() < lineEnd + 4)
                        return; // need more data
                    QByteArray remaining = m_bodyBuffer.mid(lineEnd + 4);
                    completeResponse();
                    if (!remaining.isEmpty())
                        m_headerBuffer = remaining;
                    processQueue();
                    break;
                }

                // Need: size line + \r\n + chunkSize bytes + \r\n
                int dataStart = lineEnd + 2;
                int dataEnd = dataStart + chunkSize + 2;
                if (m_bodyBuffer.size() < dataEnd)
                    return; // need more data

                m_chunkedBody.append(m_bodyBuffer.mid(dataStart, chunkSize));
                m_bodyBuffer = m_bodyBuffer.mid(dataEnd);
            }
        } else {
            // Content-Length mode
            if (m_bodyBuffer.size() < m_contentLength)
                return; // need more data

            QByteArray remaining = m_bodyBuffer.mid(m_contentLength);
            m_chunkedBody = m_bodyBuffer.left(m_contentLength);
            completeResponse();
            if (!remaining.isEmpty())
                m_headerBuffer = remaining;
            processQueue();
        }
    }
}

void DaemonClient::completeResponse() {
    QByteArray body = m_chunkedBody;
    QString tag = m_currentRequest.tag;
    int statusCode = m_responseStatusCode;

    m_requestInFlight = false;
    resetParserState();

    handleResponse(statusCode, body, tag);
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
