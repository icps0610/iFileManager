//go:build !(windows && (amd64 || arm64))

package fsops

// SetClipboardFiles 在此平台不支援系統剪貼簿，僅使用程式內剪貼簿。
func SetClipboardFiles(paths []string, cut bool) error { return nil }

// GetClipboardFiles 在此平台不支援系統剪貼簿，回傳空清單。
func GetClipboardFiles() ([]string, bool, error) { return nil, false, nil }

// ClearClipboard 在此平台不做任何事。
func ClearClipboard() error { return nil }
