//go:build windows

package fsops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// runPowerShell 以隱藏視窗執行 PowerShell 腳本並回傳標準輸出。
func runPowerShell(script string, timeout time.Duration) ([]byte, error) {
	exe, err := exec.LookPath("powershell.exe")
	if err != nil {
		exe = filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePowerShell(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("PowerShell 執行逾時（%v）", timeout)
		}
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("PowerShell 執行失敗：%v %s", err, msg)
	}
	return stdout.Bytes(), nil
}
