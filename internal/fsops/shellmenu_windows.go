//go:build windows && (amd64 || arm64)

package fsops

// Windows 原生（Shell）右鍵選單。
//
// 作法與檔案總管相同：以 IShellFolder::GetUIObjectOf 取得項目的 IContextMenu，
// QueryContextMenu 把系統與第三方（7-Zip、Git…）的選單項目填進一個 popup menu，
// 用 TrackPopupMenuEx 顯示，再以 InvokeCommand 執行使用者選的項目。
//
// 全部使用 syscall 呼叫 COM（不依賴第三方套件）。選單必須在一條固定的 STA 執行緒上、
// 搭配自己的隱藏視窗與訊息迴圈運作；「傳送到」「開啟檔案時使用」等子選單要靠
// IContextMenu2/3.HandleMenuMsg 轉送 WM_INITMENUPOPUP、WM_DRAWITEM 等訊息才會有內容。

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenuEx    = user32.NewProc("TrackPopupMenuEx")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procGetMenuItemCount    = user32.NewProc("GetMenuItemCount")
	procGetMenuItemInfoW    = user32.NewProc("GetMenuItemInfoW")
	procEnableMenuItem      = user32.NewProc("EnableMenuItem")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")

	procSHParseDisplayName = shell32.NewProc("SHParseDisplayName")
	procSHBindToParent     = shell32.NewProc("SHBindToParent")
)

const (
	wmNull         = 0x0000
	wmDrawItem     = 0x002B
	wmMeasureItem  = 0x002C
	wmInitMenuPop  = 0x0117
	wmMenuChar     = 0x0120
	wmShowMenu     = 0x8000 + 1 // WM_APP+1：要求主機執行緒顯示選單
	wsPopup        = 0x80000000
	wsExToolWindow = 0x00000080

	cmfNormal        = 0x00000000
	cmfCanRename     = 0x00000010
	cmfExtendedVerbs = 0x00000100

	idCmdFirst = 1
	idCmdLast  = 0x7FFF

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	gcsVerbA = 0
	gcsVerbW = 4

	cmicMaskUnicode  = 0x00004000
	cmicMaskPtInvoke = 0x20000000

	miimState   = 0x0001
	miimID      = 0x0002
	miimSubmenu = 0x0004
	mfsGrayed   = 0x0003 // MFS_GRAYED | MFS_DISABLED
	mfByPos     = 0x0400
	mfEnabled   = 0x0000

	vkShift = 0x10
)

// COM 介面的 vtable 索引。
const (
	vtQueryInterface = 0
	vtRelease        = 2

	vtSFBindToObject     = 5  // IShellFolder::BindToObject
	vtSFCreateViewObject = 8  // IShellFolder::CreateViewObject
	vtSFGetUIObjectOf    = 10 // IShellFolder::GetUIObjectOf

	vtCMQueryContextMenu = 3 // IContextMenu::QueryContextMenu
	vtCMInvokeCommand    = 4
	vtCMGetCommandString = 5
	vtCM2HandleMenuMsg   = 6 // IContextMenu2::HandleMenuMsg
	vtCM3HandleMenuMsg2  = 7 // IContextMenu3::HandleMenuMsg2
)

