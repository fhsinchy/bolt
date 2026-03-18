#include "adddownloaddialog.h"

#include <QClipboard>
#include <QFileDialog>
#include <QFormLayout>
#include <QGroupBox>
#include <QGuiApplication>
#include <QHBoxLayout>
#include <QMessageBox>
#include <QVBoxLayout>

AddDownloadDialog::AddDownloadDialog(DaemonClient *client, QWidget *parent)
    : QDialog(parent)
    , m_client(client)
{
    setWindowTitle("Add Download");
    setMinimumWidth(500);

    auto *mainLayout = new QVBoxLayout(this);

    // URL row
    auto *urlLayout = new QHBoxLayout();
    m_urlEdit = new QLineEdit();
    m_urlEdit->setPlaceholderText("Enter URL...");
    m_probeButton = new QPushButton("Probe");
    urlLayout->addWidget(m_urlEdit, 1);
    urlLayout->addWidget(m_probeButton);

    auto *urlForm = new QFormLayout();
    urlForm->addRow("URL:", urlLayout);
    mainLayout->addLayout(urlForm);

    // Probe results group
    auto *probeGroup = new QGroupBox("Probe Results");
    auto *probeLayout = new QFormLayout(probeGroup);
    m_filenameEdit = new QLineEdit();
    m_sizeLabel = new QLabel();
    m_resumableLabel = new QLabel();
    probeLayout->addRow("Filename:", m_filenameEdit);
    probeLayout->addRow("Size:", m_sizeLabel);
    probeLayout->addRow("Resumable:", m_resumableLabel);
    mainLayout->addWidget(probeGroup);

    // Options group
    auto *optionsGroup = new QGroupBox("Options");
    auto *optionsLayout = new QFormLayout(optionsGroup);
    auto *dirLayout = new QHBoxLayout();
    m_dirEdit = new QLineEdit();
    m_browseButton = new QPushButton("Browse");
    dirLayout->addWidget(m_dirEdit, 1);
    dirLayout->addWidget(m_browseButton);
    m_segmentsSpin = new QSpinBox();
    m_segmentsSpin->setRange(1, 32);
    m_segmentsSpin->setValue(16);
    optionsLayout->addRow("Save to:", dirLayout);
    optionsLayout->addRow("Segments:", m_segmentsSpin);
    mainLayout->addWidget(optionsGroup);

    // Error label
    m_errorLabel = new QLabel();
    m_errorLabel->setStyleSheet("color: red;");
    m_errorLabel->setWordWrap(true);
    m_errorLabel->hide();
    mainLayout->addWidget(m_errorLabel);

    // Buttons
    auto *buttonLayout = new QHBoxLayout();
    buttonLayout->addStretch();
    m_cancelButton = new QPushButton("Cancel");
    m_downloadButton = new QPushButton("Download");
    m_downloadButton->setDefault(true);
    buttonLayout->addWidget(m_cancelButton);
    buttonLayout->addWidget(m_downloadButton);
    mainLayout->addLayout(buttonLayout);

    // Connections
    connect(m_probeButton, &QPushButton::clicked, this, &AddDownloadDialog::onProbe);
    connect(m_urlEdit, &QLineEdit::returnPressed, this, &AddDownloadDialog::onProbe);
    connect(m_downloadButton, &QPushButton::clicked, this, &AddDownloadDialog::onDownload);
    connect(m_cancelButton, &QPushButton::clicked, this, &QDialog::reject);
    connect(m_browseButton, &QPushButton::clicked, this, [this]() {
        QString dir = QFileDialog::getExistingDirectory(this, "Select Directory", m_dirEdit->text());
        if (!dir.isEmpty())
            m_dirEdit->setText(dir);
    });

    connect(m_client, &DaemonClient::probeCompleted, this, &AddDownloadDialog::onProbeCompleted);
    connect(m_client, &DaemonClient::probeFailed, this, &AddDownloadDialog::onProbeFailed);
    connect(m_client, &DaemonClient::downloadAdded, this, &AddDownloadDialog::onDownloadAdded);
    connect(m_client, &DaemonClient::requestFailed, this, &AddDownloadDialog::onRequestFailed);
    connect(m_client, &DaemonClient::configFetched, this, &AddDownloadDialog::onConfigFetched);

    // Check clipboard for URL
    QString clipText = QGuiApplication::clipboard()->text().trimmed();
    if (clipText.startsWith("http://") || clipText.startsWith("https://"))
        m_urlEdit->setText(clipText);

    // Fetch config for defaults
    m_client->fetchConfig();
}

