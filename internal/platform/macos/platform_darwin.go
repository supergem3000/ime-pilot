//go:build darwin && cgo

package macime

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework AppKit -framework Carbon
#include "native.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"

	"ime-pilot/internal/automation"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
)

type App struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
}

type InputSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Chinese bool   `json:"chinese"`
	English bool   `json:"english"`
}

func decodeJSON(value *C.char, dest any) error {
	if value == nil {
		return fmt.Errorf("%w: 无法读取 macOS 状态", core.ErrUnsupported)
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

func ValidateSources(c config.MacOS) error {
	sources, err := InputSources()
	if err != nil {
		return err
	}
	for _, selection := range []struct {
		id      string
		chinese bool
	}{{c.ChineseSourceID, true}, {c.EnglishSourceID, false}} {
		if selection.id == "" {
			continue
		}
		found := false
		for _, source := range sources {
			if source.ID == selection.id && ((selection.chinese && source.Chinese) || (!selection.chinese && source.English)) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: 输入源未启用或类型不匹配：%s", core.ErrUnsupported, selection.id)
		}
	}
	return nil
}

type nativeState struct {
	PID      uint32 `json:"pid"`
	BundleID string `json:"bundleId"`
	Epoch    uint64 `json:"epoch"`
	Window   uint64 `json:"window"`
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
}

func snapshot(manual bool) (nativeState, error) {
	var state nativeState
	var manualWindow C.int
	if manual {
		manualWindow = 1
	}
	err := decodeJSON(C.ime_snapshot(manualWindow), &state)
	return state, err
}

type Backend struct {
	target automation.Target
	config config.MacOS
	manual bool
	last   nativeState
}

func NewManual(c config.MacOS) (*Backend, error) {
	state, err := snapshot(true)
	if err != nil {
		return nil, err
	}
	if state.PID != uint32(os.Getpid()) || state.Window == 0 {
		return nil, core.ErrFocusChanged
	}
	return &Backend{config: c, manual: true, target: automation.Target{PID: state.PID, Valid: func() bool { return uint64(C.ime_epoch()) == state.Epoch }}, last: state}, nil
}

func NewTarget(target automation.Target, c config.MacOS) *Backend {
	return &Backend{target: target, config: c}
}

func (b *Backend) TargetSource(mode core.Mode) (string, error) {
	id := b.config.EnglishSourceID
	if mode == core.Chinese {
		id = b.config.ChineseSourceID
	}
	if id == "" {
		return "", fmt.Errorf("%w: 请在设置中选择目标输入源", core.ErrUnsupported)
	}
	return id, nil
}

func (b *Backend) Snapshot() (core.State, error) {
	if !b.target.Valid() {
		return core.State{}, core.ErrFocusChanged
	}
	state, err := snapshot(b.manual)
	if err != nil {
		return core.State{}, err
	}
	if !b.target.Valid() || state.PID != b.target.PID || (b.manual && state.Window != b.last.Window) || (!b.manual && state.BundleID != b.target.BundleID) {
		return core.State{}, core.ErrFocusChanged
	}
	b.last = state
	mode := core.Unknown
	if state.SourceID == b.config.ChineseSourceID {
		mode = core.Chinese
	}
	if state.SourceID == b.config.EnglishSourceID {
		mode = core.English
	}
	return core.State{Mode: mode, ContextID: fmt.Sprintf("%d/%d/%d", state.PID, state.Epoch, state.Window), InputSourceID: state.SourceID,
		Description: state.Name, Details: "输入源：" + state.SourceID + "；仅确认系统输入源，不读取输入法内部中英文状态。"}, nil
}

func (b *Backend) Apply(ctx context.Context, expected core.State, mode core.Mode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := b.Snapshot()
	if err != nil {
		return err
	}
	if state.ContextID != expected.ContextID || state.InputSourceID != expected.InputSourceID {
		return core.ErrFocusChanged
	}
	id, err := b.TargetSource(mode)
	if err != nil {
		return err
	}
	selection := config.MacOS{EnglishSourceID: id}
	if mode == core.Chinese {
		selection = config.MacOS{ChineseSourceID: id}
	}
	if err := ValidateSources(selection); err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return fmt.Errorf("input-source operation requires a deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.DeadlineExceeded
	}
	nativeDeadline := C.ime_now() + C.uint64_t(remaining.Nanoseconds())
	if !b.target.Valid() {
		return core.ErrFocusChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	previous, target := C.CString(expected.InputSourceID), C.CString(id)
	defer C.free(unsafe.Pointer(previous))
	defer C.free(unsafe.Pointer(target))
	switch C.ime_select(C.uint32_t(b.last.PID), C.uint64_t(b.last.Epoch), C.uint64_t(b.last.Window), previous, target, nativeDeadline) {
	case 0:
		return nil
	case -2:
		return core.ErrFocusChanged
	case -4:
		return context.DeadlineExceeded
	case -1:
		return fmt.Errorf("%w: 目标输入源已不可用", core.ErrUnsupported)
	default:
		return fmt.Errorf("macOS 拒绝选择输入源：%s", id)
	}
}

type ForegroundEvent struct {
	Target   automation.Target
	Token    uint64
	Baseline bool
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
	target := automation.Target{PID: uint32(pid), BundleID: C.GoString(bundle), At: time.Now(),
		Valid: func() bool { return m.ctx.Err() == nil && uint64(C.ime_epoch()) == uint64(epoch) }}
	event := ForegroundEvent{Target: target, Token: token, Baseline: baseline != 0}
	select {
	case m.Events <- event:
	default:
		select {
		case <-m.Events:
		default:
		}
		event.Target.HistoryGap = true
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

func ResolveEvent(event ForegroundEvent) (automation.Target, error) { return event.Target, nil }

func (m *Monitor) CaptureBinding(pid uint32) (automation.Target, uint64, error) {
	state, err := snapshot(false)
	if err != nil {
		return automation.Target{}, 0, err
	}
	if pid == 0 || state.PID != pid || state.BundleID == "" {
		return automation.Target{}, 0, fmt.Errorf("%w: 目标进程 %d，前台进程 %d（%s）", core.ErrFocusChanged, pid, state.PID, state.BundleID)
	}
	valid := func() bool {
		return m.ctx.Err() == nil && activeMonitor.Load() == m && uint64(C.ime_epoch()) == state.Epoch
	}
	if !valid() {
		return automation.Target{}, 0, core.ErrFocusChanged
	}
	return automation.Target{PID: pid, BundleID: state.BundleID, At: time.Now(), Valid: valid}, state.Epoch, nil
}
