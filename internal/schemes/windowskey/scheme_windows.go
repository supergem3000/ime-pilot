//go:build windows

// Package windowskey switches the current IME with one state-aware Shift tap.
// It reads IMM compatibility state but never writes it or selects an input source.
package windowskey

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
)

const ID = "windows.shift"

func Validate(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("Shift 方案配置必须是 JSON 对象")
	}
	return nil
}

type Switcher struct {
	mu    sync.Mutex
	scope inputContext
	valid func() bool
	last  core.Result
}

func New(raw json.RawMessage, valid func() bool, window uintptr) (*Switcher, error) {
	if err := Validate(raw); err != nil {
		return nil, err
	}
	if valid == nil || !valid() {
		return nil, core.ErrFocusChanged
	}
	scope, err := capture()
	if err != nil {
		return nil, err
	}
	if window != 0 && (scope.root != window || scope.rootPID != uint32(os.Getpid())) {
		return nil, core.ErrFocusChanged
	}
	if !valid() {
		return nil, core.ErrFocusChanged
	}
	return &Switcher{scope: scope, valid: valid}, nil
}

func (s *Switcher) SwitchEn(ctx context.Context) error {
	return s.switchMode(ctx, core.English)
}

func (s *Switcher) SwitchCN(ctx context.Context) error {
	return s.switchMode(ctx, core.Chinese)
}

func (s *Switcher) Report() core.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func (s *Switcher) Inspect() (core.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return s.read(ctx)
}

func (s *Switcher) switchMode(ctx context.Context, mode core.Mode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = core.Result{}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("Shift 切换需要超时限制")
	}

	before, err := s.read(ctx)
	s.last.Before = before
	if err != nil {
		return err
	}
	if before.Mode == mode {
		s.last.After = before
		return nil
	}

	// Window activation may arrive while Alt/Tab, Esc or a mouse button is
	// still held. Wait within this request's budget, never carry it to a new
	// window. Re-read afterwards because the user's key release may toggle IME.
	if err := s.waitForRelease(ctx); err != nil {
		return err
	}
	before, err = s.read(ctx)
	s.last.Before = before
	if err != nil {
		return err
	}
	if before.Mode == mode {
		s.last.After = before
		return nil
	}
	if err := native.TapKey(0x10, 0x2a, func() error { return s.check(ctx) }); err != nil {
		return err
	}

	// Shift is a toggle. Poll only the readback; another tap could undo a late
	// successful switch. The compatibility state does not prove actual typing.
	for {
		after, err := s.read(ctx)
		s.last.After = after
		if err != nil {
			return err
		}
		if after.Mode == mode {
			s.last.Changed = true
			return nil
		}
		if err := wait(ctx, 15*time.Millisecond); err != nil {
			return fmt.Errorf("%w: %w", core.ErrVerification, err)
		}
	}
}

func (s *Switcher) waitForRelease(ctx context.Context) error {
	for {
		if err := s.check(ctx); err != nil {
			return err
		}
		if !native.AnyKeyDown() {
			return nil
		}
		if err := wait(ctx, 10*time.Millisecond); err != nil {
			return fmt.Errorf("等待按键松开超时：%w", err)
		}
	}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
