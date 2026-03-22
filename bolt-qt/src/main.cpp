#include "daemonclient.h"
#include "mainwindow.h"

#include <QApplication>
#include <QDir>
#include <QLockFile>
#include <QStandardPaths>

#include <cstdio>

int main(int argc, char *argv[]) {
    QApplication app(argc, argv);
    app.setApplicationName("bolt-qt");
    app.setOrganizationName("fhsinchy");
    app.setApplicationDisplayName("Bolt Download Manager");
    app.setQuitOnLastWindowClosed(false);

    // Single instance check
    QString runtimeDir = QStandardPaths::writableLocation(QStandardPaths::RuntimeLocation);
    if (runtimeDir.isEmpty())
        runtimeDir = QDir::tempPath();
    QString lockPath = runtimeDir + "/bolt/bolt-qt.lock";
    QDir().mkpath(runtimeDir + "/bolt");

    QLockFile lockFile(lockPath);
    if (!lockFile.tryLock(100)) {
        std::fputs("bolt-qt is already running.\n", stderr);
        return 1;
    }

    DaemonClient client;
    MainWindow window(&client);
    window.show();

    return app.exec();
}
