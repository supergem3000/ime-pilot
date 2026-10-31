package verification

import (
	"context"
	"errors"
	"fmt"
	"ime-pilot/internal/core"
	"sync"
	"time"
)

// Backend must reject a changed context before applying an operation.
// It must not simulate keystrokes or commit text. Input-source changes require
// an explicit SourceSelector so the controller can verify the destination.
type Backend interface {
	Snapshot() (core.State, error)
	Apply(ctx context.Context, expected core.State, mode core.Mode) error
}

type SourceSelector interface {
	TargetSource(core.Mode) (string, error)
}

type Controller struct {
	backend Backend
	mu      sync.Mutex
}

func NewController(backend Backend) *Controller {
	return &Controller{backend: backend}
}

func (c *Controller) Inspect() (core.State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.backend.Snapshot()
}

// Switch requires a deadline so a non-responsive mode never causes endless
// polling. Cancelling cannot undo an operation already accepted by the OS.
func (c *Controller) Switch(ctx context.Context, mode core.Mode) (core.Result, error) {
	if mode != core.Chinese && mode != core.English {
		return core.Result{}, core.ErrInvalidMode
	}
	if _, ok := ctx.Deadline(); !ok {
		return core.Result{}, errors.New("input-mode operation requires a deadline")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	before, err := c.backend.Snapshot()
	result := core.Result{Before: before}
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
	if before.ContextID == "" || before.InputSourceID == "" || expectedSource == "" || (!selectsSource && (before.Mode == core.Unknown || before.Mode == "")) {
		return result, core.ErrUnsupported
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
			return result, fmt.Errorf("%w: %w", core.ErrVerification, err)
		}
		after, err := c.backend.Snapshot()
		result.After = after
		if err != nil {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("%w: %w", core.ErrVerification, err)
		}
		if after.ContextID != before.ContextID || (after.InputSourceID != before.InputSourceID && after.InputSourceID != expectedSource) {
			return result, core.ErrFocusChanged
		}
		if after.Mode == mode && after.InputSourceID == expectedSource {
			result.Changed = true
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("%w: %w", core.ErrVerification, ctx.Err())
		case <-ticker.C:
		}
	}
}
