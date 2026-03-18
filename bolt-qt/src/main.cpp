#include <QApplication>

int main(int argc, char *argv[]) {
    QApplication app(argc, argv);
    app.setApplicationName("bolt-qt");
    app.setOrganizationName("fhsinchy");
    app.setApplicationDisplayName("Bolt Download Manager");

    return app.exec();
}
