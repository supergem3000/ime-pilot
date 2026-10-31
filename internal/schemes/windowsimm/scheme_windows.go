//go:build windows

package winime

import (
	"encoding/json"
	"fmt"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
	"ime-pilot/internal/schemes/verification"
)

const ID = "windows.imm"

func Validate(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("IMM 方案配置必须是 JSON 对象")
	}
	return nil
}

// New captures native context at execution time. No business Target crosses
// the scheme boundary; valid retains core cancellation/ownership protection.
func New(raw json.RawMessage, valid func() bool, window uintptr) (*verification.Executor, error) {
	if err := Validate(raw); err != nil {
		return nil, err
	}
	if !valid() {
		return nil, core.ErrFocusChanged
	}
	var backend *Backend
	if window != 0 {
		var err error
		backend, err = NewManual(window)
		if err != nil {
			return nil, err
		}
		backend.valid = valid
	} else {
		hwnd := native.ForegroundWindow()
		_, pid := native.WindowThreadProcessID(hwnd)
		path, err := native.ProcessPath(pid)
		if err != nil {
			return nil, err
		}
		backend = &Backend{window: hwnd, ownerPID: pid, path: path, valid: valid, external: true}
	}
	if !valid() {
		return nil, core.ErrFocusChanged
	}
	return verification.New(backend), nil
}
