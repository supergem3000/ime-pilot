package automation

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
)

type backend struct {
	applies atomic.Int32
	mode    atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *backend) Snapshot() (core.State, error) {
	if b.entered != nil {
		b.once.Do(func() { close(b.entered); <-b.release })
	}
	mode := core.Chinese
	if b.mode.Load() {
		mode = core.English
	}
	return core.State{Mode: mode, ContextID: "window/focus", InputSourceID: "pinyin"}, nil
}
func (b *backend) Apply(ctx context.Context, _ core.State, mode core.Mode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.applies.Add(1)
	b.mode.Store(mode == core.English)
	return nil
}
func setup(t *testing.T, b *backend) (*Agent, config.Config, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "editor.exe")
	c := config.Config{SchemaVersion: 2, Revision: 1, Rules: []config.Rule{{AppPath: path, Name: "Editor", Enabled: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	a := New(ctx, c, &sync.Mutex{}, func(Target) core.Backend { return b }, nil)
	t.Cleanup(func() {
		cancel()
		select {
		case <-a.Done():
		case <-time.After(time.Second):
			t.Error("worker did not stop")
		}
	})
	return a, c, path
}
func observe(a *Agent, path string, baseline bool) {
	token := a.Invalidate()
	a.Observe(Target{Window: 1, PID: 1, Path: path, At: time.Now(), Valid: func() bool { return true }}, token, baseline)
}
func outcome(t *testing.T, a *Agent) Outcome {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r := a.Recent()
		if r.Status != "" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no result")
	return Outcome{}
}
func TestActivationDoesNotRepeatForSameAppOrOnResume(t *testing.T) {
	b := &backend{}
	a, c, path := setup(t, b)
	observe(a, path, true)
	observe(a, path, false)
	time.Sleep(90 * time.Millisecond)
	if b.applies.Load() != 0 {
		t.Fatal("startup/same-app caused switch")
	}
	observe(a, "other.exe", false)
	observe(a, path, false)
	if r := outcome(t, a); r.Status != "changed" {
		t.Fatalf("result: %+v", r)
	}
	b.mode.Store(false)
	observe(a, path, false)
	time.Sleep(90 * time.Millisecond)
	if b.applies.Load() != 1 {
		t.Fatal("manual Chinese was corrected without app activation")
	}
	c.Paused = true
	c.Revision++
	a.Update(c)
	observe(a, "other.exe", false)
	observe(a, path, false)
	c.Paused = false
	c.Revision++
	a.Update(c)
	observe(a, path, false)
	time.Sleep(90 * time.Millisecond)
	if b.applies.Load() != 1 {
		t.Fatal("resume replayed the current application")
	}
}

func TestActivationUsesConfiguredMode(t *testing.T) {
	for _, mode := range []core.Mode{"", core.English, core.Chinese} {
		t.Run(string(mode), func(t *testing.T) {
			b := &backend{}
			// Start in the opposite mode so the test verifies an actual switch.
			b.mode.Store(mode == core.Chinese)
			a, c, path := setup(t, b)
			c.Rules[0].TargetMode = mode
			c.Revision++
			a.Update(c)
			observe(a, "other.exe", true)
			observe(a, path, false)
			if r := outcome(t, a); r.Status != "changed" || b.mode.Load() != (mode != core.Chinese) {
				t.Fatalf("mode %q: %+v, english=%v", mode, r, b.mode.Load())
			}
		})
	}
}
func TestFocusAndConfigurationChangesCancelBeforeSetting(t *testing.T) {
	for _, change := range []string{"focus", "pause", "rule"} {
		t.Run(change, func(t *testing.T) {
			b := &backend{entered: make(chan struct{}), release: make(chan struct{})}
			a, c, path := setup(t, b)
			observe(a, "other.exe", true)
			observe(a, path, false)
			select {
			case <-b.entered:
			case <-time.After(time.Second):
				t.Fatal("no query")
			}
			if change == "focus" {
				observe(a, "other.exe", false)
			} else {
				c.Revision++
				if change == "pause" {
					c.Paused = true
				} else {
					c.Rules[0].Enabled = false
				}
				a.Update(c)
			}
			close(b.release)
			r := outcome(t, a)
			if b.applies.Load() != 0 || r.Status != "skipped" {
				t.Fatalf("cancelled setting: %+v applies=%d", r, b.applies.Load())
			}
		})
	}
}
func TestStaleNativeGenerationAndEventAgeNeverSet(t *testing.T) {
	for _, test := range []string{"generation", "age"} {
		t.Run(test, func(t *testing.T) {
			b := &backend{}
			a, _, path := setup(t, b)
			observe(a, "other.exe", true)
			token := a.Invalidate()
			var valid atomic.Bool
			valid.Store(true)
			at := time.Now()
			if test == "age" {
				at = at.Add(-time.Second)
			}
			a.Observe(Target{Path: path, At: at, Valid: valid.Load}, token, false)
			if test == "generation" {
				valid.Store(false)
			}
			if r := outcome(t, a); r.Status != "skipped" || b.applies.Load() != 0 {
				t.Fatalf("stale request executed: %+v", r)
			}
		})
	}
}
