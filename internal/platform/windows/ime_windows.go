//go:build windows

// Package winime controls the focused input context inside the desktop window.
package winime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
)

const (
	langSimplifiedChinese = 0x0804
	conversionNative      = 0x0001
	conversionFullShape   = 0x0008
	conversionSymbol      = 0x0400
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	imm32                    = windows.NewLazySystemDLL("imm32.dll")
	getForegroundWindow      = user32.NewProc("GetForegroundWindow")
	getWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	getGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	getKeyboardLayout        = user32.NewProc("GetKeyboardLayout")
	isChild                  = user32.NewProc("IsChild")
	immGetContext            = imm32.NewProc("ImmGetContext")
	immReleaseContext        = imm32.NewProc("ImmReleaseContext")
	immGetConversionStatus   = imm32.NewProc("ImmGetConversionStatus")
	immGetDescription        = imm32.NewProc("ImmGetDescriptionW")
)

type guiThreadInfo struct {
	Size, Flags                                        uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	CaretRect                                          [4]int32
}

type windowContext struct {
	root, focus, layout, ime uintptr
	rootThread, thread, pid  uint32
}

func (c windowContext) identity() string {
	return fmt.Sprintf("%X/%X/%d/%d/%d/%X", c.root, c.focus, c.rootThread, c.thread, c.pid, c.ime)
}

// Backend is scoped to an exact foreground root and its verified owner.
// New limits control to this process; NewTarget uses a native activation event.
// WebView2 focus may have a different PID but must remain a native descendant.
type Backend struct {
	window   uintptr
	ownerPID uint32
	path     string
	valid    func() bool
	external bool
}

