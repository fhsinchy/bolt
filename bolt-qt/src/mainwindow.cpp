#include "mainwindow.h"
#include "adddownloaddialog.h"
#include "settingsdialog.h"

#include <QCheckBox>
#include <QCloseEvent>
#include <QDialog>
#include <QDialogButtonBox>
#include <QHBoxLayout>
#include <QHeaderView>
#include <QItemSelection>
#include <QResizeEvent>
#include <QIcon>
#include <QLabel>
#include <QSettings>
#include <QStatusBar>
#include <QToolBar>
#include <QVBoxLayout>

MainWindow::MainWindow(DaemonClient *client, QWidget *parent)
    : QMainWindow(parent)
    , m_client(client)
    , m_model(new DownloadListModel(this))
    , m_tableView(new QTableView(this))
    , m_progressDelegate(new ProgressDelegate(this))
{
    setWindowTitle("Bolt Download Manager");
    resize(800, 500);

    // Table view setup
    m_tableView->setModel(m_model);
    m_tableView->setItemDelegateForColumn(DownloadListModel::ColProgress, m_progressDelegate);
    m_tableView->setSelectionBehavior(QAbstractItemView::SelectRows);
    m_tableView->setSelectionMode(QAbstractItemView::ExtendedSelection);
    m_tableView->verticalHeader()->hide();
    m_tableView->horizontalHeader()->setStretchLastSection(true);
    m_tableView->setColumnWidth(DownloadListModel::ColFilename, 250);
    m_tableView->setColumnWidth(DownloadListModel::ColSize, 80);
    m_tableView->setColumnWidth(DownloadListModel::ColProgress, 120);
    m_tableView->setColumnWidth(DownloadListModel::ColSpeed, 100);
    m_tableView->setColumnWidth(DownloadListModel::ColEta, 80);

    setCentralWidget(m_tableView);

    // Empty state label overlaid on table
    m_emptyLabel = new QLabel("No downloads yet. Click + to add one.", m_tableView);
    m_emptyLabel->setAlignment(Qt::AlignCenter);
    m_emptyLabel->setStyleSheet("color: gray; font-size: 14px;");
    m_emptyLabel->setVisible(true);

    setupToolbar();
    setupStatusBar();

    // Connect client signals
    connect(m_client, &DaemonClient::connected, this, &MainWindow::onConnected);
    connect(m_client, &DaemonClient::disconnected, this, &MainWindow::onDisconnected);
    connect(m_client, &DaemonClient::downloadsFetched, this, &MainWindow::onDownloadsFetched);
    connect(m_client, &DaemonClient::requestFailed, this, &MainWindow::onRequestFailed);

    // Selection changes
    connect(m_tableView->selectionModel(), &QItemSelectionModel::selectionChanged,
            this, &MainWindow::onSelectionChanged);

    // Restore geometry
    QSettings settings;
    restoreGeometry(settings.value("mainwindow/geometry").toByteArray());

    updateToolbarState();
}

void MainWindow::closeEvent(QCloseEvent *event) {
    QSettings settings;
    settings.setValue("mainwindow/geometry", saveGeometry());
    QMainWindow::closeEvent(event);
}

void MainWindow::setupToolbar() {
    QToolBar *toolbar = addToolBar("Main");
    toolbar->setMovable(false);

    m_addAction = toolbar->addAction(QIcon::fromTheme("list-add"), "Add URL");
    m_pauseAction = toolbar->addAction(QIcon::fromTheme("media-playback-pause"), "Pause");
    m_resumeAction = toolbar->addAction(QIcon::fromTheme("media-playback-start"), "Resume");
    m_retryAction = toolbar->addAction(QIcon::fromTheme("view-refresh"), "Retry");
    m_deleteAction = toolbar->addAction(QIcon::fromTheme("edit-delete"), "Delete");
    toolbar->addSeparator();
    m_settingsAction = toolbar->addAction(QIcon::fromTheme("configure"), "Settings");

    connect(m_addAction, &QAction::triggered, this, &MainWindow::onAddUrl);
    connect(m_pauseAction, &QAction::triggered, this, &MainWindow::onPause);
    connect(m_resumeAction, &QAction::triggered, this, &MainWindow::onResume);
    connect(m_retryAction, &QAction::triggered, this, &MainWindow::onRetry);
    connect(m_deleteAction, &QAction::triggered, this, &MainWindow::onDelete);
    connect(m_settingsAction, &QAction::triggered, this, &MainWindow::onSettings);
}

void MainWindow::setupStatusBar() {
    m_connectionLabel = new QLabel("Connecting...");
    m_activeCountLabel = new QLabel();
    m_totalSpeedLabel = new QLabel();

    statusBar()->addPermanentWidget(m_connectionLabel);
    statusBar()->addPermanentWidget(m_activeCountLabel);
    statusBar()->addPermanentWidget(m_totalSpeedLabel);
}

void MainWindow::onConnected() {
    m_connectionLabel->setText("Connected");
}

void MainWindow::onDisconnected() {
    m_connectionLabel->setText("Disconnected \u2014 retrying...");
    m_activeCountLabel->setText(QString());
    m_totalSpeedLabel->setText(QString());
    m_model->resetSpeeds();
}

