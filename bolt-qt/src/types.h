#pragma once

#include <QDateTime>
#include <QJsonObject>
#include <QString>
#include <QVector>

struct Download {
    QString id;
    QString url;
    QString filename;
    QString dir;
    qint64 totalSize = 0;
    qint64 downloaded = 0;
    QString status;
    int segmentCount = 0;
    qint64 speedLimit = 0;
    QString error;
    QDateTime createdAt;
    QDateTime completedAt;
    int queueOrder = 0;

    static Download fromJson(const QJsonObject &obj) {
        Download d;
        d.id = obj["id"].toString();
        d.url = obj["url"].toString();
        d.filename = obj["filename"].toString();
        d.dir = obj["dir"].toString();
        d.totalSize = obj["total_size"].toInteger();
        d.downloaded = obj["downloaded"].toInteger();
        d.status = obj["status"].toString();
        d.segmentCount = obj["segments"].toInt();
        d.speedLimit = obj["speed_limit"].toInteger();
        d.error = obj["error"].toString();
        d.createdAt = QDateTime::fromString(obj["created_at"].toString(), Qt::ISODate);
        if (obj.contains("completed_at") && !obj["completed_at"].isNull())
            d.completedAt = QDateTime::fromString(obj["completed_at"].toString(), Qt::ISODate);
        d.queueOrder = obj["queue_order"].toInt();
        return d;
    }
};

struct Segment {
    QString downloadId;
    int index = 0;
    qint64 startByte = 0;
    qint64 endByte = 0;
    qint64 downloaded = 0;
    bool done = false;

    static Segment fromJson(const QJsonObject &obj) {
        Segment s;
        s.downloadId = obj["download_id"].toString();
        s.index = obj["index"].toInt();
        s.startByte = obj["start_byte"].toInteger();
        s.endByte = obj["end_byte"].toInteger();
        s.downloaded = obj["downloaded"].toInteger();
        s.done = obj["done"].toBool();
        return s;
    }
};

struct AddRequest {
    QString url;
    QString traceId = "gui-qt";
    QString filename;
    QString dir;
    int segments = 0;
    qint64 speedLimit = 0;
    bool force = false;
    bool paused = false;

    QJsonObject toJson() const {
        QJsonObject obj;
        obj["url"] = url;
        obj["trace_id"] = traceId;
        if (!filename.isEmpty())
            obj["filename"] = filename;
        if (!dir.isEmpty())
            obj["dir"] = dir;
        if (segments > 0)
            obj["segments"] = segments;
        if (speedLimit > 0)
            obj["speed_limit"] = speedLimit;
        if (force)
            obj["force"] = true;
        if (paused)
            obj["paused"] = true;
        return obj;
    }
};

struct ProbeResult {
    QString filename;
    qint64 totalSize = 0;
    bool acceptsRanges = false;
    QString finalUrl;
    QString contentType;

    static ProbeResult fromJson(const QJsonObject &obj) {
        ProbeResult r;
        r.filename = obj["filename"].toString();
        r.totalSize = obj["total_size"].toInteger();
        r.acceptsRanges = obj["accepts_ranges"].toBool();
        r.finalUrl = obj["final_url"].toString();
        r.contentType = obj["content_type"].toString();
        return r;
    }
};

struct Config {
    QString downloadDir;
    int maxConcurrent = 0;
    int defaultSegments = 0;
    qint64 globalSpeedLimit = 0;
    bool notifications = true;
    int maxRetries = 0;
    qint64 minSegmentSize = 0;

    static Config fromJson(const QJsonObject &obj) {
        Config c;
        c.downloadDir = obj["download_dir"].toString();
        c.maxConcurrent = obj["max_concurrent"].toInt();
        c.defaultSegments = obj["default_segments"].toInt();
        c.globalSpeedLimit = obj["global_speed_limit"].toInteger();
        c.notifications = obj["notifications"].toBool();
        c.maxRetries = obj["max_retries"].toInt();
        c.minSegmentSize = obj["min_segment_size"].toInteger();
        return c;
    }
};

struct Stats {
    int activeCount = 0;
    int queuedCount = 0;
    int completedCount = 0;
    int totalCount = 0;
    QString version;

    static Stats fromJson(const QJsonObject &obj) {
        Stats s;
        s.activeCount = obj["active_count"].toInt();
        s.queuedCount = obj["queued_count"].toInt();
        s.completedCount = obj["completed_count"].toInt();
        s.totalCount = obj["total_count"].toInt();
        s.version = obj["version"].toString();
        return s;
    }
};

// Formatting helpers

