//go:build windows

package rpc

import native "ime-pilot/internal/platform/windows"

func protect(path string, dir bool) error {
	return native.ProtectCurrentUser(path, dir)
}

func replace(from, to string) error {
	return native.ReplaceFile(from, to)
}
