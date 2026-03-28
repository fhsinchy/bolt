from __future__ import annotations

import os

from PySide6.QtCore import Qt, QTimer, QUrl
from PySide6.QtGui import QColor, QDesktopServices, QPainter
from PySide6.QtWidgets import (
    QAbstractItemView,
    QDialog,
    QFormLayout,
    QGroupBox,
    QHBoxLayout,
    QLabel,
    QLayout,
    QProgressBar,
    QPushButton,
    QTableWidget,
    QTableWidgetItem,
    QVBoxLayout,
    QWidget,
)

from .daemon_client import DaemonClient
from .types import Segment, format_bytes, format_eta, format_speed, status_display_text


class SegmentProgressWidget(QWidget):
    def __init__(self, parent=None):
        super().__init__(parent)
        self.setMinimumHeight(20)
        self.setMaximumHeight(20)
        self._segments: list[Segment] = []
        self._total_size: int = 0

    def set_segments(self, segments: list[Segment], total_size: int) -> None:
        self._segments = segments
        self._total_size = total_size
        self.update()

    def paintEvent(self, _event):
        p = QPainter(self)
        p.setRenderHint(QPainter.Antialiasing)

        r = self.rect()
        p.fillRect(r, QColor(40, 40, 40))

        if not self._segments or self._total_size <= 0:
            return

        for seg in self._segments:
            if seg.end_byte < 0:
                continue
            seg_size = seg.end_byte - seg.start_byte + 1
            if seg_size <= 0:
                continue

            start_frac = seg.start_byte / self._total_size
            dl_frac = seg.downloaded / self._total_size

            x = int(start_frac * r.width())
            w = int(dl_frac * r.width())
            if w < 1 and seg.downloaded > 0:
                w = 1

            color = QColor(80, 180, 80) if seg.done else QColor(70, 130, 210)
            p.fillRect(x, 0, w, r.height(), color)


