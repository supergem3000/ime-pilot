package automation

import (
	"context"
	"testing"
	"time"

	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	"ime-pilot/internal/takeover"
)

// Exercise macOS identities on any host without pretending to call native APIs.
func TestBundleIdentityTakeoverAndRuleRestoration(t *testing.T) {
	b := &backend{}
	a, c, _ := setup(t, b)
	c.Rules = []config.Rule{{BundleID: "com.example.Editor", Name: "Editor", Enabled: true, TargetMode: core.Chinese}}
	a.Update(c)
	lease := takeover.Lease{SessionID: "mac", Expires: time.Now().Add(time.Hour), Programs: []takeover.Program{{Kind: "macos.bundleId", Value: "com.example.Editor"}}}
	a.SetTakeovers([]takeover.Lease{lease})
	target := Target{BundleID: "com.example.Editor", Path: "/Applications/Editor.app/Contents/MacOS/editor", At: time.Now(), Valid: func() bool { return true }}
	if result := a.SubmitPlugin(context.Background(), target, "mac", core.English); result.Code != "changed" {
		t.Fatal(result)
	}
	// A filesystem path that happens to equal the ID must not confer ownership.
	impostor := target
	impostor.BundleID, impostor.Path = "", target.BundleID
	if result := a.SubmitPlugin(context.Background(), impostor, "mac", core.Chinese); result.Code != "scope_not_owned" {
		t.Fatal(result)
	}
	activate := func(target Target) {
		target.At = time.Now()
		a.Observe(target, a.Invalidate(), false)
	}
	activate(target)
	time.Sleep(90 * time.Millisecond)
	if !b.mode.Load() {
		t.Fatal("ordinary rule ran during takeover")
	}
	a.SetTakeovers(nil)
	activate(target)
	time.Sleep(90 * time.Millisecond)
	if !b.mode.Load() {
		t.Fatal("disconnect replayed current activation")
	}
	activate(Target{BundleID: "com.example.Other", Valid: func() bool { return true }})
	// Moving the app must not break a Bundle ID rule.
	target.Path = "/Users/test/Applications/Editor.app/Contents/MacOS/editor"
	activate(target)
	deadline := time.Now().Add(time.Second)
	for b.mode.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if b.mode.Load() {
		t.Fatal("Chinese rule did not resume after disconnect")
	}
}
