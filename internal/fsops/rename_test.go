package fsops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkFiles(t *testing.T, dir string, names ...string) []string {
	t.Helper()
	var out []string
	for _, n := range names {
		p := filepath.Join(dir, n)
		write(t, p, "content-of-"+n)
		out = append(out, p)
	}
	return out
}

func newNames(items []RenameItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.NewName)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRenameReplace(t *testing.T) {
	dir := t.TempDir()
	ps := mkFiles(t, dir, "IMG_001.jpg", "img_002.JPG", "other.txt")

	// 一般文字、不分大小寫
	items, err := RenamePreview(ps, RenameRule{Mode: "replace", Find: "img", Replace: "照片"})
	if err != nil {
		t.Fatal(err)
	}
	if got := newNames(items); !eq(got, []string{"照片_001.jpg", "照片_002.JPG", "other.txt"}) {
		t.Errorf("取代結果：%v", got)
	}
	if items[2].Status != "same" {
		t.Errorf("未變更的項目應為 same：%+v", items[2])
	}

	// 區分大小寫
	items, _ = RenamePreview(ps, RenameRule{Mode: "replace", Find: "IMG", Replace: "X", CaseSensitive: true})
	if got := newNames(items); !eq(got, []string{"X_001.jpg", "img_002.JPG", "other.txt"}) {
		t.Errorf("區分大小寫結果：%v", got)
	}

	// 規則運算式與群組
	items, _ = RenamePreview(ps, RenameRule{Mode: "replace", Find: `^(?:img)_(\d+)$`, Replace: "第$1張", Regex: true})
	if got := newNames(items); !eq(got, []string{"第001張.jpg", "第002張.JPG", "other.txt"}) {
		t.Errorf("規則運算式結果：%v", got)
	}

	// 預設不動副檔名；勾選後才一併處理
	items, _ = RenamePreview(ps[:1], RenameRule{Mode: "replace", Find: "jpg", Replace: "png"})
	if items[0].NewName != "IMG_001.jpg" {
		t.Errorf("預設不應改動副檔名：%v", items[0].NewName)
	}
	items, _ = RenamePreview(ps[:1], RenameRule{Mode: "replace", Find: "jpg", Replace: "png", ApplyToExt: true})
	if items[0].NewName != "IMG_001.png" {
		t.Errorf("勾選副檔名後應改動：%v", items[0].NewName)
	}

	// 規則運算式語法錯誤
	if _, err := RenamePreview(ps, RenameRule{Mode: "replace", Find: "(", Regex: true}); err == nil {
		t.Error("無效的規則運算式應回傳錯誤")
	}
	// 空的尋找字串：不變更
	items, _ = RenamePreview(ps, RenameRule{Mode: "replace", Find: "", Replace: "x"})
	for _, it := range items {
		if it.Status != "same" {
			t.Errorf("空尋找字串應全部為 same：%+v", it)
		}
	}
}

func TestRenameAffixTemplateCase(t *testing.T) {
	dir := t.TempDir()
	ps := mkFiles(t, dir, "b.txt", "a.txt", "c.md")

	items, _ := RenamePreview(ps, RenameRule{Mode: "affix", Prefix: "前_", Suffix: "_後"})
	if got := newNames(items); !eq(got, []string{"前_b_後.txt", "前_a_後.txt", "前_c_後.md"}) {
		t.Errorf("前後綴：%v", got)
	}

	items, _ = RenamePreview(ps, RenameRule{Mode: "template", Template: "文件_{n}", Start: 1, Step: 1, Digits: 3})
	if got := newNames(items); !eq(got, []string{"文件_001.txt", "文件_002.txt", "文件_003.md"}) {
		t.Errorf("流水號（依傳入順序）：%v", got)
	}

	items, _ = RenamePreview(ps, RenameRule{Mode: "template", Template: "{name}-{n}", Start: 10, Step: 5})
	if got := newNames(items); !eq(got, []string{"b-10.txt", "a-15.txt", "c-20.md"}) {
		t.Errorf("{name} 與起始／間隔：%v", got)
	}

	items, _ = RenamePreview(ps, RenameRule{Mode: "case", CaseMode: "upper"})
	if got := newNames(items); !eq(got, []string{"B.txt", "A.txt", "C.md"}) {
		t.Errorf("大寫（不含副檔名）：%v", got)
	}
	items, _ = RenamePreview(ps, RenameRule{Mode: "case", CaseMode: "upper", ApplyToExt: true})
	if got := newNames(items); !eq(got, []string{"B.TXT", "A.TXT", "C.MD"}) {
		t.Errorf("大寫（含副檔名）：%v", got)
	}

	ps2 := mkFiles(t, dir, "my_FILE-name here.txt")
	items, _ = RenamePreview(ps2, RenameRule{Mode: "case", CaseMode: "title"})
	if items[0].NewName != "My_File-Name Here.txt" {
		t.Errorf("首字大寫：%q", items[0].NewName)
	}
}

