#include "daemonclient.h"
#include "mainwindow.h"

#include <QApplication>

int main(int argc, char *argv[]) {
    QApplication app(argc, argv);
    app.setApplicationName("bolt-qt");
    app.setOrganizationName("fhsinchy");
    app.setApplicationDisplayName("Bolt Download Manager");
    app.setQuitOnLastWindowClosed(false);

    DaemonClient client;
    MainWindow window(&client);
    window.show();

    return app.exec();
}
