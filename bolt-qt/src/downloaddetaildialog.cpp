#include "downloaddetaildialog.h"

#include <QDesktopServices>
#include <QFile>
#include <QFormLayout>
#include <QGroupBox>
#include <QHeaderView>
#include <QUrl>
#include <QVBoxLayout>

DownloadDetailDialog::DownloadDetailDialog(const QString &downloadId, DaemonClient *client, QWidget *parent)
    : QDialog(parent)
    , m_client(client)
    , m_downloadId(downloadId)
{
    setWindowTitle("Download Details");
    setMinimumWidth(550);

    auto *mainLayout = new QVBoxLayout(this);
    mainLayout->setSizeConstraint(QLayout::SetFixedSize);

    // Info section
    auto *infoGroup = new QGroupBox("Information");
    auto *infoLayout = new QFormLayout(infoGroup);
    infoLayout->setLabelAlignment(Qt::AlignLeft);
    infoLayout->setFormAlignment(Qt::AlignLeft | Qt::AlignTop);

    m_urlLabel = new QLabel();
    m_urlLabel->setWordWrap(true);
    m_urlLabel->setTextFormat(Qt::PlainText);
    m_urlLabel->setTextInteractionFlags(Qt::TextSelectableByMouse);
    m_urlLabel->setMinimumWidth(400);
    m_filenameLabel = new QLabel();
    m_statusLabel = new QLabel();
    m_sizeLabel = new QLabel();
    m_downloadedLabel = new QLabel();
    m_speedLabel = new QLabel();
    m_etaLabel = new QLabel();

    infoLayout->addRow("URL:", m_urlLabel);
    infoLayout->addRow("Filename:", m_filenameLabel);
    infoLayout->addRow("Status:", m_statusLabel);
    infoLayout->addRow("File size:", m_sizeLabel);
    infoLayout->addRow("Downloaded:", m_downloadedLabel);
    infoLayout->addRow("Speed:", m_speedLabel);
    infoLayout->addRow("ETA:", m_etaLabel);

    mainLayout->addWidget(infoGroup);

    // Error label (for failed downloads)
    m_errorLabel = new QLabel();
    m_errorLabel->setStyleSheet("color: red;");
    m_errorLabel->setWordWrap(true);
    m_errorLabel->hide();
    mainLayout->addWidget(m_errorLabel);

    // Progress bar
    m_progressBar = new QProgressBar();
    m_progressBar->setRange(0, 100);
    mainLayout->addWidget(m_progressBar);

    // Button row: Show Details / Pause/Resume / Close
    auto *buttonLayout = new QHBoxLayout();
    m_toggleDetailsBtn = new QPushButton("Show Details >>");
    m_pauseResumeBtn = new QPushButton("Pause");
    m_pauseResumeBtn->hide();
    auto *closeBtn = new QPushButton("Close");
    buttonLayout->addWidget(m_toggleDetailsBtn);
    buttonLayout->addStretch();
    m_openFileBtn = new QPushButton("Open File");
    m_openFolderBtn = new QPushButton("Open Folder");
    m_openFileBtn->hide();
    m_openFolderBtn->hide();
    buttonLayout->addWidget(m_openFileBtn);
    buttonLayout->addWidget(m_openFolderBtn);
    buttonLayout->addWidget(m_pauseResumeBtn);
    buttonLayout->addWidget(closeBtn);
    mainLayout->addLayout(buttonLayout);

    // File warning
    m_fileWarningLabel = new QLabel("File not found on disk.");
    m_fileWarningLabel->setStyleSheet("color: orange;");
    m_fileWarningLabel->hide();
    mainLayout->addWidget(m_fileWarningLabel);

    // Detail section (hidden by default)
    m_detailSection = new QWidget();
    auto *detailLayout = new QVBoxLayout(m_detailSection);
    detailLayout->setContentsMargins(0, 0, 0, 0);

    m_segLabel = new QLabel("Segment progress by connections");
    m_segLabel->setAlignment(Qt::AlignCenter);
    m_segLabel->setStyleSheet("color: gray; font-size: 11px;");
    detailLayout->addWidget(m_segLabel);

    m_segmentProgressWidget = new SegmentProgressWidget();
    detailLayout->addWidget(m_segmentProgressWidget);

    m_segmentTable = new QTableWidget();
    m_segmentTable->setColumnCount(3);
    m_segmentTable->setHorizontalHeaderLabels({"N.", "Downloaded", "Info"});
    m_segmentTable->horizontalHeader()->setStretchLastSection(true);
    m_segmentTable->verticalHeader()->hide();
    m_segmentTable->setEditTriggers(QAbstractItemView::NoEditTriggers);
    m_segmentTable->setSelectionMode(QAbstractItemView::NoSelection);
    m_segmentTable->setColumnWidth(0, 40);
    m_segmentTable->setColumnWidth(1, 100);
    detailLayout->addWidget(m_segmentTable);

    m_detailSection->hide();
    mainLayout->addWidget(m_detailSection);

    // Connections
    connect(closeBtn, &QPushButton::clicked, this, &QDialog::accept);
    connect(m_openFileBtn, &QPushButton::clicked, this, &DownloadDetailDialog::onOpenFile);
    connect(m_openFolderBtn, &QPushButton::clicked, this, &DownloadDetailDialog::onOpenFolder);

    connect(m_toggleDetailsBtn, &QPushButton::clicked, this, [this]() {
        bool showing = m_detailSection->isVisible();
        m_detailSection->setVisible(!showing);
        m_toggleDetailsBtn->setText(showing ? "Show Details >>" : "<< Hide Details");
    });

    connect(m_pauseResumeBtn, &QPushButton::clicked, this, [this]() {
        if (!m_client->isConnected())
            return;
        if (m_pauseResumeBtn->text() == "Pause")
            m_client->pauseDownload(m_downloadId);
        else
            m_client->resumeDownload(m_downloadId);
    });
    connect(m_client, &DaemonClient::downloadDetailFetched, this, &DownloadDetailDialog::onDetailFetched);

    // Poll timer for live updates
    m_pollTimer = new QTimer(this);
    m_pollTimer->setInterval(1000);
    connect(m_pollTimer, &QTimer::timeout, this, &DownloadDetailDialog::fetchDetail);

    // Initial fetch
    fetchDetail();
}

