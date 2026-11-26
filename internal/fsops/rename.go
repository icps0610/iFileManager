package fsops

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// RenameRule 是批次改名的規則。Mode 為 replace | affix | template | case 其中之一。
type RenameRule struct {
	Mode          string `json:"mode"`
	Find          string `json:"find"`
	Replace       string `json:"replace"`
	Regex         bool   `json:"regex"`
	CaseSensitive bool   `json:"caseSensitive"`
	Prefix        string `json:"prefix"`
	Suffix        string `json:"suffix"`
	Template      string `json:"template"` // 可用 {name} 原檔名（不含副檔名）、{n} 流水號
	Start         int    `json:"start"`
	Step          int    `json:"step"`
	Digits        int    `json:"digits"`
	CaseMode      string `json:"caseMode"` // upper | lower | title
	ApplyToExt    bool   `json:"applyToExt"`
}

// RenameItem 是單一項目的改名預覽／結果。Status 為 ok | same | error | done。
type RenameItem struct {
	Path    string `json:"path"`
	OldName string `json:"oldName"`
	NewName string `json:"newName"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func compileRule(rule RenameRule) (*regexp.Regexp, error) {
	if rule.Mode != "replace" || rule.Find == "" {
		return nil, nil
	}
	pat := rule.Find
	if !rule.Regex {
		pat = regexp.QuoteMeta(pat)
	}
	if !rule.CaseSensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("規則運算式有誤：%v", err)
	}
	return re, nil
}

// normalizeReplacement 把取代字串中的 $1、$2… 轉成 ${1}、${2}…。
// Go 的 $1張 會被解讀為名為「1張」的群組（結果為空），這裡避免此陷阱；$$ 仍代表字面的 $。
func normalizeReplacement(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '$' || i+1 >= len(rs) {
			b.WriteRune(rs[i])
			continue
		}
		switch next := rs[i+1]; {
		case next == '$':
			b.WriteString("$$")
			i++
		case next >= '0' && next <= '9':
			j := i + 1
			for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
				j++
			}
			b.WriteString("${" + string(rs[i+1:j]) + "}")
			i = j - 1
		default:
			b.WriteRune('$')
		}
	}
	return b.String()
}

func splitExt(name string, isDir bool) (base, ext string) {
	if isDir {
		return name, ""
	}
	ext = filepath.Ext(name)
	if ext == name { // 例如 .gitignore：整個視為名稱
		return name, ""
	}
	return strings.TrimSuffix(name, ext), ext
}

func titleCase(s string) string {
	var b strings.Builder
	start := true
	for _, r := range s {
		if unicode.IsLetter(r) {
			if start {
				b.WriteRune(unicode.ToUpper(r))
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
			start = false
			continue
		}
		b.WriteRune(r)
		start = r == ' ' || r == '_' || r == '-' || r == '.'
	}
	return b.String()
}

func applyCase(mode, s string) string {
	switch mode {
	case "upper":
		return strings.ToUpper(s)
	case "lower":
		return strings.ToLower(s)
	case "title":
		return titleCase(s)
	}
	return s
}

func transform(rule RenameRule, name string, isDir bool, idx int, re *regexp.Regexp) string {
	base, ext := splitExt(name, isDir)
	switch rule.Mode {
	case "replace":
		if re == nil {
			return name
		}
		repl := func(s string) string {
			if rule.Regex {
				return re.ReplaceAllString(s, normalizeReplacement(rule.Replace))
			}
			return re.ReplaceAllLiteralString(s, rule.Replace)
		}
		if rule.ApplyToExt {
			return repl(name)
		}
		return repl(base) + ext
	case "affix":
		return rule.Prefix + base + rule.Suffix + ext
	case "template":
		tpl := rule.Template
		if tpl == "" {
			tpl = "{name}"
		}
		digits := rule.Digits
		if digits < 0 {
			digits = 0
		}
		if digits > 9 {
			digits = 9
		}
		step := rule.Step
		if step == 0 {
			step = 1
		}
		num := fmt.Sprintf("%0*d", digits, rule.Start+step*idx)
		return strings.NewReplacer("{name}", base, "{n}", num).Replace(tpl) + ext
	case "case":
		if rule.ApplyToExt {
			return applyCase(rule.CaseMode, name)
		}
		return applyCase(rule.CaseMode, base) + ext
	}
	return name
}

func cmpName(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(s)
	}
	return s
}

func nameKey(dir, name string) string {
	return normCmp(dir) + "\x00" + cmpName(name)
}

// RenamePreview 依規則計算每個項目的新名稱並檢查衝突，不會改動任何檔案。
// 項目順序即流水號順序。規則本身有誤（例如規則運算式語法錯誤）時回傳 error。
func RenamePreview(paths []string, rule RenameRule) ([]RenameItem, error) {
	re, err := compileRule(rule)
	if err != nil {
		return nil, err
	}
	items := make([]RenameItem, len(paths))

	// 第一階段：計算新名稱並檢查名稱本身是否合法
	for i, p := range paths {
		it := RenameItem{Path: p, OldName: filepath.Base(p), Status: "ok"}
		n, err := statNode(p, false)
		if err != nil {
			it.NewName, it.Status, it.Message = it.OldName, "error", "找不到項目"
			items[i] = it
			continue
		}
		it.NewName = transform(rule, it.OldName, n.isDir, i, re)
		switch {
		case it.NewName == it.OldName:
			it.Status = "same"
		default:
			if err := ValidateName(it.NewName); err != nil {
				it.Status, it.Message = "error", err.Error()
			}
		}
		items[i] = it
	}

	// 第二階段：新名稱彼此重複
	count := map[string]int{}
	for _, it := range items {
		if it.Status == "ok" {
			count[nameKey(filepath.Dir(it.Path), it.NewName)]++
		}
	}
	for i := range items {
		it := &items[i]
		if it.Status == "ok" && count[nameKey(filepath.Dir(it.Path), it.NewName)] > 1 {
			it.Status, it.Message = "error", "新名稱與其他項目重複"
		}
	}

	// 第三階段：與磁碟上現有項目衝突（允許「換名」與「接龍」：新名稱恰為另一個將被改名項目的舊名稱）
	oldIdx := map[string]int{}
	for i, it := range items {
		oldIdx[nameKey(filepath.Dir(it.Path), it.OldName)] = i
	}
	for i := range items {
		it := &items[i]
		if it.Status != "ok" {
			continue
		}
		dir := filepath.Dir(it.Path)
		if j, found := oldIdx[nameKey(dir, it.NewName)]; found {
			if j != i && (items[j].Status == "same" || items[j].Status == "error") {
				it.Status, it.Message = "error", "已存在同名項目"
			}
			continue
		}
		if dst := filepath.Join(dir, it.NewName); exists(dst) && !sameFile(it.Path, dst) {
			it.Status, it.Message = "error", "已存在同名項目"
		}
	}
	return items, nil
}

// RenameApply 執行批次改名。任何一項有錯誤就整批不執行；執行中失敗會盡力還原。
func RenameApply(paths []string, rule RenameRule) ([]RenameItem, error) {
	items, err := RenamePreview(paths, rule)
	if err != nil {
		return nil, err
	}
	bad := 0
	var todo []int
	for i, it := range items {
		switch it.Status {
		case "error":
			bad++
		case "ok":
			todo = append(todo, i)
		}
	}
	if bad > 0 {
		return items, fmt.Errorf("有 %d 個項目無法改名，請先修正", bad)
	}
	if len(todo) == 0 {
		return items, nil
	}

	// 只要有任何新名稱等於另一項的舊名稱（換名、接龍），就必須經過暫存名稱
	oldSet := map[string]bool{}
	for _, i := range todo {
		oldSet[nameKey(filepath.Dir(items[i].Path), items[i].OldName)] = true
	}
	chain := false
	for _, i := range todo {
		dir := filepath.Dir(items[i].Path)
		nk := nameKey(dir, items[i].NewName)
		if oldSet[nk] && nk != nameKey(dir, items[i].OldName) {
			chain = true
			break
		}
	}

	finalPath := func(i int) string { return filepath.Join(filepath.Dir(items[i].Path), items[i].NewName) }

	if !chain {
		var done []int
		for _, i := range todo {
			if err := os.Rename(items[i].Path, finalPath(i)); err != nil {
				for k := len(done) - 1; k >= 0; k-- {
					j := done[k]
					_ = os.Rename(finalPath(j), items[j].Path)
				}
				return items, friendlyErr("重新命名", items[i].Path, err)
			}
			done = append(done, i)
			items[i].Status = "done"
		}
		return items, nil
	}

	// 兩階段：舊名稱 → 暫存名稱 → 新名稱
	stamp := time.Now().UnixNano()
	tmp := make(map[int]string, len(todo))
	var moved []int
	restore := func() {
		for _, i := range moved {
			_ = os.Rename(tmp[i], items[i].Path)
		}
	}
	for _, i := range todo {
		t := filepath.Join(filepath.Dir(items[i].Path), fmt.Sprintf(".fmtmp-%d-%d", stamp, i))
		if err := os.Rename(items[i].Path, t); err != nil {
			restore()
			return items, friendlyErr("重新命名", items[i].Path, err)
		}
		tmp[i] = t
		moved = append(moved, i)
	}
	var finished []int
	for _, i := range todo {
		if err := os.Rename(tmp[i], finalPath(i)); err != nil {
			// 盡力還原：已完成者退回舊名稱，其餘從暫存名稱退回
			for pass := 0; pass < len(todo); pass++ {
				for _, j := range finished {
					if exists(finalPath(j)) && !exists(items[j].Path) {
						_ = os.Rename(finalPath(j), items[j].Path)
					}
				}
			}
			for _, j := range todo {
				if exists(tmp[j]) {
					_ = os.Rename(tmp[j], items[j].Path)
				}
			}
			return items, friendlyErr("重新命名", items[i].Path, err)
		}
		finished = append(finished, i)
		items[i].Status = "done"
	}
	return items, nil
}