AddDownloadDialog::~AddDownloadDialog() {
    disconnect(m_client, &DaemonClient::probeCompleted, this, &AddDownloadDialog::onProbeCompleted);
    disconnect(m_client, &DaemonClient::probeFailed, this, &AddDownloadDialog::onProbeFailed);
    disconnect(m_client, &DaemonClient::downloadAdded, this, &AddDownloadDialog::onDownloadAdded);
    disconnect(m_client, &DaemonClient::requestFailed, this, &AddDownloadDialog::onRequestFailed);
    disconnect(m_client, &DaemonClient::configFetched, this, &AddDownloadDialog::onConfigFetched);
}

void AddDownloadDialog::onProbe() {
    QString url = m_urlEdit->text().trimmed();
    if (url.isEmpty())
        return;
    if (!m_client->isConnected()) {
        m_errorLabel->setText("Not connected to daemon");
        m_errorLabel->show();
        return;
    }
    m_errorLabel->hide();
    m_probeButton->setEnabled(false);
    m_probeButton->setText("Probing...");
    m_client->probeUrl(url);
}

void AddDownloadDialog::onProbeCompleted(ProbeResult result) {
    m_probeButton->setEnabled(true);
    m_probeButton->setText("Probe");
    m_filenameEdit->setText(result.filename);
    m_sizeLabel->setText(formatBytes(result.totalSize));
    m_resumableLabel->setText(result.acceptsRanges ? "Yes" : "No");
    m_errorLabel->hide();
}

void AddDownloadDialog::onProbeFailed(QString error) {
    m_probeButton->setEnabled(true);
    m_probeButton->setText("Probe");
    m_errorLabel->setText(error);
    m_errorLabel->show();
}

void AddDownloadDialog::onDownload() {
    QString url = m_urlEdit->text().trimmed();
    if (url.isEmpty())
        return;
    if (!m_client->isConnected()) {
        m_errorLabel->setText("Not connected to daemon");
        m_errorLabel->show();
        return;
    }

    AddRequest req;
    req.url = url;
    req.filename = m_filenameEdit->text().trimmed();
    req.dir = m_dirEdit->text().trimmed();
    req.segments = m_segmentsSpin->value();
    req.force = m_force;

    m_errorLabel->hide();
    m_downloadButton->setEnabled(false);
    m_client->addDownload(req);
}

void AddDownloadDialog::onDownloadAdded(Download) {
    accept();
}

void AddDownloadDialog::onRequestFailed(QString endpoint, int, QString errorCode, QString errorMessage) {
    if (endpoint != "addDownload")
        return;

    m_downloadButton->setEnabled(true);

    if (errorCode == "DUPLICATE_FILENAME") {
        auto reply = QMessageBox::question(this, "File Exists",
            "File already exists. Download anyway?",
            QMessageBox::Yes | QMessageBox::No);
        if (reply == QMessageBox::Yes) {
            m_force = true;
            onDownload();
        }
        return;
    }

    m_errorLabel->setText(errorMessage);
    m_errorLabel->show();
}

void AddDownloadDialog::onConfigFetched(Config cfg) {
    m_dirEdit->setText(cfg.downloadDir);
    m_segmentsSpin->setValue(cfg.defaultSegments);
}
