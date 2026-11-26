package fsops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustCopy(t *testing.T, srcs []string, dst string, pol Policy) Result {
	t.Helper()
	res, err := Copy(context.Background(), srcs, dst, pol, nil)
	if err != nil {
		t.Fatalf("Copy 失敗：%v", err)
	}
	return res
}

func mustMove(t *testing.T, srcs []string, dst string, pol Policy) Result {
	t.Helper()
	res, err := Move(context.Background(), srcs, dst, pol, nil)
	if err != nil {
		t.Fatalf("Move 失敗：%v", err)
	}
	return res
}

func TestCopyTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	write(t, filepath.Join(src, "資料夾", "a.txt"), "AAA")
	write(t, filepath.Join(src, "資料夾", "深層", "b.txt"), "BBB")
	write(t, filepath.Join(src, "top.txt"), "TOP")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(src, "top.txt"), old, old); err != nil {
		t.Fatal(err)
	}

	res := mustCopy(t, []string{filepath.Join(src, "資料夾"), filepath.Join(src, "top.txt")}, dst, PolicyKeepBoth)
	if res.OK != 2 || len(res.Failed) != 0 || res.Cancelled {
		t.Fatalf("結果不符預期：%+v", res)
	}
	if got := read(t, filepath.Join(dst, "資料夾", "深層", "b.txt")); got != "BBB" {
		t.Errorf("深層檔案內容錯誤：%q", got)
	}
	if got := read(t, filepath.Join(dst, "top.txt")); got != "TOP" {
		t.Errorf("top.txt 內容錯誤：%q", got)
	}
	st, err := os.Stat(filepath.Join(dst, "top.txt"))
	if err != nil || !st.ModTime().Equal(old) {
		t.Errorf("修改時間應被保留：%v %v", err, st)
	}
	// 來源仍在
	if _, err := os.Stat(filepath.Join(src, "top.txt")); err != nil {
		t.Error("複製後來源不應消失")
	}
	// 不可留下暫存檔
	filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if strings.HasSuffix(p, ".fmpart") {
			t.Errorf("留下暫存檔：%s", p)
		}
		return nil
	})
}

func TestCopyPolicies(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "f.txt")
	dstDir := filepath.Join(root, "dst")
	write(t, src, "NEW")
	write(t, filepath.Join(dstDir, "f.txt"), "OLD")

	// 略過
	res := mustCopy(t, []string{src}, dstDir, PolicySkip)
	if res.Skipped != 1 || read(t, filepath.Join(dstDir, "f.txt")) != "OLD" {
		t.Errorf("略過策略錯誤：%+v", res)
	}
	// 保留兩者
	res = mustCopy(t, []string{src}, dstDir, PolicyKeepBoth)
	if res.OK != 1 || read(t, filepath.Join(dstDir, "f - 複本.txt")) != "NEW" || read(t, filepath.Join(dstDir, "f.txt")) != "OLD" {
		t.Errorf("保留兩者策略錯誤：%+v", res)
	}
	res = mustCopy(t, []string{src}, dstDir, PolicyKeepBoth)
	if read(t, filepath.Join(dstDir, "f - 複本 (2).txt")) != "NEW" {
		t.Errorf("第二份複本命名錯誤：%+v", res)
	}
	// 覆寫
	res = mustCopy(t, []string{src}, dstDir, PolicyOverwrite)
	if res.OK != 1 || read(t, filepath.Join(dstDir, "f.txt")) != "NEW" {
		t.Errorf("覆寫策略錯誤：%+v", res)
	}
}

func TestCopyMergeFolders(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "D", "same.txt"), "FROM_SRC")
	write(t, filepath.Join(root, "src", "D", "only_src.txt"), "S")
	write(t, filepath.Join(root, "dst", "D", "same.txt"), "FROM_DST")
	write(t, filepath.Join(root, "dst", "D", "only_dst.txt"), "T")

	mustCopy(t, []string{filepath.Join(root, "src", "D")}, filepath.Join(root, "dst"), PolicyOverwrite)
	d := filepath.Join(root, "dst", "D")
	if read(t, filepath.Join(d, "same.txt")) != "FROM_SRC" || read(t, filepath.Join(d, "only_src.txt")) != "S" || read(t, filepath.Join(d, "only_dst.txt")) != "T" {
		t.Error("合併資料夾結果不正確")
	}
}

