//go:build windows

package winime

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
)

type Source struct {
	mu      sync.Mutex
	monitor *native.Monitor
}

func (s *Source) Apps() ([]core.App, error) {
	apps, err := native.RunningApps()
	out := make([]core.App, 0, len(apps))
	for _, app := range apps {
		out = append(out, core.App{Name: app.Name, Path: app.Path})
	}
	return out, err
}
func (s *Source) Run(ctx context.Context, invalidate func() uint64, observe func(core.Activation)) error {
	m, err := native.StartMonitor(ctx, invalidate)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.monitor = m
	s.mu.Unlock()
	first := true
	for event := range m.Events {
		target, err := ResolveEvent(event)
		if err != nil {
			target.Path = ""
		} else {
			target = guardIdentity(target)
		}
		observe(core.Activation{Target: target, Token: event.Token, Baseline: first || event.Baseline})
		first = false
	}
	<-m.Done
	return nil
}
func (s *Source) Capture(e core.Evidence) (core.Target, error) {
	hwnd, err := strconv.ParseUint(e.WindowID, 10, 64)
	if err != nil {
		return core.Target{}, err
	}
	s.mu.Lock()
	m := s.monitor
	s.mu.Unlock()
	if m == nil {
		return core.Target{}, fmt.Errorf("foreground monitor unavailable")
	}
	binding, _, err := m.CaptureBinding(e.PID, uintptr(hwnd))
	if err != nil {
		return core.Target{}, err
	}
	target := core.Target{
		Window: binding.Window, PID: binding.PID, Path: binding.Path,
		At: binding.At, Valid: binding.Valid,
	}
	return guardIdentity(target), nil
}

// Recheck the captured identity, not only the HWND. Schemes capture their own
// native context, so core's guard must reject a reused handle or changed owner.
func guardIdentity(t core.Target) core.Target {
	original := t.Valid
	t.Valid = func() bool {
		if !original() {
			return false
		}
		_, pid := native.WindowThreadProcessID(t.Window)
		if pid != t.PID {
			return false
		}
		path, err := native.ProcessPath(pid)
		return err == nil && core.Key(path) == core.Key(t.Path) && original()
	}
	return t
}
