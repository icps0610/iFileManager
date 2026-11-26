package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"ifilemanager/internal/fsops"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是前端可呼叫的後端。所有匯出的方法都會被 Wails 綁定到前端的 window.go.main.App。
type App struct {
	ctx    context.Context
	mu     sync.Mutex
	cancel context.CancelFunc // 目前進行中的複製／移動／刪除
}

// BootInfo 是前端啟動時需要的環境資訊。
type BootInfo struct {
	Home   string        `json:"home"`
	Sep    string        `json:"sep"`
	OS     string        `json:"os"`
	Drives []fsops.Drive `json:"drives"`
	Places []fsops.Place `json:"places"`
}

// NewApp 建立 App。
func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	log.Printf("[資訊] 應用程式啟動（%s/%s）", runtime.GOOS, runtime.GOARCH)
	go a.watchDrives(ctx)
	// 啟動時先在背景讀取快速存取（PowerShell 冷啟動可能要好幾秒），前端第一次查詢時就能直接用快取
	go func() {
		t0 := time.Now()
		if q, err := fsops.QuickAccess(); err != nil {
			logErr("預先讀取快速存取", err)
		} else {
			log.Printf("[資訊] 預先讀取快速存取完成：%d 項，耗時 %v", len(q), time.Since(t0).Round(time.Millisecond))
		}
	}()
}

// DriveChange 是磁碟機插入／移除時送給前端的事件內容（事件名稱 drives:changed）。
type DriveChange struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// watchDrives 每秒檢查一次磁碟機代號，插入或拔除 USB 等磁碟時通知前端更新。
func (a *App) watchDrives(ctx context.Context) {
	last := fsops.DriveMask()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		m := fsops.DriveMask()
		if m == last {
			continue
		}
		added, removed := fsops.DiffDriveMask(last, m)
		last = m
		log.Printf("[資訊] 磁碟機變更：新增 %v，移除 %v", added, removed)
		wr.EventsEmit(ctx, "drives:changed", DriveChange{Added: added, Removed: removed})
	}
}

// initLog 把記錄寫到使用者快取資料夾（Windows 為 %LOCALAPPDATA%\iFileManager\fm.log），超過 1MB 時重新開始。
func initLog() {
	dir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	dir = filepath.Join(dir, "iFileManager")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "fm.log")
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > 1<<20 {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
}

// logErr 記錄錯誤並原樣回傳，方便在 return 時一併使用。
func logErr(op string, err error) error {
	if err != nil {
		log.Printf("[錯誤] %s：%v", op, err)
	}
	return err
}

// Bootstrap 回傳啟動資訊。
func (a *App) Bootstrap() BootInfo {
	home, _ := os.UserHomeDir()
	return BootInfo{
		Home:   home,
		Sep:    string(filepath.Separator),
		OS:     runtime.GOOS,
		Drives: fsops.Drives(),
		Places: fsops.Places(),
	}
}

// Locations 是右側「位置」區塊需要的資料。
type Locations struct {
	Drives    []fsops.Drive `json:"drives"`
	Quick     []fsops.Place `json:"quick"`
	QuickFrom string        `json:"quickFrom"` // system：Windows 快速存取；fallback：讀不到時改用常用資料夾
}

// Locations 回傳所有磁碟機與系統的「快速存取」。讀不到快速存取時改回傳常用資料夾。
func (a *App) Locations() Locations {
	loc := Locations{Drives: fsops.Drives()}
	q, err := fsops.QuickAccess()
	if err == nil && len(q) > 0 {
		loc.Quick, loc.QuickFrom = q, "system"
		return loc
	}
	if err != nil {
		logErr("讀取快速存取", err)
	}
	loc.Quick, loc.QuickFrom = fsops.Places(), "fallback"
	return loc
}

// RefreshLocations 清除快速存取快取後重新讀取磁碟與快速存取（右側面板的「重新整理」按鈕）。
func (a *App) RefreshLocations() Locations {
	log.Printf("[執行] 重新讀取磁碟與快速存取")
	fsops.ResetQuickAccessCache()
	return a.Locations()
}

// PinQuick 把資料夾釘選到 Windows「快速存取」。
func (a *App) PinQuick(path string) error {
	log.Printf("[執行] 釘選到快速存取：%s", path)
	if err := fsops.PinQuickAccess(path); err != nil {
		logErr("釘選到快速存取", err)
		return err
	}
	return nil
}

// UnpinQuick 把資料夾從 Windows「快速存取」取消釘選。
func (a *App) UnpinQuick(path string) error {
	log.Printf("[執行] 取消釘選快速存取：%s", path)
	if err := fsops.UnpinQuickAccess(path); err != nil {
		logErr("取消釘選快速存取", err)
		return err
	}
	return nil
}

// ShellMenu 在滑鼠位置顯示 Windows 原生右鍵選單。paths 為選取項目（空陣列＝資料夾 dir 的空白處）。
// 回傳：空字串＝取消；copy／cut／paste／rename／delete＝由前端自行處理；invoked＝系統已執行。
func (a *App) ShellMenu(dir string, paths []string, canPaste bool) (string, error) {
	log.Printf("[執行] 顯示系統右鍵選單：%d 項，資料夾 %s", len(paths), dir)
	act, err := fsops.ShowShellMenu(dir, paths, canPaste)
	return act, logErr("系統右鍵選單", err)
}

