//go:build windows

package windowskey

import (
	"context"
	"fmt"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
)

type inputContext struct {
	root, focus, layout, ime              uintptr
	rootThread, thread, rootPID, focusPID uint32
}

func capture() (inputContext, error) {
	var c inputContext
	c.root = native.ForegroundWindow()
	c.rootThread, c.rootPID = native.WindowThreadProcessID(c.root)
	if c.root == 0 || c.rootThread == 0 || c.rootPID == 0 {
		return c, core.ErrFocusChanged
	}
	info, err := native.ThreadInfo(c.rootThread)
	if err != nil {
		return c, err
	}
	c.focus = info.Focus
	// Root focus is valid: switching after application activation does not
	// require clicking an edit box. WebView child processes are also allowed,
	// but the focused control must remain a native descendant of this root.
	if c.focus == 0 || (c.focus != c.root && !native.IsChild(c.root, c.focus)) {
		return c, core.ErrFocusChanged
	}
	c.thread, c.focusPID = native.WindowThreadProcessID(c.focus)
	if c.thread == 0 || c.focusPID == 0 {
		return c, core.ErrFocusChanged
	}
	c.layout = native.KeyboardLayout(c.thread)
	c.ime = native.DefaultIMEWindow(c.focus)
	return c, nil
}

func (s *Switcher) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.valid() {
		return core.ErrFocusChanged
	}
	current, err := capture()
	if err != nil {
		return err
	}
	if current != s.scope || !s.valid() {
		return core.ErrFocusChanged
	}
	return ctx.Err()
}

func (s *Switcher) read(ctx context.Context) (core.State, error) {
	if err := s.check(ctx); err != nil {
		return core.State{}, err
	}
	c := s.scope
	if uint16(c.layout) != 0x0804 || c.ime == 0 {
		return core.State{}, fmt.Errorf("%w: 请先在目标应用中选中豆包输入法", core.ErrUnsupported)
	}
	// Read-only IMM: writing conversion/open values can contaminate the
	// compatibility state without changing Doubao's actual input mode.
	conversion, err := native.IMEControl(ctx, c.ime, native.IMEGetConversion, 0)
	if err != nil {
		return core.State{}, err
	}
	open, err := native.IMEControl(ctx, c.ime, native.IMEGetOpen, 0)
	if err != nil {
		return core.State{}, err
	}
	if err := s.check(ctx); err != nil {
		return core.State{}, err
	}
	if open != 1 || conversion&(0x0008|0x0400) != 0 {
		return core.State{}, fmt.Errorf("%w: 输入法未打开或当前为全角/符号模式，无法可靠使用 Shift 切换", core.ErrUnsupported)
	}
	mode := core.English
	if conversion&0x0001 != 0 {
		mode = core.Chinese
	}
	return core.State{
		Mode:          mode,
		ContextID:     fmt.Sprintf("%X/%X/%d/%d/%d/%d/%X", c.root, c.focus, c.rootThread, c.thread, c.rootPID, c.focusPID, c.ime),
		InputSourceID: fmt.Sprintf("session-hkl:%X", c.layout),
		Description:   "Shift 切换 · IMM 状态回读",
		Details:       fmt.Sprintf("conversion=0x%X · open=%d · 不识别具体输入法，请确认目标应用使用豆包及 Shift 中英文切换", conversion, open),
	}, nil
}
