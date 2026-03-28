from __future__ import annotations

from pathlib import Path

from PySide6.QtCore import Qt, QItemSelection, QItemSelectionModel, QSettings
from PySide6.QtGui import QAction, QCloseEvent, QFont, QIcon, QResizeEvent
from PySide6.QtWidgets import (
    QAbstractItemView,
    QApplication,
    QCheckBox,
    QDialog,
    QDialogButtonBox,
    QHBoxLayout,
    QHeaderView,
    QLabel,
    QListWidget,
    QListWidgetItem,
    QMainWindow,
    QMenu,
    QPushButton,
    QSplitter,
    QSystemTrayIcon,
    QTableView,
    QToolBar,
    QTreeWidget,
    QTreeWidgetItem,
    QTreeWidgetItemIterator,
    QVBoxLayout,
)

from .add_download_dialog import AddDownloadDialog
from .daemon_client import DaemonClient
from .download_detail_dialog import DownloadDetailDialog
from .download_model import (
    COL_COUNT,
    COL_ETA,
    COL_FILENAME,
    COL_PROGRESS,
    COL_SIZE,
    COL_SPEED,
    DownloadFilterProxy,
    DownloadListModel,
)
from .progress_delegate import ProgressDelegate
from .settings_dialog import SettingsDialog
from .types import FileCategory, category_for_filename, format_speed

_PACKAGE_DIR = Path(__file__).resolve().parent


