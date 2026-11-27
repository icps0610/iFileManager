package fsops

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// quickAccessScript 以 Shell.Application 讀取 Windows「快速存取」。
// 每個項目輸出名稱、路徑，以及是否為「已釘選」（System.IsPinnedToNameSpaceTree）；
// 整份結果轉成 JSON 後再 Base64，避免主控台編碼問題。
const quickAccessScript = `$ErrorActionPreference = 'SilentlyContinue'
$sh = New-Object -ComObject Shell.Application
$ns = $sh.Namespace('shell:::{679f85cb-0220-4080-b29b-5540cc05aab6}')
$out = @()
foreach ($i in $ns.Items()) {
  if (-not $i.IsFolder) { continue }
  $p = $i.Path
  if (-not $p) { continue }
  $pin = $false
  try { $pin = [bool]$i.ExtendedProperty('System.IsPinnedToNameSpaceTree') } catch { }
  $out += [pscustomobject]@{ n = [string]$i.Name; p = [string]$p; pin = $pin }
}
$json = ConvertTo-Json -InputObject @($out) -Compress
[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
`

type qaItem struct {
	Name   string `json:"n"`
	Path   string `json:"p"`
	Pinned bool   `json:"pin"`
}

// 只接受磁碟機路徑與網路路徑，排除「::{GUID}」之類沒有檔案系統路徑的特殊項目。
var qaPathPattern = regexp.MustCompile(`^(?:[A-Za-z]:\\|\\\\[^\\]+\\[^\\]+)`)

// maxQuickAccess 是沒有任何「已釘選」資訊時最多顯示的項目數。
const maxQuickAccess = 12

