//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// showFatal 以訊息方塊顯示啟動失敗原因（視窗模式的 exe 沒有主控台，錯誤否則看不到）。
func showFatal(msg string) {
	title, _ := syscall.UTF16PtrFromString("檔案管理")
	text, _ := syscall.UTF16PtrFromString(msg)
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10) // MB_ICONERROR
}
