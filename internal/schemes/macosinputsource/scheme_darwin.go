//go:build darwin && cgo

package macosinputsource

import (
	"encoding/json"
	"ime-pilot/internal/core"
	"ime-pilot/internal/schemes/verification"
)

func Validate(raw json.RawMessage) error {
	c, err := Decode(raw)
	if err != nil {
		return err
	}
	return ValidateSources(c)
}

func New(raw json.RawMessage, valid func() bool, window uintptr) (*verification.Executor, error) {
	c, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	if !valid() {
		return nil, core.ErrFocusChanged
	}
	var backend *Backend
	if window != 0 {
		backend, err = NewManual(c)
		if err == nil {
			original := backend.valid
			backend.valid = func() bool { return valid() && original() }
		}
	} else {
		backend, err = NewTarget(c, valid)
	}
	if err != nil {
		return nil, err
	}
	return verification.New(backend), nil
}
