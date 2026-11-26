//go:build windows && (amd64 || arm64)

package fsops

// 與 Windows 系統剪貼簿互通檔案清單（CF_HDROP），讓本程式與檔案總管之間可以互相複製、剪下、貼上。
// 「剪下」以註冊格式 "Preferred DropEffect" 標示（DROPEFFECT_MOVE），與檔案總管的作法相同。

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalFree                 = kernel32.NewProc("GlobalFree")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
)

const (
	cfHDrop       = 15
	gmemMoveable  = 0x0002
	dropFilesSize = 20 // sizeof(DROPFILES)
)

func preferredDropEffectFormat() uintptr {
	name, _ := syscall.UTF16PtrFromString("Preferred DropEffect")
	f, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	return f
}

// withClipboard 開啟剪貼簿執行 fn。Open／Close 必須在同一條 OS 執行緒；剪貼簿被其他程式占用時重試數次。
func withClipboard(fn func() error) error {
	hostOnce.Do(startHost)
	if hostErr != nil {
		return hostErr
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var err error
	for i := 0; i < 10; i++ {
		var r uintptr
		r, _, err = procOpenClipboard.Call(hostHwnd)
		if r != 0 {
			defer procCloseClipboard.Call()
			return fn()
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("無法開啟系統剪貼簿（可能被其他程式占用）：%v", err)
}

// globalCopy 配置一塊可移動的全域記憶體並複製 data 進去（交給 SetClipboardData 後由系統負責釋放）。
func globalCopy(data []byte) (uintptr, error) {
	h, _, e := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return 0, fmt.Errorf("GlobalAlloc 失敗：%v", e)
	}
	p, _, e := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return 0, fmt.Errorf("GlobalLock 失敗：%v", e)
	}
	dst := unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), len(data))
	copy(dst, data)
	procGlobalUnlock.Call(h)
	return h, nil
}

// SetClipboardFiles 把檔案清單放到系統剪貼簿；cut 為 true 時標示為「剪下」。
func SetClipboardFiles(paths []string, cut bool) error {
	if len(paths) == 0 {
		return errors.New("沒有要放到剪貼簿的項目")
	}
	// DROPFILES：pFiles(4) pt(8) fNC(4) fWide(4)，後面接以 NUL 分隔、雙 NUL 結尾的 UTF-16 路徑
	buf := make([]byte, dropFilesSize)
	buf[0] = dropFilesSize
	buf[16] = 1 // fWide
	for _, p := range paths {
		u, err := syscall.UTF16FromString(p)
		if err != nil {
			return fmt.Errorf("路徑含有無效字元：%s", p)
		}
		for _, c := range u { // 含結尾 NUL
			buf = append(buf, byte(c), byte(c>>8))
		}
	}
	buf = append(buf, 0, 0)

	effect := uint32(dropEffectCopy | dropEffectLink)
	if cut {
		effect = dropEffectMove
	}
	eff := []byte{byte(effect), byte(effect >> 8), byte(effect >> 16), byte(effect >> 24)}

	return withClipboard(func() error {
		if r, _, e := procEmptyClipboard.Call(); r == 0 {
			return fmt.Errorf("EmptyClipboard 失敗：%v", e)
		}
		h, err := globalCopy(buf)
		if err != nil {
			return err
		}
		if r, _, e := procSetClipboardData.Call(cfHDrop, h); r == 0 {
			procGlobalFree.Call(h)
			return fmt.Errorf("SetClipboardData(CF_HDROP) 失敗：%v", e)
		}
		if f := preferredDropEffectFormat(); f != 0 {
			if he, err := globalCopy(eff); err == nil {
				if r, _, _ := procSetClipboardData.Call(f, he); r == 0 {
					procGlobalFree.Call(he)
				}
			}
		}
		return nil
	})
}

// GetClipboardFiles 讀取系統剪貼簿中的檔案清單；沒有檔案時回傳空清單。cut 表示來源標示為「剪下」。
func GetClipboardFiles() (paths []string, cut bool, err error) {
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfHDrop); r == 0 {
		return nil, false, nil
	}
	err = withClipboard(func() error {
		h, _, _ := procGetClipboardData.Call(cfHDrop)
		if h == 0 {
			return nil
		}
		n, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
		for i := uintptr(0); i < n; i++ {
			l, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
			b := make([]uint16, l+1)
			procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&b[0])), l+1)
			paths = append(paths, syscall.UTF16ToString(b))
		}
		if f := preferredDropEffectFormat(); f != 0 {
			if he, _, _ := procGetClipboardData.Call(f); he != 0 {
				if p, _, _ := procGlobalLock.Call(he); p != 0 {
					v := *(*uint32)(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
					procGlobalUnlock.Call(he)
					cut = v&dropEffectMove != 0 && v&dropEffectCopy == 0
				}
			}
		}
		return nil
	})
	return paths, cut, err
}

// ClearClipboard 清空系統剪貼簿（剪下並貼上完成後使用，避免重複移動）。
func ClearClipboard() error {
	return withClipboard(func() error {
		procEmptyClipboard.Call()
		return nil
	})
}
