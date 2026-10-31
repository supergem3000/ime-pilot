//go:build windows

package windows

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestForegroundMonitorReleasesHookAndMessageLoop(t *testing.T) {
	foreground, _, _ := getForegroundWindow.Call()
	if foreground == 0 {
		t.Skip("interactive desktop unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var generation atomic.Uint64
	monitor, err := StartMonitor(ctx, func() uint64 { return generation.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case initial := <-monitor.Events:
		if !initial.Baseline {
			t.Error("startup event must only establish a baseline")
		}
	case <-time.After(time.Second):
		t.Fatal("no baseline event")
	}
	cancel()
	select {
	case <-monitor.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("foreground hook/message loop did not stop")
	}
}
