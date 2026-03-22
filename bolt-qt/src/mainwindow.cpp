#include "mainwindow.h"
#include "adddownloaddialog.h"
#include "settingsdialog.h"

#include <QApplication>
#include <QCheckBox>
#include <QCloseEvent>
#include <QDialog>
#include <QDialogButtonBox>
#include <QHBoxLayout>
#include <QHeaderView>
#include <QItemSelection>
#include <QListWidget>
#include <QResizeEvent>
#include <QIcon>
#include <QLabel>
#include <QSettings>
#include <QStatusBar>
#include <QToolBar>
#include <QTreeWidgetItemIterator>
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

    // Proxy model for filtering
    m_proxyModel = new DownloadFilterProxy(this);
    m_proxyModel->setSourceModel(m_model);

    // Table view setup
    m_tableView->setModel(m_proxyModel);
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

    // Splitter: sidebar + table
    m_splitter = new QSplitter(Qt::Horizontal, this);
    setupSidebar();
    m_splitter->addWidget(m_sidebar);
    m_splitter->addWidget(m_tableView);
    m_splitter->setStretchFactor(0, 0);
    m_splitter->setStretchFactor(1, 1);
    m_sidebar->setMinimumWidth(150);
    m_sidebar->setMaximumWidth(250);

    setCentralWidget(m_splitter);

    // Empty state label overlaid on table
    m_emptyLabel = new QLabel("No downloads yet. Click + to add one.", m_tableView);
    m_emptyLabel->setAlignment(Qt::AlignCenter);
    m_emptyLabel->setStyleSheet("color: gray; font-size: 14px;");
    m_emptyLabel->setVisible(true);

    setupToolbar();
    setupStatusBar();
    setupTrayIcon();

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
    if (settings.contains("mainwindow/splitter"))
        m_splitter->restoreState(settings.value("mainwindow/splitter").toByteArray());

    updateToolbarState();
}

void MainWindow::closeEvent(QCloseEvent *event) {
    persistGeometry();

    QSettings settings;
    bool minimizeToTray = settings.value("minimizeToTray", true).toBool();

    if (minimizeToTray && m_trayIcon) {
        event->ignore();
        hide();
        return;
    }

    // setQuitOnLastWindowClosed is false, so we must quit explicitly
    QApplication::quit();
}

void MainWindow::persistGeometry() {
    QSettings settings;
    settings.setValue("mainwindow/geometry", saveGeometry());
    if (m_splitter)
        settings.setValue("mainwindow/splitter", m_splitter->saveState());
}

void MainWindow::setupTrayIcon() {
    if (!QSystemTrayIcon::isSystemTrayAvailable())
        return;

    m_trayIcon = new QSystemTrayIcon(QIcon(":/tray-icon.png"), this);
    m_trayMenu = new QMenu(this);

    QAction *openAction = m_trayMenu->addAction("Open Bolt");
    connect(openAction, &QAction::triggered, this, [this]() {
        show();
        raise();
        activateWindow();
    });

    m_trayMenu->addSeparator();

    QAction *pauseAllAction = m_trayMenu->addAction("Pause All");
    connect(pauseAllAction, &QAction::triggered, this, [this]() {
        if (m_client->isConnected())
            m_client->pauseAll();
    });

    QAction *resumeAllAction = m_trayMenu->addAction("Resume All");
    connect(resumeAllAction, &QAction::triggered, this, [this]() {
        if (m_client->isConnected())
            m_client->resumeAll();
    });

    m_trayMenu->addSeparator();

    QAction *settingsAction = m_trayMenu->addAction("Settings");
    connect(settingsAction, &QAction::triggered, this, &MainWindow::onSettings);

    m_trayMenu->addSeparator();

    QAction *quitAction = m_trayMenu->addAction("Quit");
    connect(quitAction, &QAction::triggered, this, [this]() {
        persistGeometry();
        QApplication::quit();
    });

    m_trayIcon->setContextMenu(m_trayMenu);
    m_trayIcon->setToolTip("Bolt Download Manager");

    connect(m_trayIcon, &QSystemTrayIcon::activated,
            this, &MainWindow::onTrayActivated);

    m_trayIcon->show();
}

