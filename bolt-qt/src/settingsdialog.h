#pragma once

#include "daemonclient.h"
#include "types.h"

#include <QCheckBox>
#include <QDialog>
#include <QDoubleSpinBox>
#include <QLabel>
#include <QLineEdit>
#include <QPushButton>
#include <QSpinBox>

class SettingsDialog : public QDialog {
    Q_OBJECT

public:
    explicit SettingsDialog(DaemonClient *client, QWidget *parent = nullptr);
    ~SettingsDialog() override;

private slots:
    void onConfigFetched(Config cfg);
    void onConfigUpdated();
    void onRequestFailed(QString endpoint, int statusCode, QString errorCode, QString errorMessage);
    void onSave();
    void onBrowse();

private:
    DaemonClient *m_client;
    Config m_originalConfig;

    QLineEdit *m_dirEdit;
    QSpinBox *m_maxConcurrentSpin;
    QSpinBox *m_defaultSegmentsSpin;
    QDoubleSpinBox *m_speedLimitSpin;
    QSpinBox *m_maxRetriesSpin;
    QDoubleSpinBox *m_minSegmentSizeSpin;
    QCheckBox *m_notificationsCheck;
    QLabel *m_errorLabel;
    QPushButton *m_saveButton;
};
