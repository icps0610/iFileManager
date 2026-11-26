//go:build windows && (amd64 || arm64)

package fsops

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	procSHFileOperationW = shell32.NewProc("SHFileOperationW")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
)

const (
	foDelete           = 0x0003
	fofNoConfirmation  = 0x0010
	fofAllowUndo       = 0x0040
	fofNoErrorUI       = 0x0400
	fofWantNukeWarning = 0x4000 // 檔案無法進資源回收筒（將被永久刪除）時仍要警告
)

// shFileOpStruct 對應 SHFILEOPSTRUCTW（64 位元 Windows 使用自然對齊，大小 56 位元組）。
type shFileOpStruct struct {
	Hwnd                 uintptr
	Func                 uint32
	From                 *uint16
	To                   *uint16
	Flags                uint16
	AnyOperationsAborted int32
	NameMappings         uintptr
	ProgressTitle        *uint16
}

// 編譯期檢查：結構大小必須為 56 位元組，否則編譯失敗。
const _ = uint(unsafe.Sizeof(shFileOpStruct{}) - 56)
const _ = uint(56 - unsafe.Sizeof(shFileOpStruct{}))

// Trash 把項目移到資源回收筒。失敗時絕不會退而求其次改成永久刪除。
func Trash(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// COM 初始化：S_OK(0) 或 S_FALSE(1) 才需要配對 CoUninitialize
	hr, _, _ := procCoInitializeEx.Call(0, 0x2|0x4) // APARTMENTTHREADED | DISABLE_OLE1DDE
	if hr == 0 || hr == 1 {
		defer procCoUninitialize.Call()
	}

	var from []uint16
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("路徑無效：%s", p)
		}
		u, err := syscall.UTF16FromString(abs) // 結尾已含 NUL
		if err != nil {
			return fmt.Errorf("路徑含有無效字元：%s", p)
		}
		from = append(from, u...)
	}
	from = append(from, 0) // 整串以雙 NUL 結尾

	op := shFileOpStruct{
		Func:  foDelete,
		From:  &from[0],
		Flags: fofAllowUndo | fofNoConfirmation | fofNoErrorUI | fofWantNukeWarning,
	}
	r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	runtime.KeepAlive(from)
	if r != 0 {
		return fmt.Errorf("移到資源回收筒失敗（代碼 0x%X）", r)
	}
	if op.AnyOperationsAborted != 0 {
		return errors.New("操作已被取消，部分項目可能尚未刪除")
	}
	return nil
}
