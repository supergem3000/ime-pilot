// Package core coordinates manual input-mode changes without a GUI dependency.
package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

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

// Backend must reject a changed context before applying an operation.
// It must not simulate keystrokes or commit text. Input-source changes require
// an explicit SourceSelector so the controller can verify the destination.
type Backend interface {
	Snapshot() (State, error)
	Apply(ctx context.Context, expected State, mode Mode) error
}

type SourceSelector interface {
	TargetSource(Mode) (string, error)
}

type Result struct {
	Before  State
	After   State
	Changed bool
}

type Controller struct {
	backend Backend
	mu      sync.Mutex
}

func NewController(backend Backend) *Controller {
	return &Controller{backend: backend}
}

func (c *Controller) Inspect() (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.backend.Snapshot()
}

// Switch requires a deadline so a non-responsive mode never causes endless
// polling. Cancelling cannot undo an operation already accepted by the OS.
func (c *Controller) Switch(ctx context.Context, mode Mode) (Result, error) {
	if mode != Chinese && mode != English {
		return Result{}, ErrInvalidMode
	}
	if _, ok := ctx.Deadline(); !ok {
		return Result{}, errors.New("input-mode operation requires a deadline")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	before, err := c.backend.Snapshot()
	result := Result{Before: before}
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	expectedSource := before.InputSourceID
	selector, selectsSource := c.backend.(SourceSelector)
	if selectsSource {
		expectedSource, err = selector.TargetSource(mode)
		if err != nil {
			return result, err
		}
	}
	if before.ContextID == "" || before.InputSourceID == "" || expectedSource == "" || (!selectsSource && (before.Mode == Unknown || before.Mode == "")) {
		return result, ErrUnsupported
	}
	if before.Mode == mode && before.InputSourceID == expectedSource {
		result.After = before
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := c.backend.Apply(ctx, before, mode); err != nil {
		return result, err
	}
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("%w: %w", ErrVerification, err)
		}
		after, err := c.backend.Snapshot()
		result.After = after
		if err != nil {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("%w: %w", ErrVerification, err)
		}
		if after.ContextID != before.ContextID || (after.InputSourceID != before.InputSourceID && after.InputSourceID != expectedSource) {
			return result, ErrFocusChanged
		}
		if after.Mode == mode && after.InputSourceID == expectedSource {
			result.Changed = true
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("%w: %w", ErrVerification, ctx.Err())
		case <-ticker.C:
		}
	}
}
