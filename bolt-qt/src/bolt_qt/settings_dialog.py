from __future__ import annotations

from PySide6.QtCore import Qt
from PySide6.QtWidgets import (
    QCheckBox,
    QDialog,
    QDialogButtonBox,
    QDoubleSpinBox,
    QFileDialog,
    QFormLayout,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QPushButton,
    QSpinBox,
    QSystemTrayIcon,
    QVBoxLayout,
)
from PySide6.QtCore import QSettings

from .daemon_client import DaemonClient
from .types import Config

_MB = 1024.0 * 1024.0


def _parse_ext_list(text: str) -> list[str]:
    result = []
    for s in text.split(","):
        trimmed = s.strip().lower()
        if trimmed:
            result.append(trimmed)
    return result


class SettingsDialog(QDialog):
    def __init__(self, client: DaemonClient, parent=None):
        super().__init__(parent)
        self._client = client
        self._original_config = Config()

        self.setWindowTitle("Settings")
        self.setMinimumWidth(450)

        main_layout = QVBoxLayout(self)
        form = QFormLayout()

        # Download directory
        dir_layout = QHBoxLayout()
        self._dir_edit = QLineEdit()
        browse_button = QPushButton("Browse")
        dir_layout.addWidget(self._dir_edit, 1)
        dir_layout.addWidget(browse_button)
        dir_hint = QLabel(
            "<small>Note: if the daemon runs sandboxed (systemd), only ~/Downloads may be writable.</small>"
        )
        dir_hint.setWordWrap(True)
        dir_hint.setTextFormat(Qt.RichText)
        form.addRow("Download directory:", dir_layout)
        form.addRow("", dir_hint)

        # Max concurrent
        self._max_concurrent_spin = QSpinBox()
        self._max_concurrent_spin.setRange(1, 10)
        form.addRow("Max concurrent:", self._max_concurrent_spin)

        # Default segments
        self._default_segments_spin = QSpinBox()
        self._default_segments_spin.setRange(1, 32)
        form.addRow("Default segments:", self._default_segments_spin)

        # Global speed limit (MB/s)
        self._speed_limit_spin = QDoubleSpinBox()
        self._speed_limit_spin.setRange(0.0, 100000.0)
        self._speed_limit_spin.setDecimals(1)
        self._speed_limit_spin.setSuffix(" MB/s")
        self._speed_limit_spin.setSpecialValueText("Unlimited")
        form.addRow("Global speed limit:", self._speed_limit_spin)

        # Max retries
        self._max_retries_spin = QSpinBox()
        self._max_retries_spin.setRange(0, 100)
        form.addRow("Max retries:", self._max_retries_spin)

        # Min segment size (MB)
        self._min_segment_size_spin = QDoubleSpinBox()
        self._min_segment_size_spin.setRange(0.0625, 1000.0)
        self._min_segment_size_spin.setDecimals(2)
        self._min_segment_size_spin.setSuffix(" MB")
        form.addRow("Min segment size:", self._min_segment_size_spin)

        # Min file size (MB)
        self._min_file_size_spin = QDoubleSpinBox()
        self._min_file_size_spin.setRange(0.0, 10000.0)
        self._min_file_size_spin.setDecimals(1)
        self._min_file_size_spin.setSuffix(" MB")
        self._min_file_size_spin.setSpecialValueText("No minimum")
        form.addRow("Min file size:", self._min_file_size_spin)

        # Extension whitelist
        self._extension_whitelist_edit = QLineEdit()
        self._extension_whitelist_edit.setPlaceholderText("e.g. .zip, .iso, .tar.gz (empty = allow all)")
        form.addRow("Extension whitelist:", self._extension_whitelist_edit)

        # Extension blacklist
        self._extension_blacklist_edit = QLineEdit()
        self._extension_blacklist_edit.setPlaceholderText("e.g. .exe, .msi (empty = block none)")
        form.addRow("Extension blacklist:", self._extension_blacklist_edit)

        # Notifications
        self._notifications_check = QCheckBox("Desktop notifications")
        form.addRow("", self._notifications_check)

        # Minimize to tray (GUI-only)
        self._minimize_to_tray_check = QCheckBox("Minimize to system tray on close")
        form.addRow("", self._minimize_to_tray_check)
        tray_hint = QLabel(
            "<small>Downloads continue in the background even when the app is closed.</small>"
        )
        tray_hint.setWordWrap(True)
        tray_hint.setTextFormat(Qt.RichText)
        form.addRow("", tray_hint)

        if not QSystemTrayIcon.isSystemTrayAvailable():
            self._minimize_to_tray_check.hide()
            tray_hint.hide()

        main_layout.addLayout(form)

        # Error label
        self._error_label = QLabel()
        self._error_label.setStyleSheet("color: red;")
        self._error_label.setWordWrap(True)
        self._error_label.hide()
        main_layout.addWidget(self._error_label)

        # Buttons
        buttons = QDialogButtonBox(QDialogButtonBox.Save | QDialogButtonBox.Cancel)
        self._save_button = buttons.button(QDialogButtonBox.Save)
        main_layout.addWidget(buttons)

        buttons.accepted.connect(self._on_save)
        buttons.rejected.connect(self.reject)
        browse_button.clicked.connect(self._on_browse)

        self._client.config_fetched.connect(self._on_config_fetched)
        self._client.config_updated.connect(self._on_config_updated)
        self._client.request_failed.connect(self._on_request_failed)
        self._client.disconnected.connect(self._on_disconnected)

        self._client.fetch_config()

    def _on_config_fetched(self, cfg: Config) -> None:
        self._original_config = cfg
        self._dir_edit.setText(cfg.download_dir)
        self._max_concurrent_spin.setValue(cfg.max_concurrent)
        self._default_segments_spin.setValue(cfg.default_segments)
        self._speed_limit_spin.setValue(cfg.global_speed_limit / _MB)
        self._max_retries_spin.setValue(cfg.max_retries)
        self._min_segment_size_spin.setValue(cfg.min_segment_size / _MB)
        self._min_file_size_spin.setValue(cfg.min_file_size / _MB)
        self._extension_whitelist_edit.setText(", ".join(cfg.extension_whitelist))
        self._extension_blacklist_edit.setText(", ".join(cfg.extension_blacklist))
        self._notifications_check.setChecked(cfg.notifications)

        gui_settings = QSettings()
        self._minimize_to_tray_check.setChecked(gui_settings.value("minimizeToTray", True, type=bool))

    def _on_save(self) -> None:
        if not self._client.is_connected():
            self._error_label.setText("Not connected to daemon")
            self._error_label.show()
            return

        changes: dict = {}
        oc = self._original_config

        dir_val = self._dir_edit.text().strip()
        if dir_val != oc.download_dir:
            changes["download_dir"] = dir_val

        max_concurrent = self._max_concurrent_spin.value()
        if max_concurrent != oc.max_concurrent:
            changes["max_concurrent"] = max_concurrent

        default_segments = self._default_segments_spin.value()
        if default_segments != oc.default_segments:
            changes["default_segments"] = default_segments

        speed_limit = round(self._speed_limit_spin.value() * _MB)
        if speed_limit != oc.global_speed_limit:
            changes["global_speed_limit"] = speed_limit

        max_retries = self._max_retries_spin.value()
        if max_retries != oc.max_retries:
            changes["max_retries"] = max_retries

        min_seg_size = round(self._min_segment_size_spin.value() * _MB)
        if min_seg_size != oc.min_segment_size:
            changes["min_segment_size"] = min_seg_size

        min_file_size = round(self._min_file_size_spin.value() * _MB)
        if min_file_size != oc.min_file_size:
            changes["min_file_size"] = min_file_size

        new_whitelist = _parse_ext_list(self._extension_whitelist_edit.text())
        if new_whitelist != oc.extension_whitelist:
            changes["extension_whitelist"] = new_whitelist

        new_blacklist = _parse_ext_list(self._extension_blacklist_edit.text())
        if new_blacklist != oc.extension_blacklist:
            changes["extension_blacklist"] = new_blacklist

        notifications = self._notifications_check.isChecked()
        if notifications != oc.notifications:
            changes["notifications"] = notifications

        # Save GUI-local setting immediately
        gui_settings = QSettings()
        gui_settings.setValue("minimizeToTray", self._minimize_to_tray_check.isChecked())

        if not changes:
            self.accept()
            return

        self._error_label.hide()
        self._save_button.setEnabled(False)
        self._client.update_config(changes)

    def _on_config_updated(self) -> None:
        self.accept()

    def _on_request_failed(self, endpoint: str, _status: int, _code: str, error_message: str) -> None:
        if endpoint != "updateConfig":
            return
        self._save_button.setEnabled(True)
        self._error_label.setText(error_message)
        self._error_label.show()

    def _on_browse(self) -> None:
        d = QFileDialog.getExistingDirectory(self, "Select Directory", self._dir_edit.text())
        if d:
            self._dir_edit.setText(d)

    def _on_disconnected(self) -> None:
        self._save_button.setEnabled(True)
        self._error_label.setText("Connection lost")
        self._error_label.show()
