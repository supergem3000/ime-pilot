//go:build windows

package windows

import (
	"errors"
	"fmt"

	winapi "golang.org/x/sys/windows"
)

var ErrMutexExists = errors.New("named mutex already exists")

// AcquireMutex keeps a named Windows object alive until release is called.
// The caller chooses the namespace and name; no registry entries are created.
func AcquireMutex(name string) (release func(), err error) {
	value, err := winapi.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := winapi.CreateMutex(nil, false, value)
	if errors.Is(err, winapi.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = winapi.CloseHandle(handle)
		}
		return nil, ErrMutexExists
	}
	if err != nil {
		return nil, fmt.Errorf("create named mutex: %w", err)
	}
	return func() { _ = winapi.CloseHandle(handle) }, nil
}
