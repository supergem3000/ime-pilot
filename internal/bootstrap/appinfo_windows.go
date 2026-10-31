//go:build windows

package bootstrap

import (
	"encoding/base64"
	"path/filepath"
	"strings"

	"ime-pilot/internal/desktop"
	native "ime-pilot/internal/platform/windows"
)

func appInfo(path, bundleID string) desktop.AppInfo {
	if bundleID != "" {
		return desktop.AppInfo{Name: bundleID}
	}
	info := desktop.AppInfo{Name: filepath.Base(path)}
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return info
	}
	info.Name = native.ExecutableName(path)
	if png, err := native.ExecutableIcon(path); err == nil {
		info.Icon = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	return info
}