var (
	iidIShellFolder  = guid{0x000214E6, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIContextMenu  = guid{0x000214E4, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIContextMenu2 = guid{0x000214F4, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIContextMenu3 = guid{0xBCFCE0A0, 0xEC17, 0x11D0, [8]byte{0x8D, 0x10, 0x00, 0xA0, 0xC9, 0x0F, 0x27, 0x19}}
)

type point struct{ X, Y int32 }

// WNDCLASSEXW（64 位元：80 位元組）
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// MSG（64 位元：48 位元組）
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// CMINVOKECOMMANDINFOEX（64 位元：104 位元組）
type cmInvokeInfoEx struct {
	Size    uint32
	Mask    uint32
	Hwnd    uintptr
	Verb    uintptr
	Params  uintptr
	Dir     uintptr
	Show    int32
	HotKey  uint32
	Icon    uintptr
	Title   uintptr
	VerbW   uintptr
	ParamsW uintptr
	DirW    uintptr
	TitleW  uintptr
	Pt      point
}

// MENUITEMINFOW（64 位元：80 位元組）
type menuItemInfo struct {
	Size         uint32
	Mask         uint32
	Type         uint32
	State        uint32
	ID           uint32
	SubMenu      uintptr
	BmpChecked   uintptr
	BmpUnchecked uintptr
	ItemData     uintptr
	TypeData     uintptr
	Cch          uint32
	BmpItem      uintptr
}

// 編譯期檢查：結構大小不符就無法編譯。
const (
	_ = uint(unsafe.Sizeof(wndClassEx{}) - 80)
	_ = uint(80 - unsafe.Sizeof(wndClassEx{}))
	_ = uint(unsafe.Sizeof(winMsg{}) - 48)
	_ = uint(48 - unsafe.Sizeof(winMsg{}))
	_ = uint(unsafe.Sizeof(cmInvokeInfoEx{}) - 104)
	_ = uint(104 - unsafe.Sizeof(cmInvokeInfoEx{}))
	_ = uint(unsafe.Sizeof(menuItemInfo{}) - 80)
	_ = uint(80 - unsafe.Sizeof(menuItemInfo{}))
)

func failed(hr uintptr) bool { return int32(hr) < 0 }

func hrErr(what string, hr uintptr) error {
	return fmt.Errorf("%s 失敗（HRESULT 0x%08X）", what, uint32(hr))
}

// comCall 呼叫 COM 物件 vtable 中第 idx 個方法。
//
//go:uintptrescapes
func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	p := *(*unsafe.Pointer)(unsafe.Pointer(&obj)) // 物件指標（以重新解讀 uintptr 變數記憶體的方式取得，避免 uintptr→Pointer 轉換）
	vtbl := *(*unsafe.Pointer)(p)
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*int(unsafe.Sizeof(uintptr(0)))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, obj)
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, vtRelease)
	}
}

func comQueryInterface(obj uintptr, iid *guid) uintptr {
	var out uintptr
	if failed(comCall(obj, vtQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) {
		return 0
	}
	return out
}

// shellMenu 代表一個已填好項目的原生選單，以及要釋放的所有資源。
type shellMenu struct {
	cm, cm2, cm3 uintptr
	hmenu        uintptr
	cleanups     []func()
}

func (m *shellMenu) onClose(f func()) { m.cleanups = append(m.cleanups, f) }

func (m *shellMenu) close() {
	for i := len(m.cleanups) - 1; i >= 0; i-- {
		m.cleanups[i]()
	}
	m.cleanups = nil
}

func parseDisplayName(path string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("路徑含有無效字元：%s", path)
	}
	var pidl uintptr
	hr, _, _ := procSHParseDisplayName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0)
	if failed(hr) || pidl == 0 {
		return 0, fmt.Errorf("找不到項目或無法取得系統資訊（0x%08X）：%s", uint32(hr), path)
	}
	return pidl, nil
}

// bindToParent 取得 PIDL 所在資料夾的 IShellFolder，以及項目本身（相對於該資料夾）的 PIDL。
func bindToParent(pidl uintptr) (folder, child uintptr, err error) {
	hr, _, _ := procSHBindToParent.Call(pidl, uintptr(unsafe.Pointer(&iidIShellFolder)), uintptr(unsafe.Pointer(&folder)), uintptr(unsafe.Pointer(&child)))
	if failed(hr) || folder == 0 {
		return 0, 0, hrErr("SHBindToParent", hr)
	}
	return folder, child, nil
}

// itemsContextMenu 取得「選取項目」的 IContextMenu（所有項目必須在同一個資料夾）。
func itemsContextMenu(m *shellMenu, hwnd uintptr, paths []string) (uintptr, error) {
	var folder uintptr
	children := make([]uintptr, 0, len(paths))
	for i, p := range paths {
		pidl, err := parseDisplayName(p)
		if err != nil {
			return 0, err
		}
		m.onClose(func() { procCoTaskMemFree.Call(pidl) })
		f, child, err := bindToParent(pidl)
		if err != nil {
			return 0, err
		}
		if i == 0 {
			folder = f
			m.onClose(func() { comRelease(f) })
		} else {
			comRelease(f)
		}
		children = append(children, child)
	}
	var cm uintptr
	hr := comCall(folder, vtSFGetUIObjectOf, hwnd, uintptr(len(children)), uintptr(unsafe.Pointer(&children[0])),
		uintptr(unsafe.Pointer(&iidIContextMenu)), 0, uintptr(unsafe.Pointer(&cm)))
	if failed(hr) || cm == 0 {
		return 0, hrErr("GetUIObjectOf(IContextMenu)", hr)
	}
	return cm, nil
}

