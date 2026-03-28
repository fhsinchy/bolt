from __future__ import annotations

import sys

from PySide6.QtWidgets import QApplication
from PySide6.QtNetwork import QLocalServer, QLocalSocket

from .daemon_client import DaemonClient
from .main_window import MainWindow

_SOCKET_NAME = "bolt-qt-single-instance"


def _signal_existing_instance() -> bool:
    sock = QLocalSocket()
    sock.connectToServer(_SOCKET_NAME)
    if sock.waitForConnected(500):
        sock.write(b"raise")
        sock.waitForBytesWritten(500)
        sock.disconnectFromServer()
        return True
    return False


def main():
    app = QApplication(sys.argv)
    app.setApplicationName("bolt-qt")
    app.setOrganizationName("fhsinchy")
    app.setApplicationDisplayName("Bolt Download Manager")
    app.setQuitOnLastWindowClosed(False)

    if _signal_existing_instance():
        return 0

    QLocalServer.removeServer(_SOCKET_NAME)

    client = DaemonClient()
    window = MainWindow(client)
    window.show()

    server = QLocalServer()
    server.listen(_SOCKET_NAME)

    def _handle_new_connection():
        conn = server.nextPendingConnection()
        if conn:
            conn.readyRead.connect(lambda: _raise_window(conn, window))

    def _raise_window(conn, win):
        conn.readAll()
        win.show()
        win.raise_()
        win.activateWindow()
        conn.deleteLater()

    server.newConnection.connect(_handle_new_connection)

    ret = app.exec()
    client.shutdown()
    return ret


if __name__ == "__main__":
    sys.exit(main())
