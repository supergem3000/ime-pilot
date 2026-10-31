//go:build windows

package windows

import (
	"path/filepath"

	winapi "golang.org/x/sys/windows"
)

func ProcessPath(pid uint32) (string, error) {
	h, err := winapi.OpenProcess(winapi.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer winapi.CloseHandle(h)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err = winapi.QueryFullProcessImageName(h, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	// 与插件注册的完整路径使用相同的符号链接解析，避免别名造成覆盖遗漏。
	path, err := filepath.EvalSymlinks(winapi.UTF16ToString(buffer[:size]))
	if err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}
