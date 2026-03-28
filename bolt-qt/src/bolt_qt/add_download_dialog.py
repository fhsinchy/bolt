from __future__ import annotations

from PySide6.QtWidgets import (
    QDialog,
    QFileDialog,
    QFormLayout,
    QGroupBox,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QMessageBox,
    QPushButton,
    QSpinBox,
    QVBoxLayout,
)
from PySide6.QtGui import QGuiApplication

from .daemon_client import DaemonClient
from .types import AddRequest, format_bytes


class AddDownloadDialog(QDialog):
    def __init__(self, client: DaemonClient, parent=None):
        super().__init__(parent)
        self._client = client
        self._force = False
        self._add_paused = False

        self.setWindowTitle("Add Download")
        self.setMinimumWidth(500)

        main_layout = QVBoxLayout(self)

        # URL row
        url_layout = QHBoxLayout()
        self._url_edit = QLineEdit()
        self._url_edit.setPlaceholderText("Enter URL...")
        self._probe_button = QPushButton("Get Info")
        url_layout.addWidget(self._url_edit, 1)
        url_layout.addWidget(self._probe_button)

        url_form = QFormLayout()
        url_form.addRow("URL:", url_layout)
        main_layout.addLayout(url_form)

        # Probe results group
        probe_group = QGroupBox("File Info")
        probe_layout = QFormLayout(probe_group)
        self._filename_edit = QLineEdit()
        self._size_label = QLabel()
        self._resumable_label = QLabel()
        probe_layout.addRow("Filename:", self._filename_edit)
        probe_layout.addRow("Size:", self._size_label)
        probe_layout.addRow("Resumable:", self._resumable_label)
        main_layout.addWidget(probe_group)

        # Options group
        options_group = QGroupBox("Options")
        options_layout = QFormLayout(options_group)
        dir_layout = QHBoxLayout()
        self._dir_edit = QLineEdit()
        browse_button = QPushButton("Browse")
        dir_layout.addWidget(self._dir_edit, 1)
        dir_layout.addWidget(browse_button)
        self._segments_spin = QSpinBox()
        self._segments_spin.setRange(1, 32)
        self._segments_spin.setValue(16)
        options_layout.addRow("Save to:", dir_layout)
        options_layout.addRow("Segments:", self._segments_spin)
        main_layout.addWidget(options_group)

        # Error label
        self._error_label = QLabel()
        self._error_label.setStyleSheet("color: red;")
        self._error_label.setWordWrap(True)
        self._error_label.hide()
        main_layout.addWidget(self._error_label)

        # Buttons
        button_layout = QHBoxLayout()
        button_layout.addStretch()
        self._cancel_button = QPushButton("Cancel")
        self._download_paused_button = QPushButton("Download Paused")
        self._download_button = QPushButton("Download")
        self._download_button.setDefault(True)
        button_layout.addWidget(self._cancel_button)
        button_layout.addWidget(self._download_paused_button)
        button_layout.addWidget(self._download_button)
        main_layout.addLayout(button_layout)

        # Connections
        self._probe_button.clicked.connect(self._on_probe)
        self._url_edit.returnPressed.connect(self._on_probe)
        self._download_button.clicked.connect(lambda: self._on_download(paused=False))
        self._download_paused_button.clicked.connect(lambda: self._on_download(paused=True))
        self._cancel_button.clicked.connect(self.reject)
        browse_button.clicked.connect(self._on_browse)

        self._client.probe_completed.connect(self._on_probe_completed)
        self._client.probe_failed.connect(self._on_probe_failed)
        self._client.download_added.connect(self._on_download_added)
        self._client.request_failed.connect(self._on_request_failed)
        self._client.config_fetched.connect(self._on_config_fetched)
        self._client.disconnected.connect(self._on_disconnected)

        # Reset force flag when URL changes
        self._url_edit.textChanged.connect(lambda: setattr(self, "_force", False))

        # Check clipboard for URL
        clip_text = (QGuiApplication.clipboard().text() or "").strip()
        if clip_text.startswith("http://") or clip_text.startswith("https://"):
            self._url_edit.setText(clip_text)

        # Fetch config for defaults
        self._client.fetch_config()

    def _on_browse(self) -> None:
        d = QFileDialog.getExistingDirectory(self, "Select Directory", self._dir_edit.text())
        if d:
            self._dir_edit.setText(d)

    def _on_probe(self) -> None:
        url = self._url_edit.text().strip()
        if not url:
            return
        if not self._client.is_connected():
            self._error_label.setText("Not connected to daemon")
            self._error_label.show()
            return
        self._error_label.hide()
        self._probe_button.setEnabled(False)
        self._probe_button.setText("Fetching...")
        self._client.probe_url(url)

    def _on_probe_completed(self, result) -> None:
        self._probe_button.setEnabled(True)
        self._probe_button.setText("Get Info")
        self._filename_edit.setText(result.filename)
        self._size_label.setText(format_bytes(result.total_size))
        self._resumable_label.setText("Yes" if result.accepts_ranges else "No")
        self._error_label.hide()

    def _on_probe_failed(self, error: str) -> None:
        self._probe_button.setEnabled(True)
        self._probe_button.setText("Get Info")
        self._error_label.setText(error)
        self._error_label.show()

    def _on_download(self, paused: bool = False) -> None:
        self._add_paused = paused
        url = self._url_edit.text().strip()
        if not url:
            return
        if not self._client.is_connected():
            self._error_label.setText("Not connected to daemon")
            self._error_label.show()
            return

        req = AddRequest(
            url=url,
            filename=self._filename_edit.text().strip(),
            dir=self._dir_edit.text().strip(),
            segments=self._segments_spin.value(),
            force=self._force,
            paused=paused,
        )

        self._error_label.hide()
        self._download_button.setEnabled(False)
        self._download_paused_button.setEnabled(False)
        self._client.add_download(req)

    def _on_download_added(self, _dl) -> None:
        self.accept()

    def _on_request_failed(self, endpoint: str, _status: int, error_code: str, error_message: str) -> None:
        if endpoint != "addDownload":
            return

        self._download_button.setEnabled(True)
        self._download_paused_button.setEnabled(True)

        if error_code == "DUPLICATE_FILENAME":
            reply = QMessageBox.question(
                self,
                "File Exists",
                "File already exists. Download anyway?",
                QMessageBox.Yes | QMessageBox.No,
            )
            if reply == QMessageBox.Yes:
                self._force = True
                self._on_download(paused=self._add_paused)
            return

        self._error_label.setText(error_message)
        self._error_label.show()

    def _on_config_fetched(self, cfg) -> None:
        self._dir_edit.setText(cfg.download_dir)
        self._segments_spin.setValue(cfg.default_segments)

    def _on_disconnected(self) -> None:
        self._probe_button.setEnabled(True)
        self._probe_button.setText("Get Info")
        self._download_button.setEnabled(True)
        self._download_paused_button.setEnabled(True)
        self._error_label.setText("Connection lost")
        self._error_label.show()
