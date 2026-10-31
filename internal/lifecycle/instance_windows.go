//go:build windows

package lifecycle

import (
	"errors"
	"fmt"
	"os/user"

	native "ime-pilot/internal/platform/windows"
)

var ErrAlreadyRunning = errors.New("IME Pilot is already running; open it from the system tray")

// AcquireInstance keeps a named object alive until close is called. The Local
// namespace and SID separate interactive sessions and Windows user accounts.
// No startup item, registry setting, or discovery file is created here.
func AcquireInstance() (close func(), err error) {
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("read Windows user identity: %w", err)
	}
	release, err := native.AcquireMutex(`Local\ime-pilot.ManualPrototype.` + currentUser.Uid)
	if errors.Is(err, native.ErrMutexExists) {
		return nil, ErrAlreadyRunning
	}
	return release, err
}