func New(window uintptr) (*Backend, error) {
	if window == 0 {
		return nil, fmt.Errorf("%w: native window is not ready", core.ErrUnsupported)
	}
	return &Backend{window: window, ownerPID: uint32(os.Getpid())}, nil
}
func (b *Backend) acquire() (windowContext, error) {
	if b.valid != nil && !b.valid() {
		return windowContext{}, core.ErrFocusChanged
	}
	foreground, _, _ := getForegroundWindow.Call()
	if foreground != b.window {
		return windowContext{}, core.ErrFocusChanged
	}
	var pid uint32
	rootThread, _, _ := getWindowThreadProcessID.Call(b.window, uintptr(unsafe.Pointer(&pid)))
	if rootThread == 0 || pid != b.ownerPID {
		return windowContext{}, core.ErrFocusChanged
	}
	if b.external {
		path, err := processPath(pid)
		if err != nil {
			return windowContext{}, fmt.Errorf("%w: process identity: %v", core.ErrUnsupported, err)
		}
		if config.Key(path) != config.Key(b.path) {
			return windowContext{}, core.ErrFocusChanged
		}
	}
	info := guiThreadInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, err := getGUIThreadInfo.Call(rootThread, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return windowContext{}, nativeError("GetGUIThreadInfo", err)
	}
	child, _, _ := isChild.Call(b.window, info.Focus)
	if info.Focus == 0 || (info.Focus != b.window && child == 0) {
		return windowContext{}, core.ErrFocusChanged
	}
	var focusPID uint32
	thread, _, _ := getWindowThreadProcessID.Call(info.Focus, uintptr(unsafe.Pointer(&focusPID)))
	if thread == 0 || focusPID == 0 {
		return windowContext{}, core.ErrFocusChanged
	}
	layout, _, _ := getKeyboardLayout.Call(thread)
	if b.external && uint16(layout)&0x3ff == 9 {
		return windowContext{b.window, info.Focus, layout, 0, uint32(rootThread), uint32(thread), focusPID}, nil
	}
	if layout == 0 || uint16(layout) != langSimplifiedChinese {
		return windowContext{}, fmt.Errorf("%w: select Microsoft Pinyin first (HKL=%X)", core.ErrUnsupported, layout)
	}
	ime, _, _ := immGetDefaultIMEWindow.Call(info.Focus)
	if ime == 0 {
		return windowContext{}, fmt.Errorf("%w: focused control has no IME window", core.ErrUnsupported)
	}
	return windowContext{b.window, info.Focus, layout, ime, uint32(rootThread), uint32(thread), focusPID}, nil
}
func (b *Backend) check(c windowContext) error {
	current, err := b.acquire()
	if err != nil {
		return err
	}
	if current != c {
		return core.ErrFocusChanged
	}
	return nil
}
func (b *Backend) read(ctx context.Context, c windowContext) (core.State, uintptr, uintptr, error) {
	if c.ime == 0 && b.external && uint16(c.layout)&0x3ff == 9 {
		if err := b.check(c); err != nil {
			return core.State{}, 0, 0, err
		}
		return core.State{Mode: core.English, ContextID: c.identity(), InputSourceID: fmt.Sprintf("session-hkl:%X", c.layout), Description: "英文键盘布局", Details: fmt.Sprintf("HKL=%X", c.layout)}, 0, 0, nil
	}
	conversion, err := imeControl(ctx, c.ime, imeGetConversion, 0)
	if err != nil {
		return core.State{}, 0, 0, err
	}
	open, err := imeControl(ctx, c.ime, imeGetOpen, 0)
	if err != nil {
		return core.State{}, 0, 0, err
	}
	if open > 1 {
		return core.State{}, 0, 0, core.ErrUnsupported
	}
	if err = b.check(c); err != nil {
		return core.State{}, 0, 0, err
	}
	mode := core.Unknown
	if open == 0 {
		mode = core.English
	} else if conversion&conversionNative != 0 {
		mode = core.Chinese
	} else if conversion&(conversionFullShape|conversionSymbol) == 0 {
		mode = core.English
	}
	name := make([]uint16, 256)
	count, _, _ := immGetDescription.Call(c.layout, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	description := "简体中文输入布局（具体输入法待实测确认）"
	if count > 0 {
		description = windows.UTF16ToString(name)
	}
	return core.State{Mode: mode, ContextID: c.identity(), InputSourceID: fmt.Sprintf("session-hkl:%X", c.layout), Description: description,
		Details: fmt.Sprintf("HKL=%X · IME open=%t · conversion=0x%X · focus PID=%d", c.layout, open != 0, conversion, c.pid)}, conversion, open, nil
}
func (b *Backend) Snapshot() (core.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c, err := b.acquire()
	if err != nil {
		return core.State{}, err
	}
	state, _, _, err := b.read(ctx, c)
	return state, err
}
func (b *Backend) Apply(ctx context.Context, expected core.State, mode core.Mode) error {
	if mode != core.Chinese && mode != core.English {
		return core.ErrInvalidMode
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := b.acquire()
	if err != nil {
		return err
	}
	state, conversion, open, err := b.read(ctx, c)
	if err != nil {
		return err
	}
	if state.ContextID != expected.ContextID || state.InputSourceID != expected.InputSourceID {
		return core.ErrFocusChanged
	}
	if state.Mode == mode {
		return nil
	}
	if mode == core.Chinese && open == 0 {
		if err = b.check(c); err != nil {
			return err
		}
		// Legacy IMC_SETOPENSTATUS. Preserve an already-open IME whenever possible.
		if _, err = imeControl(ctx, c.ime, imeSetOpen, 1); err != nil {
			return err
		}
		_, conversion, open, err = b.read(ctx, c)
		if err != nil {
			return err
		}
		if open != 1 {
			return fmt.Errorf("%w: IME did not open", core.ErrVerification)
		}
	}
	if mode == core.Chinese {
		conversion |= conversionNative
	} else {
		conversion &^= conversionNative | conversionFullShape | conversionSymbol
	}
	if err = b.check(c); err != nil {
		return err
	}
	_, err = imeControl(ctx, c.ime, imeSetConversion, conversion)
	return err
}
func nativeError(operation string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return fmt.Errorf("%s rejected the operation (no Windows error code)", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
