#include "settingsdialog.h"

#include <cmath>

#include <QDialogButtonBox>
#include <QFileDialog>
#include <QFormLayout>
#include <QHBoxLayout>
#include <QVBoxLayout>

SettingsDialog::SettingsDialog(DaemonClient *client, QWidget *parent)
    : QDialog(parent)
    , m_client(client)
{
    setWindowTitle("Settings");
    setMinimumWidth(450);

    auto *mainLayout = new QVBoxLayout(this);
    auto *form = new QFormLayout();

    // Download directory
    auto *dirLayout = new QHBoxLayout();
    m_dirEdit = new QLineEdit();
    auto *browseButton = new QPushButton("Browse");
    dirLayout->addWidget(m_dirEdit, 1);
    dirLayout->addWidget(browseButton);
    form->addRow("Download directory:", dirLayout);

    // Max concurrent
    m_maxConcurrentSpin = new QSpinBox();
    m_maxConcurrentSpin->setRange(1, 10);
    form->addRow("Max concurrent:", m_maxConcurrentSpin);

    // Default segments
    m_defaultSegmentsSpin = new QSpinBox();
    m_defaultSegmentsSpin->setRange(1, 32);
    form->addRow("Default segments:", m_defaultSegmentsSpin);

    // Global speed limit (MB/s)
    m_speedLimitSpin = new QDoubleSpinBox();
    m_speedLimitSpin->setRange(0.0, 100000.0);
    m_speedLimitSpin->setDecimals(1);
    m_speedLimitSpin->setSuffix(" MB/s");
    m_speedLimitSpin->setSpecialValueText("Unlimited");
    form->addRow("Global speed limit:", m_speedLimitSpin);

    // Max retries
    m_maxRetriesSpin = new QSpinBox();
    m_maxRetriesSpin->setRange(0, 100);
    form->addRow("Max retries:", m_maxRetriesSpin);

    // Min segment size (MB)
    m_minSegmentSizeSpin = new QDoubleSpinBox();
    m_minSegmentSizeSpin->setRange(0.0625, 1000.0); // 64KB minimum
    m_minSegmentSizeSpin->setDecimals(2);
    m_minSegmentSizeSpin->setSuffix(" MB");
    form->addRow("Min segment size:", m_minSegmentSizeSpin);

    // Notifications
    m_notificationsCheck = new QCheckBox("Desktop notifications");
    form->addRow("", m_notificationsCheck);

    mainLayout->addLayout(form);

    // Error label
    m_errorLabel = new QLabel();
    m_errorLabel->setStyleSheet("color: red;");
    m_errorLabel->setWordWrap(true);
    m_errorLabel->hide();
    mainLayout->addWidget(m_errorLabel);

    // Buttons
    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Save | QDialogButtonBox::Cancel);
    m_saveButton = buttons->button(QDialogButtonBox::Save);
    mainLayout->addWidget(buttons);

    connect(buttons, &QDialogButtonBox::accepted, this, &SettingsDialog::onSave);
    connect(buttons, &QDialogButtonBox::rejected, this, &QDialog::reject);
    connect(browseButton, &QPushButton::clicked, this, &SettingsDialog::onBrowse);

    connect(m_client, &DaemonClient::configFetched, this, &SettingsDialog::onConfigFetched);
    connect(m_client, &DaemonClient::configUpdated, this, &SettingsDialog::onConfigUpdated);
    connect(m_client, &DaemonClient::requestFailed, this, &SettingsDialog::onRequestFailed);

    // Fetch current config
    m_client->fetchConfig();
}

void SettingsDialog::onConfigFetched(Config cfg) {
    m_originalConfig = cfg;
    m_dirEdit->setText(cfg.downloadDir);
    m_maxConcurrentSpin->setValue(cfg.maxConcurrent);
    m_defaultSegmentsSpin->setValue(cfg.defaultSegments);
    m_speedLimitSpin->setValue(static_cast<double>(cfg.globalSpeedLimit) / (1024.0 * 1024.0));
    m_maxRetriesSpin->setValue(cfg.maxRetries);
    m_minSegmentSizeSpin->setValue(static_cast<double>(cfg.minSegmentSize) / (1024.0 * 1024.0));
    m_notificationsCheck->setChecked(cfg.notifications);
}

void SettingsDialog::onSave() {
    if (!m_client->isConnected()) {
        m_errorLabel->setText("Not connected to daemon");
        m_errorLabel->show();
        return;
    }

    QJsonObject changes;

    QString dir = m_dirEdit->text().trimmed();
    if (dir != m_originalConfig.downloadDir)
        changes["download_dir"] = dir;

    int maxConcurrent = m_maxConcurrentSpin->value();
    if (maxConcurrent != m_originalConfig.maxConcurrent)
        changes["max_concurrent"] = maxConcurrent;

    int defaultSegments = m_defaultSegmentsSpin->value();
    if (defaultSegments != m_originalConfig.defaultSegments)
        changes["default_segments"] = defaultSegments;

    qint64 speedLimit = std::llround(m_speedLimitSpin->value() * 1024.0 * 1024.0);
    if (speedLimit != m_originalConfig.globalSpeedLimit)
        changes["global_speed_limit"] = speedLimit;

    int maxRetries = m_maxRetriesSpin->value();
    if (maxRetries != m_originalConfig.maxRetries)
        changes["max_retries"] = maxRetries;

    qint64 minSegSize = std::llround(m_minSegmentSizeSpin->value() * 1024.0 * 1024.0);
    if (minSegSize != m_originalConfig.minSegmentSize)
        changes["min_segment_size"] = minSegSize;

    bool notifications = m_notificationsCheck->isChecked();
    if (notifications != m_originalConfig.notifications)
        changes["notifications"] = notifications;

    if (changes.isEmpty()) {
        accept();
        return;
    }

    m_errorLabel->hide();
    m_saveButton->setEnabled(false);
    m_client->updateConfig(changes);
}

void SettingsDialog::onConfigUpdated() {
    accept();
}

void SettingsDialog::onRequestFailed(QString endpoint, int, QString, QString errorMessage) {
    if (endpoint != "updateConfig")
        return;
    m_saveButton->setEnabled(true);
    m_errorLabel->setText(errorMessage);
    m_errorLabel->show();
}

void SettingsDialog::onBrowse() {
    QString dir = QFileDialog::getExistingDirectory(this, "Select Directory", m_dirEdit->text());
    if (!dir.isEmpty())
        m_dirEdit->setText(dir);
}
