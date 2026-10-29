//go:build !windows

package rpc

import "os"

func protect(path string, dir bool) error {
	mode := os.FileMode(0600)
	if dir {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
func replace(from, to string) error { return os.Rename(from, to) }
