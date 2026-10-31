//go:build darwin && cgo

package bootstrap

import (
	"encoding/json"
	"fmt"
	"ime-pilot/internal/core"
	"ime-pilot/internal/lifecycle"
	source "ime-pilot/internal/schemes/macosinputsource"
	signals "ime-pilot/internal/signals/macos"
	"path/filepath"
)

func schemes() []registration {
	return []registration{{Info: source.Info(), Form: source.Form, Validate: source.Validate, Create: func(raw json.RawMessage, valid func() bool, window uintptr) (core.Switcher, error) {
		return source.New(raw, valid, window)
	}}}
}
func newSignals() core.SignalSource { return &signals.Source{} }
func configPath() (string, error) {
	dir, err := lifecycle.DataDir()
	return filepath.Join(dir, "config.json"), err
}
func validateApps(c core.Config) error {
	for _, r := range c.Rules {
		if r.Enabled && r.BundleID == "" {
			return fmt.Errorf("macOS 无法启用 Windows 应用规则：%s", r.AppPath)
		}
	}
	return nil
}
