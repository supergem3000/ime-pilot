package core_test

import (
	"context"
	. "ime-pilot/internal/core"
	"testing"
	"time"
)

func owner(session, path string) Lease {
	return Lease{SessionID: session, PluginID: "test", Name: "Test",
		Expires: time.Now().Add(time.Hour), Programs: []Program{{Kind: "windows.executableName", Value: "editor.exe"}}}
}

func TestPluginBypassesRulesAndSupportsBothModes(t *testing.T) {
	b := &backend{}
	a, c, path := setup(t, b)
	c.Rules = nil
	a.Update(c)
	a.SetTakeovers([]Lease{owner("session", path)})
	target := Target{Path: path, At: time.Now(), Valid: func() bool { return true }}
	for _, mode := range []Mode{English, Chinese} {
		target.At = time.Now()
		if r := a.SubmitPlugin(context.Background(), target, "session", mode); r.Code != "changed" || r.After.Mode != mode {
			t.Fatalf("%s: %+v", mode, r)
		}
	}
	c.Paused = true
	c.Revision++
	a.Update(c)
	if r := a.SubmitPlugin(context.Background(), target, "session", English); r.Code != "skipped_paused" {
		t.Fatal(r)
	}
}

func TestTakeoverCancelsOrdinaryRequestAndReleaseDoesNotReplay(t *testing.T) {
	b := &backend{entered: make(chan struct{}), release: make(chan struct{})}
	a, _, path := setup(t, b)
	observe(a, "other.exe", true)
	observe(a, path, false)
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("query not started")
	}
	a.SetTakeovers([]Lease{owner("a", path), owner("b", path)})
	close(b.release)
	if r := outcome(t, a); r.Status != "skipped" || b.applies.Load() != 0 {
		t.Fatalf("ordinary operation escaped: %+v", r)
	}
	a.SetTakeovers([]Lease{owner("b", path)})
	observe(a, "other.exe", false)
	observe(a, path, false)
	time.Sleep(90 * time.Millisecond)
	if b.applies.Load() != 0 {
		t.Fatal("other instance coverage lost")
	}
	a.SetTakeovers(nil)
	time.Sleep(90 * time.Millisecond)
	if b.applies.Load() != 0 {
		t.Fatal("release replayed activation")
	}
	observe(a, "other.exe", false)
	observe(a, path, false)
	deadline := time.Now().Add(time.Second)
	for b.applies.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if b.applies.Load() != 1 {
		t.Fatal("ordinary rule did not resume on new activation")
	}
}

func TestRevocationAndPauseCancelPluginBeforeApply(t *testing.T) {
	for _, change := range []string{"revoke", "pause", "focus"} {
		t.Run(change, func(t *testing.T) {
			b := &backend{entered: make(chan struct{}), release: make(chan struct{})}
			a, c, path := setup(t, b)
			a.SetTakeovers([]Lease{owner("session", path)})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan ModeResult, 1)
			go func() {
				done <- a.SubmitPlugin(ctx, Target{Path: path, At: time.Now(), Valid: func() bool { return true }}, "session", English)
			}()
			select {
			case <-b.entered:
			case <-time.After(time.Second):
				t.Fatal("query not started")
			}
			switch change {
			case "revoke":
				a.SetTakeovers(nil)
			case "pause":
				c.Paused = true
				c.Revision++
				a.Update(c)
			case "focus":
				a.Invalidate()
			}
			close(b.release)
			select {
			case r := <-done:
				if r.Code == "changed" || b.applies.Load() != 0 {
					t.Fatal("cancelled operation applied")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation blocked")
			}
		})
	}
}

func TestExpiredOwnershipCannotExecuteBeforeCleanup(t *testing.T) {
	b := &backend{}
	a, _, path := setup(t, b)
	lease := owner("session", path)
	lease.Expires = time.Now().Add(-time.Second)
	a.SetTakeovers([]Lease{lease})
	r := a.SubmitPlugin(context.Background(), Target{Path: path, At: time.Now(), Valid: func() bool { return true }}, "session", English)
	if r.Code != "scope_not_owned" || b.applies.Load() != 0 {
		t.Fatal(r)
	}
}
