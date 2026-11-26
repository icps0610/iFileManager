//go:build !windows

package fsops

import "testing"

// 非 Windows 平台以檔名開頭的點判斷隱藏。
const dotfileIsHidden = true

func markHidden(t *testing.T, path string) { t.Helper() }
