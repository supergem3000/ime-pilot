//go:build darwin

package lifecycle

import (
	"errors"
	"os"
	"path/filepath"

	native "ime-pilot/internal/platform/darwin"
)

var ErrAlreadyRunning = errors.New("IME Pilot 已在运行，请从菜单栏打开")

func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ime-pilot"), nil
}

func AcquireInstance() (func(), error) {
	dir, err := DataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	release, err := native.LockFile(filepath.Join(dir, "instance.lock"))
	if errors.Is(err, native.ErrLockHeld) {
		return nil, ErrAlreadyRunning
	}
	return release, err
}
