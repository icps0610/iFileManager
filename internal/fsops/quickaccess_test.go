package fsops

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func qaOut(t *testing.T, items []qaItem, crlf bool) []byte {
	t.Helper()
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	s := base64.StdEncoding.EncodeToString(b)
	if crlf {
		s += "\r\n"
	}
	return []byte(s)
}

func TestParseQuickAccessPinnedOnly(t *testing.T) {
	out := qaOut(t, []qaItem{
		{"桌面", `C:\Users\u\Desktop`, true},
		{"下載", `C:\Users\u\Downloads`, true},
		{"專案", `E:\script\project`, false},
		{"本機", `::{20D04FE0-3AEA-1069-A2D8-08002B30309D}`, true},
		{"共用", `\\nas\share\docs`, true},
		{"重複", `c:\users\u\desktop\`, true},
	}, true)
	got, err := parseQuickAccess(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "桌面" || got[1].Path != `C:\Users\u\Downloads` || got[2].Path != `\\nas\share\docs` {
		t.Fatalf("只應保留已釘選且有檔案系統路徑的項目並去除重複：%+v", got)
	}
}

func TestParseQuickAccessNoPinInfo(t *testing.T) {
	var items []qaItem
	for i := 0; i < 20; i++ {
		items = append(items, qaItem{Name: "", Path: `D:\f` + string(rune('a'+i))})
	}
	got, err := parseQuickAccess(append([]byte("\ufeff"), qaOut(t, items, false)...))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxQuickAccess {
		t.Fatalf("沒有釘選資訊時應限制為 %d 項，得到 %d", maxQuickAccess, len(got))
	}
	if got[0].Name != "fa" {
		t.Errorf("名稱空白時應取資料夾名稱，得到 %q", got[0].Name)
	}
}

func TestParseQuickAccessSingleObject(t *testing.T) {
	b, _ := json.Marshal(qaItem{"桌面", `C:\Desktop`, true})
	got, err := parseQuickAccess([]byte(base64.StdEncoding.EncodeToString(b)))
	if err != nil || len(got) != 1 || got[0].Name != "桌面" {
		t.Fatalf("單一物件應可解析：%+v %v", got, err)
	}
}

func TestParseQuickAccessErrors(t *testing.T) {
	for _, in := range []string{"", "   \r\n", "!!!不是base64", base64.StdEncoding.EncodeToString([]byte("not json"))} {
		if _, err := parseQuickAccess([]byte(in)); err == nil {
			t.Errorf("輸入 %q 應回報錯誤", in)
		}
	}
	if got, err := parseQuickAccess(qaOut(t, []qaItem{}, false)); err != nil || len(got) != 0 {
		t.Errorf("空陣列應回傳空結果：%+v %v", got, err)
	}
}

func TestEncodePowerShell(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(encodePowerShell("A中"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'A', 0, 0x2D, 0x4E} // UTF-16LE：U+0041、U+4E2D
	if string(raw) != string(want) {
		t.Errorf("UTF-16LE 編碼錯誤：% x", raw)
	}
}
