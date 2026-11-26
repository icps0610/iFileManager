//go:build windows

package fsops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procGetLogicalDrives     = kernel32.NewProc("GetLogicalDrives")
	procGetDriveTypeW        = kernel32.NewProc("GetDriveTypeW")
	procGetVolumeInformation = kernel32.NewProc("GetVolumeInformationW")
	procGetDiskFreeSpaceExW  = kernel32.NewProc("GetDiskFreeSpaceExW")
	procSetErrorMode         = kernel32.NewProc("SetErrorMode")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

// platformAttrs 回傳項目是否為資料夾、是否隱藏（隱藏或系統屬性）、是否為符號連結。
// 資料夾判斷直接讀取 Windows 屬性，避免 junction／連結資料夾被誤判為檔案。
func platformAttrs(dir, name string, info fs.FileInfo) (isDir, hidden, link bool) {
	isDir = info.IsDir()
	link = info.Mode()&fs.ModeSymlink != 0
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		a := d.FileAttributes
		isDir = a&syscall.FILE_ATTRIBUTE_DIRECTORY != 0
		hidden = a&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
	}
	return isDir, hidden, link
}

// DriveMask 回傳目前存在的磁碟機代號（bit 0＝A:、bit 1＝B:…）。只讀系統資料，不會存取磁碟本身，可頻繁呼叫。
func DriveMask() uint32 {
	m, _, _ := procGetLogicalDrives.Call()
	return uint32(m)
}

// Drives 列出所有磁碟機。
func Drives() []Drive {
	procSetErrorMode.Call(1) // SEM_FAILCRITICALERRORS：磁碟機沒有媒體時不跳出系統對話框
	mask, _, _ := procGetLogicalDrives.Call()
	var out []Drive
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		rootPtr, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(rootPtr)))
		d := Drive{Path: root, Kind: "other"}
		switch t {
		case 1: // DRIVE_NO_ROOT_DIR
			continue
		case 2:
			d.Kind = "removable"
		case 3:
			d.Kind = "fixed"
		case 4:
			d.Kind = "network"
		case 5:
			d.Kind = "cdrom"
		}
		if d.Kind == "fixed" || d.Kind == "removable" { // 網路磁碟讀標籤可能卡很久，略過
			d.Label = volumeLabel(rootPtr)
			d.Total, d.Free = diskSpace(rootPtr)
		}
		out = append(out, d)
	}
	return out
}

// diskSpace 回傳磁碟總容量與目前使用者可用空間；讀不到（例如沒有媒體）時回傳 0。
func diskSpace(root *uint16) (total, free int64) {
	var avail, tot, totFree uint64
	r, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(root)),
		uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&tot)),
		uintptr(unsafe.Pointer(&totFree)),
	)
	if r == 0 {
		return 0, 0
	}
	return int64(tot), int64(avail)
}

func volumeLabel(root *uint16) string {
	buf := make([]uint16, 261)
	r, _, _ := procGetVolumeInformation.Call(
		uintptr(unsafe.Pointer(root)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, 0, 0, 0, 0,
	)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	fidDesktop   = guid{0xB4BFCC3A, 0xDB2C, 0x424C, [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}}
	fidDocuments = guid{0xFDD39AD0, 0x238F, 0x46AF, [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7}}
	fidDownloads = guid{0x374DE290, 0x123F, 0x4565, [8]byte{0x91, 0x64, 0x39, 0xC4, 0x92, 0x5E, 0x46, 0x7B}}
	fidPictures  = guid{0x33E28130, 0x4E1E, 0x4676, [8]byte{0x83, 0x5A, 0x98, 0x39, 0x5C, 0x3B, 0xC3, 0xBB}}
	fidMusic     = guid{0x4BD8D571, 0x6D19, 0x48D3, [8]byte{0xBE, 0x97, 0x42, 0x22, 0x20, 0x08, 0x0E, 0x43}}
	fidVideos    = guid{0x18989B1D, 0x99B5, 0x455B, [8]byte{0x84, 0x1C, 0xAB, 0x7C, 0x74, 0xE4, 0xDD, 0xFC}}
)

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; n++ {
		ptr = unsafe.Add(ptr, 2)
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}

// knownFolder 取得 Windows 已知資料夾路徑（會正確處理 OneDrive 重新導向）。
func knownFolder(id *guid) string {
	var p *uint16
	hr, _, _ := procSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(id)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if hr != 0 || p == nil {
		return ""
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return utf16PtrToString(p)
}

// Places 回傳常用位置：使用者資料夾、桌面、文件、下載、圖片、音樂、影片。
func Places() []Place {
	var out []Place
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, Place{Name: "使用者資料夾", Path: home})
	}
	items := []struct {
		name, fallback string
		id             *guid
	}{
		{"桌面", "Desktop", &fidDesktop},
		{"文件", "Documents", &fidDocuments},
		{"下載", "Downloads", &fidDownloads},
		{"圖片", "Pictures", &fidPictures},
		{"音樂", "Music", &fidMusic},
		{"影片", "Videos", &fidVideos},
	}
	for _, it := range items {
		p := knownFolder(it.id)
		if st, err := os.Stat(p); p == "" || err != nil || !st.IsDir() {
			home, herr := os.UserHomeDir()
			if herr != nil {
				continue
			}
			p = filepath.Join(home, it.fallback)
			if st, err := os.Stat(p); err != nil || !st.IsDir() {
				continue
			}
		}
		out = append(out, Place{Name: it.name, Path: p})
	}
	return out
}

// Open 以系統預設程式開啟檔案。
func Open(path string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	dir, _ := syscall.UTF16PtrFromString(filepath.Dir(path))
	r, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		uintptr(unsafe.Pointer(dir)),
		1, // SW_SHOWNORMAL
	)
	if r <= 32 {
		return fmt.Errorf("無法開啟檔案（ShellExecute 代碼 %d）：%s", r, path)
	}
	return nil
}

// reveal 以 explorer.exe 顯示項目；以原始命令列傳入，避免路徑含空白時被錯誤分割。
func reveal(path string, isDir bool) error {
	cmdLine := `explorer.exe /select,"` + path + `"`
	if isDir {
		cmdLine = `explorer.exe "` + path + `"`
	}
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdLine}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // explorer 的結束碼不具意義，只需回收程序
	return nil
}

// isCrossDevice 判斷改名失敗是否因為跨磁碟區（ERROR_NOT_SAME_DEVICE = 17）。
func isCrossDevice(err error) bool {
	var en syscall.Errno
	return errors.As(err, &en) && en == 17
}
