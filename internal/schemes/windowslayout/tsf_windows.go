//go:build windows

package windowslayout

import (
	"context"
	"fmt"
	"strings"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
)

func (b *backend) usesProfiles() bool {
	return strings.HasPrefix(b.config.ChineseLayoutID, "tsf:") || strings.HasPrefix(b.config.EnglishLayoutID, "tsf:")
}

// Desktop activation cannot inspect another application's TSF profile. Do not
// implement core.Reporter or core.Inspector: an accepted call is not verified
// target state, and HKL alone cannot distinguish Doubao from Microsoft Pinyin.
type profileSwitcher struct {
	backend *backend
}

func (s *profileSwitcher) SwitchEn(ctx context.Context) error {
	return s.switchMode(ctx, core.English)
}

func (s *profileSwitcher) SwitchCN(ctx context.Context) error {
	return s.switchMode(ctx, core.Chinese)
}

func (s *profileSwitcher) switchMode(ctx context.Context, mode core.Mode) error {
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.backend.check()
	}
	if err := check(); err != nil {
		return err
	}
	chinese, english, err := configuredLayouts(s.backend.config)
	if err != nil {
		return fmt.Errorf("%w: %v", core.ErrUnsupported, err)
	}
	target := chinese
	if mode == core.English {
		target = english
	}
	handle, err := native.LanguageLayout(target)
	if err != nil {
		return err
	}
	if err := s.backend.requestLayout(ctx, handle); err != nil {
		return err
	}

	// COM calls stay in this process. FORSESSION explicitly applies to the
	// current desktop, including background apps; no target DLL is loaded.
	return native.ActivateDesktopInputSource(target, check)
}