void DownloadDetailDialog::fetchDetail() {
    if (m_client->isConnected())
        m_client->fetchDownloadDetail(m_downloadId);
}

void DownloadDetailDialog::onDetailFetched(Download dl, QVector<Segment> segments) {
    // Only handle responses for our download
    if (dl.id != m_downloadId)
        return;

    updateUI(dl, segments);

    // Poll for any non-terminal status (so resume from paused state is reflected)
    bool terminal = (dl.status == "completed" || dl.status == "error" || dl.status == "refresh");
    if (!terminal) {
        if (!m_pollTimer->isActive())
            m_pollTimer->start();
    } else {
        m_pollTimer->stop();
    }
}

void DownloadDetailDialog::updateUI(const Download &dl, const QVector<Segment> &segments) {
    m_filePath = dl.dir + "/" + dl.filename;
    m_dirPath = dl.dir;

    m_urlLabel->setText(dl.url);
    m_filenameLabel->setText(dl.filename);
    m_statusLabel->setText(statusDisplayText(dl.status));
    m_sizeLabel->setText(formatBytes(dl.totalSize));

    // Calculate speed
    if (dl.status == "active" && m_prevDownloaded >= 0) {
        qint64 delta = dl.downloaded - m_prevDownloaded;
        double instantSpeed = static_cast<double>(delta);
        m_speed = (m_speed > 0.0) ? 0.3 * instantSpeed + 0.7 * m_speed : instantSpeed;
    } else if (dl.status != "active") {
        m_speed = 0.0;
    }
    m_prevDownloaded = dl.downloaded;

    if (dl.totalSize > 0) {
        int pct = static_cast<int>(dl.downloaded * 100 / dl.totalSize);
        m_downloadedLabel->setText(QString("%1 / %2 (%3%)")
            .arg(formatBytes(dl.downloaded))
            .arg(formatBytes(dl.totalSize))
            .arg(pct));
        m_progressBar->setValue(pct);
        m_progressBar->show();
    } else {
        m_downloadedLabel->setText(formatBytes(dl.downloaded));
        m_progressBar->hide();
    }

    if (dl.status == "active" && m_speed > 0.0) {
        m_speedLabel->setText(formatSpeed(m_speed));
        if (dl.totalSize > 0) {
            qint64 remaining = dl.totalSize - dl.downloaded;
            m_etaLabel->setText(formatEta(remaining, m_speed));
        } else {
            m_etaLabel->setText(QString());
        }
    } else {
        m_speedLabel->setText(QString());
        m_etaLabel->setText(QString());
    }

    // Error
    if (dl.status == "error" && !dl.error.isEmpty()) {
        m_errorLabel->setText("Error: " + dl.error);
        m_errorLabel->show();
    } else {
        m_errorLabel->hide();
    }

    // Segments
    m_segmentProgressWidget->setSegments(segments, dl.totalSize);

    m_segmentTable->setRowCount(segments.size());
    for (int i = 0; i < segments.size(); i++) {
        const Segment &seg = segments[i];
        m_segmentTable->setItem(i, 0, new QTableWidgetItem(QString::number(seg.index + 1)));
        m_segmentTable->setItem(i, 1, new QTableWidgetItem(formatBytes(seg.downloaded)));

        QString info;
        if (seg.done)
            info = "Done";
        else if (dl.status == "active" && seg.downloaded > 0)
            info = "Receiving data...";
        else if (dl.status == "active")
            info = "Connecting...";
        else if (dl.status == "paused")
            info = "Paused";
        else if (dl.status == "queued")
            info = "Waiting";
        else
            info = statusDisplayText(dl.status);
        m_segmentTable->setItem(i, 2, new QTableWidgetItem(info));
    }

    // Pause/Resume button
    if (dl.status == "active") {
        m_pauseResumeBtn->setText("Pause");
        m_pauseResumeBtn->show();
    } else if (dl.status == "paused") {
        m_pauseResumeBtn->setText("Resume");
        m_pauseResumeBtn->show();
    } else {
        m_pauseResumeBtn->hide();
    }

    // Completed: show open buttons, check file exists
    if (dl.status == "completed") {
        m_openFileBtn->show();
        m_openFolderBtn->show();
        m_progressBar->setValue(100);

        if (!QFile::exists(m_filePath)) {
            m_fileWarningLabel->show();
            m_openFileBtn->setEnabled(false);
        } else {
            m_fileWarningLabel->hide();
            m_openFileBtn->setEnabled(true);
        }
    } else {
        m_openFileBtn->hide();
        m_openFolderBtn->hide();
        m_fileWarningLabel->hide();
    }
}

void DownloadDetailDialog::onOpenFile() {
    QDesktopServices::openUrl(QUrl::fromLocalFile(m_filePath));
}

void DownloadDetailDialog::onOpenFolder() {
    QDesktopServices::openUrl(QUrl::fromLocalFile(m_dirPath));
}