// backgroundContextMenu 取得「資料夾空白處」的 IContextMenu。
func backgroundContextMenu(m *shellMenu, hwnd uintptr, dir string) (uintptr, error) {
	pidl, err := parseDisplayName(dir)
	if err != nil {
		return 0, err
	}
	m.onClose(func() { procCoTaskMemFree.Call(pidl) })
	parent, child, err := bindToParent(pidl)
	if err != nil {
		return 0, err
	}
	m.onClose(func() { comRelease(parent) })
	var folder uintptr
	hr := comCall(parent, vtSFBindToObject, child, 0, uintptr(unsafe.Pointer(&iidIShellFolder)), uintptr(unsafe.Pointer(&folder)))
	if failed(hr) || folder == 0 {
		return 0, hrErr("BindToObject", hr)
	}
	m.onClose(func() { comRelease(folder) })
	var cm uintptr
	hr = comCall(folder, vtSFCreateViewObject, hwnd, uintptr(unsafe.Pointer(&iidIContextMenu)), uintptr(unsafe.Pointer(&cm)))
	if failed(hr) || cm == 0 {
		return 0, hrErr("CreateViewObject(IContextMenu)", hr)
	}
	return cm, nil
}

// buildShellMenu 建立並填好選單。paths 為空時為資料夾空白處選單。
func buildShellMenu(hwnd uintptr, dir string, paths []string, extended bool) (m *shellMenu, err error) {
	m = &shellMenu{}
	defer func() {
		if err != nil {
			m.close()
			m = nil
		}
	}()

	var cm uintptr
	if len(paths) == 0 {
		cm, err = backgroundContextMenu(m, hwnd, dir)
	} else {
		cm, err = itemsContextMenu(m, hwnd, paths)
	}
	if err != nil {
		return
	}
	m.cm = cm
	m.onClose(func() { comRelease(cm) })

	hm, _, e := procCreatePopupMenu.Call()
	if hm == 0 {
		err = fmt.Errorf("CreatePopupMenu 失敗：%v", e)
		return
	}
	m.hmenu = hm
	m.onClose(func() { procDestroyMenu.Call(hm) })

	flags := uintptr(cmfNormal)
	if len(paths) > 0 {
		flags |= cmfCanRename
	}
	if extended {
		flags |= cmfExtendedVerbs
	}
	hr := comCall(cm, vtCMQueryContextMenu, hm, 0, idCmdFirst, idCmdLast, flags)
	if failed(hr) {
		err = hrErr("QueryContextMenu", hr)
		return
	}

	if m.cm3 = comQueryInterface(cm, &iidIContextMenu3); m.cm3 != 0 {
		c := m.cm3
		m.onClose(func() { comRelease(c) })
	}
	if m.cm2 = comQueryInterface(cm, &iidIContextMenu2); m.cm2 != 0 {
		c := m.cm2
		m.onClose(func() { comRelease(c) })
	}
	return
}

// handleMsg 把選單相關訊息轉送給 IContextMenu2/3，回傳 (結果, 是否已處理)。
func (m *shellMenu) handleMsg(msg uint32, wp, lp uintptr) (uintptr, bool) {
	if m.cm3 != 0 {
		var res uintptr
		if failed(comCall(m.cm3, vtCM3HandleMenuMsg2, uintptr(msg), wp, lp, uintptr(unsafe.Pointer(&res)))) {
			return 0, false
		}
		switch msg {
		case wmMenuChar:
			return res, true
		case wmInitMenuPop:
			return 0, true
		}
		return 1, true
	}
	if m.cm2 != 0 && msg != wmMenuChar {
		if failed(comCall(m.cm2, vtCM2HandleMenuMsg, uintptr(msg), wp, lp)) {
			return 0, false
		}
		if msg == wmInitMenuPop {
			return 0, true
		}
		return 1, true
	}
	return 0, false
}