void MainWindow::onTrayActivated(QSystemTrayIcon::ActivationReason reason) {
    if (reason == QSystemTrayIcon::Trigger) {
        if (isVisible()) {
            hide();
        } else {
            show();
            raise();
            activateWindow();
        }
    }
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
    m_reorderAction = toolbar->addAction(QIcon::fromTheme("view-sort-ascending"), "Reorder Queue");
    m_reorderAction->setVisible(false);

    connect(m_addAction, &QAction::triggered, this, &MainWindow::onAddUrl);
    connect(m_pauseAction, &QAction::triggered, this, &MainWindow::onPause);
    connect(m_resumeAction, &QAction::triggered, this, &MainWindow::onResume);
    connect(m_retryAction, &QAction::triggered, this, &MainWindow::onRetry);
    connect(m_deleteAction, &QAction::triggered, this, &MainWindow::onDelete);
    connect(m_settingsAction, &QAction::triggered, this, &MainWindow::onSettings);
    connect(m_reorderAction, &QAction::triggered, this, &MainWindow::onReorder);
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
    QModelIndexList proxySelected = m_tableView->selectionModel()->selectedRows();
    QModelIndexList sourceSelected;
    for (const QModelIndex &proxyIdx : proxySelected)
        sourceSelected.append(m_proxyModel->mapToSource(proxyIdx));
    QStringList selectedIds = m_model->selectedIds(sourceSelected);

    m_model->updateFromPoll(downloads);

    // Restore selection by ID
    if (!selectedIds.isEmpty()) {
        QItemSelection sel;
        for (int srcRow = 0; srcRow < m_model->rowCount(); srcRow++) {
            if (selectedIds.contains(m_model->downloadIdAt(srcRow))) {
                QModelIndex proxyFirst = m_proxyModel->mapFromSource(m_model->index(srcRow, 0));
                QModelIndex proxyLast = m_proxyModel->mapFromSource(
                    m_model->index(srcRow, DownloadListModel::ColCount - 1));
                if (proxyFirst.isValid())
                    sel.select(proxyFirst, proxyLast);
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

    updateCategoryCounts(downloads);
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

    for (const QModelIndex &proxyIdx : selected) {
        QModelIndex srcIdx = m_proxyModel->mapToSource(proxyIdx);
        const Download &dl = m_model->downloadAt(srcIdx.row());
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
    bool proxyEmpty = m_proxyModel->rowCount() == 0;
    m_emptyLabel->setVisible(proxyEmpty);
    if (proxyEmpty) {
        if (m_proxyModel->isFiltered() && m_model->rowCount() > 0)
            m_emptyLabel->setText("No downloads in this category.");
        else
            m_emptyLabel->setText("No downloads yet. Click + to add one.");
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
    for (const QModelIndex &proxyIdx : selected) {
        QModelIndex srcIdx = m_proxyModel->mapToSource(proxyIdx);
        const Download &dl = m_model->downloadAt(srcIdx.row());
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
    for (const QModelIndex &proxyIdx : selected) {
        QModelIndex srcIdx = m_proxyModel->mapToSource(proxyIdx);
        const Download &dl = m_model->downloadAt(srcIdx.row());
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
    for (const QModelIndex &proxyIdx : selected) {
        QModelIndex srcIdx = m_proxyModel->mapToSource(proxyIdx);
        const Download &dl = m_model->downloadAt(srcIdx.row());
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
    // Map proxy indices to source indices before extracting IDs
    QModelIndexList proxySelected = m_tableView->selectionModel()->selectedRows();
    QModelIndexList sourceSelected;
    for (const QModelIndex &proxyIdx : proxySelected)
        sourceSelected.append(m_proxyModel->mapToSource(proxyIdx));
    QStringList ids = m_model->selectedIds(sourceSelected);
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

void MainWindow::onReorder() {
    if (!m_client->isConnected()) {
        statusBar()->showMessage("Not connected to daemon", 5000);
        return;
    }

    // Collect queued downloads from the source model
    QVector<QPair<QString, QString>> queued; // id, filename
    for (int i = 0; i < m_model->rowCount(); i++) {
        const Download &dl = m_model->downloadAt(i);
        if (dl.status == "queued")
            queued.append({dl.id, dl.filename});
    }

    if (queued.isEmpty()) {
        statusBar()->showMessage("No queued downloads to reorder", 3000);
        return;
    }

    QDialog dialog(this);
    dialog.setWindowTitle("Reorder Queue");
    dialog.setMinimumSize(400, 300);

    auto *layout = new QVBoxLayout(&dialog);
    layout->addWidget(new QLabel("Drag items or use buttons to reorder. Top item downloads first."));

    auto *listWidget = new QListWidget();
    listWidget->setDragDropMode(QAbstractItemView::InternalMove);
    listWidget->setDefaultDropAction(Qt::MoveAction);
    for (const auto &item : queued) {
        auto *listItem = new QListWidgetItem(item.second);
        listItem->setData(Qt::UserRole, item.first);
        listWidget->addItem(listItem);
    }
    layout->addWidget(listWidget, 1);

    // Move Up / Move Down buttons
    auto *moveLayout = new QHBoxLayout();
    auto *moveUpBtn = new QPushButton("Move Up");
    auto *moveDownBtn = new QPushButton("Move Down");
    moveLayout->addStretch();
    moveLayout->addWidget(moveUpBtn);
    moveLayout->addWidget(moveDownBtn);
    moveLayout->addStretch();
    layout->addLayout(moveLayout);

    QObject::connect(moveUpBtn, &QPushButton::clicked, [listWidget]() {
        int row = listWidget->currentRow();
        if (row > 0) {
            auto *item = listWidget->takeItem(row);
            listWidget->insertItem(row - 1, item);
            listWidget->setCurrentRow(row - 1);
        }
    });
    QObject::connect(moveDownBtn, &QPushButton::clicked, [listWidget]() {
        int row = listWidget->currentRow();
        if (row >= 0 && row < listWidget->count() - 1) {
            auto *item = listWidget->takeItem(row);
            listWidget->insertItem(row + 1, item);
            listWidget->setCurrentRow(row + 1);
        }
    });

    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    layout->addWidget(buttons);
    QObject::connect(buttons, &QDialogButtonBox::accepted, &dialog, &QDialog::accept);
    QObject::connect(buttons, &QDialogButtonBox::rejected, &dialog, &QDialog::reject);

    if (dialog.exec() != QDialog::Accepted)
        return;

    QStringList orderedIds;
    for (int i = 0; i < listWidget->count(); i++)
        orderedIds.append(listWidget->item(i)->data(Qt::UserRole).toString());

    m_client->reorderDownloads(orderedIds);
}

void MainWindow::setupSidebar() {
    m_sidebar = new QTreeWidget();
    m_sidebar->setHeaderHidden(true);
    m_sidebar->setRootIsDecorated(false);
    m_sidebar->setIndentation(16);

    // Status section
    auto *statusHeader = new QTreeWidgetItem(m_sidebar, {"Status"});
    statusHeader->setFlags(Qt::ItemIsEnabled);
    QFont boldFont = statusHeader->font(0);
    boldFont.setBold(true);
    statusHeader->setFont(0, boldFont);

    auto addCategory = [](QTreeWidgetItem *parent, const QString &name, const QString &key) {
        auto *item = new QTreeWidgetItem(parent, {name});
        item->setData(0, Qt::UserRole, key);
        item->setFlags(Qt::ItemIsEnabled | Qt::ItemIsSelectable);
        return item;
    };

    auto *allItem = addCategory(statusHeader, "All Downloads", "all");
    addCategory(statusHeader, "Queued", "queued");
    addCategory(statusHeader, "Unfinished", "unfinished");
    addCategory(statusHeader, "Failed", "failed");
    addCategory(statusHeader, "Finished", "finished");

    // Type section
    auto *typeHeader = new QTreeWidgetItem(m_sidebar, {"Types"});
    typeHeader->setFlags(Qt::ItemIsEnabled);
    typeHeader->setFont(0, boldFont);

    addCategory(typeHeader, "Compressed", "compressed");
    addCategory(typeHeader, "Documents", "documents");
    addCategory(typeHeader, "Music", "music");
    addCategory(typeHeader, "Video", "video");
    addCategory(typeHeader, "Images", "images");
    addCategory(typeHeader, "Programs", "programs");
    addCategory(typeHeader, "Disk Images", "diskimages");

    m_sidebar->expandAll();
    m_sidebar->setCurrentItem(allItem);

    connect(m_sidebar, &QTreeWidget::currentItemChanged,
            this, [this]() { onCategoryChanged(); });
}

void MainWindow::onCategoryChanged() {
    QTreeWidgetItem *item = m_sidebar->currentItem();
    if (!item)
        return;

    QString key = item->data(0, Qt::UserRole).toString();
    if (key.isEmpty())
        return;

    if (key == "all") {
        m_proxyModel->clearFilter();
    } else if (key == "queued") {
        m_proxyModel->setStatusFilter({"queued"});
    } else if (key == "unfinished") {
        m_proxyModel->setStatusFilter({"active", "paused", "verifying"});
    } else if (key == "failed") {
        m_proxyModel->setStatusFilter({"error", "refresh"});
    } else if (key == "finished") {
        m_proxyModel->setStatusFilter({"completed"});
    } else if (key == "compressed") {
        m_proxyModel->setTypeFilter(FileCategory::Compressed);
    } else if (key == "documents") {
        m_proxyModel->setTypeFilter(FileCategory::Documents);
    } else if (key == "music") {
        m_proxyModel->setTypeFilter(FileCategory::Music);
    } else if (key == "video") {
        m_proxyModel->setTypeFilter(FileCategory::Video);
    } else if (key == "images") {
        m_proxyModel->setTypeFilter(FileCategory::Images);
    } else if (key == "programs") {
        m_proxyModel->setTypeFilter(FileCategory::Programs);
    } else if (key == "diskimages") {
        m_proxyModel->setTypeFilter(FileCategory::DiskImages);
    }

    m_reorderAction->setVisible(key == "queued");
    updateEmptyState();
}

void MainWindow::updateCategoryCounts(const QVector<Download> &downloads) {
    int allCount = downloads.size();
    int queuedCount = 0, unfinishedCount = 0, failedCount = 0, finishedCount = 0;
    int compressedCount = 0, documentsCount = 0, musicCount = 0, videoCount = 0;
    int imagesCount = 0, programsCount = 0, diskImagesCount = 0;

    for (const Download &dl : downloads) {
        if (dl.status == "queued") queuedCount++;
        else if (dl.status == "active" || dl.status == "paused" || dl.status == "verifying") unfinishedCount++;
        else if (dl.status == "error" || dl.status == "refresh") failedCount++;
        else if (dl.status == "completed") finishedCount++;

        switch (categoryForFilename(dl.filename)) {
        case FileCategory::Compressed:  compressedCount++; break;
        case FileCategory::Documents:   documentsCount++; break;
        case FileCategory::Music:       musicCount++; break;
        case FileCategory::Video:       videoCount++; break;
        case FileCategory::Images:      imagesCount++; break;
        case FileCategory::Programs:    programsCount++; break;
        case FileCategory::DiskImages:  diskImagesCount++; break;
        default: break;
        }
    }

    auto updateItem = [this](const QString &key, const QString &baseName, int count) {
        QTreeWidgetItemIterator it(m_sidebar);
        while (*it) {
            if ((*it)->data(0, Qt::UserRole).toString() == key) {
                (*it)->setText(0, QString("%1 (%2)").arg(baseName).arg(count));
                break;
            }
            ++it;
        }
    };

    updateItem("all", "All Downloads", allCount);
    updateItem("queued", "Queued", queuedCount);
    updateItem("unfinished", "Unfinished", unfinishedCount);
    updateItem("failed", "Failed", failedCount);
    updateItem("finished", "Finished", finishedCount);
    updateItem("compressed", "Compressed", compressedCount);
    updateItem("documents", "Documents", documentsCount);
    updateItem("music", "Music", musicCount);
    updateItem("video", "Video", videoCount);
    updateItem("images", "Images", imagesCount);
    updateItem("programs", "Programs", programsCount);
    updateItem("diskimages", "Disk Images", diskImagesCount);
}
