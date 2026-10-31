//go:build windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
	"ime-pilot/internal/automation"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	winime "ime-pilot/internal/platform/windows"
	localrpc "ime-pilot/internal/rpc"
)

const platformName = "windows"

type platformMonitor = winime.Monitor
type platformApp = winime.App

var runningApps = winime.RunningApps
var startMonitor = winime.StartMonitor
var resolveEvent = winime.ResolveEvent

func newManualBackend(window uintptr, _ *config.Store) (core.Backend, error) {
	return winime.New(window)
}
func newTargetBackend(t automation.Target, _ *config.Store) core.Backend { return winime.NewTarget(t) }
func captureBinding(m *platformMonitor, e localrpc.Evidence) (automation.Target, uint64, error) {
	window, err := strconv.ParseUint(e.WindowID, 10, 64)
	if err != nil {
		return automation.Target{}, 0, err
	}
	return m.CaptureBinding(e.PID, uintptr(window))
}

func configPath() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ime-pilot", "config.json"), nil
}

func validatePlatformConfig(c config.Config) error {
	for _, r := range c.Rules {
		if !r.Enabled {
			continue
		}
		if r.BundleID != "" {
			return fmt.Errorf("Windows 无法启用 macOS 应用规则：%s", r.Name)
		}
		info, err := os.Stat(r.AppPath)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("应用文件不可用：%s", r.AppPath)
		}
	}
	return nil
}

func unsupportedMessage() string {
	return "当前环境暂不可控。请确认选中微软拼音；详细信息："
}
func switchMessage(mode core.Mode) string {
	return "接口已查询确认" + modeName(mode) + "模式；请在测试框输入验证实际效果。"
}
