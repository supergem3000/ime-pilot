//go:build darwin && cgo

package darwin

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework AppKit -framework Carbon
#include "native.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"
)

// ErrUnavailable means the native bridge could not read the requested state.
var ErrUnavailable = errors.New("无法读取 macOS 状态")

type App struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
}

type AppInfo struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

func ApplicationInfo(bundleID string) (AppInfo, error) {
	identifier := C.CString(bundleID)
	defer C.free(unsafe.Pointer(identifier))
	var info AppInfo
	err := decodeJSON(C.ime_app_info(identifier), &info)
	return info, err
}

type InputSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Chinese bool   `json:"chinese"`
	English bool   `json:"english"`
}

func decodeJSON(value *C.char, dest any) error {
	if value == nil {
		return ErrUnavailable
	}
	defer C.free(unsafe.Pointer(value))
	return json.Unmarshal([]byte(C.GoString(value)), dest)
}

func RunningApps() ([]App, error) {
	apps := []App{}
	if err := decodeJSON(C.ime_apps(), &apps); err != nil {
		return nil, err
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].BundleID < apps[j].BundleID })
	result := apps[:0]
	for _, app := range apps {
		if len(result) == 0 || result[len(result)-1].BundleID != app.BundleID {
			result = append(result, app)
		}
	}
	return result, nil
}

func InputSources() ([]InputSource, error) {
	sources := []InputSource{}
	err := decodeJSON(C.ime_sources(), &sources)
	return sources, err
}

type State struct {
	PID      uint32 `json:"pid"`
	BundleID string `json:"bundleId"`
	Epoch    uint64 `json:"epoch"`
	Window   uint64 `json:"window"`
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
}

func Snapshot(manual bool) (State, error) {
	var state State
	var manualWindow C.int
	if manual {
		manualWindow = 1
	}
	err := decodeJSON(C.ime_snapshot(manualWindow), &state)
	return state, err
}

type ForegroundEvent struct {
	State      State
	At         time.Time
	Token      uint64
	Baseline   bool
	HistoryGap bool
}

type Monitor struct {
	Events     chan ForegroundEvent
	Done       chan struct{}
	ctx        context.Context
	invalidate func() uint64
}

var activeMonitor atomic.Pointer[Monitor]

//export goForegroundChanged
func goForegroundChanged(pid C.uint32_t, bundle *C.char, epoch C.uint64_t, baseline C.int) {
	m := activeMonitor.Load()
	if m == nil || m.ctx.Err() != nil {
		return
	}
	token := m.invalidate()
	event := ForegroundEvent{At: time.Now(), State: State{PID: uint32(pid), BundleID: C.GoString(bundle), Epoch: uint64(epoch)}, Token: token, Baseline: baseline != 0}
	select {
	case m.Events <- event:
	default:
		select {
		case <-m.Events:
		default:
		}
		event.HistoryGap = true
		m.Events <- event
	}
}

func StartMonitor(ctx context.Context, invalidate func() uint64) (*Monitor, error) {
	m := &Monitor{Events: make(chan ForegroundEvent, 64), Done: make(chan struct{}), ctx: ctx, invalidate: invalidate}
	if !activeMonitor.CompareAndSwap(nil, m) {
		return nil, fmt.Errorf("foreground monitor already running")
	}
	C.ime_monitor_start()
	go func() {
		<-ctx.Done()
		C.ime_monitor_stop()
		activeMonitor.Store(nil)
		close(m.Events)
		close(m.Done)
	}()
	return m, nil
}

func Epoch() uint64 { return uint64(C.ime_epoch()) }
func Select(state State, previous, target string, deadline time.Time) int {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return -4
	}
	from, to := C.CString(previous), C.CString(target)
	defer C.free(unsafe.Pointer(from))
	defer C.free(unsafe.Pointer(to))
	return int(C.ime_select(C.uint32_t(state.PID), C.uint64_t(state.Epoch), C.uint64_t(state.Window), from, to, C.ime_now()+C.uint64_t(remaining.Nanoseconds())))
}
