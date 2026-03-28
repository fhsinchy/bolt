from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum


@dataclass
class Download:
    id: str = ""
    url: str = ""
    filename: str = ""
    dir: str = ""
    total_size: int = 0
    downloaded: int = 0
    status: str = ""
    segment_count: int = 0
    speed_limit: int = 0
    error: str = ""
    created_at: str = ""
    completed_at: str = ""
    queue_order: int = 0

    @classmethod
    def from_json(cls, obj: dict) -> Download:
        return cls(
            id=obj.get("id", ""),
            url=obj.get("url", ""),
            filename=obj.get("filename", ""),
            dir=obj.get("dir", ""),
            total_size=obj.get("total_size", 0) or 0,
            downloaded=obj.get("downloaded", 0) or 0,
            status=obj.get("status", ""),
            segment_count=obj.get("segments", 0) or 0,
            speed_limit=obj.get("speed_limit", 0) or 0,
            error=obj.get("error", ""),
            created_at=obj.get("created_at", "") or "",
            completed_at=obj.get("completed_at", "") or "",
            queue_order=obj.get("queue_order", 0) or 0,
        )


@dataclass
class Segment:
    download_id: str = ""
    index: int = 0
    start_byte: int = 0
    end_byte: int = 0
    downloaded: int = 0
    done: bool = False

    @classmethod
    def from_json(cls, obj: dict) -> Segment:
        return cls(
            download_id=obj.get("download_id", ""),
            index=obj.get("index", 0) or 0,
            start_byte=obj.get("start_byte", 0) or 0,
            end_byte=obj.get("end_byte", 0) or 0,
            downloaded=obj.get("downloaded", 0) or 0,
            done=obj.get("done", False),
        )


@dataclass
class AddRequest:
    url: str = ""
    trace_id: str = "gui-qt"
    filename: str = ""
    dir: str = ""
    segments: int = 0
    speed_limit: int = 0
    force: bool = False
    paused: bool = False

    def to_json(self) -> dict:
        obj: dict = {"url": self.url, "trace_id": self.trace_id}
        if self.filename:
            obj["filename"] = self.filename
        if self.dir:
            obj["dir"] = self.dir
        if self.segments > 0:
            obj["segments"] = self.segments
        if self.speed_limit > 0:
            obj["speed_limit"] = self.speed_limit
        if self.force:
            obj["force"] = True
        if self.paused:
            obj["paused"] = True
        return obj


@dataclass
class ProbeResult:
    filename: str = ""
    total_size: int = 0
    accepts_ranges: bool = False
    final_url: str = ""
    content_type: str = ""

    @classmethod
    def from_json(cls, obj: dict) -> ProbeResult:
        return cls(
            filename=obj.get("filename", ""),
            total_size=obj.get("total_size", 0) or 0,
            accepts_ranges=obj.get("accepts_ranges", False),
            final_url=obj.get("final_url", ""),
            content_type=obj.get("content_type", ""),
        )


@dataclass
class Config:
    download_dir: str = ""
    max_concurrent: int = 0
    default_segments: int = 0
    global_speed_limit: int = 0
    notifications: bool = True
    max_retries: int = 0
    min_segment_size: int = 0
    min_file_size: int = 0
    extension_whitelist: list[str] = field(default_factory=list)
    extension_blacklist: list[str] = field(default_factory=list)

    @classmethod
    def from_json(cls, obj: dict) -> Config:
        return cls(
            download_dir=obj.get("download_dir", ""),
            max_concurrent=obj.get("max_concurrent", 0) or 0,
            default_segments=obj.get("default_segments", 0) or 0,
            global_speed_limit=obj.get("global_speed_limit", 0) or 0,
            notifications=obj.get("notifications", True),
            max_retries=obj.get("max_retries", 0) or 0,
            min_segment_size=obj.get("min_segment_size", 0) or 0,
            min_file_size=obj.get("min_file_size", 0) or 0,
            extension_whitelist=[v for v in obj.get("extension_whitelist") or []],
            extension_blacklist=[v for v in obj.get("extension_blacklist") or []],
        )


@dataclass
class Stats:
    active_count: int = 0
    queued_count: int = 0
    completed_count: int = 0
    total_count: int = 0
    version: str = ""

    @classmethod
    def from_json(cls, obj: dict) -> Stats:
        return cls(
            active_count=obj.get("active_count", 0) or 0,
            queued_count=obj.get("queued_count", 0) or 0,
            completed_count=obj.get("completed_count", 0) or 0,
            total_count=obj.get("total_count", 0) or 0,
            version=obj.get("version", ""),
        )