// commandVerb 取得選單命令的標準動詞（例如 copy、cut、delete、properties）；取不到時回傳空字串。
func commandVerb(cm, id uintptr) string {
	wbuf := make([]uint16, 128)
	if !failed(comCall(cm, vtCMGetCommandString, id, gcsVerbW, 0, uintptr(unsafe.Pointer(&wbuf[0])), uintptr(len(wbuf)))) {
		if s := syscall.UTF16ToString(wbuf); s != "" {
			return strings.ToLower(s)
		}
	}
	abuf := make([]byte, 128)
	if !failed(comCall(cm, vtCMGetCommandString, id, gcsVerbA, 0, uintptr(unsafe.Pointer(&abuf[0])), uintptr(len(abuf)))) {
		if n := bytes.IndexByte(abuf, 0); n > 0 {
			return strings.ToLower(string(abuf[:n]))
		}
	}
	return ""
}

func (m *shellMenu) itemCount() int {
	n, _, _ := procGetMenuItemCount.Call(m.hmenu)
	return int(int32(n))
}

// enablePaste 把系統判定為停用的「貼上」改成可用（本程式有自己的剪貼簿，系統剪貼簿是空的）。
func (m *shellMenu) enablePaste() {
	n := m.itemCount()
	for i := 0; i < n; i++ {
		mii := menuItemInfo{Mask: miimID | miimState | miimSubmenu}
		mii.Size = uint32(unsafe.Sizeof(mii))
		r, _, _ := procGetMenuItemInfoW.Call(m.hmenu, uintptr(i), 1, uintptr(unsafe.Pointer(&mii)))
		if r == 0 || mii.SubMenu != 0 || mii.State&mfsGrayed == 0 || mii.ID < idCmdFirst || mii.ID > idCmdLast {
			continue
		}
		if commandVerb(m.cm, uintptr(mii.ID-idCmdFirst)) == "paste" {
			procEnableMenuItem.Call(m.hmenu, uintptr(i), mfByPos|mfEnabled)
		}
	}
}

func invokeCommand(cm, hwnd, id uintptr, pt point, dir string) error {
	var dirW *uint16
	if dir != "" {
		dirW, _ = syscall.UTF16PtrFromString(dir)
	}
	ci := cmInvokeInfoEx{Hwnd: hwnd, Verb: id, VerbW: id, Show: 1, Pt: pt, Mask: cmicMaskUnicode | cmicMaskPtInvoke}
	ci.Size = uint32(unsafe.Sizeof(ci))
	if dirW != nil {
		ci.DirW = uintptr(unsafe.Pointer(dirW))
	}
	hr := comCall(cm, vtCMInvokeCommand, uintptr(unsafe.Pointer(&ci)))
	runtime.KeepAlive(dirW)
	if failed(hr) {
		return hrErr("InvokeCommand", hr)
	}
	return nil
}

// ---- 主機執行緒：固定的 STA 執行緒 + 隱藏視窗 + 訊息迴圈 ----

type menuReq struct {
	dir      string
	paths    []string
	canPaste bool
	done     chan menuRes
}

type menuRes struct {
	action string
	err    error
}

var (
	hostOnce  sync.Once
	hostReady chan struct{}
	hostHwnd  uintptr
	hostErr   error

	reqMu   sync.Mutex // 一次只處理一個選單
	pendMu  sync.Mutex
	pending *menuReq

	currentMenu *shellMenu // 只會在主機執行緒存取
)

func startHost() {
	hostReady = make(chan struct{})
	go hostThread()
	<-hostReady
}

