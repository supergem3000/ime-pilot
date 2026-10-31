//go:build darwin && cgo

package macosinputsource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/darwin"
)

func platformError(err error) error {
	if errors.Is(err, native.ErrUnavailable) {
		return fmt.Errorf("%w: %v", core.ErrUnsupported, err)
	}
	return err
}

func InputSources() ([]native.InputSource, error) {
	sources, err := native.InputSources()
	return sources, platformError(err)
}

func snapshot(manual bool) (native.State, error) {
	state, err := native.Snapshot(manual)
	return state, platformError(err)
}

func ValidateSources(c Config) error {
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

type Backend struct {
	pid      uint32
	bundleID string
	valid    func() bool
	config   Config
	manual   bool
	last     native.State
}

func NewManual(c Config) (*Backend, error) {
	state, err := snapshot(true)
	if err != nil {
		return nil, err
	}
	if state.PID != uint32(os.Getpid()) || state.Window == 0 {
		return nil, core.ErrFocusChanged
	}
	return &Backend{config: c, manual: true, pid: state.PID, valid: func() bool { return native.Epoch() == state.Epoch }, last: state}, nil
}

func NewTarget(c Config, valid func() bool) (*Backend, error) {
	if !valid() {
		return nil, core.ErrFocusChanged
	}
	state, err := snapshot(false)
	if err != nil {
		return nil, err
	}
	if !valid() {
		return nil, core.ErrFocusChanged
	}
	return &Backend{pid: state.PID, bundleID: state.BundleID, valid: func() bool { return valid() && native.Epoch() == state.Epoch }, config: c}, nil
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
	if !b.valid() {
		return core.State{}, core.ErrFocusChanged
	}
	state, err := snapshot(b.manual)
	if err != nil {
		return core.State{}, err
	}
	if !b.valid() || state.PID != b.pid || (b.manual && state.Window != b.last.Window) || (!b.manual && state.BundleID != b.bundleID) {
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
	selection := Config{EnglishSourceID: id}
	if mode == core.Chinese {
		selection = Config{ChineseSourceID: id}
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
	if !b.valid() {
		return core.ErrFocusChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch native.Select(b.last, expected.InputSourceID, id, deadline) {
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
