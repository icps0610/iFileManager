//go:build !(windows && (amd64 || arm64))

package fsops

import "errors"

// StartFileDrag 在此平台不支援原生拖放。
func StartFileDrag(paths []string) (string, error) {
	return "", errors.New("拖曳到其他程式僅支援 Windows")
}
