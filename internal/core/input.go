package core

import "errors"

type Mode string

const (
	Chinese Mode = "chinese"
	English Mode = "english"
	Unknown Mode = "unknown"
)

var (
	ErrInvalidMode  = errors.New("invalid input mode")
	ErrUnsupported  = errors.New("input-method control is unavailable")
	ErrFocusChanged = errors.New("input context lost focus or changed")
	ErrVerification = errors.New("input-mode change could not be verified")
)

// State contains metadata only. It never contains typed or composition text.
type State struct {
	Mode          Mode
	ContextID     string
	InputSourceID string
	Description   string
	Details       string
}

type Result struct {
	Before  State
	After   State
	Changed bool
}
