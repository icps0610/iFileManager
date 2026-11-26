//go:build windows

package fsops

import (
	"syscall"
	"testing"
)

// Windows 的隱藏檔是檔案屬性，不是檔名開頭的點。
const dotfileIsHidden = false

func markHidden(t *testing.T, path string) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatalf("設定隱藏屬性失敗：%v", err)
	}
}
