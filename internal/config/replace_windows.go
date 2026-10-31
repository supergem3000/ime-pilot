//go:build windows

package config

import native "ime-pilot/internal/platform/windows"

func replaceFile(from, to string) error {
	return native.ReplaceFile(from, to)
}
