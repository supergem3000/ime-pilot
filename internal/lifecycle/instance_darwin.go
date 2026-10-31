//go:build darwin

package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrAlreadyRunning = errors.New("ime-pilot 已在运行，请从菜单栏打开")

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
	f, err := os.OpenFile(filepath.Join(dir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock instance: %w", err)
	}
	// Keep the inode: unlinking it would let a second process lock a different file.
	return func() { _ = f.Close() }, nil
}
