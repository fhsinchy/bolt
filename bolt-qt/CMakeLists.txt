cmake_minimum_required(VERSION 3.16)
project(bolt-qt VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
set(CMAKE_AUTORCC ON)
set(CMAKE_AUTOUIC ON)

find_package(Qt6 REQUIRED COMPONENTS Core Gui Widgets Network)

add_executable(bolt-qt
    src/main.cpp
    src/daemonclient.h
    src/daemonclient.cpp
    src/downloadlistmodel.h
    src/downloadlistmodel.cpp
    src/progressdelegate.h
    src/progressdelegate.cpp
    src/mainwindow.h
    src/mainwindow.cpp
    src/adddownloaddialog.h
    src/adddownloaddialog.cpp
    src/settingsdialog.h
    src/settingsdialog.cpp
    src/downloaddetaildialog.h
    src/downloaddetaildialog.cpp
    resources.qrc
)

target_link_libraries(bolt-qt PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Widgets
    Qt6::Network
)
