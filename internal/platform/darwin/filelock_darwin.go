//go:build darwin

package darwin

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var ErrLockHeld = errors.New("file lock already held")

// LockFile takes an exclusive nonblocking lock. The caller owns the path and
// parent directory. Keep the inode after release so another opener cannot lock
// a different file under the same name while the original lock is still held.
func LockFile(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("lock file: %w", err)
	}
	return func() { _ = f.Close() }, nil
}