func TestCopySameDirKeepsBoth(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	write(t, f, "X")
	info := CheckConflicts([]string{f}, dir)
	if !info.SameDir {
		t.Error("應偵測為同資料夾")
	}
	res := mustCopy(t, []string{f}, dir, PolicyOverwrite) // 即使指定覆寫也必須保留兩者，避免自己覆蓋自己
	if res.OK != 1 || read(t, filepath.Join(dir, "a - 複本.txt")) != "X" || read(t, f) != "X" {
		t.Errorf("同資料夾複製錯誤：%+v", res)
	}
}

func TestCopyIntoItself(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "D")
	write(t, filepath.Join(d, "a.txt"), "A")
	sub := filepath.Join(d, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res := mustCopy(t, []string{d}, sub, PolicyKeepBoth)
	if res.OK != 0 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0], "內部") {
		t.Errorf("應拒絕複製到自己內部：%+v", res)
	}
	if entries, _ := os.ReadDir(sub); len(entries) != 0 {
		t.Error("不應在子資料夾產生任何內容")
	}
}

func TestMoveSameVolume(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "D", "a.txt"), "A")
	write(t, filepath.Join(root, "src", "f.txt"), "F")
	dst := filepath.Join(root, "dst")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	res := mustMove(t, []string{filepath.Join(root, "src", "D"), filepath.Join(root, "src", "f.txt")}, dst, PolicyKeepBoth)
	if res.OK != 2 || len(res.Failed) != 0 {
		t.Fatalf("移動結果錯誤：%+v", res)
	}
	if read(t, filepath.Join(dst, "D", "a.txt")) != "A" || read(t, filepath.Join(dst, "f.txt")) != "F" {
		t.Error("移動後內容不正確")
	}
	if _, err := os.Stat(filepath.Join(root, "src", "f.txt")); !os.IsNotExist(err) {
		t.Error("移動後來源應消失")
	}
}

func TestMoveToSameDirIsNoop(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	write(t, f, "A")
	res := mustMove(t, []string{f}, dir, PolicyKeepBoth)
	if res.Skipped != 1 || read(t, f) != "A" {
		t.Errorf("移到同資料夾應不做事：%+v", res)
	}
}

func TestMoveConflictPolicies(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "f.txt")
	dst := filepath.Join(root, "dst")
	write(t, src, "NEW")
	write(t, filepath.Join(dst, "f.txt"), "OLD")

	res := mustMove(t, []string{src}, dst, PolicySkip)
	if res.Skipped != 1 || read(t, src) != "NEW" || read(t, filepath.Join(dst, "f.txt")) != "OLD" {
		t.Errorf("略過策略錯誤：%+v", res)
	}
	res = mustMove(t, []string{src}, dst, PolicyOverwrite)
	if res.OK != 1 || read(t, filepath.Join(dst, "f.txt")) != "NEW" {
		t.Errorf("覆寫策略錯誤：%+v", res)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("覆寫移動後來源應消失")
	}
}

func TestMoveMergeFolders(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "D", "a.txt"), "A")
	write(t, filepath.Join(root, "src", "D", "sub", "b.txt"), "B")
	write(t, filepath.Join(root, "dst", "D", "keep.txt"), "K")
	res := mustMove(t, []string{filepath.Join(root, "src", "D")}, filepath.Join(root, "dst"), PolicyOverwrite)
	if res.OK != 1 || len(res.Failed) != 0 {
		t.Fatalf("合併移動結果錯誤：%+v", res)
	}
	d := filepath.Join(root, "dst", "D")
	if read(t, filepath.Join(d, "a.txt")) != "A" || read(t, filepath.Join(d, "sub", "b.txt")) != "B" || read(t, filepath.Join(d, "keep.txt")) != "K" {
		t.Error("合併移動內容不正確")
	}
	if _, err := os.Stat(filepath.Join(root, "src", "D")); !os.IsNotExist(err) {
		t.Error("合併移動後，來源資料夾應被清除")
	}
}

