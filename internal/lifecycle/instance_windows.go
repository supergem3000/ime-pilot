//go:build windows

package lifecycle

import (
	"errors"
	"fmt"
	"os/user"

	"golang.org/x/sys/windows"
)

var ErrAlreadyRunning = errors.New("ime-pilot is already running; open it from the system tray")

// AcquireInstance keeps a named object alive until close is called. The Local
// namespace and SID separate interactive sessions and Windows user accounts.
// No startup item, registry setting, or discovery file is created here.
func AcquireInstance() (close func(), err error) {
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("read Windows user identity: %w", err)
	}
	name, err := windows.UTF16PtrFromString(`Local\ime-pilot.ManualPrototype.` + currentUser.Uid)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("create single-instance mutex: %w", err)
	}
	return func() { _ = windows.CloseHandle(handle) }, nil
}
