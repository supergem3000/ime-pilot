//go:build darwin && cgo

package macos

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/darwin"
)

type Source struct {
	mu  sync.Mutex
	ctx context.Context
}

func (s *Source) Apps() ([]core.App, error) {
	apps, err := native.RunningApps()
	out := make([]core.App, 0, len(apps))
	for _, app := range apps {
		out = append(out, core.App{Name: app.Name, BundleID: app.BundleID})
	}
	return out, err
}
func (s *Source) Run(ctx context.Context, invalidate func() uint64, observe func(core.Activation)) error {
	m, err := native.StartMonitor(ctx, invalidate)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	first := true
	for event := range m.Events {
		state := event.State
		t := core.Target{PID: state.PID, BundleID: state.BundleID, At: event.At, HistoryGap: event.HistoryGap, Valid: func() bool { return current(ctx, state) }}
		observe(core.Activation{Target: t, Token: event.Token, Baseline: first || event.Baseline})
		first = false
	}
	<-m.Done
	return nil
}
func (s *Source) Capture(e core.Evidence) (core.Target, error) {
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil {
		return core.Target{}, fmt.Errorf("foreground monitor unavailable")
	}
	state, err := native.Snapshot(false)
	if err != nil {
		return core.Target{}, err
	}
	if e.PID == 0 || state.PID != e.PID || state.BundleID == "" {
		return core.Target{}, core.ErrFocusChanged
	}
	valid := func() bool { return current(ctx, state) }
	if !valid() {
		return core.Target{}, core.ErrFocusChanged
	}
	return core.Target{PID: state.PID, BundleID: state.BundleID, At: time.Now(), Valid: valid}, nil
}

func current(ctx context.Context, expected native.State) bool {
	if ctx.Err() != nil || native.Epoch() != expected.Epoch {
		return false
	}
	state, err := native.Snapshot(false)
	return err == nil && state.PID == expected.PID && state.BundleID == expected.BundleID && state.Epoch == expected.Epoch && ctx.Err() == nil
}