func hostThread() {
	runtime.LockOSThread()
	// 不 UnlockOSThread：這條執行緒的生命週期與程式相同
	procCoInitializeEx.Call(0, 0x2|0x4) // APARTMENTTHREADED | DISABLE_OLE1DDE

	inst, _, _ := procGetModuleHandleW.Call(0)
	cls, _ := syscall.UTF16PtrFromString("iFileManagerShellMenuHost")
	wc := wndClassEx{WndProc: syscall.NewCallback(hostWndProc), Instance: inst, ClassName: cls}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		hostErr = fmt.Errorf("RegisterClassEx 失敗：%v", e)
		close(hostReady)
		return
	}
	hwnd, _, e := procCreateWindowExW.Call(wsExToolWindow, uintptr(unsafe.Pointer(cls)), 0, wsPopup, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		hostErr = fmt.Errorf("CreateWindowEx 失敗：%v", e)
		close(hostReady)
		return
	}
	hostHwnd = hwnd
	close(hostReady)

	var msg winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func hostWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch uint32(msg) {
	case wmShowMenu:
		runPending(hwnd)
		return 0
	case wmDoDrag:
		runPendingDrag(hwnd)
		return 0
	case wmInitMenuPop, wmDrawItem, wmMeasureItem, wmMenuChar:
		if currentMenu != nil {
			if res, ok := currentMenu.handleMsg(uint32(msg), wp, lp); ok {
				return res
			}
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func runPending(hwnd uintptr) {
	pendMu.Lock()
	req := pending
	pending = nil
	pendMu.Unlock()
	if req == nil {
		return
	}
	var res menuRes
	func() {
		defer func() {
			if r := recover(); r != nil {
				res = menuRes{err: fmt.Errorf("系統選單發生內部錯誤：%v", r)}
			}
		}()
		res = doMenu(hwnd, req)
	}()
	req.done <- res
}

func doMenu(hwnd uintptr, req *menuReq) menuRes {
	prevFg, _, _ := procGetForegroundWindow.Call()
	restore := func() {
		if prevFg != 0 {
			procSetForegroundWindow.Call(prevFg)
		}
	}
	shift, _, _ := procGetAsyncKeyState.Call(vkShift)
	m, err := buildShellMenu(hwnd, req.dir, req.paths, shift&0x8000 != 0)
	if err != nil {
		return menuRes{err: err}
	}
	defer m.close()
	if req.canPaste {
		m.enablePaste()
	}
	currentMenu = m
	defer func() { currentMenu = nil }()

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	r, _, _ := procTrackPopupMenuEx.Call(m.hmenu, tpmReturnCmd|tpmRightButton, uintptr(int(pt.X)), uintptr(int(pt.Y)), hwnd, 0)
	procPostMessageW.Call(hwnd, wmNull, 0, 0) // 讓選單可正常關閉的慣用作法
	cmd := uint32(r)
	if cmd == 0 { // 取消
		restore()
		return menuRes{}
	}
	id := uintptr(cmd - idCmdFirst)
	verb := commandVerb(m.cm, id)
	log.Printf("[資訊] 系統選單：選擇命令 %d（動詞：%q）", id, verb)
	switch verb {
	case "copy", "cut", "paste", "rename", "delete":
		// 這幾個動作交給本程式自己的剪貼簿、重新命名與資源回收筒流程處理
		restore()
		return menuRes{action: verb}
	}
	dir := req.dir
	if len(req.paths) > 0 {
		dir = filepath.Dir(req.paths[0])
	}
	if err := invokeCommand(m.cm, hwnd, id, pt, dir); err != nil {
		return menuRes{err: err}
	}
	return menuRes{action: "invoked"}
}

// ShowShellMenu 在滑鼠位置顯示 Windows 原生右鍵選單，並執行使用者選的項目。
// paths 為選取的項目（必須位於同一個資料夾）；paths 為空時顯示資料夾 dir 的空白處選單。
// canPaste 為 true 時，把選單中的「貼上」改為可用（由呼叫端自行處理貼上）。
//
// 回傳值：空字串＝使用者取消；copy／cut／paste／rename／delete＝請呼叫端自己處理；
// invoked＝已由系統執行。
func ShowShellMenu(dir string, paths []string, canPaste bool) (string, error) {
	if len(paths) == 0 && dir == "" {
		return "", errors.New("沒有可顯示選單的項目")
	}
	for _, p := range paths {
		if p == "" {
			return "", errors.New("項目路徑是空的")
		}
		if !strings.EqualFold(filepath.Dir(p), filepath.Dir(paths[0])) {
			return "", errors.New("選取的項目不在同一個資料夾，無法顯示系統選單")
		}
	}
	hostOnce.Do(startHost)
	if hostErr != nil {
		return "", hostErr
	}
	reqMu.Lock()
	defer reqMu.Unlock()
	req := &menuReq{dir: dir, paths: paths, canPaste: canPaste, done: make(chan menuRes, 1)}
	pendMu.Lock()
	pending = req
	pendMu.Unlock()
	if r, _, e := procPostMessageW.Call(hostHwnd, wmShowMenu, 0, 0); r == 0 {
		pendMu.Lock()
		pending = nil
		pendMu.Unlock()
		return "", fmt.Errorf("PostMessage 失敗：%v", e)
	}
	select {
	case res := <-req.done:
		return res.action, res.err
	case <-time.After(10 * time.Minute):
		return "", errors.New("系統選單逾時沒有回應")
	}
}
