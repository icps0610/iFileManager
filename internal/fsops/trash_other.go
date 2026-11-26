//go:build !(windows && (amd64 || arm64))

package fsops

import "errors"

// Trash 在此平台不支援；請改用永久刪除。
func Trash(paths []string) error {
	return errors.New("此平台不支援資源回收筒，請改用永久刪除")
}
