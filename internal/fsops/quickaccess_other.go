//go:build !windows

package fsops

import (
	"errors"
	"time"
)

func runPowerShell(string, time.Duration) ([]byte, error) {
	return nil, errors.New("僅支援 Windows")
}