// ListDir 列出資料夾內容；path 為空字串代表「本機」（磁碟機清單）。
func (a *App) ListDir(path string) (*fsops.Listing, error) {
	l, err := fsops.ListDir(path)
	return l, logErr("列出資料夾 "+path, err)
}

// CreateDir 建立資料夾。
func (a *App) CreateDir(parent, name string) (string, error) {
	p, err := fsops.CreateDir(parent, name)
	return p, logErr("建立資料夾 "+name, err)
}

// CreateFile 建立空白檔案。
func (a *App) CreateFile(parent, name string) (string, error) {
	p, err := fsops.CreateFile(parent, name)
	return p, logErr("建立檔案 "+name, err)
}

// Rename 重新命名單一項目。
func (a *App) Rename(path, newName string) (string, error) {
	p, err := fsops.Rename(path, newName)
	return p, logErr("重新命名 "+path, err)
}

// RenamePreview 預覽批次改名結果（不改動檔案）。
func (a *App) RenamePreview(paths []string, rule fsops.RenameRule) ([]fsops.RenameItem, error) {
	return fsops.RenamePreview(paths, rule)
}

// RenameApply 執行批次改名。
func (a *App) RenameApply(paths []string, rule fsops.RenameRule) ([]fsops.RenameItem, error) {
	items, err := fsops.RenameApply(paths, rule)
	if err == nil {
		log.Printf("[資訊] 批次改名完成，共 %d 項", len(items))
	}
	return items, logErr("批次改名", err)
}

// CheckConflicts 貼上前檢查目的地是否已有同名項目。
func (a *App) CheckConflicts(srcs []string, dstDir string) fsops.ConflictInfo {
	return fsops.CheckConflicts(srcs, dstDir)
}

// CalcSize 計算選取項目（含子資料夾）的總大小。
func (a *App) CalcSize(paths []string) (fsops.Size, error) {
	ctx, cancel := a.beginOp()
	if ctx == nil {
		return fsops.Size{}, errors.New("已有檔案操作進行中，請稍候")
	}
	defer a.endOp(cancel)
	return fsops.Measure(ctx, paths)
}

// Copy 複製項目到 dstDir。policy 為 overwrite | skip | keepboth。
func (a *App) Copy(srcs []string, dstDir, policy string) (fsops.Result, error) {
	return a.transfer(false, srcs, dstDir, policy)
}

// Move 移動項目到 dstDir。
func (a *App) Move(srcs []string, dstDir, policy string) (fsops.Result, error) {
	return a.transfer(true, srcs, dstDir, policy)
}

func (a *App) transfer(move bool, srcs []string, dstDir, policy string) (fsops.Result, error) {
	ctx, cancel := a.beginOp()
	if ctx == nil {
		return fsops.Result{}, errors.New("已有檔案操作進行中，請稍候")
	}
	defer a.endOp(cancel)

	kind := "複製"
	run := fsops.Copy
	if move {
		kind = "移動"
		run = fsops.Move
	}
	log.Printf("[執行] %s %d 項 → %s（衝突策略：%s）", kind, len(srcs), dstDir, policy)
	res, err := run(ctx, srcs, dstDir, fsops.Policy(policy), func(p fsops.Progress) {
		if a.ctx != nil {
			wr.EventsEmit(a.ctx, "op:progress", p)
		}
	})
	if err != nil {
		return res, logErr(kind, err)
	}
	log.Printf("[資訊] %s結束：成功 %d、略過 %d、失敗 %d、已取消=%v", kind, res.OK, res.Skipped, len(res.Failed)+res.MoreFailed, res.Cancelled)
	for _, f := range res.Failed {
		log.Printf("[錯誤] %s失敗項目：%s", kind, f)
	}
	return res, nil
}

// CancelOp 取消進行中的複製／移動／計算大小。
func (a *App) CancelOp() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *App) beginOp() (context.Context, context.CancelFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	return ctx, cancel
}

func (a *App) endOp(cancel context.CancelFunc) {
	a.mu.Lock()
	a.cancel = nil
	a.mu.Unlock()
	cancel()
}

// Trash 把項目移到資源回收筒（失敗時不會改為永久刪除）。
func (a *App) Trash(paths []string) error {
	log.Printf("[執行] 移到資源回收筒 %d 項", len(paths))
	return logErr("移到資源回收筒", fsops.Trash(paths))
}

// DeletePermanent 永久刪除項目。
func (a *App) DeletePermanent(paths []string) fsops.Result {
	log.Printf("[執行] 永久刪除 %d 項", len(paths))
	res := fsops.DeletePermanent(context.Background(), paths)
	for _, f := range res.Failed {
		log.Printf("[錯誤] 永久刪除失敗項目：%s", f)
	}
	return res
}

// Open 以系統預設程式開啟檔案。
func (a *App) Open(path string) error {
	return logErr("開啟 "+path, fsops.Open(path))
}

// Reveal 在系統檔案總管中顯示項目。
func (a *App) Reveal(path string) error {
	return logErr("顯示位置 "+path, fsops.Reveal(path))
}
