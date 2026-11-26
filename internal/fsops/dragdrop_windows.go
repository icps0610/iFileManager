//go:build windows && (amd64 || arm64)

package fsops

// 把檔案以 Windows 原生（OLE）拖放拖出程式，例如拖到檔案總管、桌面或瀏覽器的上傳區。
//
// 作法：以 IShellFolder::GetUIObjectOf 取得系統提供的 IDataObject（與檔案總管拖曳時相同，內含 CF_HDROP 等格式），
// 自己實作最簡單的 IDropSource，再於右鍵選單共用的 STA 主機執行緒上呼叫 DoDragDrop。
// 拖到本程式自己的視窗時，WebView2 會收到一般的 dragover／drop 事件，由前端照原本的方式處理。

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	procOleInitialize            = ole32.NewProc("OleInitialize")
	procDoDragDrop               = ole32.NewProc("DoDragDrop")
	procSHDoDragDrop             = shell32.NewProc("SHDoDragDrop")
	procWindowFromPt             = user32.NewProc("WindowFromPoint")
	procGetCapture               = user32.NewProc("GetCapture")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
)

const (
	wmDoDrag    = 0x8000 + 2 // WM_APP+2：要求主機執行緒開始拖曳
	wmMouseMove = 0x0200
	vkLButton   = 0x01

	sOK                        = 0
	eNoInterface               = 0x80004002
	dragdropSDrop              = 0x00040100
	dragdropSCancel            = 0x00040101
	dragdropSUseDefaultCursors = 0x00040102

	mkLButton = 0x0001
	mkRButton = 0x0002

	dropEffectCopy = 1
	dropEffectMove = 2
	dropEffectLink = 4
)

