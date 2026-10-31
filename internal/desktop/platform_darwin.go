//go:build darwin

package desktop

import (
	"fmt"
	"path/filepath"

	"ime-pilot/internal/automation"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	"ime-pilot/internal/lifecycle"
	macime "ime-pilot/internal/platform/macos"
	localrpc "ime-pilot/internal/rpc"
)

const platformName = "darwin"

type platformMonitor = macime.Monitor
type platformApp = macime.App

var runningApps = macime.RunningApps
var startMonitor = macime.StartMonitor
var resolveEvent = macime.ResolveEvent

func newManualBackend(_ uintptr, store *config.Store) (core.Backend, error) {
	c, err := store.Snapshot()
	if err != nil {
		return nil, err
	}
	return macime.NewManual(c.MacOS)
}
func newTargetBackend(t automation.Target, store *config.Store) core.Backend {
	c, _ := store.Snapshot()
	return macime.NewTarget(t, c.MacOS)
}
func captureBinding(m *platformMonitor, e localrpc.Evidence) (automation.Target, uint64, error) {
	return m.CaptureBinding(e.PID)
}
func configPath() (string, error) {
	dir, err := lifecycle.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}
func validatePlatformConfig(c config.Config) error {
	for _, r := range c.Rules {
		if r.Enabled && r.BundleID == "" {
			return fmt.Errorf("macOS 无法启用 Windows 应用规则：%s", r.Name)
		}
	}
	return macime.ValidateSources(c.MacOS)
}
func (s *InputService) ListInputSources() ([]macime.InputSource, error) { return macime.InputSources() }
func unsupportedMessage() string {
	return "请先配置已启用的中文和英文输入源；详细信息："
}
func switchMessage(mode core.Mode) string {
	return "已确认选中配置的" + modeName(mode) + "输入源；输入法内部中英文状态请在测试框核对。"
}
