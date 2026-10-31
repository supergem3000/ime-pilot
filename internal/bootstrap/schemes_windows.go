//go:build windows

package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
	imm "ime-pilot/internal/schemes/windowsimm"
	"ime-pilot/internal/schemes/windowskey"
	"ime-pilot/internal/schemes/windowslayout"
	signals "ime-pilot/internal/signals/windows"
)

func schemes() []registration {
	return []registration{
		{
			Info: imm.Info(), Form: imm.Form, Validate: imm.Validate,
			Create: func(raw json.RawMessage, valid func() bool, window uintptr) (core.Switcher, error) {
				return imm.New(raw, valid, window)
			},
		},
		{
			Info: windowskey.Info(), Form: windowskey.Form, Validate: windowskey.Validate,
			Create: func(raw json.RawMessage, valid func() bool, window uintptr) (core.Switcher, error) {
				return windowskey.New(raw, valid, window)
			},
		},
		{
			Info: windowslayout.Info(), Form: windowslayout.Form, Validate: windowslayout.Validate,
			Create: func(raw json.RawMessage, valid func() bool, window uintptr) (core.Switcher, error) {
				return windowslayout.New(raw, valid, window)
			},
		},
	}
}
func newSignals() core.SignalSource { return &signals.Source{} }
func configPath() (string, error) {
	base, err := native.LocalAppData()
	return filepath.Join(base, "ime-pilot", "config.json"), err
}
func validateApps(c core.Config) error {
	for _, r := range c.Rules {
		if r.Enabled {
			if r.BundleID != "" {
				return fmt.Errorf("Windows 无法启用 macOS 应用规则：%s", r.BundleID)
			}
			info, err := os.Stat(r.AppPath)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("应用文件不可用：%s", r.AppPath)
			}
		}
	}
	return nil
}
