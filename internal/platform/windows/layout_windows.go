//go:build windows

package windows

import (
	"fmt"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

var (
	getKeyboardLayoutList = user32.NewProc("GetKeyboardLayoutList")
	getLocaleInfo         = winapi.NewLazySystemDLL("kernel32.dll").NewProc("GetLocaleInfoW")
)

// InputSource describes a loaded keyboard layout or an enabled TSF profile.
// The COM profile stays private; callers select entries returned by enumeration.
type InputSource struct {
	Handle   uintptr
	ID, Name string
	Language uint16
	profile  inputProfile
}

// HKLs may be sign-extended on 64-bit Windows; compare their 32-bit identity,
// but always send the actual handle returned by the current enumeration.
func LayoutID(handle uintptr) string { return fmt.Sprintf("%08X", uint32(handle)) }

func KeyboardLayouts() ([]InputSource, error) {
	// Leave headroom so layouts added during enumeration are not silently dropped.
	for attempt := 0; attempt < 3; attempt++ {
		count, _, err := getKeyboardLayoutList.Call(0, 0)
		if count == 0 || count > 256 {
			return nil, fmt.Errorf("无法枚举键盘布局：%v", err)
		}
		handles := make([]uintptr, count+8)
		n, _, err := getKeyboardLayoutList.Call(uintptr(len(handles)), uintptr(unsafe.Pointer(&handles[0])))
		if n == 0 {
			return nil, fmt.Errorf("无法读取键盘布局：%v", err)
		}
		if n >= uintptr(len(handles)) {
			continue
		}
		result := make([]InputSource, 0, n)
		for _, handle := range handles[:n] {
			name := make([]uint16, 256)
			length, _, _ := immGetDescription.Call(handle, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
			if length == 0 {
				// LOCALE_SLOCALIZEDDISPLAYNAME. The HKL suffix distinguishes variants.
				getLocaleInfo.Call(uintptr(uint16(handle)), 0x00000002, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
			}
			label := winapi.UTF16ToString(name)
			if label == "" {
				label = "键盘布局"
			}
			result = append(result, InputSource{Handle: handle, ID: LayoutID(handle), Name: label, Language: uint16(handle) & 0x3ff})
		}
		return result, nil
	}
	return nil, fmt.Errorf("键盘布局列表正在变化，请刷新后重试")
}

func (s InputSource) IsTextService() bool {
	return s.profile.Kind == profileTextService
}
