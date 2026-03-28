from __future__ import annotations

import http.client
import json
import os
import socket
from dataclasses import dataclass
from queue import Empty, Queue

from PySide6.QtCore import QObject, QThread, QTimer, Signal

from .types import Config, Download, ProbeResult, Segment, Stats


def _socket_path() -> str:
    runtime_dir = os.environ.get("XDG_RUNTIME_DIR", "")
    if not runtime_dir:
        runtime_dir = f"/tmp/bolt-{os.getuid()}"
    return os.path.join(runtime_dir, "bolt", "bolt.sock")


class _UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, socket_path: str):
        super().__init__("localhost")
        self._socket_path = socket_path

    def connect(self):
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.connect(self._socket_path)
        self.sock = sock


_SILENT_TAGS = frozenset(("poll", "fetchDownloads", "fetchStats", "fetchConfig"))


@dataclass
class _RequestItem:
    method: str
    path: str
    body: bytes
    tag: str


class _WorkerThread(QThread):
    response_ready = Signal(str, int, bytes)  # tag, status_code, body
    connection_changed = Signal(bool)  # True = connected, False = disconnected

    def __init__(self, socket_path: str, parent: QObject | None = None):
        super().__init__(parent)
        self._socket_path = socket_path
        self._queue: Queue[_RequestItem | None] = Queue()
        self._stop = False
        self._conn: _UnixHTTPConnection | None = None

    def enqueue(self, item: _RequestItem) -> None:
        self._queue.put(item)

    def request_stop(self) -> None:
        self._stop = True
        self._queue.put(None)  # unblock the get()

    def run(self) -> None:
        while not self._stop:
            try:
                item = self._queue.get(timeout=0.2)
            except Empty:
                continue
            if item is None:
                continue
            self._execute(item)

        if self._conn:
            try:
                self._conn.close()
            except OSError:
                pass

    def _ensure_connection(self) -> _UnixHTTPConnection:
        if self._conn is None:
            conn = _UnixHTTPConnection(self._socket_path)
            conn.connect()
            self._conn = conn
            self.connection_changed.emit(True)
        return self._conn

    def _close_connection(self) -> None:
        if self._conn:
            try:
                self._conn.close()
            except OSError:
                pass
            self._conn = None

    def _execute(self, item: _RequestItem) -> None:
        headers = {"Host": "localhost", "Connection": "keep-alive"}
        if item.body:
            headers["Content-Type"] = "application/json"
            headers["Content-Length"] = str(len(item.body))

        for attempt in range(2):
            try:
                conn = self._ensure_connection()
                conn.request(item.method, item.path, item.body or None, headers)
                resp = conn.getresponse()
                body = resp.read()
                self.response_ready.emit(item.tag, resp.status, body)
                return
            except (OSError, http.client.HTTPException):
                self._close_connection()
                if attempt == 0:
                    continue
                self.connection_changed.emit(False)
                # Fail the request
                self.response_ready.emit(item.tag, 0, b"")


