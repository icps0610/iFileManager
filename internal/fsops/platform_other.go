//go:build !windows

package fsops

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// platformAttrs 回傳項目是否為資料夾、是否隱藏、是否為符號連結。
func platformAttrs(dir, name string, info fs.FileInfo) (isDir, hidden, link bool) {
	isDir = info.IsDir()
	link = info.Mode()&fs.ModeSymlink != 0
	if link { // 連結指向資料夾時視為資料夾
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
			isDir = st.IsDir()
		}
	}
	return isDir, strings.HasPrefix(name, "."), link
}

// DriveMask 在非 Windows 平台沒有磁碟機代號，固定回傳 0。
func DriveMask() uint32 { return 0 }

// Drives 在非 Windows 平台只回傳根目錄。
func Drives() []Drive {
	return []Drive{{Path: "/", Kind: "fixed"}}
}

// Places 回傳常用位置。
func Places() []Place {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	out := []Place{{Name: "使用者資料夾", Path: home}}
	for _, n := range [][2]string{{"桌面", "Desktop"}, {"文件", "Documents"}, {"下載", "Downloads"}, {"圖片", "Pictures"}, {"音樂", "Music"}, {"影片", "Videos"}} {
		p := filepath.Join(home, n[1])
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, Place{Name: n[0], Path: p})
		}
	}
	return out
}

// Open 以系統預設程式開啟檔案。
func Open(path string) error {
	return exec.Command("xdg-open", path).Start()
}

func reveal(path string, isDir bool) error {
	dir := path
	if !isDir {
		dir = filepath.Dir(path)
	}
	return exec.Command("xdg-open", dir).Start()
}

// isCrossDevice 判斷改名失敗是否因為跨磁碟區。
func isCrossDevice(err error) bool {
	var en syscall.Errno
	return errors.As(err, &en) && en == syscall.EXDEV
}
