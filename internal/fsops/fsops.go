// Package fsops 提供檔案管理所需的純 Go 檔案操作。
// 本套件只使用標準庫，不依賴 Wails，可在任何平台獨立測試。
package fsops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
)

// Entry 代表目錄中的一個項目（欄位名稱對應前端 JSON）。
type Entry struct {
	Name   string `json:"name"`
	IsDir  bool   `json:"isDir"`
	Size   int64  `json:"size"`
	Mod    int64  `json:"mod"` // Unix 毫秒
	Hidden bool   `json:"hidden"`
	Link   bool   `json:"link"`
	Label  string `json:"label,omitempty"` // 磁碟機標籤（僅「本機」檢視使用）
}

// Listing 是 ListDir 的回傳內容。
type Listing struct {
	Path      string  `json:"path"`
	Parent    string  `json:"parent"`
	HasParent bool    `json:"hasParent"`
	Entries   []Entry `json:"entries"`
}

// Drive 代表一個磁碟機。
type Drive struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Kind  string `json:"kind"`  // fixed | removable | network | cdrom | other
	Total int64  `json:"total"` // 總容量（位元組），未知為 0
	Free  int64  `json:"free"`  // 可用空間（位元組），未知為 0
}

// Place 代表常用位置（桌面、文件、下載…）。
type Place struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

var envPattern = regexp.MustCompile(`%([^%\\/]+)%`)

// NormalizePath 清理使用者輸入的路徑：去除引號與空白、展開 %ENV%（僅 Windows）、補齊磁碟機根目錄。
// 空字串代表「本機」（磁碟機清單）。
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"`)
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		p = envPattern.ReplaceAllStringFunc(p, func(m string) string {
			if v := os.Getenv(m[1 : len(m)-1]); v != "" {
				return v
			}
			return m
		})
		p = strings.ReplaceAll(p, "/", `\`)
		if len(p) == 2 && p[1] == ':' {
			p += `\`
		}
	}
	return filepath.Clean(p)
}

// friendlyErr 把系統錯誤轉成清楚的繁體中文訊息。
func friendlyErr(op, path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("拒絕存取：%s", path)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("找不到路徑：%s", path)
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("已存在同名項目：%s", path)
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s失敗：%s（%v）", op, path, pe.Err)
	}
	return fmt.Errorf("%s失敗：%v", op, err)
}

func parentOf(p string) (string, bool) {
	d := filepath.Dir(p)
	if d != p {
		return d, true
	}
	if runtime.GOOS == "windows" {
		return "", true // 磁碟機根目錄的上一層是「本機」
	}
	return "", false
}

func listRoots() (*Listing, error) {
	ds := Drives()
	es := make([]Entry, 0, len(ds))
	for _, d := range ds {
		es = append(es, Entry{Name: d.Path, IsDir: true, Label: d.Label})
	}
	return &Listing{Path: "", Entries: es}, nil
}

// ListDir 列出資料夾內容。path 為空字串時列出磁碟機。
func ListDir(path string) (*Listing, error) {
	p := NormalizePath(path)
	if p == "" {
		return listRoots()
	}
	if !filepath.IsAbs(p) {
		return nil, fmt.Errorf("請輸入完整路徑：%s", path)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, friendlyErr("開啟資料夾", p, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, friendlyErr("讀取資料夾", p, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("不是資料夾：%s", p)
	}
	des, err := f.ReadDir(-1) // 不排序，排序交給前端
	if err != nil && len(des) == 0 {
		return nil, friendlyErr("讀取資料夾", p, err)
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		entries = append(entries, entryFrom(p, de))
	}
	parent, has := parentOf(p)
	return &Listing{Path: p, Parent: parent, HasParent: has, Entries: entries}, nil
}

func entryFrom(dir string, de fs.DirEntry) Entry {
	e := Entry{Name: de.Name()}
	info, err := de.Info()
	if err != nil { // 列舉期間檔案消失或無權限：仍顯示名稱
		e.IsDir = de.IsDir()
		return e
	}
	e.IsDir, e.Hidden, e.Link = platformAttrs(dir, de.Name(), info)
	if !e.IsDir {
		e.Size = info.Size()
	}
	e.Mod = info.ModTime().UnixMilli()
	return e
}

var reserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// ValidateName 檢查檔名／資料夾名稱是否合法（依 Windows 規則）。
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("名稱不可為空白")
	}
	if len([]rune(name)) > 255 {
		return errors.New("名稱過長（上限 255 個字元）")
	}
	if strings.ContainsAny(name, `<>:"/\|?*`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return errors.New(`名稱不可包含下列字元：\ / : * ? " < > |`)
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return errors.New("名稱結尾不可為句點或空白")
	}
	base := strings.ToUpper(name)
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, " ")
	if reserved[base] {
		return fmt.Errorf("「%s」是系統保留名稱", name)
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func sameFile(a, b string) bool {
	sa, err := os.Lstat(a)
	if err != nil {
		return false
	}
	sb, err := os.Lstat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// CreateDir 在 parent 底下建立資料夾，回傳完整路徑。
func CreateDir(parent, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	p := filepath.Join(parent, name)
	if exists(p) {
		return "", fmt.Errorf("已存在同名項目：%s", name)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		return "", friendlyErr("建立資料夾", p, err)
	}
	return p, nil
}

// CreateFile 在 parent 底下建立空白檔案，回傳完整路徑。
func CreateFile(parent, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	p := filepath.Join(parent, name)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("已存在同名項目：%s", name)
		}
		return "", friendlyErr("建立檔案", p, err)
	}
	if err := f.Close(); err != nil {
		return "", friendlyErr("建立檔案", p, err)
	}
	return p, nil
}

// Rename 重新命名單一項目，回傳新的完整路徑。
func Rename(path, newName string) (string, error) {
	if err := ValidateName(newName); err != nil {
		return "", err
	}
	if newName == filepath.Base(path) {
		return path, nil
	}
	dst := filepath.Join(filepath.Dir(path), newName)
	if exists(dst) && !sameFile(path, dst) { // 僅大小寫變更時 dst 即為自己
		return "", fmt.Errorf("已存在同名項目：%s", newName)
	}
	if err := os.Rename(path, dst); err != nil {
		return "", friendlyErr("重新命名", path, err)
	}
	return dst, nil
}

// Reveal 在系統檔案總管中顯示項目。
func Reveal(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return friendlyErr("顯示位置", path, err)
	}
	return reveal(path, st.IsDir())
}
