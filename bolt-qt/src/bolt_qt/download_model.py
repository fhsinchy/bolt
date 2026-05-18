from __future__ import annotations

from PySide6.QtCore import QModelIndex, Qt
from PySide6.QtCore import QSortFilterProxyModel, QAbstractTableModel

from .types import (
    Download,
    FileCategory,
    Segment,
    category_for_filename,
    format_bytes,
    format_eta,
    format_speed,
    status_display_text,
)

COL_FILENAME = 0
COL_SIZE = 1
COL_PROGRESS = 2
COL_SPEED = 3
COL_ETA = 4
COL_STATUS = 5
COL_COUNT = 6

_HEADERS = ("Filename", "Size", "Progress", "Speed", "ETA", "Status")


class DownloadListModel(QAbstractTableModel):
    def __init__(self, parent=None):
        super().__init__(parent)
        self._downloads: list[Download] = []
        self._prev_downloaded: dict[str, int] = {}
        self._speeds: dict[str, float] = {}
        self._segments_cache: dict[str, list] = {}

    def rowCount(self, parent=QModelIndex()):
        if parent.isValid():
            return 0
        return len(self._downloads)

    def columnCount(self, parent=QModelIndex()):
        if parent.isValid():
            return 0
        return COL_COUNT

    def data(self, index: QModelIndex, role: int = Qt.DisplayRole):
        if not index.isValid() or index.row() >= len(self._downloads):
            return None

        dl = self._downloads[index.row()]
        col = index.column()

        if role == Qt.DisplayRole:
            if col == COL_FILENAME:
                return dl.filename
            if col == COL_SIZE:
                return format_bytes(dl.total_size)
            if col == COL_PROGRESS:
                if dl.total_size <= 0:
                    return 0
                return int(dl.downloaded * 100 / dl.total_size)
            if col == COL_SPEED:
                speed = self._speeds.get(dl.id, 0.0)
                if dl.status != "active" or speed <= 0.0:
                    return ""
                return format_speed(speed)
            if col == COL_ETA:
                speed = self._speeds.get(dl.id, 0.0)
                if dl.status != "active" or speed <= 0.0 or dl.total_size <= 0:
                    return ""
                return format_eta(dl.total_size - dl.downloaded, speed)
            if col == COL_STATUS:
                return status_display_text(dl.status)

        if role == Qt.ToolTipRole:
            if col == COL_STATUS and dl.status == "refresh":
                return "This download needs a new URL. Refresh UI planned for a future version."
            if col == COL_STATUS and dl.status == "error" and dl.error:
                return dl.error
            if col == COL_FILENAME:
                return dl.filename

        return None

    def headerData(self, section: int, orientation, role: int = Qt.DisplayRole):
        if orientation != Qt.Horizontal or role != Qt.DisplayRole:
            return None
        if 0 <= section < COL_COUNT:
            return _HEADERS[section]
        return None

    def download_at(self, row: int) -> Download:
        return self._downloads[row]

    def download_id_at(self, row: int) -> str:
        if 0 <= row < len(self._downloads):
            return self._downloads[row].id
        return ""

    def selected_ids(self, indexes) -> list[str]:
        rows = set()
        for idx in indexes:
            rows.add(idx.row())
        ids = []
        for row in rows:
            if 0 <= row < len(self._downloads):
                ids.append(self._downloads[row].id)
        return ids

    def speed_for_id(self, dl_id: str) -> float:
        return self._speeds.get(dl_id, 0.0)

    def reset_speeds(self) -> None:
        self._prev_downloaded.clear()
        self._speeds.clear()
        if self._downloads:
            self.dataChanged.emit(
                self.index(0, COL_SPEED),
                self.index(len(self._downloads) - 1, COL_ETA),
            )

    def _update_speed(self, dl: Download) -> None:
        if dl.status == "active" and dl.id in self._prev_downloaded:
            delta = dl.downloaded - self._prev_downloaded[dl.id]
            instant_speed = float(delta)
            prev = self._speeds.get(dl.id, 0.0)
            self._speeds[dl.id] = (
                0.3 * instant_speed + 0.7 * prev if prev > 0.0 else instant_speed
            )
        elif dl.status != "active":
            self._speeds.pop(dl.id, None)
        self._prev_downloaded[dl.id] = dl.downloaded

    # ------------------------------------------------------------------
    # WebSocket in-place update methods
    # ------------------------------------------------------------------

    def apply_progress(self, dl_id: str, data: dict) -> None:
        """Apply a WebSocket progress event in-place.

        Updates downloaded, status, total_size, and recalculates speed via EMA.
        Segments data from the event is available via get_segments_for().
        """
        for i, dl in enumerate(self._downloads):
            if dl.id == dl_id:
                dl.downloaded = data.get("downloaded", dl.downloaded)
                dl.status = data.get("status", dl.status)
                if "total" in data and data["total"] > 0:
                    dl.total_size = data["total"]
                self._update_speed(dl)
                # Cache segment data for detail dialog
                if "segments" in data:
                    self._segments_cache[dl_id] = data["segments"]
                self.dataChanged.emit(
                    self.index(i, 0), self.index(i, COL_COUNT - 1)
                )
                return

    def apply_completed(self, dl_id: str, data: dict) -> None:
        for i, dl in enumerate(self._downloads):
            if dl.id == dl_id:
                dl.status = "completed"
                dl.downloaded = data.get("downloaded", dl.total_size)
                dl.total_size = data.get("total_size", dl.total_size)
                dl.filename = data.get("filename", dl.filename)
                dl.dir = data.get("dir", dl.dir)
                self._speeds.pop(dl_id, None)
                self._prev_downloaded.pop(dl_id, None)
                self.dataChanged.emit(
                    self.index(i, 0), self.index(i, COL_COUNT - 1)
                )
                return

    def apply_failed(self, dl_id: str, data: dict) -> None:
        for i, dl in enumerate(self._downloads):
            if dl.id == dl_id:
                dl.status = "error"
                dl.error = data.get("error", "")
                dl.filename = data.get("filename", dl.filename)
                self._speeds.pop(dl_id, None)
                self._prev_downloaded.pop(dl_id, None)
                self.dataChanged.emit(
                    self.index(i, 0), self.index(i, COL_COUNT - 1)
                )
                return

    def apply_status(self, dl_id: str, status: str) -> None:
        for i, dl in enumerate(self._downloads):
            if dl.id == dl_id:
                dl.status = status
                if status != "active":
                    self._speeds.pop(dl_id, None)
                self.dataChanged.emit(
                    self.index(i, 0), self.index(i, COL_COUNT - 1)
                )
                return

    def insert_download(self, data: dict) -> None:
        """Insert a new download from a WebSocket 'added' event."""
        dl = Download(
            id=data.get("id", ""),
            url=data.get("url", ""),
            filename=data.get("filename", ""),
            dir=data.get("dir", ""),
            total_size=data.get("total_size", 0) or 0,
            status=data.get("status", ""),
            segment_count=data.get("segments", 0) or 0,
            queue_order=data.get("queue_order", 0) or 0,
            created_at=data.get("created_at", "") or "",
        )
        row = len(self._downloads)
        self.beginInsertRows(QModelIndex(), row, row)
        self._downloads.append(dl)
        self._prev_downloaded[dl.id] = 0
        self.endInsertRows()

    def remove_download(self, dl_id: str) -> None:
        for i, dl in enumerate(self._downloads):
            if dl.id == dl_id:
                self.beginRemoveRows(QModelIndex(), i, i)
                self._prev_downloaded.pop(dl_id, None)
                self._speeds.pop(dl_id, None)
                self._segments_cache.pop(dl_id, None)
                del self._downloads[i]
                self.endRemoveRows()
                return

    def get_segments_for(self, dl_id: str) -> list:
        """Return cached segment progress data for a download."""
        raw_segments = self._segments_cache.get(dl_id, [])
        return [Segment.from_json(s) for s in raw_segments]

    def all_downloads(self) -> list[Download]:
        """Return a snapshot of all downloads (for stats / category counts)."""
        return list(self._downloads)

    # ------------------------------------------------------------------
    # Bulk update from REST poll
    # ------------------------------------------------------------------

    def update_from_poll(self, incoming: list[Download]) -> None:
        incoming_by_id = {dl.id: i for i, dl in enumerate(incoming)}

        # Remove rows absent from incoming (walk backwards)
        for i in range(len(self._downloads) - 1, -1, -1):
            dl_id = self._downloads[i].id
            if dl_id not in incoming_by_id:
                self.beginRemoveRows(QModelIndex(), i, i)
                self._prev_downloaded.pop(dl_id, None)
                self._speeds.pop(dl_id, None)
                del self._downloads[i]
                self.endRemoveRows()

        current_by_id = {dl.id: i for i, dl in enumerate(self._downloads)}

        has_new_rows = any(dl.id not in current_by_id for dl in incoming)

        order_changed = False
        if not has_new_rows and len(self._downloads) == len(incoming):
            for i, dl in enumerate(incoming):
                if self._downloads[i].id != dl.id:
                    order_changed = True
                    break

        if has_new_rows or order_changed:
            self.beginResetModel()
            for dl in incoming:
                self._update_speed(dl)
            incoming_ids = {dl.id for dl in incoming}
            self._prev_downloaded = {
                k: v for k, v in self._prev_downloaded.items() if k in incoming_ids
            }
            self._speeds = {k: v for k, v in self._speeds.items() if k in incoming_ids}
            self._downloads = list(incoming)
            self.endResetModel()
            return

        # In-place update
        for i in range(len(self._downloads)):
            dl = incoming[incoming_by_id[self._downloads[i].id]]
            old_speed = self._speeds.get(dl.id, 0.0)
            self._update_speed(dl)
            new_speed = self._speeds.get(dl.id, 0.0)

            changed = (
                self._downloads[i].downloaded != dl.downloaded
                or self._downloads[i].status != dl.status
                or self._downloads[i].filename != dl.filename
                or self._downloads[i].total_size != dl.total_size
                or old_speed != new_speed
            )
            self._downloads[i] = dl
            if changed:
                self.dataChanged.emit(
                    self.index(i, 0), self.index(i, COL_COUNT - 1)
                )