class DaemonClient(QObject):
    connected = Signal()
    disconnected = Signal()
    downloads_fetched = Signal(list)
    download_added = Signal(object)
    probe_completed = Signal(object)
    probe_failed = Signal(str)
    config_fetched = Signal(object)
    config_updated = Signal()
    stats_fetched = Signal(object)
    download_detail_fetched = Signal(object, list)
    request_failed = Signal(str, int, str, str)  # endpoint, status, code, message

    def __init__(self, parent: QObject | None = None):
        super().__init__(parent)
        self._connected = False
        self._poll_in_flight = False
        self._socket_path = _socket_path()

        self._worker = _WorkerThread(self._socket_path)
        self._worker.response_ready.connect(self._handle_response)
        self._worker.connection_changed.connect(self._on_connection_changed)
        self._worker.start()

        self._poll_timer = QTimer(self)
        self._poll_timer.setInterval(1000)
        self._poll_timer.timeout.connect(self._poll)

        self._reconnect_timer = QTimer(self)
        self._reconnect_timer.setInterval(3000)
        self._reconnect_timer.setSingleShot(True)
        self._reconnect_timer.timeout.connect(self._try_connect)

        # Defer initial connection
        QTimer.singleShot(0, self._try_connect)

    def is_connected(self) -> bool:
        return self._connected

    def shutdown(self) -> None:
        self._poll_timer.stop()
        self._reconnect_timer.stop()
        self._worker.request_stop()
        self._worker.wait(3000)

    # --- Connection management ---

    def _try_connect(self) -> None:
        self._worker.enqueue(_RequestItem("GET", "/api/downloads", b"", "poll"))

    def _on_connection_changed(self, is_connected: bool) -> None:
        if is_connected and not self._connected:
            self._connected = True
            self._reconnect_timer.stop()
            self._poll_timer.start()
            self.connected.emit()
        elif not is_connected and self._connected:
            self._connected = False
            self._poll_in_flight = False
            self._poll_timer.stop()
            if not self._reconnect_timer.isActive():
                self._reconnect_timer.start()
            self.disconnected.emit()
        elif not is_connected and not self._connected:
            # Failed to connect
            if not self._reconnect_timer.isActive():
                self._reconnect_timer.start()
            self.disconnected.emit()

    # --- Response handling ---

    def _handle_response(self, tag: str, status_code: int, body: bytes) -> None:
        # Connection failure
        if status_code == 0:
            if tag == "probe":
                self.probe_failed.emit("Connection lost")
            elif tag not in _SILENT_TAGS:
                self.request_failed.emit(tag, 0, "CONNECTION_LOST", "Connection lost")
            if tag == "poll":
                self._poll_in_flight = False
            return

        obj: dict = {}
        if body:
            try:
                obj = json.loads(body)
            except (json.JSONDecodeError, ValueError):
                pass

        # Error responses
        if status_code >= 400:
            error_msg = obj.get("error", "")
            error_code = obj.get("code", "")
            if tag == "probe":
                self.probe_failed.emit(error_msg)
            else:
                self.request_failed.emit(tag, status_code, error_code, error_msg)
            if tag == "poll":
                self._poll_in_flight = False
            return

        # Route by tag
        if tag in ("poll", "fetchDownloads"):
            downloads = [Download.from_json(d) for d in obj.get("downloads") or []]
            if tag == "poll":
                self._poll_in_flight = False
            self.downloads_fetched.emit(downloads)
        elif tag == "addDownload":
            dl = Download.from_json(obj.get("download", {}))
            self.download_added.emit(dl)
            self.fetch_downloads()
        elif tag == "probe":
            result = ProbeResult.from_json(obj)
            self.probe_completed.emit(result)
        elif tag == "fetchConfig":
            cfg = Config.from_json(obj)
            self.config_fetched.emit(cfg)
        elif tag == "updateConfig":
            self.config_updated.emit()
        elif tag == "fetchStats":
            stats = Stats.from_json(obj)
            self.stats_fetched.emit(stats)
        elif tag == "fetchDetail":
            dl = Download.from_json(obj.get("download", {}))
            segments = [Segment.from_json(s) for s in obj.get("segments") or []]
            self.download_detail_fetched.emit(dl, segments)
        elif tag in ("pause", "resume", "retry", "delete", "pauseAll", "resumeAll", "reorder"):
            if not self._poll_in_flight:
                self.fetch_downloads()

    # --- Polling ---

    def _poll(self) -> None:
        if self._poll_in_flight:
            return
        self._poll_in_flight = True
        self._worker.enqueue(_RequestItem("GET", "/api/downloads", b"", "poll"))

    # --- Public API ---

    def fetch_downloads(self) -> None:
        self._worker.enqueue(_RequestItem("GET", "/api/downloads", b"", "fetchDownloads"))

    def add_download(self, req) -> None:
        body = json.dumps(req.to_json()).encode()
        self._worker.enqueue(_RequestItem("POST", "/api/downloads", body, "addDownload"))

    def delete_download(self, dl_id: str, delete_file: bool) -> None:
        path = f"/api/downloads/{dl_id}"
        if delete_file:
            path += "?delete_file=true"
        self._worker.enqueue(_RequestItem("DELETE", path, b"", "delete"))

    def pause_download(self, dl_id: str) -> None:
        self._worker.enqueue(_RequestItem("POST", f"/api/downloads/{dl_id}/pause", b"", "pause"))

    def resume_download(self, dl_id: str) -> None:
        self._worker.enqueue(_RequestItem("POST", f"/api/downloads/{dl_id}/resume", b"", "resume"))

    def retry_download(self, dl_id: str) -> None:
        self._worker.enqueue(_RequestItem("POST", f"/api/downloads/{dl_id}/retry", b"", "retry"))

    def pause_all(self) -> None:
        self._worker.enqueue(_RequestItem("POST", "/api/downloads/pause-all", b"", "pauseAll"))

    def resume_all(self) -> None:
        self._worker.enqueue(_RequestItem("POST", "/api/downloads/resume-all", b"", "resumeAll"))

    def reorder_downloads(self, ordered_ids: list[str]) -> None:
        body = json.dumps({"ordered_ids": ordered_ids}).encode()
        self._worker.enqueue(_RequestItem("PUT", "/api/downloads/reorder", body, "reorder"))

    def probe_url(self, url: str) -> None:
        body = json.dumps({"url": url}).encode()
        self._worker.enqueue(_RequestItem("POST", "/api/probe", body, "probe"))

    def fetch_config(self) -> None:
        self._worker.enqueue(_RequestItem("GET", "/api/config", b"", "fetchConfig"))

    def update_config(self, partial: dict) -> None:
        body = json.dumps(partial).encode()
        self._worker.enqueue(_RequestItem("PUT", "/api/config", body, "updateConfig"))

    def fetch_stats(self) -> None:
        self._worker.enqueue(_RequestItem("GET", "/api/stats", b"", "fetchStats"))

    def fetch_download_detail(self, dl_id: str) -> None:
        self._worker.enqueue(_RequestItem("GET", f"/api/downloads/{dl_id}", b"", "fetchDetail"))