void MainWindow::onDownloadsFetched(const QVector<Download> &downloads) {
    // Save selection by ID before model update (survives resets)
    QStringList selectedIds = m_model->selectedIds(
        m_tableView->selectionModel()->selectedRows());

    m_model->updateFromPoll(downloads);

    // Restore selection by ID
    if (!selectedIds.isEmpty()) {
        QItemSelection sel;
        for (int row = 0; row < m_model->rowCount(); row++) {
            if (selectedIds.contains(m_model->downloadIdAt(row))) {
                sel.select(m_model->index(row, 0),
                           m_model->index(row, DownloadListModel::ColCount - 1));
            }
        }
        if (!sel.isEmpty())
            m_tableView->selectionModel()->select(sel, QItemSelectionModel::ClearAndSelect);
    }

    int activeCount = 0;
    for (const Download &dl : downloads) {
        if (dl.status == "active")
            activeCount++;
    }

    m_activeCountLabel->setText(activeCount > 0
        ? QString::number(activeCount) + " downloading"
        : QString());

    // Sum speeds from model
    double totalSpeed = 0.0;
    for (const Download &dl : downloads) {
        if (dl.status == "active")
            totalSpeed += m_model->speedForId(dl.id);
    }
    m_totalSpeedLabel->setText(totalSpeed > 0.0 ? formatSpeed(totalSpeed) : QString());

    updateEmptyState();
    updateToolbarState();
}

void MainWindow::onRequestFailed(const QString &, int, const QString &, const QString &errorMessage) {
    statusBar()->showMessage("Error: " + errorMessage, 5000);
}

void MainWindow::onSelectionChanged() {
    updateToolbarState();
}

void MainWindow::updateToolbarState() {
    QModelIndexList selected = m_tableView->selectionModel()->selectedRows();
    bool hasSelection = !selected.isEmpty();
    bool hasActive = false;
    bool hasPaused = false;
    bool hasError = false;

    for (const QModelIndex &idx : selected) {
        const Download &dl = m_model->downloadAt(idx.row());
        if (dl.status == "active") hasActive = true;
        if (dl.status == "paused") hasPaused = true;
        if (dl.status == "error") hasError = true;
    }

    m_pauseAction->setEnabled(hasActive);
    m_resumeAction->setEnabled(hasPaused);
    m_retryAction->setEnabled(hasError);
    m_deleteAction->setEnabled(hasSelection);
}

void MainWindow::updateEmptyState() {
    bool empty = m_model->rowCount() == 0;
    m_emptyLabel->setVisible(empty);
    if (empty) {
        m_emptyLabel->setGeometry(m_tableView->viewport()->rect());
    }
}

void MainWindow::resizeEvent(QResizeEvent *event) {
    QMainWindow::resizeEvent(event);
    updateEmptyState();
}

void MainWindow::onAddUrl() {
    if (m_activeDialog)
        return;
    auto *dialog = new AddDownloadDialog(m_client, this);
    dialog->setAttribute(Qt::WA_DeleteOnClose);
    m_activeDialog = dialog;
    dialog->open();
}

void MainWindow::onPause() {
    if (!m_client->isConnected()) {
        statusBar()->showMessage("Not connected to daemon", 5000);
        return;
    }
    QModelIndexList selected = m_tableView->selectionModel()->selectedRows();
    for (const QModelIndex &idx : selected) {
        const Download &dl = m_model->downloadAt(idx.row());
        if (dl.status == "active")
            m_client->pauseDownload(dl.id);
    }
}

void MainWindow::onResume() {
    if (!m_client->isConnected()) {
        statusBar()->showMessage("Not connected to daemon", 5000);
        return;
    }
    QModelIndexList selected = m_tableView->selectionModel()->selectedRows();
    for (const QModelIndex &idx : selected) {
        const Download &dl = m_model->downloadAt(idx.row());
        if (dl.status == "paused")
            m_client->resumeDownload(dl.id);
    }
}

void MainWindow::onRetry() {
    if (!m_client->isConnected()) {
        statusBar()->showMessage("Not connected to daemon", 5000);
        return;
    }
    QModelIndexList selected = m_tableView->selectionModel()->selectedRows();
    for (const QModelIndex &idx : selected) {
        const Download &dl = m_model->downloadAt(idx.row());
        if (dl.status == "error")
            m_client->retryDownload(dl.id);
    }
}

void MainWindow::onDelete() {
    if (!m_client->isConnected()) {
        statusBar()->showMessage("Not connected to daemon", 5000);
        return;
    }

    // Capture IDs before the modal dialog — the model can poll and
    // reorder while the dialog is open, invalidating row indexes.
    QStringList ids = m_model->selectedIds(
        m_tableView->selectionModel()->selectedRows());
    if (ids.isEmpty())
        return;

    // Custom delete confirmation dialog
    QDialog dialog(this);
    dialog.setWindowTitle("Confirm Delete");

    auto *layout = new QVBoxLayout(&dialog);
    int count = ids.size();
    layout->addWidget(new QLabel(
        count == 1
            ? "Delete this download?"
            : QString("Delete %1 downloads?").arg(count)));

    auto *deleteFileCheck = new QCheckBox("Also delete downloaded file");
    layout->addWidget(deleteFileCheck);

    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    layout->addWidget(buttons);

    connect(buttons, &QDialogButtonBox::accepted, &dialog, &QDialog::accept);
    connect(buttons, &QDialogButtonBox::rejected, &dialog, &QDialog::reject);

    if (dialog.exec() != QDialog::Accepted)
        return;

    bool deleteFile = deleteFileCheck->isChecked();
    for (const QString &id : ids)
        m_client->deleteDownload(id, deleteFile);
}

void MainWindow::onSettings() {
    if (m_activeDialog)
        return;
    auto *dialog = new SettingsDialog(m_client, this);
    dialog->setAttribute(Qt::WA_DeleteOnClose);
    m_activeDialog = dialog;
    dialog->open();
}
