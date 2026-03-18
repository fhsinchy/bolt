#include "downloadlistmodel.h"

DownloadListModel::DownloadListModel(QObject *parent)
    : QAbstractTableModel(parent)
{
}

int DownloadListModel::rowCount(const QModelIndex &parent) const {
    if (parent.isValid())
        return 0;
    return m_downloads.size();
}

int DownloadListModel::columnCount(const QModelIndex &parent) const {
    if (parent.isValid())
        return 0;
    return ColCount;
}

QVariant DownloadListModel::data(const QModelIndex &index, int role) const {
    if (!index.isValid() || index.row() >= m_downloads.size())
        return {};

    const Download &dl = m_downloads[index.row()];

    if (role == Qt::DisplayRole) {
        switch (index.column()) {
        case ColFilename:
            return dl.filename;
        case ColSize:
            return formatBytes(dl.totalSize);
        case ColProgress:
            if (dl.totalSize <= 0)
                return 0;
            return static_cast<int>(dl.downloaded * 100 / dl.totalSize);
        case ColSpeed: {
            double speed = m_speeds.value(dl.id, 0.0);
            if (dl.status != "active" || speed <= 0.0)
                return QString();
            return formatSpeed(speed);
        }
        case ColEta: {
            double speed = m_speeds.value(dl.id, 0.0);
            if (dl.status != "active" || speed <= 0.0 || dl.totalSize <= 0)
                return QString();
            return formatEta(dl.totalSize - dl.downloaded, speed);
        }
        case ColStatus:
            return statusDisplayText(dl.status);
        }
    }

    if (role == Qt::ToolTipRole) {
        if (index.column() == ColStatus && dl.status == "refresh")
            return QStringLiteral("This download needs a new URL. Refresh UI planned for a future version.");
        if (index.column() == ColStatus && dl.status == "error" && !dl.error.isEmpty())
            return dl.error;
        if (index.column() == ColFilename)
            return dl.filename;
    }

    return {};
}

QVariant DownloadListModel::headerData(int section, Qt::Orientation orientation, int role) const {
    if (orientation != Qt::Horizontal || role != Qt::DisplayRole)
        return {};

    switch (section) {
    case ColFilename:  return QStringLiteral("Filename");
    case ColSize:      return QStringLiteral("Size");
    case ColProgress:  return QStringLiteral("Progress");
    case ColSpeed:     return QStringLiteral("Speed");
    case ColEta:       return QStringLiteral("ETA");
    case ColStatus:    return QStringLiteral("Status");
    }
    return {};
}

const Download &DownloadListModel::downloadAt(int row) const {
    Q_ASSERT(row >= 0 && row < m_downloads.size());
    return m_downloads[row];
}

QString DownloadListModel::downloadIdAt(int row) const {
    if (row < 0 || row >= m_downloads.size())
        return {};
    return m_downloads[row].id;
}

QStringList DownloadListModel::selectedIds(const QModelIndexList &indexes) const {
    QSet<int> rows;
    for (const QModelIndex &idx : indexes)
        rows.insert(idx.row());

    QStringList ids;
    for (int row : rows)
        if (row >= 0 && row < m_downloads.size())
            ids.append(m_downloads[row].id);
    return ids;
}

void DownloadListModel::resetSpeeds() {
    m_prevDownloaded.clear();
    m_speeds.clear();
}

void DownloadListModel::updateSpeed(const Download &dl) {
    if (dl.status == "active" && m_prevDownloaded.contains(dl.id)) {
        qint64 delta = dl.downloaded - m_prevDownloaded[dl.id];
        double instantSpeed = static_cast<double>(delta);
        double prev = m_speeds.value(dl.id, 0.0);
        m_speeds[dl.id] = (prev > 0.0)
            ? 0.3 * instantSpeed + 0.7 * prev
            : instantSpeed;
    } else if (dl.status != "active") {
        m_speeds.remove(dl.id);
    }
    m_prevDownloaded[dl.id] = dl.downloaded;
}

void DownloadListModel::updateFromPoll(const QVector<Download> &incoming) {
    // Build lookup of incoming by ID
    QHash<QString, int> incomingById;
    for (int i = 0; i < incoming.size(); i++)
        incomingById[incoming[i].id] = i;

    // Remove rows whose IDs are absent from incoming (walk backwards)
    for (int i = m_downloads.size() - 1; i >= 0; i--) {
        if (!incomingById.contains(m_downloads[i].id)) {
            beginRemoveRows(QModelIndex(), i, i);
            m_prevDownloaded.remove(m_downloads[i].id);
            m_speeds.remove(m_downloads[i].id);
            m_downloads.removeAt(i);
            endRemoveRows();
        }
    }

    // Build lookup of current by ID
    QHash<QString, int> currentById;
    for (int i = 0; i < m_downloads.size(); i++)
        currentById[m_downloads[i].id] = i;

    // Check if we need to handle new rows or reordering
    bool hasNewRows = false;
    bool orderChanged = false;

    for (int i = 0; i < incoming.size(); i++) {
        if (!currentById.contains(incoming[i].id)) {
            hasNewRows = true;
            break;
        }
    }

    // Check order
    if (!hasNewRows && m_downloads.size() == incoming.size()) {
        for (int i = 0; i < incoming.size(); i++) {
            if (m_downloads[i].id != incoming[i].id) {
                orderChanged = true;
                break;
            }
        }
    }

    if (hasNewRows || orderChanged) {
        // Full replace — simpler than merging insertions
        beginResetModel();

        for (const Download &dl : incoming)
            updateSpeed(dl);

        // Clean stale entries from hashes
        QSet<QString> incomingIds;
        for (const Download &dl : incoming)
            incomingIds.insert(dl.id);
        for (auto it = m_prevDownloaded.begin(); it != m_prevDownloaded.end(); ) {
            if (!incomingIds.contains(it.key()))
                it = m_prevDownloaded.erase(it);
            else
                ++it;
        }
        for (auto it = m_speeds.begin(); it != m_speeds.end(); ) {
            if (!incomingIds.contains(it.key()))
                it = m_speeds.erase(it);
            else
                ++it;
        }

        m_downloads = incoming;
        endResetModel();
        return;
    }

    // Update existing rows in-place, only emit dataChanged for rows that differ
    for (int i = 0; i < m_downloads.size(); i++) {
        const Download &dl = incoming[incomingById[m_downloads[i].id]];
        updateSpeed(dl);

        bool changed = m_downloads[i].downloaded != dl.downloaded
                    || m_downloads[i].status != dl.status
                    || m_downloads[i].filename != dl.filename
                    || m_downloads[i].totalSize != dl.totalSize;
        m_downloads[i] = dl;
        if (changed)
            emit dataChanged(index(i, 0), index(i, ColCount - 1));
    }
}