class DownloadDetailDialog(QDialog):
    def __init__(self, download_id: str, client: DaemonClient, parent=None):
        super().__init__(parent)
        self._client = client
        self._download_id = download_id
        self._file_path = ""
        self._dir_path = ""
        self._prev_downloaded: int = -1
        self._speed: float = 0.0

        self.setWindowTitle("Download Details")
        self.setMinimumWidth(550)

        main_layout = QVBoxLayout(self)
        main_layout.setSizeConstraint(QLayout.SetFixedSize)

        # Info section
        info_group = QGroupBox("Information")
        info_layout = QFormLayout(info_group)
        info_layout.setLabelAlignment(Qt.AlignLeft)
        info_layout.setFormAlignment(Qt.AlignLeft | Qt.AlignTop)

        self._url_label = QLabel()
        self._url_label.setWordWrap(True)
        self._url_label.setTextFormat(Qt.PlainText)
        self._url_label.setTextInteractionFlags(Qt.TextSelectableByMouse)
        self._url_label.setMinimumWidth(400)
        self._filename_label = QLabel()
        self._status_label = QLabel()
        self._size_label = QLabel()
        self._downloaded_label = QLabel()
        self._speed_label = QLabel()
        self._eta_label = QLabel()

        info_layout.addRow("URL:", self._url_label)
        info_layout.addRow("Filename:", self._filename_label)
        info_layout.addRow("Status:", self._status_label)
        info_layout.addRow("File size:", self._size_label)
        info_layout.addRow("Downloaded:", self._downloaded_label)
        info_layout.addRow("Speed:", self._speed_label)
        info_layout.addRow("ETA:", self._eta_label)

        main_layout.addWidget(info_group)

        # Error label
        self._error_label = QLabel()
        self._error_label.setStyleSheet("color: red;")
        self._error_label.setWordWrap(True)
        self._error_label.hide()
        main_layout.addWidget(self._error_label)

        # Progress bar
        self._progress_bar = QProgressBar()
        self._progress_bar.setRange(0, 100)
        main_layout.addWidget(self._progress_bar)

        # Button row
        button_layout = QHBoxLayout()
        self._toggle_details_btn = QPushButton("Show Details >>")
        self._pause_resume_btn = QPushButton("Pause")
        self._pause_resume_btn.hide()
        close_btn = QPushButton("Close")
        button_layout.addWidget(self._toggle_details_btn)
        button_layout.addStretch()
        self._open_file_btn = QPushButton("Open File")
        self._open_folder_btn = QPushButton("Open Folder")
        self._open_file_btn.hide()
        self._open_folder_btn.hide()
        button_layout.addWidget(self._open_file_btn)
        button_layout.addWidget(self._open_folder_btn)
        button_layout.addWidget(self._pause_resume_btn)
        button_layout.addWidget(close_btn)
        main_layout.addLayout(button_layout)

        # File warning
        self._file_warning_label = QLabel("File not found on disk.")
        self._file_warning_label.setStyleSheet("color: orange;")
        self._file_warning_label.hide()
        main_layout.addWidget(self._file_warning_label)

        # Detail section (hidden by default)
        self._detail_section = QWidget()
        detail_layout = QVBoxLayout(self._detail_section)
        detail_layout.setContentsMargins(0, 0, 0, 0)

        seg_label = QLabel("Segment progress by connections")
        seg_label.setAlignment(Qt.AlignCenter)
        seg_label.setStyleSheet("color: gray; font-size: 11px;")
        detail_layout.addWidget(seg_label)

        self._segment_progress_widget = SegmentProgressWidget()
        detail_layout.addWidget(self._segment_progress_widget)

        self._segment_table = QTableWidget()
        self._segment_table.setColumnCount(3)
        self._segment_table.setHorizontalHeaderLabels(["N.", "Downloaded", "Info"])
        self._segment_table.horizontalHeader().setStretchLastSection(True)
        self._segment_table.verticalHeader().hide()
        self._segment_table.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self._segment_table.setSelectionMode(QAbstractItemView.NoSelection)
        self._segment_table.setColumnWidth(0, 40)
        self._segment_table.setColumnWidth(1, 100)
        detail_layout.addWidget(self._segment_table)

        self._detail_section.hide()
        main_layout.addWidget(self._detail_section)

        # Connections
        close_btn.clicked.connect(self.accept)
        self._open_file_btn.clicked.connect(self._on_open_file)
        self._open_folder_btn.clicked.connect(self._on_open_folder)

        self._toggle_details_btn.clicked.connect(self._on_toggle_details)
        self._pause_resume_btn.clicked.connect(self._on_pause_resume)
        self._client.download_detail_fetched.connect(self._on_detail_fetched)

        # Poll timer
        self._poll_timer = QTimer(self)
        self._poll_timer.setInterval(1000)
        self._poll_timer.timeout.connect(self._fetch_detail)

        # Initial fetch
        self._fetch_detail()

    def _fetch_detail(self) -> None:
        if self._client.is_connected():
            self._client.fetch_download_detail(self._download_id)

    def _on_detail_fetched(self, dl, segments) -> None:
        if dl.id != self._download_id:
            return

        self._update_ui(dl, segments)

        terminal = dl.status in ("completed", "error", "refresh")
        if not terminal:
            if not self._poll_timer.isActive():
                self._poll_timer.start()
        else:
            self._poll_timer.stop()

    def _update_ui(self, dl, segments: list[Segment]) -> None:
        self._file_path = os.path.join(dl.dir, dl.filename)
        self._dir_path = dl.dir

        self._url_label.setText(dl.url)
        self._filename_label.setText(dl.filename)
        self._status_label.setText(status_display_text(dl.status))
        self._size_label.setText(format_bytes(dl.total_size))

        # Speed calculation (EMA)
        if dl.status == "active" and self._prev_downloaded >= 0:
            delta = dl.downloaded - self._prev_downloaded
            instant_speed = float(delta)
            self._speed = (
                0.3 * instant_speed + 0.7 * self._speed
                if self._speed > 0.0
                else instant_speed
            )
        elif dl.status != "active":
            self._speed = 0.0
        self._prev_downloaded = dl.downloaded

        if dl.total_size > 0:
            pct = int(dl.downloaded * 100 / dl.total_size)
            self._downloaded_label.setText(
                f"{format_bytes(dl.downloaded)} / {format_bytes(dl.total_size)} ({pct}%)"
            )
            self._progress_bar.setValue(pct)
            self._progress_bar.show()
        else:
            self._downloaded_label.setText(format_bytes(dl.downloaded))
            self._progress_bar.hide()

        if dl.status == "active" and self._speed > 0.0:
            self._speed_label.setText(format_speed(self._speed))
            if dl.total_size > 0:
                remaining = dl.total_size - dl.downloaded
                self._eta_label.setText(format_eta(remaining, self._speed))
            else:
                self._eta_label.setText("")
        else:
            self._speed_label.setText("")
            self._eta_label.setText("")

        # Error
        if dl.status == "error" and dl.error:
            self._error_label.setText("Error: " + dl.error)
            self._error_label.show()
        else:
            self._error_label.hide()

        # Segments
        self._segment_progress_widget.set_segments(segments, dl.total_size)

        self._segment_table.setRowCount(len(segments))
        for i, seg in enumerate(segments):
            self._segment_table.setItem(i, 0, QTableWidgetItem(str(seg.index + 1)))
            self._segment_table.setItem(i, 1, QTableWidgetItem(format_bytes(seg.downloaded)))

            if seg.done:
                info = "Done"
            elif dl.status == "active" and seg.downloaded > 0:
                info = "Receiving data..."
            elif dl.status == "active":
                info = "Connecting..."
            elif dl.status == "paused":
                info = "Paused"
            elif dl.status == "queued":
                info = "Waiting"
            else:
                info = status_display_text(dl.status)
            self._segment_table.setItem(i, 2, QTableWidgetItem(info))

        # Pause/Resume button
        if dl.status == "active":
            self._pause_resume_btn.setText("Pause")
            self._pause_resume_btn.show()
        elif dl.status == "paused":
            self._pause_resume_btn.setText("Resume")
            self._pause_resume_btn.show()
        else:
            self._pause_resume_btn.hide()

        # Completed: show open buttons, check file exists
        if dl.status == "completed":
            self._open_file_btn.show()
            self._open_folder_btn.show()
            self._progress_bar.setValue(100)

            if not os.path.isfile(self._file_path):
                self._file_warning_label.show()
                self._open_file_btn.setEnabled(False)
            else:
                self._file_warning_label.hide()
                self._open_file_btn.setEnabled(True)
        else:
            self._open_file_btn.hide()
            self._open_folder_btn.hide()
            self._file_warning_label.hide()

    def _on_toggle_details(self) -> None:
        showing = self._detail_section.isVisible()
        self._detail_section.setVisible(not showing)
        self._toggle_details_btn.setText(
            "Show Details >>" if showing else "<< Hide Details"
        )

    def _on_pause_resume(self) -> None:
        if not self._client.is_connected():
            return
        if self._pause_resume_btn.text() == "Pause":
            self._client.pause_download(self._download_id)
        else:
            self._client.resume_download(self._download_id)

    def _on_open_file(self) -> None:
        QDesktopServices.openUrl(QUrl.fromLocalFile(self._file_path))

    def _on_open_folder(self) -> None:
        QDesktopServices.openUrl(QUrl.fromLocalFile(self._dir_path))
