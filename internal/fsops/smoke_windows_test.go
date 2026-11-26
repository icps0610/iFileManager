//go:build windows && (amd64 || arm64)

package fsops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// 這些「自我檢測」會真的呼叫 Windows Shell／PowerShell，只在設定 FM_SHELL_SMOKE=1 時執行
// （build.bat 會在單元測試之後單獨執行，失敗不會阻止建置）。

func smokeEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("FM_SHELL_SMOKE") != "1" {
		t.Skip("設定 FM_SHELL_SMOKE=1 才會執行")
	}
}

func menuVerbs(m *shellMenu) (verbs []string, submenus int) {
	n := m.itemCount()
	for i := 0; i < n; i++ {
		mii := menuItemInfo{Mask: miimID | miimSubmenu}
		mii.Size = uint32(unsafe.Sizeof(mii))
		if r, _, _ := procGetMenuItemInfoW.Call(m.hmenu, uintptr(i), 1, uintptr(unsafe.Pointer(&mii))); r == 0 {
			continue
		}
		if mii.SubMenu != 0 {
			submenus++
			continue
		}
		if mii.ID >= idCmdFirst && mii.ID <= idCmdLast {
			if v := commandVerb(m.cm, uintptr(mii.ID-idCmdFirst)); v != "" {
				verbs = append(verbs, v)
			}
		}
	}
	return
}

func TestShellMenuSmoke(t *testing.T) {
	smokeEnabled(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, 0x2); hr == 0 || hr == 1 {
		defer procCoUninitialize.Call()
	}

	dir := t.TempDir()
	f1 := filepath.Join(dir, "測試 a.txt")
	f2 := filepath.Join(dir, "b.txt")
	for _, f := range []string{f1, f2} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "子資料夾")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		dir   string
		paths []string
	}{
		{"單一檔案", dir, []string{f1}},
		{"多個檔案", dir, []string{f1, f2}},
		{"資料夾", dir, []string{sub}},
		{"資料夾空白處", dir, nil},
		{"磁碟機", filepath.VolumeName(dir) + `\`, []string{filepath.VolumeName(dir) + `\`}},
	}
	for _, c := range cases {
		m, err := buildShellMenu(0, c.dir, c.paths, false)
		if err != nil {
			t.Errorf("%s：建立選單失敗：%v", c.name, err)
			continue
		}
		n := m.itemCount()
		verbs, subs := menuVerbs(m)
		m.enablePaste()
		t.Logf("%s：%d 個項目（%d 個子選單），可辨識的動詞：%s", c.name, n, subs, strings.Join(verbs, " "))
		if n < 3 {
			t.Errorf("%s：選單項目過少（%d）", c.name, n)
		}
		if len(verbs) == 0 {
			t.Errorf("%s：GetCommandString 取不到任何動詞", c.name)
		}
		if m.cm2 == 0 && m.cm3 == 0 {
			t.Logf("%s：此選單不支援 IContextMenu2/3（子選單可能是空的）", c.name)
		}
		m.close()
	}
}

func TestShellHostSmoke(t *testing.T) {
	smokeEnabled(t)
	hostOnce.Do(startHost)
	if hostErr != nil {
		t.Fatalf("主機視窗建立失敗：%v", hostErr)
	}
	if hostHwnd == 0 {
		t.Fatal("主機視窗控制代碼為 0")
	}
	t.Logf("主機視窗已建立（HWND=0x%X）", hostHwnd)
}

func TestQuickAccessSmoke(t *testing.T) {
	smokeEnabled(t)
	items, err := QuickAccess()
	if err != nil {
		t.Fatalf("讀取快速存取失敗：%v", err)
	}
	for _, p := range items {
		t.Logf("快速存取：%s → %s", p.Name, p.Path)
	}
	if len(items) == 0 {
		t.Error("快速存取是空的（可能是腳本沒有取得資料）")
	}
}

func TestDrivesSmoke(t *testing.T) {
	smokeEnabled(t)
	ds := Drives()
	if len(ds) == 0 {
		t.Fatal("沒有任何磁碟機")
	}
	for _, d := range ds {
		t.Logf("磁碟機 %s [%s] %q 容量 %d 可用 %d", d.Path, d.Kind, d.Label, d.Total, d.Free)
	}
}
