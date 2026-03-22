#pragma once

#include "types.h"

#include <QJsonObject>
#include <QLocalSocket>
#include <QObject>
#include <QQueue>
#include <QTimer>
#include <QVector>

class DaemonClient : public QObject {
    Q_OBJECT

public:
    explicit DaemonClient(QObject *parent = nullptr);

    bool isConnected() const { return m_connected; }

    // API methods
    void fetchDownloads();
    void addDownload(const AddRequest &req);
    void deleteDownload(const QString &id, bool deleteFile);
    void pauseDownload(const QString &id);
    void resumeDownload(const QString &id);
    void retryDownload(const QString &id);
    void pauseAll();
    void resumeAll();
    void reorderDownloads(const QStringList &orderedIds);
    void probeUrl(const QString &url);
    void fetchConfig();
    void updateConfig(const QJsonObject &partial);
    void fetchStats();

signals:
    void connected();
    void disconnected();
    void downloadsFetched(QVector<Download> list);
    void downloadAdded(Download dl);
    void probeCompleted(ProbeResult result);
    void probeFailed(QString error);
    void configFetched(Config cfg);
    void configUpdated();
    void statsFetched(Stats stats);
    void requestFailed(QString endpoint, int statusCode, QString errorCode, QString errorMessage);

private slots:
    void onSocketConnected();
    void onSocketDisconnected();
    void onSocketError(QLocalSocket::LocalSocketError);
    void onReadyRead();
    void tryConnect();
    void poll();

private:
    struct PendingRequest {
        QByteArray method;
        QByteArray path;
        QByteArray body;
        QString tag;
    };

    static QString socketPath();
    void sendRequest(const QByteArray &method, const QByteArray &path,
                     const QByteArray &body, const QString &tag);
    void processQueue();
    void handleResponse(int statusCode, const QByteArray &body, const QString &tag);
    void resetParserState();
    void failAbandonedRequests();

    QLocalSocket *m_socket;
    QTimer *m_pollTimer;
    QTimer *m_reconnectTimer;

    bool m_connected = false;
    bool m_requestInFlight = false;
    bool m_pollInFlight = false;

    QQueue<PendingRequest> m_queue;
    PendingRequest m_currentRequest;

    void completeResponse();

    // HTTP response parser state
    QByteArray m_headerBuffer;
    QByteArray m_bodyBuffer;
    QByteArray m_chunkedBody;   // assembled decoded body for chunked responses
    int m_contentLength = -1;
    int m_responseStatusCode = 0;
    bool m_headersComplete = false;
    bool m_chunked = false;
};
