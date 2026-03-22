#include "daemonclient.h"
#include "mainwindow.h"

#include <QApplication>
#include <QLocalServer>
#include <QLocalSocket>

static const char *kSocketName = "bolt-qt-single-instance";

// Try to signal the existing instance to raise its window.
// Returns true if another instance is running.
static bool signalExistingInstance() {
    QLocalSocket socket;
    socket.connectToServer(kSocketName);
    if (socket.waitForConnected(500)) {
        socket.write("raise");
        socket.waitForBytesWritten(500);
        socket.disconnectFromServer();
        return true;
    }
    return false;
}

int main(int argc, char *argv[]) {
    QApplication app(argc, argv);
    app.setApplicationName("bolt-qt");
    app.setOrganizationName("fhsinchy");
    app.setApplicationDisplayName("Bolt Download Manager");
    app.setQuitOnLastWindowClosed(false);

    // Single instance check — if another instance is running, raise it and exit
    if (signalExistingInstance())
        return 0;

    // Clean up stale socket from a previous crash
    QLocalServer::removeServer(kSocketName);

    DaemonClient client;
    MainWindow window(&client);
    window.show();

    // Listen for raise requests from new instances
    QLocalServer server;
    server.listen(kSocketName);
    QObject::connect(&server, &QLocalServer::newConnection, [&window, &server]() {
        auto *conn = server.nextPendingConnection();
        QObject::connect(conn, &QLocalSocket::readyRead, [&window, conn]() {
            conn->readAll(); // consume the "raise" message
            window.show();
            window.raise();
            window.activateWindow();
            conn->deleteLater();
        });
    });

    return app.exec();
}
