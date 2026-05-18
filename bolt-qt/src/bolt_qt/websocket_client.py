from __future__ import annotations

import json
import logging
import time

from PySide6.QtCore import QObject, QThread, Signal

from websockets.exceptions import ConnectionClosed
from websockets.sync.client import unix_connect

logger = logging.getLogger(__name__)


class WebSocketWorker(QThread):
    """QThread that maintains a WebSocket connection to the Bolt daemon.

    Connects over a Unix socket, receives JSON event messages, and emits
    them as Qt signals to the main thread. Handles reconnection with
    exponential backoff.
    """

    connected = Signal()
    disconnected = Signal()
    event_received = Signal(dict)

    def __init__(self, socket_path: str, parent: QObject | None = None):
        super().__init__(parent)
        self._socket_path = socket_path
        self._stop = False

    def stop(self) -> None:
        """Signal the worker to stop and disconnect."""
        self._stop = True

    def run(self) -> None:
        backoff = 1.0
        max_backoff = 30.0

        while not self._stop:
            try:
                with unix_connect(
                    path=self._socket_path,
                    uri="ws://localhost/ws",
                    open_timeout=5,
                    ping_interval=20,
                    ping_timeout=10,
                    close_timeout=5,
                ) as ws:
                    self.connected.emit()
                    backoff = 1.0  # reset on successful connect

                    for message in ws:
                        if self._stop:
                            return
                        try:
                            event = json.loads(message)
                        except (json.JSONDecodeError, TypeError):
                            logger.warning("websocket: invalid JSON message")
                            continue
                        self.event_received.emit(event)

                # Normal closure (context manager exited cleanly)
                if not self._stop:
                    self.disconnected.emit()

            except ConnectionClosed as e:
                if not self._stop:
                    logger.debug("websocket connection closed: %s", e)
                    self.disconnected.emit()
            except OSError as e:
                if not self._stop:
                    logger.debug("websocket connection error: %s", e)
                    self.disconnected.emit()
            except Exception as e:
                if not self._stop:
                    logger.warning("websocket unexpected error: %s", e)
                    self.disconnected.emit()

            if self._stop:
                return

            time.sleep(backoff)
            backoff = min(backoff * 2, max_backoff)