var (
	iidIUnknown    = guid{0x00000000, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDropSource = guid{0x00000121, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDataObject = guid{0x0000010E, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

// dropSource 是 IDropSource 的 COM 物件：第一個欄位必須是 vtable 指標。
// 物件與 vtable 都是套件層級變數，生命週期與程式相同，不需要參考計數。
type dropSource struct{ vtbl *[5]uintptr }

var (
	dsOnce  sync.Once
	dsVtbl  [5]uintptr
	dsObj   dropSource
	oleInit bool // 只在主機執行緒存取
)

func initDropSource() {
	dsVtbl = [5]uintptr{
		syscall.NewCallback(dsQueryInterface),
		syscall.NewCallback(dsAddRef),
		syscall.NewCallback(dsRelease),
		syscall.NewCallback(dsQueryContinueDrag),
		syscall.NewCallback(dsGiveFeedback),
	}
	dsObj.vtbl = &dsVtbl
}

func dsQueryInterface(this, riid, ppv uintptr) uintptr {
	id := *(**guid)(unsafe.Pointer(&riid))
	out := *(**uintptr)(unsafe.Pointer(&ppv))
	if *id == iidIUnknown || *id == iidIDropSource {
		*out = this
		return sOK
	}
	*out = 0
	return eNoInterface
}

func dsAddRef(this uintptr) uintptr  { return 1 }
func dsRelease(this uintptr) uintptr { return 1 }

// dsQueryContinueDrag：按 Esc 取消；滑鼠按鍵放開即放下。
func dsQueryContinueDrag(this, escapePressed, keyState uintptr) uintptr {
	if int32(escapePressed) != 0 {
		return dragdropSCancel
	}
	if keyState&(mkLButton|mkRButton) == 0 {
		return dragdropSDrop
	}
	return sOK
}

func dsGiveFeedback(this, effect uintptr) uintptr { return dragdropSUseDefaultCursors }

type dragReq struct {
	paths []string
	done  chan dragRes
}

type dragRes struct {
	effect string
	err    error
}

var (
	dragMu      sync.Mutex // 一次只處理一個拖曳
	dragPendMu  sync.Mutex
	dragPending *dragReq
)

// runPendingDrag 在主機執行緒上執行等待中的拖曳要求。
func runPendingDrag(hwnd uintptr) {
	dragPendMu.Lock()
	req := dragPending
	dragPending = nil
	dragPendMu.Unlock()
	if req == nil {
		return
	}
	var res dragRes
	func() {
		defer func() {
			if r := recover(); r != nil {
				res = dragRes{err: fmt.Errorf("拖曳發生內部錯誤：%v", r)}
			}
		}()
		res = doDrag(hwnd, req.paths)
	}()
	req.done <- res
}

// itemsDataObject 取得選取項目的 IDataObject（所有項目必須在同一個資料夾）。
func itemsDataObject(m *shellMenu, hwnd uintptr, paths []string) (uintptr, error) {
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
	var obj uintptr
	hr := comCall(folder, vtSFGetUIObjectOf, hwnd, uintptr(len(children)), uintptr(unsafe.Pointer(&children[0])),
		uintptr(unsafe.Pointer(&iidIDataObject)), 0, uintptr(unsafe.Pointer(&obj)))
	if failed(hr) || obj == 0 {
		return 0, hrErr("GetUIObjectOf(IDataObject)", hr)
	}
	m.onClose(func() { comRelease(obj) })
	return obj, nil
}

func doDrag(hwnd uintptr, paths []string) dragRes {
	if !oleInit {
		// DoDragDrop 需要 OleInitialize（不只是 CoInitializeEx）；主機執行緒已是 STA，可直接呼叫
		if hr, _, _ := procOleInitialize.Call(0); failed(hr) {
			return dragRes{err: hrErr("OleInitialize", hr)}
		}
		oleInit = true
	}
	dsOnce.Do(initDropSource)

	m := &shellMenu{} // 借用資源清理的機制
	defer m.close()
	obj, err := itemsDataObject(m, hwnd, paths)
	if err != nil {
		return dragRes{err: err}
	}
	// 按下滑鼠時，WebView2（另一個行程）的視窗握有滑鼠擷取（capture），而本執行緒的輸入佇列與它不同，
	// OLE 拖放迴圈因此收不到任何滑鼠訊息，DoDragDrop 會一直卡住（記錄上只有「開始」沒有「結束」）。
	// 解法：暫時把本執行緒的輸入佇列接到握有擷取的執行緒（AttachThreadInput），OLE 才能取得擷取；
	// 另外定時送 WM_MOUSEMOVE 喚醒拖放迴圈，確保放開滑鼠或按 Esc 時一定會結束。
	if tid := foreignCaptureThread(); tid != 0 {
		me, _, _ := procGetCurrentThreadId.Call()
		if tid != me {
			if r, _, e := procAttachThreadInput.Call(me, tid, 1); r == 0 {
				log.Printf("[警告] AttachThreadInput 失敗：%v", e)
			} else {
				defer procAttachThreadInput.Call(me, tid, 0)
			}
		}
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		var lastKeys uintptr
		var lastPt point
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				// 只在游標位置或按鍵狀態改變時才補送，避免重複的滑鼠訊息讓拖曳影像與目標反覆刷新（閃動）。
				// wParam 必須帶目前真正的按鍵狀態：OLE 以滑鼠訊息的 wParam（MK_*）判斷左鍵是否還按著，
				// 傳 0 會被當成「已放開」而立刻放下；lParam 帶游標實際位置（主機視窗在 (0,0)，用螢幕座標即可），
				// 傳 0 會讓目標在 (0,0) 與實際位置之間來回切換。
				keys := mouseKeyState()
				var pt point
				procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				if keys == lastKeys && pt == lastPt {
					continue
				}
				lastKeys, lastPt = keys, pt
				lp := uintptr(uint16(int16(pt.X))) | uintptr(uint16(int16(pt.Y)))<<16
				procPostMessageW.Call(hwnd, wmMouseMove, keys, lp)
			}
		}
	}()
	var effect uint32
	t0 := time.Now()
	how := "SHDoDragDrop"
	// 優先用 SHDoDragDrop：由系統提供 IDropSource 並產生拖曳縮圖（檔案總管同款的半透明圖示）；
	// 只用 DoDragDrop 時游標旁不會有任何拖曳影像。找不到該函式時才退回自己的 IDropSource。
	var hr uintptr
	if procSHDoDragDrop.Find() == nil {
		hr, _, _ = procSHDoDragDrop.Call(hwnd, obj, 0, dropEffectCopy|dropEffectMove|dropEffectLink, uintptr(unsafe.Pointer(&effect)))
	} else {
		how = "DoDragDrop"
		hr, _, _ = procDoDragDrop.Call(obj, uintptr(unsafe.Pointer(&dsObj)),
			dropEffectCopy|dropEffectMove|dropEffectLink, uintptr(unsafe.Pointer(&effect)))
	}
	log.Printf("[資訊] 原生拖曳結束（%s）：HRESULT 0x%08X，效果 %d，%d 項，耗時 %v", how, uint32(hr), effect, len(paths), time.Since(t0).Round(time.Millisecond))
	switch {
	case uint32(hr) == dragdropSCancel:
		return dragRes{effect: ""}
	case uint32(hr) == dragdropSDrop:
		switch {
		case effect&dropEffectMove != 0:
			return dragRes{effect: "move"}
		case effect&dropEffectCopy != 0:
			return dragRes{effect: "copy"}
		case effect&dropEffectLink != 0:
			return dragRes{effect: "link"}
		}
		return dragRes{effect: "none"}
	case failed(hr):
		return dragRes{err: hrErr("DoDragDrop", hr)}
	}
	return dragRes{effect: "none"}
}

// StartFileDrag 以 Windows 原生拖放拖曳 paths（必須位於同一個資料夾），在放開滑鼠後才回傳。
// 回傳值：空字串＝取消；move／copy／link＝目的地採用的效果；none＝放在不接受的位置。
func StartFileDrag(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("沒有要拖曳的項目")
	}
	for _, p := range paths {
		if p == "" {
			return "", errors.New("項目路徑是空的")
		}
		if !strings.EqualFold(filepath.Dir(p), filepath.Dir(paths[0])) {
			return "", errors.New("選取的項目不在同一個資料夾，無法拖曳")
		}
	}
	hostOnce.Do(startHost)
	if hostErr != nil {
		return "", hostErr
	}
	dragMu.Lock()
	defer dragMu.Unlock()
	req := &dragReq{paths: paths, done: make(chan dragRes, 1)}
	dragPendMu.Lock()
	dragPending = req
	dragPendMu.Unlock()
	if r, _, e := procPostMessageW.Call(hostHwnd, wmDoDrag, 0, 0); r == 0 {
		dragPendMu.Lock()
		dragPending = nil
		dragPendMu.Unlock()
		return "", fmt.Errorf("PostMessage 失敗：%v", e)
	}
	select {
	case res := <-req.done:
		return res.effect, res.err
	case <-time.After(10 * time.Minute):
		return "", errors.New("拖曳逾時沒有回應")
	}
}

// guiThreadInfo 對應 GUITHREADINFO（64 位元：72 位元組）。
type guiThreadInfo struct {
	Size      uint32
	Flags     uint32
	Active    uintptr
	Focus     uintptr
	Capture   uintptr
	MenuOwner uintptr
	MoveSize  uintptr
	Caret     uintptr
	CaretRect [4]int32
}

func className(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	buf := make([]uint16, 128)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// foreignCaptureThread 找出目前握有滑鼠擷取的視窗（WebView2 的視窗）所屬的執行緒 ID，並記錄當下狀態方便除錯。
func foreignCaptureThread() uintptr {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	under, _, _ := procWindowFromPt.Call(uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32)
	gti := guiThreadInfo{}
	gti.Size = uint32(unsafe.Sizeof(gti))
	procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&gti))) // 0＝前景執行緒
	btn, _, _ := procGetAsyncKeyState.Call(vkLButton)
	log.Printf("[資訊] 原生拖曳開始：游標 (%d,%d)，下方視窗 %s，前景擷取視窗 %s，左鍵%s",
		pt.X, pt.Y, className(under), className(gti.Capture), map[bool]string{true: "按著", false: "已放開"}[btn&0x8000 != 0])
	target := gti.Capture
	if target == 0 {
		target = under
	}
	if target == 0 {
		return 0
	}
	tid, _, _ := procGetWindowThreadProcessId.Call(target, 0)
	return tid
}

// mouseKeyState 以 GetAsyncKeyState 組出滑鼠訊息 wParam 用的 MK_* 旗標。
func mouseKeyState() uintptr {
	down := func(vk uintptr) bool {
		r, _, _ := procGetAsyncKeyState.Call(vk)
		return r&0x8000 != 0
	}
	var k uintptr
	if down(0x01) { // VK_LBUTTON
		k |= mkLButton
	}
	if down(0x02) { // VK_RBUTTON
		k |= mkRButton
	}
	if down(0x10) { // VK_SHIFT
		k |= 0x0004 // MK_SHIFT
	}
	if down(0x11) { // VK_CONTROL
		k |= 0x0008 // MK_CONTROL
	}
	return k
}