func TestMoveMergeSkipKeepsRemainder(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "D", "dup.txt"), "SRC")
	write(t, filepath.Join(root, "src", "D", "new.txt"), "NEW")
	write(t, filepath.Join(root, "dst", "D", "dup.txt"), "DST")
	mustMove(t, []string{filepath.Join(root, "src", "D")}, filepath.Join(root, "dst"), PolicySkip)
	if read(t, filepath.Join(root, "dst", "D", "dup.txt")) != "DST" || read(t, filepath.Join(root, "dst", "D", "new.txt")) != "NEW" {
		t.Error("略過式合併結果錯誤")
	}
	// 被略過的檔案必須仍留在來源，不能遺失
	if read(t, filepath.Join(root, "src", "D", "dup.txt")) != "SRC" {
		t.Error("被略過的檔案必須保留在來源")
	}
	if _, err := os.Stat(filepath.Join(root, "src", "D", "new.txt")); !os.IsNotExist(err) {
		t.Error("已移動的檔案不應留在來源")
	}
}

func TestCopyCancel(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, 8<<20)
	write(t, filepath.Join(root, "src", "big.bin"), string(big))
	dst := filepath.Join(root, "dst")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Copy(ctx, []string{filepath.Join(root, "src", "big.bin")}, dst, PolicyKeepBoth, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cancelled {
		t.Errorf("應回報已取消：%+v", res)
	}
	if entries, _ := os.ReadDir(dst); len(entries) != 0 {
		t.Errorf("取消後不應留下任何檔案（含暫存檔），實際：%v", entries)
	}
}

func TestCopyProgress(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "a.txt"), strings.Repeat("a", 1000))
	write(t, filepath.Join(root, "src", "b.txt"), strings.Repeat("b", 500))
	dst := filepath.Join(root, "dst")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	var last Progress
	_, err := Copy(context.Background(), []string{filepath.Join(root, "src", "a.txt"), filepath.Join(root, "src", "b.txt")}, dst, PolicyKeepBoth, func(p Progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if last.Total != 1500 || last.Done != 1500 || last.FilesTotal != 2 || last.FilesDone != 2 {
		t.Errorf("進度不正確：%+v", last)
	}
}

func TestCopyMissingSourceAndBadDest(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "dst")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	res := mustCopy(t, []string{filepath.Join(root, "不存在.txt")}, dst, PolicyKeepBoth)
	if len(res.Failed) != 1 {
		t.Errorf("不存在的來源應記錄一筆失敗：%+v", res)
	}
	if _, err := Copy(context.Background(), nil, filepath.Join(root, "沒有這個資料夾"), PolicyKeepBoth, nil); err == nil {
		t.Error("目的地不存在應回傳錯誤")
	}
}

func TestCheckConflicts(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "a.txt"), "1")
	write(t, filepath.Join(root, "src", "b.txt"), "2")
	write(t, filepath.Join(root, "dst", "a.txt"), "x")
	ci := CheckConflicts([]string{filepath.Join(root, "src", "a.txt"), filepath.Join(root, "src", "b.txt")}, filepath.Join(root, "dst"))
	if ci.SameDir || len(ci.Names) != 1 || ci.Names[0] != "a.txt" {
		t.Errorf("衝突檢查錯誤：%+v", ci)
	}
}

func TestMeasure(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "D", "a.txt"), strings.Repeat("x", 100))
	write(t, filepath.Join(root, "D", "s", "b.txt"), strings.Repeat("x", 50))
	sz, err := Measure(context.Background(), []string{filepath.Join(root, "D")})
	if err != nil {
		t.Fatal(err)
	}
	if sz.Bytes != 150 || sz.Files != 2 || sz.Folders != 2 {
		t.Errorf("計算大小錯誤：%+v", sz)
	}
}

func TestDeletePermanent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "D", "a.txt"), "A")
	write(t, filepath.Join(root, "f.txt"), "F")
	res := DeletePermanent(context.Background(), []string{filepath.Join(root, "D"), filepath.Join(root, "f.txt")})
	if res.OK != 2 || len(res.Failed) != 0 {
		t.Errorf("永久刪除結果錯誤：%+v", res)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("應全部刪除：%v", entries)
	}
}

func TestUniqueNameDotfileAndDir(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".gitignore"), "x")
	if got := filepath.Base(uniqueName(filepath.Join(dir, ".gitignore"), false)); got != ".gitignore - 複本" {
		t.Errorf("dotfile 命名錯誤：%q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "v1.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(uniqueName(filepath.Join(dir, "v1.2"), true)); got != "v1.2 - 複本" {
		t.Errorf("資料夾命名錯誤：%q", got)
	}
}