inline QString formatBytes(qint64 bytes) {
    if (bytes <= 0)
        return QStringLiteral("Unknown");
    const char *units[] = {"B", "KB", "MB", "GB", "TB"};
    int i = 0;
    double size = bytes;
    while (size >= 1024.0 && i < 4) {
        size /= 1024.0;
        i++;
    }
    if (i == 0)
        return QString::number(bytes) + " B";
    return QString::number(size, 'f', (size >= 100.0) ? 0 : 1) + " " + units[i];
}

inline QString formatSpeed(double bytesPerSec) {
    if (bytesPerSec <= 0.0)
        return QString();
    return formatBytes(static_cast<qint64>(bytesPerSec)) + "/s";
}

inline QString formatEta(qint64 remainingBytes, double speed) {
    if (speed <= 0.0 || remainingBytes <= 0)
        return QString();
    qint64 secs = static_cast<qint64>(remainingBytes / speed);
    if (secs < 60)
        return QString::number(secs) + "s";
    if (secs < 3600)
        return QString::number(secs / 60) + "m" + QString::number(secs % 60) + "s";
    qint64 hours = secs / 3600;
    qint64 mins = (secs % 3600) / 60;
    return QString::number(hours) + "h" + QString::number(mins) + "m";
}

inline QString statusDisplayText(const QString &status) {
    if (status == "queued") return QStringLiteral("Queued");
    if (status == "active") return QStringLiteral("Downloading");
    if (status == "paused") return QStringLiteral("Paused");
    if (status == "completed") return QStringLiteral("Completed");
    if (status == "error") return QStringLiteral("Error");
    if (status == "refresh") return QStringLiteral("Needs Refresh");
    if (status == "verifying") return QStringLiteral("Verifying");
    return status;
}

enum class FileCategory {
    None,
    Compressed,
    Documents,
    Music,
    Video,
    Images,
    Programs,
    DiskImages,
};

inline FileCategory categoryForFilename(const QString &filename) {
    QString lower = filename.toLower();

    // Handle compound extensions first
    if (lower.endsWith(".tar.gz") || lower.endsWith(".tar.bz2") || lower.endsWith(".tar.xz"))
        return FileCategory::Compressed;

    int dot = lower.lastIndexOf('.');
    if (dot < 0)
        return FileCategory::None;
    QString ext = lower.mid(dot);

    // Compressed
    if (ext == ".zip" || ext == ".tgz" || ext == ".gz" || ext == ".bz2"
        || ext == ".xz" || ext == ".7z" || ext == ".rar" || ext == ".tar")
        return FileCategory::Compressed;

    // Documents
    if (ext == ".pdf" || ext == ".doc" || ext == ".docx" || ext == ".odt"
        || ext == ".txt" || ext == ".epub" || ext == ".xlsx" || ext == ".pptx" || ext == ".csv")
        return FileCategory::Documents;

    // Music
    if (ext == ".mp3" || ext == ".flac" || ext == ".ogg" || ext == ".wav"
        || ext == ".aac" || ext == ".opus" || ext == ".wma" || ext == ".m4a")
        return FileCategory::Music;

    // Video
    if (ext == ".mp4" || ext == ".mkv" || ext == ".avi" || ext == ".webm"
        || ext == ".mov" || ext == ".flv" || ext == ".wmv" || ext == ".m4v")
        return FileCategory::Video;

    // Images
    if (ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif"
        || ext == ".webp" || ext == ".svg" || ext == ".bmp" || ext == ".ico" || ext == ".tiff")
        return FileCategory::Images;

    // Programs
    if (ext == ".deb" || ext == ".rpm" || ext == ".appimage" || ext == ".flatpak"
        || ext == ".snap" || ext == ".bin" || ext == ".run" || ext == ".sh"
        || ext == ".exe" || ext == ".msi")
        return FileCategory::Programs;

    // Disk Images
    if (ext == ".iso" || ext == ".img")
        return FileCategory::DiskImages;

    return FileCategory::None;
}

inline QString categoryDisplayName(FileCategory cat) {
    switch (cat) {
    case FileCategory::Compressed:  return QStringLiteral("Compressed");
    case FileCategory::Documents:   return QStringLiteral("Documents");
    case FileCategory::Music:       return QStringLiteral("Music");
    case FileCategory::Video:       return QStringLiteral("Video");
    case FileCategory::Images:      return QStringLiteral("Images");
    case FileCategory::Programs:    return QStringLiteral("Programs");
    case FileCategory::DiskImages:  return QStringLiteral("Disk Images");
    default:                        return QString();
    }
}
