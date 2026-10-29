package automation

import (
	"sync/atomic"
	"testing"
	"time"
)

// Slow identity resolution must not hide an actual A -> B -> A activation.
// The obsolete B event updates history but must never be executed.
func TestRapidReturnRetainsApplicationHistory(t *testing.T) {
	b := &backend{}
	a, _, path := setup(t, b)
	observe(a, path, true)
	away := a.Invalidate()
	back := a.Invalidate()
	a.Observe(Target{Path: "other.exe", Valid: func() bool { return false }}, away, false)
	var valid atomic.Bool
	valid.Store(true)
	a.Observe(Target{Path: path, At: time.Now(), Valid: valid.Load}, back, false)
	if r := outcome(t, a); r.Status != "changed" || b.applies.Load() != 1 {
		t.Fatalf("return activation lost: %+v", r)
	}
}