// parseQuickAccess 解析 quickAccessScript 的輸出。
// 只要有任何項目標示為已釘選，就只回傳已釘選者（與檔案總管左側導覽相同）；否則回傳前 maxQuickAccess 個。
func parseQuickAccess(out []byte) ([]Place, error) {
	txt := strings.TrimSpace(strings.TrimPrefix(string(out), "\ufeff"))
	if txt == "" {
		return nil, errors.New("快速存取沒有輸出")
	}
	raw, err := base64.StdEncoding.DecodeString(txt)
	if err != nil {
		return nil, fmt.Errorf("快速存取輸出格式錯誤：%w", err)
	}
	var items []qaItem
	if err := json.Unmarshal(raw, &items); err != nil {
		// PowerShell 5.1 在只有一筆資料時可能輸出單一物件
		var one qaItem
		if err2 := json.Unmarshal(raw, &one); err2 != nil {
			return nil, fmt.Errorf("快速存取 JSON 解析失敗：%w", err)
		}
		items = []qaItem{one}
	}

	seen := map[string]bool{}
	var all, pinned []Place
	for _, it := range items {
		p := strings.TrimSpace(it.Path)
		if !qaPathPattern.MatchString(p) {
			continue
		}
		key := strings.ToLower(strings.TrimRight(p, `\`))
		if seen[key] {
			continue
		}
		seen[key] = true
		name := strings.TrimSpace(it.Name)
		if name == "" {
			name = baseOf(p)
		}
		pl := Place{Name: name, Path: p}
		all = append(all, pl)
		if it.Pinned {
			pinned = append(pinned, pl)
		}
	}
	if len(pinned) > 0 {
		return pinned, nil
	}
	if len(all) > maxQuickAccess {
		all = all[:maxQuickAccess]
	}
	return all, nil
}

func baseOf(p string) string {
	p = strings.TrimRight(p, `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// encodePowerShell 把腳本轉成 -EncodedCommand 需要的 Base64（UTF-16LE）。
func encodePowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

var qaCache struct {
	sync.Mutex
	at    time.Time
	items []Place
	err   error
}

const (
	qaTTL     = 10 * time.Second // 成功結果的快取時間
	qaErrTTL  = 15 * time.Second // 失敗結果的快取時間，避免每次回到視窗都重跑失敗的 PowerShell（太長會讓啟動時的一次失敗卡住很久）
	qaTimeout = 20 * time.Second // 開機後第一次執行 PowerShell 可能很慢，逾時放寬
)

// QuickAccess 回傳 Windows「快速存取」的資料夾（已釘選者優先）。結果快取 10 秒。
// 失敗時回傳錯誤，由呼叫端決定要不要改用 Places()。
func QuickAccess() ([]Place, error) {
	qaCache.Lock()
	defer qaCache.Unlock()
	if qaCache.err != nil && time.Since(qaCache.at) < qaErrTTL {
		return nil, qaCache.err
	}
	if qaCache.items != nil && time.Since(qaCache.at) < qaTTL {
		return append([]Place(nil), qaCache.items...), nil
	}
	fail := func(err error) ([]Place, error) {
		qaCache.items, qaCache.err, qaCache.at = nil, err, time.Now()
		return nil, err
	}
	out, err := runPowerShell(quickAccessScript, qaTimeout)
	if err != nil {
		return fail(err)
	}
	items, err := parseQuickAccess(out)
	if err != nil {
		return fail(err)
	}
	// 只保留實際存在的資料夾（網路路徑不檢查，避免離線時卡住）
	var ok []Place
	for _, p := range items {
		if strings.HasPrefix(p.Path, `\\`) {
			ok = append(ok, p)
			continue
		}
		if st, err := os.Stat(p.Path); err == nil && st.IsDir() {
			ok = append(ok, p)
		}
	}
	if len(ok) == 0 {
		// 檔案總管剛啟動時快速存取可能還是空的：不快取，下次查詢再重新讀取
		qaCache.items, qaCache.err, qaCache.at = nil, nil, time.Time{}
		return []Place{}, nil
	}
	qaCache.items, qaCache.err, qaCache.at = ok, nil, time.Now()
	return append([]Place(nil), ok...), nil
}

/* ---------- 釘選／取消釘選快速存取 ---------- */

// psQuote 把字串包成 PowerShell 單引號字串（內容不會被展開，單引號以兩個單引號表示）。
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ResetQuickAccessCache 清除快速存取的快取，下次讀取時重新向系統查詢。
func ResetQuickAccessCache() {
	qaCache.Lock()
	qaCache.items, qaCache.err, qaCache.at = nil, nil, time.Time{}
	qaCache.Unlock()
}

// qaFindScript 是釘選與取消釘選共用的前置：找出快速存取中路徑相同的項目並判斷是否已釘選。
const qaFindScript = `$ErrorActionPreference = 'Stop'
$sh = New-Object -ComObject Shell.Application
$qa = 'shell:::{679f85cb-0220-4080-b29b-5540cc05aab6}'
function Norm([string]$s) { return $s.TrimEnd('\').ToLowerInvariant() }
function IsPinned([string]$path) {
  $ns = $sh.Namespace($qa)
  foreach ($i in $ns.Items()) {
    if ((Norm $i.Path) -eq (Norm $path)) {
      try { return [bool]$i.ExtendedProperty('System.IsPinnedToNameSpaceTree') } catch { return $false }
    }
  }
  return $false
}
function FindItem([string]$path) {
  $ns = $sh.Namespace($qa)
  foreach ($i in $ns.Items()) { if ((Norm $i.Path) -eq (Norm $path)) { return $i } }
  return $null
}
`

// checkQuickPath 確認路徑是存在的資料夾（網路路徑不檢查是否存在，避免離線時卡住）。
func checkQuickPath(path string) error {
	if !qaPathPattern.MatchString(path) {
		return fmt.Errorf("不支援的路徑：%s", path)
	}
	if strings.HasPrefix(path, `\\`) {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("找不到資料夾：%w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("只能釘選資料夾：%s", path)
	}
	return nil
}

// runQuickVerb 執行釘選或取消釘選的腳本，最後清除快取。
func runQuickVerb(what, path, body string) error {
	if err := checkQuickPath(path); err != nil {
		return err
	}
	defer ResetQuickAccessCache()
	script := qaFindScript + "$p = " + psQuote(path) + "\n" + body
	out, err := runPowerShell(script, 15*time.Second)
	if err != nil {
		return fmt.Errorf("%s失敗：%w", what, err)
	}
	// 腳本只輸出 ASCII 代碼，避免主控台編碼造成中文亂碼
	switch res := strings.TrimSpace(string(out)); res {
	case "ok":
	case "E_OPEN":
		return fmt.Errorf("%s失敗：Windows 無法開啟這個資料夾", what)
	case "E_TIMEOUT":
		return fmt.Errorf("%s失敗：Windows 沒有完成操作（此版本 Windows 可能不支援）", what)
	default:
		return fmt.Errorf("%s失敗：未預期的輸出 %q", what, res)
	}
	return nil
}

// PinQuickAccess 把資料夾釘選到 Windows「快速存取」。
func PinQuickAccess(path string) error {
	return runQuickVerb("釘選到快速存取", path, `if (IsPinned $p) { 'ok'; exit }
$f = $sh.Namespace($p)
if (-not $f) { 'E_OPEN'; exit }
$f.Self.InvokeVerb('pintohome')
for ($k = 0; $k -lt 10; $k++) { if (IsPinned $p) { 'ok'; exit }; Start-Sleep -Milliseconds 200 }
'E_TIMEOUT'
`)
}

// UnpinQuickAccess 把資料夾從 Windows「快速存取」取消釘選。
func UnpinQuickAccess(path string) error {
	return runQuickVerb("取消釘選", path, `$it = FindItem $p
if (-not $it) { 'ok'; exit }
$it.InvokeVerb('unpinfromhome')
for ($k = 0; $k -lt 10; $k++) { if (-not (IsPinned $p)) { 'ok'; exit }; Start-Sleep -Milliseconds 200 }
'E_TIMEOUT'
`)
}

// qaEditScript 依 $want（要保留的已釘選項目，依新順序）同步 Windows「快速存取」：
//  1. 取消釘選不在 $want 裡的項目（刪除）
//  2. Windows 沒有調整釘選順序的 API，而新釘選的項目一律排在最後；
//     因此從第一個順序不同的位置起，把後面的項目全部取消釘選，再依新順序重新釘選。
const qaEditScript = `function Pinned() {
  $r = @()
  foreach ($i in $sh.Namespace($qa).Items()) {
    $pin = $false
    try { $pin = [bool]$i.ExtendedProperty('System.IsPinnedToNameSpaceTree') } catch { }
    if ($pin -and $i.Path) { $r += [string]$i.Path }
  }
  return ,$r
}
$wn = @($want | ForEach-Object { Norm $_ })
foreach ($p in (Pinned)) {
  if ($wn -notcontains (Norm $p)) {
    $it = FindItem $p
    if ($it) { $it.InvokeVerb('unpinfromhome') }
    for ($t = 0; $t -lt 20 -and (IsPinned $p); $t++) { Start-Sleep -Milliseconds 100 }
  }
}
$cur = Pinned
$k = 0
while ($k -lt $cur.Count -and $k -lt $want.Count -and (Norm $cur[$k]) -eq (Norm $want[$k])) { $k++ }
for ($j = $k; $j -lt $cur.Count; $j++) {
  $it = FindItem $cur[$j]
  if ($it) { $it.InvokeVerb('unpinfromhome') }
  for ($t = 0; $t -lt 20 -and (IsPinned $cur[$j]); $t++) { Start-Sleep -Milliseconds 100 }
}
for ($j = $k; $j -lt $want.Count; $j++) {
  $f = $sh.Namespace($want[$j])
  if (-not $f) { continue }
  $f.Self.InvokeVerb('pintohome')
  for ($t = 0; $t -lt 30 -and -not (IsPinned $want[$j]); $t++) { Start-Sleep -Milliseconds 100 }
}
'ok'
`

// EditQuickAccess 依 order（保留的項目與新順序）同步 Windows「快速存取」的釘選：不在 order 裡的取消釘選，其餘依 order 排列。
func EditQuickAccess(order []string) error {
	quoted := make([]string, 0, len(order))
	for _, p := range order {
		if !qaPathPattern.MatchString(p) {
			return fmt.Errorf("不支援的路徑：%s", p)
		}
		quoted = append(quoted, psQuote(p))
	}
	defer ResetQuickAccessCache()
	script := qaFindScript + "$want = @(" + strings.Join(quoted, ", ") + ")\n" + qaEditScript
	out, err := runPowerShell(script, 120*time.Second)
	if err != nil {
		return fmt.Errorf("同步快速存取失敗：%w", err)
	}
	if res := strings.TrimSpace(string(out)); res != "ok" {
		return fmt.Errorf("同步快速存取失敗：未預期的輸出 %q", res)
	}
	return nil
}
