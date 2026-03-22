#pragma once

#include "daemonclient.h"
#include "downloadlistmodel.h"
#include "progressdelegate.h"

#include <QAction>
#include <QLabel>
#include <QMainWindow>
#include <QMenu>
#include <QPointer>
#include <QSortFilterProxyModel>
#include <QSplitter>
#include <QSystemTrayIcon>
#include <QTableView>
#include <QTreeWidget>

class DownloadFilterProxy : public QSortFilterProxyModel {
    Q_OBJECT

public:
    enum FilterMode { All, ByStatus, ByType };

    explicit DownloadFilterProxy(QObject *parent = nullptr)
        : QSortFilterProxyModel(parent) {}

    void setStatusFilter(const QStringList &statuses) {
        beginFilterChange();
        m_mode = ByStatus;
        m_statuses = statuses;
        endFilterChange();
    }

    void setTypeFilter(FileCategory category) {
        beginFilterChange();
        m_mode = ByType;
        m_category = category;
        endFilterChange();
    }

    void clearFilter() {
        beginFilterChange();
        m_mode = All;
        endFilterChange();
    }

    bool isFiltered() const { return m_mode != All; }

protected:
    bool filterAcceptsRow(int sourceRow, const QModelIndex &sourceParent) const override {
        Q_UNUSED(sourceParent)
        auto *model = qobject_cast<DownloadListModel *>(sourceModel());
        if (!model || sourceRow >= model->rowCount())
            return true;
        const Download &dl = model->downloadAt(sourceRow);
        switch (m_mode) {
        case All:
            return true;
        case ByStatus:
            return m_statuses.contains(dl.status);
        case ByType:
            return categoryForFilename(dl.filename) == m_category;
        }
        return true;
    }

private:
    FilterMode m_mode = All;
    QStringList m_statuses;
    FileCategory m_category = FileCategory::None;
};

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
    void onTrayActivated(QSystemTrayIcon::ActivationReason reason);

private:
    void setupToolbar();
    void setupStatusBar();
    void setupTrayIcon();
    void persistGeometry();
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

    // Sidebar + proxy
    QSplitter *m_splitter = nullptr;
    QTreeWidget *m_sidebar = nullptr;
    DownloadFilterProxy *m_proxyModel = nullptr;

    void setupSidebar();
    void onCategoryChanged();
    void updateCategoryCounts(const QVector<Download> &downloads);

    // System tray
    QSystemTrayIcon *m_trayIcon = nullptr;
    QMenu *m_trayMenu = nullptr;
};
