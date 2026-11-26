package fsops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Policy 是目的地已有同名項目時的處理方式。
type Policy string

const (
	PolicyOverwrite Policy = "overwrite" // 取代（資料夾則合併）
	PolicySkip      Policy = "skip"      // 略過
	PolicyKeepBoth  Policy = "keepboth"  // 兩者都保留（新項目自動改名）
)

// Progress 是複製／移動過程的進度。
type Progress struct {
	Phase      string `json:"phase"` // scan | run
	Done       int64  `json:"done"`
	Total      int64  `json:"total"`
	Current    string `json:"current"`
	FilesDone  int    `json:"filesDone"`
	FilesTotal int    `json:"filesTotal"`
}

// Result 是複製／移動／刪除的結果摘要。
type Result struct {
	OK         int      `json:"ok"`
	Skipped    int      `json:"skipped"`
	Failed     []string `json:"failed"`
	MoreFailed int      `json:"moreFailed"`
	Warnings   []string `json:"warnings"`
	Cancelled  bool     `json:"cancelled"`
}

// Size 是計算大小的結果。
type Size struct {
	Bytes   int64 `json:"bytes"`
	Files   int   `json:"files"`
	Folders int   `json:"folders"`
}

// ConflictInfo 是貼上前的衝突檢查結果。
type ConflictInfo struct {
	SameDir bool     `json:"sameDir"` // 來源與目的地為同一資料夾
	Names   []string `json:"names"`   // 目的地已存在的同名項目
}

const maxListed = 50

type node struct {
	isDir bool
	link  bool
	size  int64
	mod   time.Time
	mode  fs.FileMode
}

func statNode(path string, follow bool) (node, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return node{}, err
	}
	isDir, _, link := platformAttrs(filepath.Dir(path), filepath.Base(path), info)
	n := node{isDir: isDir, link: link, size: info.Size(), mod: info.ModTime(), mode: info.Mode()}
	if link && follow { // 使用者明確選取的連結：追蹤到目標
		if st, err := os.Stat(path); err == nil {
			n = node{isDir: st.IsDir(), size: st.Size(), mod: st.ModTime(), mode: st.Mode()}
		}
	}
	if n.isDir {
		n.size = 0
	}
	return n, nil
}

var errSkipDir = errors.New("skip dir")

type walker struct {
	ctx   context.Context
	visit func(p string, n node) error // 回傳 errSkipDir 可略過該資料夾內容；其他錯誤會中止
	onErr func(p string, err error)    // 非致命錯誤（例如無權限讀取子資料夾）
}

