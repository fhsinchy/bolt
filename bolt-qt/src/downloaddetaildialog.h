#pragma once

#include "daemonclient.h"
#include "types.h"

#include <QDialog>
#include <QLabel>
#include <QPainter>
#include <QProgressBar>
#include <QPushButton>
#include <QTableWidget>
#include <QTimer>
#include <QWidget>

// Visual segment progress bar — shows each segment as a colored block
class SegmentProgressWidget : public QWidget {
    Q_OBJECT
public:
    explicit SegmentProgressWidget(QWidget *parent = nullptr) : QWidget(parent) {
        setMinimumHeight(20);
        setMaximumHeight(20);
    }

    void setSegments(const QVector<Segment> &segments, qint64 totalSize) {
        m_segments = segments;
        m_totalSize = totalSize;
        update();
    }

protected:
    void paintEvent(QPaintEvent *) override {
        QPainter p(this);
        p.setRenderHint(QPainter::Antialiasing);

        QRect r = rect();
        p.fillRect(r, QColor(40, 40, 40));

        if (m_segments.isEmpty() || m_totalSize <= 0)
            return;

        for (const Segment &seg : m_segments) {
            if (seg.endByte < 0)
                continue;
            qint64 segSize = seg.endByte - seg.startByte + 1;
            if (segSize <= 0)
                continue;

            double startFrac = static_cast<double>(seg.startByte) / m_totalSize;
            double dlFrac = static_cast<double>(seg.downloaded) / m_totalSize;

            int x = static_cast<int>(startFrac * r.width());
            int w = static_cast<int>(dlFrac * r.width());
            if (w < 1 && seg.downloaded > 0)
                w = 1;

            QColor color = seg.done ? QColor(80, 180, 80) : QColor(70, 130, 210);
            p.fillRect(x, 0, w, r.height(), color);
        }
    }

private:
    QVector<Segment> m_segments;
    qint64 m_totalSize = 0;
};

class DownloadDetailDialog : public QDialog {
    Q_OBJECT

public:
    explicit DownloadDetailDialog(const QString &downloadId, DaemonClient *client, QWidget *parent = nullptr);

private slots:
    void onDetailFetched(Download dl, QVector<Segment> segments);
    void onOpenFile();
    void onOpenFolder();

private:
    void fetchDetail();
    void updateUI(const Download &dl, const QVector<Segment> &segments);

    DaemonClient *m_client;
    QString m_downloadId;
    QTimer *m_pollTimer;

    // Info labels
    QLabel *m_urlLabel;
    QLabel *m_filenameLabel;
    QLabel *m_statusLabel;
    QLabel *m_sizeLabel;
    QLabel *m_downloadedLabel;
    QLabel *m_speedLabel;
    QLabel *m_etaLabel;
    QLabel *m_errorLabel;
    QLabel *m_fileWarningLabel;

    // Progress
    QProgressBar *m_progressBar;

    // Segments
    SegmentProgressWidget *m_segmentProgressWidget;
    QTableWidget *m_segmentTable;

    // Actions
    QPushButton *m_openFileBtn;
    QPushButton *m_openFolderBtn;
    QPushButton *m_toggleDetailsBtn;
    QPushButton *m_pauseResumeBtn;

    // Detail section widgets (toggled by Show/Hide Details)
    QLabel *m_segLabel;
    QWidget *m_detailSection; // container for segment bar + table

    // Track for file operations
    QString m_filePath;
    QString m_dirPath;

    // Track for speed calculation
    qint64 m_prevDownloaded = -1;
    double m_speed = 0.0;
};
