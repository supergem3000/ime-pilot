//go:build windows

package windowslayout

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
	"ime-pilot/internal/schemes/verification"
)

func Validate(raw json.RawMessage) error {
	c, err := Decode(raw)
	if err != nil {
		return err
	}
	_, _, err = configuredLayouts(c)
	return err
}

type scope struct {
	root, focus                           uintptr
	rootThread, thread, rootPID, focusPID uint32
}

type backend struct {
	config Config
	valid  func() bool
	scope  scope
}

// Capture the exact foreground window and focus thread at execution time.
// The callback keeps core ownership, cancellation and activation checks intact.
func New(raw json.RawMessage, valid func() bool, window uintptr) (core.Switcher, error) {
	c, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	if valid == nil || !valid() {
		return nil, core.ErrFocusChanged
	}
	current, err := capture()
	if err != nil {
		return nil, err
	}
	if window != 0 && (current.root != window || current.rootPID != uint32(os.Getpid())) {
		return nil, core.ErrFocusChanged
	}
	if !valid() {
		return nil, core.ErrFocusChanged
	}
	b := &backend{config: c, valid: valid, scope: current}
	if b.usesProfiles() {
		return &profileSwitcher{backend: b}, nil
	}
	return verification.New(b), nil
}

func capture() (scope, error) {
	var s scope
	s.root = native.ForegroundWindow()
	thread, pid := native.WindowThreadProcessID(s.root)
	s.rootPID = pid
	if s.root == 0 || thread == 0 || s.rootPID == 0 {
		return s, core.ErrFocusChanged
	}
	s.rootThread = uint32(thread)
	info, err := native.ThreadInfo(thread)
	if err != nil {
		return s, err
	}
	s.focus = info.Focus
	child := native.IsChild(s.root, s.focus)
	if s.focus == 0 || (s.focus != s.root && !child) {
		return s, core.ErrFocusChanged
	}
	thread, s.focusPID = native.WindowThreadProcessID(s.focus)
	if thread == 0 || s.focusPID == 0 {
		return s, core.ErrFocusChanged
	}
	s.thread = uint32(thread)
	return s, nil
}

func (b *backend) check() error {
	if !b.valid() {
		return core.ErrFocusChanged
	}
	current, err := capture()
	if err != nil {
		return err
	}
	if current != b.scope || !b.valid() {
		return core.ErrFocusChanged
	}
	return nil
}

func (b *backend) TargetSource(mode core.Mode) (string, error) {
	chinese, english, err := configuredLayouts(b.config)
	if err != nil {
		return "", fmt.Errorf("%w: %v", core.ErrUnsupported, err)
	}
	switch mode {
	case core.Chinese:
		return chinese.ID, nil
	case core.English:
		return english.ID, nil
	default:
		return "", core.ErrInvalidMode
	}
}

func (b *backend) Snapshot() (core.State, error) {
	if err := b.check(); err != nil {
		return core.State{}, err
	}
	handle := native.KeyboardLayout(b.scope.thread)
	if handle == 0 {
		return core.State{}, core.ErrUnsupported
	}
	id := native.LayoutID(handle)
	description := "键盘布局 " + id
	details := fmt.Sprintf("HKL=%s · 线程=%d · 仅确认布局，不代表输入法内部中英文状态", id, b.scope.thread)
	mode := core.Unknown
	if id == b.config.ChineseLayoutID {
		mode = core.Chinese
	}
	if id == b.config.EnglishLayoutID {
		mode = core.English
	}
	if err := b.check(); err != nil {
		return core.State{}, err
	}
	return core.State{
		Mode: mode, InputSourceID: id,
		ContextID:   fmt.Sprintf("%X/%X/%d/%d/%d/%d", b.scope.root, b.scope.focus, b.scope.rootThread, b.scope.thread, b.scope.rootPID, b.scope.focusPID),
		Description: description,
		Details:     details,
	}, nil
}

func (b *backend) Apply(ctx context.Context, expected core.State, mode core.Mode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chinese, english, err := configuredLayouts(b.config)
	if err != nil {
		return fmt.Errorf("%w: %v", core.ErrUnsupported, err)
	}
	var target native.InputSource
	switch mode {
	case core.Chinese:
		target = chinese
	case core.English:
		target = english
	default:
		return core.ErrInvalidMode
	}
	before, err := b.Snapshot()
	if err != nil {
		return err
	}
	if before.ContextID != expected.ContextID || before.InputSourceID != expected.InputSourceID {
		return core.ErrFocusChanged
	}
	return b.requestLayout(ctx, target.Handle)
}

// Request the language/layout on the actual input thread. TSF's desktop profile
// activation alone does not reliably change that thread's current language.
func (b *backend) requestLayout(ctx context.Context, handle uintptr) error {
	if err := b.check(); err != nil {
		return err
	}
	return native.RequestKeyboardLayout(ctx, b.scope.focus, handle)
}