# ---------------------------------------------------------------------------
# Formatting helpers
# ---------------------------------------------------------------------------

_SIZE_UNITS = ("B", "KB", "MB", "GB", "TB")


def format_bytes(b: int) -> str:
    if b <= 0:
        return "Unknown"
    size = float(b)
    i = 0
    while size >= 1024.0 and i < 4:
        size /= 1024.0
        i += 1
    if i == 0:
        return f"{b} B"
    precision = 0 if size >= 100.0 else 1
    return f"{size:.{precision}f} {_SIZE_UNITS[i]}"


def format_speed(bytes_per_sec: float) -> str:
    if bytes_per_sec <= 0.0:
        return ""
    return format_bytes(int(bytes_per_sec)) + "/s"


def format_eta(remaining_bytes: int, speed: float) -> str:
    if speed <= 0.0 or remaining_bytes <= 0:
        return ""
    secs = int(remaining_bytes / speed)
    if secs < 60:
        return f"{secs}s"
    if secs < 3600:
        return f"{secs // 60}m{secs % 60}s"
    hours = secs // 3600
    mins = (secs % 3600) // 60
    return f"{hours}h{mins}m"


def status_display_text(status: str) -> str:
    return {
        "queued": "Queued",
        "active": "Downloading",
        "paused": "Paused",
        "completed": "Completed",
        "error": "Error",
        "refresh": "Needs Refresh",
        "verifying": "Verifying",
    }.get(status, status)


# ---------------------------------------------------------------------------
# File categories
# ---------------------------------------------------------------------------


class FileCategory(Enum):
    NONE = "none"
    COMPRESSED = "compressed"
    DOCUMENTS = "documents"
    MUSIC = "music"
    VIDEO = "video"
    IMAGES = "images"
    PROGRAMS = "programs"
    DISK_IMAGES = "disk_images"


_COMPOUND_COMPRESSED = (".tar.gz", ".tar.bz2", ".tar.xz")

_EXT_MAP: dict[str, FileCategory] = {}
for _ext in (".zip", ".tgz", ".gz", ".bz2", ".xz", ".7z", ".rar", ".tar"):
    _EXT_MAP[_ext] = FileCategory.COMPRESSED
for _ext in (".pdf", ".doc", ".docx", ".odt", ".txt", ".epub", ".xlsx", ".pptx", ".csv"):
    _EXT_MAP[_ext] = FileCategory.DOCUMENTS
for _ext in (".mp3", ".flac", ".ogg", ".wav", ".aac", ".opus", ".wma", ".m4a"):
    _EXT_MAP[_ext] = FileCategory.MUSIC
for _ext in (".mp4", ".mkv", ".avi", ".webm", ".mov", ".flv", ".wmv", ".m4v"):
    _EXT_MAP[_ext] = FileCategory.VIDEO
for _ext in (".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico", ".tiff"):
    _EXT_MAP[_ext] = FileCategory.IMAGES
for _ext in (".deb", ".rpm", ".appimage", ".flatpak", ".snap", ".bin", ".run", ".sh", ".exe", ".msi"):
    _EXT_MAP[_ext] = FileCategory.PROGRAMS
for _ext in (".iso", ".img"):
    _EXT_MAP[_ext] = FileCategory.DISK_IMAGES


def category_for_filename(filename: str) -> FileCategory:
    lower = filename.lower()
    for suffix in _COMPOUND_COMPRESSED:
        if lower.endswith(suffix):
            return FileCategory.COMPRESSED
    dot = lower.rfind(".")
    if dot < 0:
        return FileCategory.NONE
    return _EXT_MAP.get(lower[dot:], FileCategory.NONE)


_CATEGORY_NAMES: dict[FileCategory, str] = {
    FileCategory.COMPRESSED: "Compressed",
    FileCategory.DOCUMENTS: "Documents",
    FileCategory.MUSIC: "Music",
    FileCategory.VIDEO: "Video",
    FileCategory.IMAGES: "Images",
    FileCategory.PROGRAMS: "Programs",
    FileCategory.DISK_IMAGES: "Disk Images",
}


def category_display_name(cat: FileCategory) -> str:
    return _CATEGORY_NAMES.get(cat, "")
