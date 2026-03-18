#include "progressdelegate.h"

#include <QApplication>
#include <QPainter>
#include <QStyleOptionProgressBar>

void ProgressDelegate::paint(QPainter *painter, const QStyleOptionViewItem &option,
                              const QModelIndex &index) const {
    int progress = index.data(Qt::DisplayRole).toInt();

    QStyleOptionProgressBar progressBarOption;
    progressBarOption.rect = option.rect;
    progressBarOption.minimum = 0;
    progressBarOption.maximum = 100;
    progressBarOption.progress = progress;
    progressBarOption.text = QString::number(progress) + "%";
    progressBarOption.textVisible = true;

    QApplication::style()->drawControl(QStyle::CE_ProgressBar, &progressBarOption, painter);
}
