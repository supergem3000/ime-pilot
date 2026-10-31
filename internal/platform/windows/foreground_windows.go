//go:build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

type App struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Reuse one callback: syscall.NewCallback allocations are not released by Go.
// EnumWindows is synchronous; the mutex scopes its temporary result map.
var enumerateMu sync.Mutex
var enumeratedApps map[string]App
var visible = user32.NewProc("IsWindowVisible")
var style = user32.NewProc("GetWindowLongW")
var owner = user32.NewProc("GetWindow")
var clientRect = user32.NewProc("GetClientRect")
var enumerateCallback = syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
	shown, _, _ := visible.Call(hwnd)
	if shown == 0 {
		return 1
	}
	exStyle, _, _ := style.Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE = -20
	owned, _, _ := owner.Call(hwnd, 4)              // GW_OWNER
	var rect [4]int32
	ok, _, _ := clientRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if exStyle&0x80 != 0 || owned != 0 || ok == 0 || rect[2]-rect[0] < 160 || rect[3]-rect[1] < 80 {
		return 1
	}
	var pid uint32
	getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 || pid == uint32(os.Getpid()) {
		return 1
	}
	path, err := ProcessPath(pid)
	if err != nil {
		return 1
	}
	if !stringsExe(path) {
		return 1
	}
	enumeratedApps[pathKey(path)] = App{filepath.Base(path), path}
	return 1
})

// RunningApps enumerates visible application windows without reading titles.
func RunningApps() ([]App, error) {
	enumerateMu.Lock()
	defer enumerateMu.Unlock()
	enumeratedApps = map[string]App{}
	defer func() { enumeratedApps = nil }()
	if err := winapi.EnumWindows(enumerateCallback, nil); err != nil {
		return nil, err
	}
	result := make([]App, 0, len(enumeratedApps))
	for _, app := range enumeratedApps {
		// Resolve once per application, after the native window callback returns.
		app.Name = ExecutableName(app.Path)
		result = append(result, app)
	}
	sort.Slice(result, func(i, j int) bool { return pathKey(result[i].Path) < pathKey(result[j].Path) })
	return result, nil
}
func stringsExe(path string) bool { return pathKey(filepath.Ext(path)) == ".exe" }

type ForegroundEvent struct {
	Window     uintptr
	PID        uint32
	Token      uint64
	At         time.Time
	Baseline   bool
	HistoryGap bool
	Valid      func() bool
}
type Monitor struct {
	Events   chan ForegroundEvent
	epoch    atomic.Uint64
	window   atomic.Uintptr
	callback uintptr
	alive    atomic.Bool
	Done     chan struct{}
}

// StartMonitor owns a dedicated OS thread and message loop for out-of-context
// foreground events. Callback work is bounded; process queries run elsewhere.
func StartMonitor(ctx context.Context, invalidate func() uint64) (*Monitor, error) {
	m := &Monitor{Events: make(chan ForegroundEvent, 64), Done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(m.Done)
		defer close(m.Events)
		defer m.alive.Store(false)
		setHook := user32.NewProc("SetWinEventHook")
		unhook := user32.NewProc("UnhookWinEvent")
		getMessage := user32.NewProc("GetMessageW")
		peekMessage := user32.NewProc("PeekMessageW")
		postMessage := user32.NewProc("PostThreadMessageW")
		// MSG uses pointer-sized HWND/WPARAM/LPARAM; POINT and lPrivate follow.
		type msg struct {
			Window         uintptr
			Message        uint32
			WParam, LParam uintptr
			Time           uint32
			Point          [2]int32
			Private        uint32
		}
		var message msg
		peekMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 0)
		emit := func(hwnd uintptr, baseline bool) {
			if !baseline && m.window.Load() == hwnd {
				return
			}
			token := invalidate()
			m.window.Store(hwnd)
			m.epoch.Store(token)
			e := ForegroundEvent{Window: hwnd, Token: token, At: time.Now(), Baseline: baseline}
			getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&e.PID)))
			e.Valid = func() bool {
				current, _, _ := getForegroundWindow.Call()
				return ctx.Err() == nil && m.alive.Load() && m.epoch.Load() == token && m.window.Load() == hwnd && current == hwnd
			}
			select {
			case m.Events <- e:
			default:
				select {
				case <-m.Events:
				default:
				}
				e.HistoryGap = true
				select {
				case m.Events <- e:
				default:
				}
			}
		}
		m.callback = syscall.NewCallback(func(hook, event, hwnd, idObject, idChild, eventThread, eventTime uintptr) uintptr {
			emit(hwnd, false)
			return 0
		})
		hook, _, err := setHook.Call(3, 3, 0, m.callback, 0, 0, 0)
		if hook == 0 {
			ready <- fmt.Errorf("SetWinEventHook: %w", err)
			return
		}
		defer unhook.Call(hook)
		m.alive.Store(true)
		thread := winapi.GetCurrentThreadId()
		go func() {
			select {
			case <-ctx.Done():
				postMessage.Call(uintptr(thread), 0x0012, 0, 0)
			case <-m.Done:
			}
		}()
		current, _, _ := getForegroundWindow.Call()
		emit(current, true)
		ready <- nil
		for {
			result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
			user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return m, nil
}

// CaptureBinding checks a Java AWT native frame handle against the actual OS
// foreground. The supplied PID alone is never enough to establish a binding.
func (m *Monitor) CaptureBinding(pid uint32, hwnd uintptr) (WindowBinding, uint64, error) {
	epoch := m.epoch.Load()
	valid := func() bool {
		current, _, _ := getForegroundWindow.Call()
		return m.alive.Load() && m.epoch.Load() == epoch && m.window.Load() == hwnd && current == hwnd
	}
	if hwnd == 0 || !valid() {
		return WindowBinding{}, 0, fmt.Errorf("foreground frame does not match")
	}
	var actual uint32
	getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&actual)))
	if actual != pid {
		return WindowBinding{}, 0, fmt.Errorf("frame process does not match")
	}
	path, err := ProcessPath(pid)
	if err != nil {
		return WindowBinding{}, 0, err
	}
	if !valid() {
		return WindowBinding{}, 0, fmt.Errorf("foreground changed")
	}
	return WindowBinding{Window: hwnd, PID: pid, Path: path, At: time.Now(), Valid: valid}, epoch, nil
}

// WindowBinding is an OS identity captured from the monitored foreground frame.
// The signals adapter turns it into a business target and applies rule identity.
type WindowBinding struct {
	Window uintptr
	PID    uint32
	Path   string
	At     time.Time
	Valid  func() bool
}

func pathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}
