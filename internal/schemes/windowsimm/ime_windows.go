//go:build windows

// Package winime controls the focused input context inside the desktop window.
package winime

import (
	"context"
	"fmt"
	"os"
	"time"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
)

const (
	langSimplifiedChinese = 0x0804
	conversionNative      = 0x0001
	conversionFullShape   = 0x0008
	conversionSymbol      = 0x0400
)

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

func NewManual(window uintptr) (*Backend, error) {
	if window == 0 {
		return nil, fmt.Errorf("%w: native window is not ready", core.ErrUnsupported)
	}
	return &Backend{window: window, ownerPID: uint32(os.Getpid())}, nil
}
func (b *Backend) acquire() (windowContext, error) {
	if b.valid != nil && !b.valid() {
		return windowContext{}, core.ErrFocusChanged
	}
	foreground := native.ForegroundWindow()
	if foreground != b.window {
		return windowContext{}, core.ErrFocusChanged
	}
	rootThread, pid := native.WindowThreadProcessID(b.window)
	if rootThread == 0 || pid != b.ownerPID {
		return windowContext{}, core.ErrFocusChanged
	}
	if b.external {
		path, err := native.ProcessPath(pid)
		if err != nil {
			return windowContext{}, fmt.Errorf("%w: process identity: %v", core.ErrUnsupported, err)
		}
		if core.Key(path) != core.Key(b.path) {
			return windowContext{}, core.ErrFocusChanged
		}
	}
	info, err := native.ThreadInfo(rootThread)
	if err != nil {
		return windowContext{}, err
	}
	child := native.IsChild(b.window, info.Focus)
	if info.Focus == 0 || (info.Focus != b.window && !child) {
		return windowContext{}, core.ErrFocusChanged
	}
	thread, focusPID := native.WindowThreadProcessID(info.Focus)
	if thread == 0 || focusPID == 0 {
		return windowContext{}, core.ErrFocusChanged
	}
	layout := native.KeyboardLayout(thread)
	if b.external && uint16(layout)&0x3ff == 9 {
		return windowContext{b.window, info.Focus, layout, 0, uint32(rootThread), uint32(thread), focusPID}, nil
	}
	if layout == 0 || uint16(layout) != langSimplifiedChinese {
		return windowContext{}, fmt.Errorf("%w: select Microsoft Pinyin first (HKL=%X)", core.ErrUnsupported, layout)
	}
	ime := native.DefaultIMEWindow(info.Focus)
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
	conversion, err := native.IMEControl(ctx, c.ime, native.IMEGetConversion, 0)
	if err != nil {
		return core.State{}, 0, 0, err
	}
	open, err := native.IMEControl(ctx, c.ime, native.IMEGetOpen, 0)
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
	description := native.IMEDescription(c.layout)
	if description == "" {
		description = "简体中文输入布局（具体输入法待实测确认）"
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
		if _, err = native.IMEControl(ctx, c.ime, native.IMESetOpen, 1); err != nil {
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
	_, err = native.IMEControl(ctx, c.ime, native.IMESetConversion, conversion)
	return err
}