# ---------------------------------------------------------------------------
# Filter proxy
# ---------------------------------------------------------------------------

_FILTER_ALL = "all"
_FILTER_BY_STATUS = "by_status"
_FILTER_BY_TYPE = "by_type"


class DownloadFilterProxy(QSortFilterProxyModel):
    def __init__(self, parent=None):
        super().__init__(parent)
        self._mode = _FILTER_ALL
        self._status_filter: list[str] = []
        self._type_filter: FileCategory = FileCategory.NONE

    def set_status_filter(self, statuses: list[str]) -> None:
        self.beginFilterChange()
        self._mode = _FILTER_BY_STATUS
        self._status_filter = statuses
        self.endFilterChange()

    def set_type_filter(self, category: FileCategory) -> None:
        self.beginFilterChange()
        self._mode = _FILTER_BY_TYPE
        self._type_filter = category
        self.endFilterChange()

    def clear_filter(self) -> None:
        self.beginFilterChange()
        self._mode = _FILTER_ALL
        self.endFilterChange()

    def is_filtered(self) -> bool:
        return self._mode != _FILTER_ALL

    def filterAcceptsRow(self, source_row: int, source_parent: QModelIndex) -> bool:
        if self._mode == _FILTER_ALL:
            return True

        model = self.sourceModel()
        if not isinstance(model, DownloadListModel):
            return True

        if source_row >= model.rowCount():
            return False

        dl = model.download_at(source_row)

        if self._mode == _FILTER_BY_STATUS:
            return dl.status in self._status_filter

        if self._mode == _FILTER_BY_TYPE:
            return category_for_filename(dl.filename) == self._type_filter

        return True
