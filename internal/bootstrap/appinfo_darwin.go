//go:build darwin && cgo

package bootstrap

import (
	"path/filepath"

	"ime-pilot/internal/core"
	"ime-pilot/internal/desktop"
	native "ime-pilot/internal/platform/darwin"
)

func appInfo(path, bundleID string) desktop.AppInfo {
	if bundleID == "" {
		return desktop.AppInfo{Name: filepath.Base(path)}
	}
	info := desktop.AppInfo{Name: bundleID}
	if !core.ValidBundleID(bundleID) {
		return info
	}
	if app, err := native.ApplicationInfo(bundleID); err == nil {
		info.Name, info.Icon = app.Name, app.Icon
	}
	return info
}
