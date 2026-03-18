#pragma once

#include "daemonclient.h"
#include "downloadlistmodel.h"
#include "progressdelegate.h"

#include <QAction>
#include <QLabel>
#include <QMainWindow>
#include <QPointer>
#include <QTableView>

class MainWindow : public QMainWindow {
    Q_OBJECT

public:
    explicit MainWindow(DaemonClient *client, QWidget *parent = nullptr);

protected:
    void closeEvent(QCloseEvent *event) override;

private slots:
    void onConnected();
    void onDisconnected();
    void onDownloadsFetched(const QVector<Download> &downloads);
    void onRequestFailed(const QString &endpoint, int statusCode,
                         const QString &errorCode, const QString &errorMessage);
    void onSelectionChanged();

    void onAddUrl();
    void onPause();
    void onResume();
    void onRetry();
    void onDelete();
    void onSettings();

private:
    void setupToolbar();
    void setupStatusBar();
    void updateToolbarState();
    void updateEmptyState();
    void resizeEvent(QResizeEvent *event) override;

    DaemonClient *m_client;
    DownloadListModel *m_model;
    QTableView *m_tableView;
    ProgressDelegate *m_progressDelegate;

    // Toolbar actions
    QAction *m_addAction;
    QAction *m_pauseAction;
    QAction *m_resumeAction;
    QAction *m_retryAction;
    QAction *m_deleteAction;
    QAction *m_settingsAction;

    // Status bar labels
    QLabel *m_connectionLabel;
    QLabel *m_activeCountLabel;
    QLabel *m_totalSpeedLabel;

    // Empty state
    QLabel *m_emptyLabel;

    // Track open dialogs to prevent cross-talk
    QPointer<QDialog> m_activeDialog;
};
