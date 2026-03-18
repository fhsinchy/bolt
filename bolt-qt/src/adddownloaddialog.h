#pragma once

#include "daemonclient.h"
#include "types.h"

#include <QDialog>
#include <QLabel>
#include <QLineEdit>
#include <QPushButton>
#include <QSpinBox>

class AddDownloadDialog : public QDialog {
    Q_OBJECT

public:
    explicit AddDownloadDialog(DaemonClient *client, QWidget *parent = nullptr);

private slots:
    void onProbe();
    void onProbeCompleted(ProbeResult result);
    void onProbeFailed(QString error);
    void onDownload();
    void onDownloadAdded(Download dl);
    void onRequestFailed(QString endpoint, int statusCode, QString errorCode, QString errorMessage);
    void onConfigFetched(Config cfg);
    void onDisconnected();

private:
    DaemonClient *m_client;

    QLineEdit *m_urlEdit;
    QPushButton *m_probeButton;
    QLineEdit *m_filenameEdit;
    QLabel *m_sizeLabel;
    QLabel *m_resumableLabel;
    QLineEdit *m_dirEdit;
    QSpinBox *m_segmentsSpin;
    QLabel *m_errorLabel;
    QPushButton *m_downloadButton;
    QPushButton *m_cancelButton;

    bool m_force = false;
};
