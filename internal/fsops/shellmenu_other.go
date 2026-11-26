//go:build !(windows && (amd64 || arm64))

package fsops

import "errors"

// ShowShellMenu 在此平台不支援系統右鍵選單。
func ShowShellMenu(dir string, paths []string, canPaste bool) (string, error) {
	return "", errors.New("系統右鍵選單僅支援 Windows")
}
