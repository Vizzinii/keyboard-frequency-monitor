package main

import _ "embed"

// macOS 菜单栏用 PNG（NSImage 不解析 ICO 容器）。
//
//go:embed assets/icon.png
var trayIcon []byte