func (w *walker) walk(p string, n node, depth int) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if err := w.visit(p, n); err != nil {
		if errors.Is(err, errSkipDir) {
			return nil
		}
		return err
	}
	if !n.isDir || n.link {
		return nil
	}
	if depth > 255 {
		w.onErr(p, errors.New("資料夾層數過深"))
		return nil
	}
	des, err := os.ReadDir(p)
	if err != nil {
		w.onErr(p, err)
		return nil
	}
	for _, de := range des {
		cp := filepath.Join(p, de.Name())
		info, err := de.Info()
		if err != nil {
			w.onErr(cp, err)
			continue
		}
		isDir, _, link := platformAttrs(p, de.Name(), info)
		cn := node{isDir: isDir, link: link, size: info.Size(), mod: info.ModTime(), mode: info.Mode()}
		if cn.isDir {
			cn.size = 0
		}
		if err := w.walk(cp, cn, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func measureOne(ctx context.Context, path string, sz *Size) error {
	n, err := statNode(path, true)
	if err != nil {
		return err
	}
	w := &walker{ctx: ctx, onErr: func(string, error) {}, visit: func(p string, n node) error {
		if n.link && p != path {
			return nil
		}
		if n.isDir {
			sz.Folders++
		} else {
			sz.Files++
			sz.Bytes += n.size
		}
		return nil
	}}
	return w.walk(path, n, 0)
}

// Measure 計算多個項目（含子資料夾）的總大小。
func Measure(ctx context.Context, paths []string) (Size, error) {
	var sz Size
	for _, p := range paths {
		if err := measureOne(ctx, p, &sz); err != nil {
			if ctx.Err() != nil {
				return sz, ctx.Err()
			}
			// 項目不存在或無權限：略過，不中斷整體計算
		}
	}
	return sz, nil
}

func normCmp(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func samePath(a, b string) bool { return normCmp(a) == normCmp(b) }

// isWithin 回傳 child 是否等於 parent 或位於 parent 底下。
func isWithin(parent, child string) bool {
	p, c := normCmp(parent), normCmp(child)
	if p == c {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(p, sep) {
		p += sep
	}
	return strings.HasPrefix(c, p)
}

// CheckConflicts 檢查貼上前目的地是否已有同名項目。
func CheckConflicts(srcs []string, dstDir string) ConflictInfo {
	ci := ConflictInfo{Names: []string{}}
	for _, s := range srcs {
		if samePath(filepath.Dir(s), dstDir) {
			ci.SameDir = true
			continue
		}
		if exists(filepath.Join(dstDir, filepath.Base(s))) {
			ci.Names = append(ci.Names, filepath.Base(s))
		}
	}
	return ci
}

// uniqueName 產生不與現有項目衝突的名稱：「名稱 - 複本.ext」、「名稱 - 複本 (2).ext」…
func uniqueName(path string, isDir bool) string {
	dir, name := filepath.Dir(path), filepath.Base(path)
	ext := ""
	if !isDir {
		ext = filepath.Ext(name)
		if ext == name { // 例如 .gitignore
			ext = ""
		}
	}
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		suffix := " - 複本"
		if i > 1 {
			suffix = fmt.Sprintf(" - 複本 (%d)", i)
		}
		cand := filepath.Join(dir, base+suffix+ext)
		if !exists(cand) {
			return cand
		}
	}
}

type runner struct {
	ctx    context.Context
	policy Policy
	move   bool
	cb     func(Progress)
	prog   Progress
	last   time.Time
	res    Result
	buf    []byte
}

func (r *runner) emit(force bool) {
	if r.cb == nil {
		return
	}
	if !force && time.Since(r.last) < 80*time.Millisecond {
		return
	}
	r.last = time.Now()
	r.cb(r.prog)
}

func (r *runner) failCount() int { return len(r.res.Failed) + r.res.MoreFailed }

func (r *runner) fail(p string, err error) {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	if len(r.res.Failed) >= maxListed {
		r.res.MoreFailed++
		return
	}
	r.res.Failed = append(r.res.Failed, fmt.Sprintf("%s：%v", filepath.Base(p), err))
}

func (r *runner) warn(msg string) {
	if len(r.res.Warnings) < maxListed {
		r.res.Warnings = append(r.res.Warnings, msg)
	}
}

// Copy 複製項目到 dstDir。
func Copy(ctx context.Context, srcs []string, dstDir string, policy Policy, cb func(Progress)) (Result, error) {
	return run(ctx, srcs, dstDir, policy, false, cb)
}

// Move 移動項目到 dstDir（同磁碟區直接改名，跨磁碟區改為複製後刪除來源）。
func Move(ctx context.Context, srcs []string, dstDir string, policy Policy, cb func(Progress)) (Result, error) {
	return run(ctx, srcs, dstDir, policy, true, cb)
}

func run(ctx context.Context, srcs []string, dstDir string, policy Policy, move bool, cb func(Progress)) (Result, error) {
	st, err := os.Stat(dstDir)
	if err != nil {
		return Result{}, friendlyErr("存取目的地", dstDir, err)
	}
	if !st.IsDir() {
		return Result{}, fmt.Errorf("目的地不是資料夾：%s", dstDir)
	}
	r := &runner{ctx: ctx, policy: policy, move: move, cb: cb}
	r.res.Failed = []string{}
	r.res.Warnings = []string{}

	r.prog.Phase = "scan"
	r.emit(true)
	sizes := make([]int64, len(srcs))
	bad := make([]bool, len(srcs)) // 掃描階段就失敗的項目，只回報一次
	for i, s := range srcs {
		var sz Size
		if err := measureOne(ctx, s, &sz); err != nil {
			if ctx.Err() != nil {
				r.res.Cancelled = true
				return r.res, nil
			}
			r.fail(s, err)
			bad[i] = true
			continue
		}
		sizes[i] = sz.Bytes
		r.prog.Total += sz.Bytes
		r.prog.FilesTotal += sz.Files
	}

	r.prog.Phase = "run"
	for i, src := range srcs {
		if ctx.Err() != nil {
			break
		}
		if bad[i] {
			continue
		}
		r.item(src, dstDir, sizes[i])
	}
	if ctx.Err() != nil {
		r.res.Cancelled = true
	}
	r.emit(true)
	return r.res, nil
}

func (r *runner) item(src, dstDir string, size int64) {
	n, err := statNode(src, true)
	if err != nil {
		r.fail(src, err)
		return
	}
	dst := filepath.Join(dstDir, filepath.Base(src))
	sameDir := samePath(filepath.Dir(src), dstDir)
	if sameDir && r.move { // 來源即目的地：不需處理
		r.res.Skipped++
		r.prog.Done += size
		return
	}
	if n.isDir && isWithin(src, dstDir) {
		r.fail(src, errors.New("無法將資料夾複製或移動到它自己的內部"))
		return
	}
	if dstSt, err := os.Lstat(dst); err == nil {
		pol := r.policy
		if sameDir {
			pol = PolicyKeepBoth // 同資料夾複製一律保留兩者
		}
		switch pol {
		case PolicySkip:
			// 資料夾對資料夾：進入合併，由逐檔規則決定哪些略過；其餘情況整個略過
			if !(n.isDir && dstSt.IsDir()) {
				r.res.Skipped++
				r.prog.Done += size
				r.emit(false)
				return
			}
		case PolicyKeepBoth:
			dst = uniqueName(dst, n.isDir)
		}
	}

	if r.move {
		if r.tryRename(src, dst, n) {
			r.prog.Done += size
			r.emit(false)
			return
		}
	}

	before := r.failCount()
	w := &walker{ctx: r.ctx, visit: r.visitor(src, dst), onErr: r.fail}
	if err := w.walk(src, n, 0); err != nil { // 只有取消會走到這裡
		return
	}
	if r.move {
		r.removeEmptyDirs(src, n)
	}
	if r.failCount() == before {
		r.res.OK++
	}
}

// tryRename 嘗試直接改名。回傳 true 表示已處理（成功或已記錄失敗），不需再複製。
func (r *runner) tryRename(src, dst string, n node) bool {
	if st, err := os.Lstat(dst); err == nil && (st.IsDir() || n.isDir) {
		return false // 需要逐檔合併
	}
	err := os.Rename(src, dst)
	switch {
	case err == nil:
		r.res.OK++
		return true
	case isCrossDevice(err):
		return false
	default:
		r.fail(src, err)
		return true
	}
}

func (r *runner) visitor(srcRoot, dstRoot string) func(string, node) error {
	return func(p string, n node) error {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		dst := dstRoot
		if p != srcRoot {
			rel, err := filepath.Rel(srcRoot, p)
			if err != nil {
				r.fail(p, err)
				return errSkipDir
			}
			dst = filepath.Join(dstRoot, rel)
			if n.link {
				r.warn("已略過符號連結：" + filepath.Base(p))
				return nil
			}
		}
		if n.isDir {
			st, err := os.Lstat(dst)
			switch {
			case err == nil && st.IsDir():
				return nil // 合併
			case err == nil:
				r.fail(p, errors.New("目的地已有同名檔案"))
				return errSkipDir
			}
			if err := os.Mkdir(dst, 0o755); err != nil {
				r.fail(p, err)
				return errSkipDir
			}
			return nil
		}
		return r.copyFile(p, dst, n)
	}
}

// copyFile 複製單一檔案：先寫入同資料夾暫存檔，成功後再改名，避免留下半成品或破壞既有檔案。
// 非致命錯誤記錄後回傳 nil；只有取消會回傳錯誤。
func (r *runner) copyFile(src, dst string, n node) error {
	r.prog.Current = filepath.Base(src)
	if st, err := os.Lstat(dst); err == nil {
		if st.IsDir() {
			r.fail(src, errors.New("目的地已有同名資料夾"))
			r.prog.Done += n.size
			return nil
		}
		switch r.policy {
		case PolicySkip:
			r.res.Skipped++
			r.prog.Done += n.size
			r.prog.FilesDone++
			r.emit(false)
			return nil
		case PolicyKeepBoth:
			dst = uniqueName(dst, false)
		}
	}

	in, err := os.Open(src)
	if err != nil {
		r.fail(src, err)
		r.prog.Done += n.size
		return nil
	}
	defer in.Close()

	tmp := dst + ".fmpart"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		r.fail(src, err)
		r.prog.Done += n.size
		return nil
	}
	ok := false
	defer func() {
		if !ok {
			out.Close()
			os.Remove(tmp)
		}
	}()

	if r.buf == nil {
		r.buf = make([]byte, 1<<20)
	}
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		nr, er := in.Read(r.buf)
		if nr > 0 {
			if _, ew := out.Write(r.buf[:nr]); ew != nil {
				r.fail(src, ew)
				return nil
			}
			r.prog.Done += int64(nr)
			r.emit(false)
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			r.fail(src, er)
			return nil
		}
	}
	// Windows 的 os.Open 沒有 FILE_SHARE_DELETE：來源檔仍開啟時無法刪除或改名，
	// 因此必須先關閉來源，之後才能在「移動」時刪除它。
	in.Close()
	if err := out.Close(); err != nil {
		r.fail(src, err)
		return nil
	}
	_ = os.Chtimes(tmp, time.Now(), n.mod)
	_ = os.Chmod(tmp, n.mode.Perm())
	if err := replaceFile(tmp, dst); err != nil {
		r.fail(src, err)
		return nil
	}
	ok = true
	r.prog.FilesDone++
	if r.move {
		if err := os.Remove(src); err != nil {
			r.fail(src, fmt.Errorf("已複製，但無法刪除來源：%w", err))
		}
	}
	r.emit(false)
	return nil
}

func replaceFile(tmp, dst string) error {
	err := os.Rename(tmp, dst)
	if err != nil { // 目的地為唯讀檔時先解除唯讀再重試一次
		_ = os.Chmod(dst, 0o666)
		err = os.Rename(tmp, dst)
	}
	return err
}

// removeEmptyDirs 由內而外刪除（移動後留下的）空資料夾；非空者會自然保留。
func (r *runner) removeEmptyDirs(p string, n node) {
	if !n.isDir || n.link {
		return
	}
	des, err := os.ReadDir(p)
	if err == nil {
		for _, de := range des {
			info, err := de.Info()
			if err != nil {
				continue
			}
			isDir, _, link := platformAttrs(p, de.Name(), info)
			if isDir && !link {
				r.removeEmptyDirs(filepath.Join(p, de.Name()), node{isDir: true})
			}
		}
	}
	_ = os.Remove(p)
}

// DeletePermanent 永久刪除項目（不經資源回收筒）。
func DeletePermanent(ctx context.Context, paths []string) Result {
	res := Result{Failed: []string{}, Warnings: []string{}}
	for _, p := range paths {
		if ctx.Err() != nil {
			res.Cancelled = true
			break
		}
		if err := os.RemoveAll(p); err != nil {
			var pe *fs.PathError
			if errors.As(err, &pe) {
				err = pe.Err
			}
			if len(res.Failed) >= maxListed {
				res.MoreFailed++
			} else {
				res.Failed = append(res.Failed, fmt.Sprintf("%s：%v", filepath.Base(p), err))
			}
			continue
		}
		res.OK++
	}
	return res
}
