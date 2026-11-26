package fsops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建立上層資料夾失敗：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("寫入檔案失敗：%v", err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("讀取檔案失敗：%v", err)
	}
	return string(b)
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"報告.docx", true},
		{"a b c.txt", true},
		{".gitignore", true},
		{"", false},
		{"   ", false},
		{"a/b", false},
		{`a\b`, false},
		{"a:b", false},
		{"a*b", false},
		{"a?b", false},
		{`a"b`, false},
		{"a<b", false},
		{"a|b", false},
		{"結尾.", false},
		{"結尾 ", false},
		{"CON", false},
		{"con.txt", false},
		{"LPT1.log", false},
		{"CONSOLE", true},
		{strings.Repeat("長", 256), false},
		{strings.Repeat("長", 255), true},
		{"tab\tname", false},
	}
	for _, c := range cases {
		err := ValidateName(c.name)
		if (err == nil) != c.ok {
			t.Errorf("ValidateName(%q) = %v，預期合法=%v", c.name, err, c.ok)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	if got := NormalizePath(""); got != "" {
		t.Errorf("空字串應維持空字串，得到 %q", got)
	}
	want := filepath.FromSlash("/tmp/foo") // Windows 為 \tmp\foo
	if got := NormalizePath(`  "/tmp/foo/"  `); got != want {
		t.Errorf("引號與結尾斜線處理錯誤：%q，預期 %q", got, want)
	}
}

func TestNormalizePathWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("僅適用於 Windows")
	}
	t.Setenv("FM_TEST_DIR", `C:\Temp\fm`)
	cases := map[string]string{
		`C:/Users/x/`:        `C:\Users\x`,
		`"C:\Users\x\"`:      `C:\Users\x`,
		`C:`:                 `C:\`,
		`%FM_TEST_DIR%\sub`:  `C:\Temp\fm\sub`,
		`%FM_NOT_EXIST%\sub`: `%FM_NOT_EXIST%\sub`,
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestListDir(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "hello")
	write(t, filepath.Join(dir, ".hidden"), "x")
	markHidden(t, filepath.Join(dir, ".hidden"))
	write(t, filepath.Join(dir, ".dot"), "x")
	if err := os.Mkdir(filepath.Join(dir, "子資料夾"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := ListDir(dir)
	if err != nil {
		t.Fatalf("ListDir 失敗：%v", err)
	}
	if len(l.Entries) != 4 {
		t.Fatalf("預期 4 個項目，得到 %d", len(l.Entries))
	}
	by := map[string]Entry{}
	for _, e := range l.Entries {
		by[e.Name] = e
	}
	if e := by["a.txt"]; e.IsDir || e.Size != 5 || e.Mod == 0 {
		t.Errorf("a.txt 屬性不正確：%+v", e)
	}
	if e := by["子資料夾"]; !e.IsDir {
		t.Errorf("子資料夾應為資料夾：%+v", e)
	}
	if e := by[".hidden"]; !e.Hidden {
		t.Errorf(".hidden 應標示為隱藏：%+v", e)
	}
	if e := by["a.txt"]; e.Hidden {
		t.Errorf("a.txt 不應隱藏：%+v", e)
	}
	if e := by[".dot"]; e.Hidden != dotfileIsHidden {
		t.Errorf(".dot（無隱藏屬性）的隱藏狀態應為 %v：%+v", dotfileIsHidden, e)
	}
	if !l.HasParent || l.Parent != filepath.Dir(dir) {
		t.Errorf("上一層錯誤：%+v", l)
	}
}

func TestListDirErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ListDir(filepath.Join(dir, "不存在")); err == nil || !strings.Contains(err.Error(), "找不到路徑") {
		t.Errorf("不存在的路徑應回報『找不到路徑』，得到 %v", err)
	}
	f := filepath.Join(dir, "f.txt")
	write(t, f, "x")
	if _, err := ListDir(f); err == nil || !strings.Contains(err.Error(), "不是資料夾") {
		t.Errorf("檔案路徑應回報『不是資料夾』，得到 %v", err)
	}
	if _, err := ListDir("relative/path"); err == nil || !strings.Contains(err.Error(), "完整路徑") {
		t.Errorf("相對路徑應被拒絕，得到 %v", err)
	}
}

func TestListDirSymlinkToDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Skip("此環境無法建立符號連結")
	}
	l, err := ListDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Name == "link" && (!e.IsDir || !e.Link) {
			t.Errorf("指向資料夾的連結應為 IsDir 且 Link：%+v", e)
		}
	}
}

func TestCreateAndRename(t *testing.T) {
	dir := t.TempDir()
	d, err := CreateDir(dir, "新資料夾")
	if err != nil {
		t.Fatalf("CreateDir：%v", err)
	}
	if _, err := CreateDir(dir, "新資料夾"); err == nil {
		t.Error("重複建立同名資料夾應失敗")
	}
	f, err := CreateFile(dir, "a.txt")
	if err != nil {
		t.Fatalf("CreateFile：%v", err)
	}
	if _, err := CreateFile(dir, "a.txt"); err == nil {
		t.Error("重複建立同名檔案應失敗")
	}
	if _, err := CreateFile(dir, "bad:name"); err == nil {
		t.Error("非法名稱應失敗")
	}

	nf, err := Rename(f, "b.txt")
	if err != nil || filepath.Base(nf) != "b.txt" {
		t.Fatalf("Rename：%v %q", err, nf)
	}
	if _, err := os.Stat(nf); err != nil {
		t.Errorf("改名後檔案不存在：%v", err)
	}
	write(t, filepath.Join(dir, "c.txt"), "c")
	if _, err := Rename(nf, "c.txt"); err == nil {
		t.Error("改成已存在的名稱應失敗")
	}
	if got, err := Rename(nf, "b.txt"); err != nil || got != nf {
		t.Errorf("名稱不變應直接成功：%v %q", err, got)
	}
	if _, err := Rename(d, "新名字"); err != nil {
		t.Errorf("資料夾改名失敗：%v", err)
	}
}