class MainWindow(QMainWindow):
    def __init__(self, client: DaemonClient, parent=None):
        super().__init__(parent)
        self._client = client
        self._model = DownloadListModel(self)
        self._table_view = QTableView(self)
        self._progress_delegate = ProgressDelegate(self)
        self._active_dialog: QDialog | None = None
        self._tray_icon: QSystemTrayIcon | None = None
        self._tray_menu: QMenu | None = None

        self.setWindowTitle("Bolt Download Manager")
        self.resize(800, 500)

        # Proxy model for filtering
        self._proxy_model = DownloadFilterProxy(self)
        self._proxy_model.setSourceModel(self._model)

        # Table view setup
        self._table_view.setModel(self._proxy_model)
        self._table_view.setItemDelegateForColumn(COL_PROGRESS, self._progress_delegate)
        self._table_view.setSelectionBehavior(QAbstractItemView.SelectRows)
        self._table_view.setSelectionMode(QAbstractItemView.ExtendedSelection)
        self._table_view.verticalHeader().hide()
        self._table_view.horizontalHeader().setStretchLastSection(True)
        self._table_view.setColumnWidth(COL_FILENAME, 250)
        self._table_view.setColumnWidth(COL_SIZE, 80)
        self._table_view.setColumnWidth(COL_PROGRESS, 120)
        self._table_view.setColumnWidth(COL_SPEED, 100)
        self._table_view.setColumnWidth(COL_ETA, 80)

        # Splitter: sidebar + table
        self._splitter = QSplitter(Qt.Horizontal, self)
        self._sidebar = QTreeWidget()
        self._setup_sidebar()
        self._splitter.addWidget(self._sidebar)
        self._splitter.addWidget(self._table_view)
        self._splitter.setStretchFactor(0, 0)
        self._splitter.setStretchFactor(1, 1)
        self._sidebar.setMinimumWidth(150)
        self._sidebar.setMaximumWidth(250)

        self.setCentralWidget(self._splitter)

        # Empty state label
        self._empty_label = QLabel("No downloads yet. Click + to add one.", self._table_view)
        self._empty_label.setAlignment(Qt.AlignCenter)
        self._empty_label.setStyleSheet("color: gray; font-size: 14px;")
        self._empty_label.setVisible(True)

        self._setup_toolbar()
        self._setup_status_bar()
        self._setup_tray_icon()

        # Connect client signals
        self._client.connected.connect(self._on_connected)
        self._client.disconnected.connect(self._on_disconnected)
        self._client.downloads_fetched.connect(self._on_downloads_fetched)
        self._client.request_failed.connect(self._on_request_failed)

        # Selection changes
        self._table_view.selectionModel().selectionChanged.connect(self._on_selection_changed)

        # Double-click to open detail dialog
        self._table_view.doubleClicked.connect(self._on_double_click)

        # Restore geometry
        settings = QSettings()
        geo = settings.value("mainwindow/geometry")
        if geo:
            self.restoreGeometry(geo)
        splitter_state = settings.value("mainwindow/splitter")
        if splitter_state:
            self._splitter.restoreState(splitter_state)

        self._update_toolbar_state()

    # ------------------------------------------------------------------
    # Close / geometry
    # ------------------------------------------------------------------

    def closeEvent(self, event: QCloseEvent):
        self._persist_geometry()

        settings = QSettings()
        minimize_to_tray = settings.value("minimizeToTray", True, type=bool)

        if minimize_to_tray and self._tray_icon:
            event.ignore()
            self.hide()
            return

        QApplication.quit()

    def _persist_geometry(self) -> None:
        settings = QSettings()
        settings.setValue("mainwindow/geometry", self.saveGeometry())
        settings.setValue("mainwindow/splitter", self._splitter.saveState())

    def resizeEvent(self, event: QResizeEvent):
        super().resizeEvent(event)
        self._update_empty_state()

    # ------------------------------------------------------------------
    # Tray icon
    # ------------------------------------------------------------------

    def _setup_tray_icon(self) -> None:
        if not QSystemTrayIcon.isSystemTrayAvailable():
            return

        icon_path = _PACKAGE_DIR / "tray-icon.png"
        self._tray_icon = QSystemTrayIcon(QIcon(str(icon_path)), self)
        self._tray_menu = QMenu(self)

        open_action = self._tray_menu.addAction("Open Bolt")
        open_action.triggered.connect(self._show_and_raise)

        self._tray_menu.addSeparator()

        pause_all_action = self._tray_menu.addAction("Pause All")
        pause_all_action.triggered.connect(
            lambda: self._client.pause_all() if self._client.is_connected() else None
        )

        resume_all_action = self._tray_menu.addAction("Resume All")
        resume_all_action.triggered.connect(
            lambda: self._client.resume_all() if self._client.is_connected() else None
        )

        self._tray_menu.addSeparator()

        settings_action = self._tray_menu.addAction("Settings")
        settings_action.triggered.connect(self._on_settings)

        self._tray_menu.addSeparator()

        quit_action = self._tray_menu.addAction("Quit")
        quit_action.triggered.connect(self._on_quit)

        self._tray_icon.setContextMenu(self._tray_menu)
        self._tray_icon.setToolTip("Bolt Download Manager")
        self._tray_icon.activated.connect(self._on_tray_activated)
        self._tray_icon.show()

    def _show_and_raise(self) -> None:
        self.show()
        self.raise_()
        self.activateWindow()

    def _on_tray_activated(self, reason) -> None:
        if reason == QSystemTrayIcon.Trigger:
            if self.isVisible():
                self.hide()
            else:
                self._show_and_raise()

    def _on_quit(self) -> None:
        self._persist_geometry()
        QApplication.quit()

    # ------------------------------------------------------------------
    # Toolbar
    # ------------------------------------------------------------------

    def _setup_toolbar(self) -> None:
        toolbar = self.addToolBar("Main")
        toolbar.setMovable(False)

        self._add_action = toolbar.addAction(QIcon.fromTheme("list-add"), "Add URL")
        self._pause_action = toolbar.addAction(QIcon.fromTheme("media-playback-pause"), "Pause")
        self._resume_action = toolbar.addAction(QIcon.fromTheme("media-playback-start"), "Resume")
        self._retry_action = toolbar.addAction(QIcon.fromTheme("view-refresh"), "Retry")
        self._delete_action = toolbar.addAction(QIcon.fromTheme("edit-delete"), "Delete")
        toolbar.addSeparator()
        self._settings_action = toolbar.addAction(QIcon.fromTheme("configure"), "Settings")
        self._reorder_action = toolbar.addAction(QIcon.fromTheme("view-sort-ascending"), "Reorder Queue")
        self._reorder_action.setVisible(False)

        self._add_action.triggered.connect(self._on_add_url)
        self._pause_action.triggered.connect(self._on_pause)
        self._resume_action.triggered.connect(self._on_resume)
        self._retry_action.triggered.connect(self._on_retry)
        self._delete_action.triggered.connect(self._on_delete)
        self._settings_action.triggered.connect(self._on_settings)
        self._reorder_action.triggered.connect(self._on_reorder)

    # ------------------------------------------------------------------
    # Status bar
    # ------------------------------------------------------------------

    def _setup_status_bar(self) -> None:
        self._connection_label = QLabel("Connecting...")
        self._active_count_label = QLabel()
        self._total_speed_label = QLabel()

        self.statusBar().addPermanentWidget(self._connection_label)
        self.statusBar().addPermanentWidget(self._active_count_label)
        self.statusBar().addPermanentWidget(self._total_speed_label)

    # ------------------------------------------------------------------
    # Client signal handlers
    # ------------------------------------------------------------------

    def _on_connected(self) -> None:
        self._connection_label.setText("Connected")

    def _on_disconnected(self) -> None:
        self._connection_label.setText("Disconnected \u2014 retrying...")
        self._active_count_label.setText("")
        self._total_speed_label.setText("")
        self._model.reset_speeds()

    def _on_downloads_fetched(self, downloads: list) -> None:
        # Save selection by ID before model update
        proxy_selected = self._table_view.selectionModel().selectedRows()
        source_selected = [self._proxy_model.mapToSource(idx) for idx in proxy_selected]
        selected_ids = self._model.selected_ids(source_selected)

        self._model.update_from_poll(downloads)

        # Restore selection by ID
        if selected_ids:
            sel = QItemSelection()
            for src_row in range(self._model.rowCount()):
                if self._model.download_id_at(src_row) in selected_ids:
                    proxy_first = self._proxy_model.mapFromSource(self._model.index(src_row, 0))
                    proxy_last = self._proxy_model.mapFromSource(
                        self._model.index(src_row, COL_COUNT - 1)
                    )
                    if proxy_first.isValid():
                        sel.select(proxy_first, proxy_last)
            if not sel.isEmpty():
                self._table_view.selectionModel().select(sel, QItemSelectionModel.ClearAndSelect)

        active_count = sum(1 for dl in downloads if dl.status == "active")
        self._active_count_label.setText(
            f"{active_count} downloading" if active_count > 0 else ""
        )

        total_speed = sum(
            self._model.speed_for_id(dl.id) for dl in downloads if dl.status == "active"
        )
        self._total_speed_label.setText(format_speed(total_speed) if total_speed > 0 else "")

        self._update_category_counts(downloads)
        self._update_empty_state()
        self._update_toolbar_state()

    def _on_request_failed(self, _endpoint: str, _status: int, _code: str, error_message: str) -> None:
        self.statusBar().showMessage("Error: " + error_message, 5000)

    # ------------------------------------------------------------------
    # Selection / toolbar state
    # ------------------------------------------------------------------

    def _on_selection_changed(self) -> None:
        self._update_toolbar_state()

    def _update_toolbar_state(self) -> None:
        selected = self._table_view.selectionModel().selectedRows()
        has_selection = len(selected) > 0
        has_active = False
        has_paused = False
        has_error = False

        for proxy_idx in selected:
            src_idx = self._proxy_model.mapToSource(proxy_idx)
            dl = self._model.download_at(src_idx.row())
            if dl.status == "active":
                has_active = True
            if dl.status == "paused":
                has_paused = True
            if dl.status == "error":
                has_error = True

        self._pause_action.setEnabled(has_active)
        self._resume_action.setEnabled(has_paused)
        self._retry_action.setEnabled(has_error)
        self._delete_action.setEnabled(has_selection)

    def _update_empty_state(self) -> None:
        proxy_empty = self._proxy_model.rowCount() == 0
        self._empty_label.setVisible(proxy_empty)
        if proxy_empty:
            if self._proxy_model.is_filtered() and self._model.rowCount() > 0:
                self._empty_label.setText("No downloads in this category.")
            else:
                self._empty_label.setText("No downloads yet. Click + to add one.")
            self._empty_label.setGeometry(self._table_view.viewport().rect())

    # ------------------------------------------------------------------
    # Double-click
    # ------------------------------------------------------------------

    def _on_double_click(self, proxy_idx) -> None:
        if self._active_dialog is not None and self._active_dialog.isVisible():
            return
        src_idx = self._proxy_model.mapToSource(proxy_idx)
        dl_id = self._model.download_id_at(src_idx.row())
        if not dl_id:
            return
        dialog = DownloadDetailDialog(dl_id, self._client, self)
        dialog.setAttribute(Qt.WA_DeleteOnClose)
        dialog.destroyed.connect(lambda: setattr(self, "_active_dialog", None))
        self._active_dialog = dialog
        dialog.open()

    # ------------------------------------------------------------------
    # Toolbar actions
    # ------------------------------------------------------------------

    def _on_add_url(self) -> None:
        if self._active_dialog is not None and self._active_dialog.isVisible():
            return
        dialog = AddDownloadDialog(self._client, self)
        dialog.setAttribute(Qt.WA_DeleteOnClose)
        dialog.destroyed.connect(lambda: setattr(self, "_active_dialog", None))
        self._active_dialog = dialog
        dialog.open()

    def _on_pause(self) -> None:
        if not self._client.is_connected():
            self.statusBar().showMessage("Not connected to daemon", 5000)
            return
        for proxy_idx in self._table_view.selectionModel().selectedRows():
            src_idx = self._proxy_model.mapToSource(proxy_idx)
            dl = self._model.download_at(src_idx.row())
            if dl.status == "active":
                self._client.pause_download(dl.id)

    def _on_resume(self) -> None:
        if not self._client.is_connected():
            self.statusBar().showMessage("Not connected to daemon", 5000)
            return
        for proxy_idx in self._table_view.selectionModel().selectedRows():
            src_idx = self._proxy_model.mapToSource(proxy_idx)
            dl = self._model.download_at(src_idx.row())
            if dl.status == "paused":
                self._client.resume_download(dl.id)

    def _on_retry(self) -> None:
        if not self._client.is_connected():
            self.statusBar().showMessage("Not connected to daemon", 5000)
            return
        for proxy_idx in self._table_view.selectionModel().selectedRows():
            src_idx = self._proxy_model.mapToSource(proxy_idx)
            dl = self._model.download_at(src_idx.row())
            if dl.status == "error":
                self._client.retry_download(dl.id)

    def _on_delete(self) -> None:
        if not self._client.is_connected():
            self.statusBar().showMessage("Not connected to daemon", 5000)
            return

        proxy_selected = self._table_view.selectionModel().selectedRows()
        source_selected = [self._proxy_model.mapToSource(idx) for idx in proxy_selected]
        ids = self._model.selected_ids(source_selected)
        if not ids:
            return

        # Custom delete confirmation dialog
        dialog = QDialog(self)
        dialog.setWindowTitle("Confirm Delete")

        layout = QVBoxLayout(dialog)
        count = len(ids)
        layout.addWidget(QLabel(
            "Delete this download?" if count == 1 else f"Delete {count} downloads?"
        ))

        delete_file_check = QCheckBox("Also delete downloaded file")
        layout.addWidget(delete_file_check)

        buttons = QDialogButtonBox(QDialogButtonBox.Ok | QDialogButtonBox.Cancel)
        layout.addWidget(buttons)

        buttons.accepted.connect(dialog.accept)
        buttons.rejected.connect(dialog.reject)

        if dialog.exec() != QDialog.Accepted:
            return

        delete_file = delete_file_check.isChecked()
        for dl_id in ids:
            self._client.delete_download(dl_id, delete_file)

    def _on_settings(self) -> None:
        if self._active_dialog is not None and self._active_dialog.isVisible():
            return
        dialog = SettingsDialog(self._client, self)
        dialog.setAttribute(Qt.WA_DeleteOnClose)
        dialog.destroyed.connect(lambda: setattr(self, "_active_dialog", None))
        self._active_dialog = dialog
        dialog.open()

    def _on_reorder(self) -> None:
        if not self._client.is_connected():
            self.statusBar().showMessage("Not connected to daemon", 5000)
            return

        queued: list[tuple[str, str]] = []
        for i in range(self._model.rowCount()):
            dl = self._model.download_at(i)
            if dl.status == "queued":
                queued.append((dl.id, dl.filename))

        if not queued:
            self.statusBar().showMessage("No queued downloads to reorder", 3000)
            return

        dialog = QDialog(self)
        dialog.setWindowTitle("Reorder Queue")
        dialog.setMinimumSize(400, 300)

        layout = QVBoxLayout(dialog)
        layout.addWidget(QLabel("Drag items or use buttons to reorder. Top item downloads first."))

        list_widget = QListWidget()
        list_widget.setDragDropMode(QAbstractItemView.InternalMove)
        list_widget.setDefaultDropAction(Qt.MoveAction)
        for dl_id, filename in queued:
            item = QListWidgetItem(filename)
            item.setData(Qt.UserRole, dl_id)
            list_widget.addItem(item)
        layout.addWidget(list_widget, 1)

        move_layout = QHBoxLayout()
        move_up_btn = QPushButton("Move Up")
        move_down_btn = QPushButton("Move Down")
        move_layout.addStretch()
        move_layout.addWidget(move_up_btn)
        move_layout.addWidget(move_down_btn)
        move_layout.addStretch()
        layout.addLayout(move_layout)

        def move_up():
            row = list_widget.currentRow()
            if row > 0:
                item = list_widget.takeItem(row)
                list_widget.insertItem(row - 1, item)
                list_widget.setCurrentRow(row - 1)

        def move_down():
            row = list_widget.currentRow()
            if 0 <= row < list_widget.count() - 1:
                item = list_widget.takeItem(row)
                list_widget.insertItem(row + 1, item)
                list_widget.setCurrentRow(row + 1)

        move_up_btn.clicked.connect(move_up)
        move_down_btn.clicked.connect(move_down)

        buttons = QDialogButtonBox(QDialogButtonBox.Ok | QDialogButtonBox.Cancel)
        layout.addWidget(buttons)
        buttons.accepted.connect(dialog.accept)
        buttons.rejected.connect(dialog.reject)

        if dialog.exec() != QDialog.Accepted:
            return

        ordered_ids = [
            list_widget.item(i).data(Qt.UserRole) for i in range(list_widget.count())
        ]
        self._client.reorder_downloads(ordered_ids)

    # ------------------------------------------------------------------
    # Sidebar
    # ------------------------------------------------------------------

    def _setup_sidebar(self) -> None:
        self._sidebar.setHeaderHidden(True)
        self._sidebar.setRootIsDecorated(False)
        self._sidebar.setIndentation(16)

        # Status section
        status_header = QTreeWidgetItem(self._sidebar, ["Status"])
        status_header.setFlags(Qt.ItemIsEnabled)
        bold_font = status_header.font(0)
        bold_font.setBold(True)
        status_header.setFont(0, bold_font)

        def add_category(parent, name, key):
            item = QTreeWidgetItem(parent, [name])
            item.setData(0, Qt.UserRole, key)
            item.setFlags(Qt.ItemIsEnabled | Qt.ItemIsSelectable)
            return item

        all_item = add_category(status_header, "All Downloads", "all")
        add_category(status_header, "Queued", "queued")
        add_category(status_header, "Unfinished", "unfinished")
        add_category(status_header, "Failed", "failed")
        add_category(status_header, "Finished", "finished")

        # Type section
        type_header = QTreeWidgetItem(self._sidebar, ["Types"])
        type_header.setFlags(Qt.ItemIsEnabled)
        type_header.setFont(0, bold_font)

        add_category(type_header, "Compressed", "compressed")
        add_category(type_header, "Documents", "documents")
        add_category(type_header, "Music", "music")
        add_category(type_header, "Video", "video")
        add_category(type_header, "Images", "images")
        add_category(type_header, "Programs", "programs")
        add_category(type_header, "Disk Images", "diskimages")

        self._sidebar.expandAll()
        self._sidebar.setCurrentItem(all_item)

        self._sidebar.currentItemChanged.connect(lambda: self._on_category_changed())

    def _on_category_changed(self) -> None:
        item = self._sidebar.currentItem()
        if not item:
            return

        key = item.data(0, Qt.UserRole)
        if not key:
            return

        _FILTER_MAP = {
            "all": lambda: self._proxy_model.clear_filter(),
            "queued": lambda: self._proxy_model.set_status_filter(["queued"]),
            "unfinished": lambda: self._proxy_model.set_status_filter(["active", "paused", "verifying"]),
            "failed": lambda: self._proxy_model.set_status_filter(["error", "refresh"]),
            "finished": lambda: self._proxy_model.set_status_filter(["completed"]),
            "compressed": lambda: self._proxy_model.set_type_filter(FileCategory.COMPRESSED),
            "documents": lambda: self._proxy_model.set_type_filter(FileCategory.DOCUMENTS),
            "music": lambda: self._proxy_model.set_type_filter(FileCategory.MUSIC),
            "video": lambda: self._proxy_model.set_type_filter(FileCategory.VIDEO),
            "images": lambda: self._proxy_model.set_type_filter(FileCategory.IMAGES),
            "programs": lambda: self._proxy_model.set_type_filter(FileCategory.PROGRAMS),
            "diskimages": lambda: self._proxy_model.set_type_filter(FileCategory.DISK_IMAGES),
        }

        action = _FILTER_MAP.get(key)
        if action:
            action()

        self._reorder_action.setVisible(key == "queued")
        self._update_empty_state()

    def _update_category_counts(self, downloads: list) -> None:
        counts = {
            "all": len(downloads),
            "queued": 0,
            "unfinished": 0,
            "failed": 0,
            "finished": 0,
            "compressed": 0,
            "documents": 0,
            "music": 0,
            "video": 0,
            "images": 0,
            "programs": 0,
            "diskimages": 0,
        }

        _CAT_KEY = {
            FileCategory.COMPRESSED: "compressed",
            FileCategory.DOCUMENTS: "documents",
            FileCategory.MUSIC: "music",
            FileCategory.VIDEO: "video",
            FileCategory.IMAGES: "images",
            FileCategory.PROGRAMS: "programs",
            FileCategory.DISK_IMAGES: "diskimages",
        }

        for dl in downloads:
            if dl.status == "queued":
                counts["queued"] += 1
            elif dl.status in ("active", "paused", "verifying"):
                counts["unfinished"] += 1
            elif dl.status in ("error", "refresh"):
                counts["failed"] += 1
            elif dl.status == "completed":
                counts["finished"] += 1

            cat_key = _CAT_KEY.get(category_for_filename(dl.filename))
            if cat_key:
                counts[cat_key] += 1

        _BASE_NAMES = {
            "all": "All Downloads",
            "queued": "Queued",
            "unfinished": "Unfinished",
            "failed": "Failed",
            "finished": "Finished",
            "compressed": "Compressed",
            "documents": "Documents",
            "music": "Music",
            "video": "Video",
            "images": "Images",
            "programs": "Programs",
            "diskimages": "Disk Images",
        }

        it = QTreeWidgetItemIterator(self._sidebar)
        while it.value():
            item = it.value()
            key = item.data(0, Qt.UserRole)
            if key and key in counts:
                base = _BASE_NAMES.get(key, key)
                item.setText(0, f"{base} ({counts[key]})")
            it += 1
