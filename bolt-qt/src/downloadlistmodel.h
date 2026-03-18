#pragma once

#include "types.h"

#include <QAbstractTableModel>
#include <QHash>
#include <QModelIndexList>
#include <QVector>

class DownloadListModel : public QAbstractTableModel {
    Q_OBJECT

public:
    enum Column {
        ColFilename,
        ColSize,
        ColProgress,
        ColSpeed,
        ColEta,
        ColStatus,
        ColCount
    };

    explicit DownloadListModel(QObject *parent = nullptr);

    int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    int columnCount(const QModelIndex &parent = QModelIndex()) const override;
    QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    QVariant headerData(int section, Qt::Orientation orientation,
                        int role = Qt::DisplayRole) const override;

    const Download &downloadAt(int row) const;
    QString downloadIdAt(int row) const;
    QStringList selectedIds(const QModelIndexList &indexes) const;

public slots:
    void updateFromPoll(const QVector<Download> &incoming);

private:
    void updateSpeed(const Download &dl);

    QVector<Download> m_downloads;
    QHash<QString, qint64> m_prevDownloaded;
    QHash<QString, double> m_speeds;
};