func TestRenameDotfileAndFolder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".gitignore"), "x")
	if err := os.Mkdir(filepath.Join(dir, "v1.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := []string{filepath.Join(dir, ".gitignore"), filepath.Join(dir, "v1.2")}
	items, _ := RenamePreview(ps, RenameRule{Mode: "affix", Prefix: "P_"})
	if got := newNames(items); !eq(got, []string{"P_.gitignore", "P_v1.2"}) {
		t.Errorf("dotfile／資料夾不應被當作有副檔名：%v", got)
	}
}

func TestRenameConflicts(t *testing.T) {
	dir := t.TempDir()
	ps := mkFiles(t, dir, "a.txt", "b.txt")
	mkFiles(t, dir, "x.txt") // 磁碟上既有、不在選取內

	// 新名稱彼此重複
	items, _ := RenamePreview(ps, RenameRule{Mode: "template", Template: "same"})
	if items[0].Status != "error" || items[1].Status != "error" {
		t.Errorf("重複的新名稱應標為錯誤：%+v", items)
	}

	// 與磁碟既有項目衝突
	items, _ = RenamePreview(ps[:1], RenameRule{Mode: "template", Template: "x"})
	if items[0].Status != "error" || !strings.Contains(items[0].Message, "已存在") {
		t.Errorf("與既有檔案衝突應標為錯誤：%+v", items[0])
	}

	// 非法字元
	items, _ = RenamePreview(ps[:1], RenameRule{Mode: "affix", Prefix: "a/b"})
	if items[0].Status != "error" {
		t.Errorf("非法名稱應標為錯誤：%+v", items[0])
	}

	// 新名稱等於另一個『不會被改名』的選取項目
	ps3 := mkFiles(t, dir, "keep.txt", "keep2.txt")
	items, _ = RenamePreview(ps3, RenameRule{Mode: "replace", Find: "keep2", Replace: "keep"})
	if items[1].Status != "error" {
		t.Errorf("與未改名項目衝突應標為錯誤：%+v", items)
	}

	// 套用時有錯誤必須整批不動
	if _, err := RenameApply(ps, RenameRule{Mode: "template", Template: "same"}); err == nil {
		t.Error("有錯誤時 RenameApply 應失敗")
	}
	for _, p := range ps {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("失敗時不得改動任何檔案：%v", err)
		}
	}
}

func TestRenameApplySimple(t *testing.T) {
	dir := t.TempDir()
	ps := mkFiles(t, dir, "a.txt", "b.txt", "c.txt")
	items, err := RenameApply(ps, RenameRule{Mode: "template", Template: "新_{n}", Start: 1, Digits: 2})
	if err != nil {
		t.Fatalf("RenameApply：%v", err)
	}
	for _, it := range items {
		if it.Status != "done" {
			t.Errorf("狀態應為 done：%+v", it)
		}
	}
	if read(t, filepath.Join(dir, "新_01.txt")) != "content-of-a.txt" || read(t, filepath.Join(dir, "新_03.txt")) != "content-of-c.txt" {
		t.Error("改名後內容不符")
	}
}

func TestRenameChainReallyWorks(t *testing.T) {
	dir := t.TempDir()
	ps := mkFiles(t, dir, "1.txt", "2.txt", "3.txt")
	// 起始 2：1→2、2→3、3→4
	items, err := RenameApply(ps, RenameRule{Mode: "template", Template: "{n}", Start: 2, Step: 1})
	if err != nil {
		t.Fatalf("接龍改名應成功：%v", err)
	}
	for _, it := range items {
		if it.Status != "done" {
			t.Errorf("狀態：%+v", it)
		}
	}
	if read(t, filepath.Join(dir, "2.txt")) != "content-of-1.txt" ||
		read(t, filepath.Join(dir, "3.txt")) != "content-of-2.txt" ||
		read(t, filepath.Join(dir, "4.txt")) != "content-of-3.txt" {
		t.Error("接龍後內容對應錯誤")
	}
	if _, err := os.Stat(filepath.Join(dir, "1.txt")); !os.IsNotExist(err) {
		t.Error("1.txt 應已改名")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".fmtmp") {
			t.Errorf("留下暫存名稱：%s", e.Name())
		}
	}
}

func TestRenameCaseOnly(t *testing.T) {
	dir := t.TempDir()
	ps := mkFiles(t, dir, "abc.txt")
	items, err := RenameApply(ps, RenameRule{Mode: "case", CaseMode: "upper"})
	if err != nil {
		t.Fatalf("僅大小寫變更應成功：%v", err)
	}
	if items[0].Status != "done" {
		t.Errorf("狀態：%+v", items[0])
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "ABC.txt" {
		t.Errorf("改名後名稱錯誤：%v", entries)
	}
}

func TestRenameMissingFile(t *testing.T) {
	dir := t.TempDir()
	items, err := RenamePreview([]string{filepath.Join(dir, "不存在.txt")}, RenameRule{Mode: "affix", Prefix: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "error" {
		t.Errorf("找不到的項目應為 error：%+v", items[0])
	}
}

func TestNormalizeReplacement(t *testing.T) {
	cases := map[string]string{
		"第$1張":    "第${1}張",
		"$1$2":    "${1}${2}",
		"$$1":     "$$1",
		"${name}": "${name}",
		"價格$":     "價格$",
		"a$12b":   "a${12}b",
	}
	for in, want := range cases {
		if got := normalizeReplacement(in); got != want {
			t.Errorf("normalizeReplacement(%q) = %q，預期 %q", in, got, want)
		}
	}
}
