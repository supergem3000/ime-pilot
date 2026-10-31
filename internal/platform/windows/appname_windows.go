//go:build windows

package windows

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

// ExecutableName reads a friendly name from the executable's version resource,
// not its window title (which may contain document names or typed content).
// Missing resources fall back to the file name without changing app identity.
func ExecutableName(path string) string {
	fallback := filepath.Base(path)
	size, err := winapi.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 || size > 8*1024*1024 {
		return fallback
	}
	block := make([]byte, size)
	if err := winapi.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&block[0])); err != nil {
		return fallback
	}
	defer runtime.KeepAlive(block)

	type translation struct{ Language, CodePage uint16 }
	var languages []translation
	if ptr, size := versionValue(block, `\VarFileInfo\Translation`, 1); ptr != nil {
		languages = append(languages, unsafe.Slice((*translation)(ptr), int(size)/4)...)
	}
	// Some resources omit Translation but contain a conventional English table.
	languages = append(languages, translation{0x0409, 0x04b0}, translation{0x0409, 0x04e4})
	for _, field := range []string{"FileDescription", "ProductName"} {
		for _, language := range languages {
			key := fmt.Sprintf(`\StringFileInfo\%04X%04X\%s`, language.Language, language.CodePage, field)
			ptr, count := versionValue(block, key, 2)
			if ptr == nil {
				continue
			}
			name := strings.TrimSpace(winapi.UTF16ToString(unsafe.Slice((*uint16)(ptr), int(count))))
			if name != "" && len(name) <= 160 {
				return name
			}
		}
	}
	return fallback
}

// VerQueryValue returns pointers into block. Bound both byte and UTF-16 values
// before building a Go slice; retain the owning buffer until decoding finishes.
func versionValue(block []byte, key string, unit uint64) (unsafe.Pointer, uint32) {
	var value unsafe.Pointer
	var count uint32
	if err := winapi.VerQueryValue(unsafe.Pointer(&block[0]), key, unsafe.Pointer(&value), &count); err != nil || value == nil || count == 0 {
		return nil, 0
	}
	base, address := uintptr(unsafe.Pointer(&block[0])), uintptr(value)
	if address < base || address-base >= uintptr(len(block)) || uint64(count)*unit > uint64(len(block))-uint64(address-base) {
		return nil, 0
	}
	return value, count
}
