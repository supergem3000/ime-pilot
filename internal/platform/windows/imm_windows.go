//go:build windows

package windows

import (
	"context"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

// Legacy WM_IME_CONTROL commands. Support must be verified per input method;
// a successful message/readback alone does not establish actual input behavior.
const (
	IMEGetConversion = 0x0001
	IMESetConversion = 0x0002
	IMEGetOpen       = 0x0005
	IMESetOpen       = 0x0006
)

var (
	immGetDefaultIMEWindow = imm32.NewProc("ImmGetDefaultIMEWnd")
	immGetContext          = imm32.NewProc("ImmGetContext")
	immReleaseContext      = imm32.NewProc("ImmReleaseContext")
	immGetConversionStatus = imm32.NewProc("ImmGetConversionStatus")
)

func DefaultIMEWindow(focus uintptr) uintptr {
	hwnd, _, _ := immGetDefaultIMEWindow.Call(focus)
	return hwnd
}

func IMEDescription(layout uintptr) string {
	var name [256]uint16
	count, _, _ := immGetDescription.Call(layout, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	if count == 0 {
		return ""
	}
	return winapi.UTF16ToString(name[:])
}

func IMEControl(ctx context.Context, hwnd, command, value uintptr) (uintptr, error) {
	return sendBoundedMessage(ctx, hwnd, 0x0283, command, value)
}

// ReadDirectConversion is diagnostic only. A foreign window's HIMC may not
// represent its actual TSF state. Always release it on the calling OS thread.
func ReadDirectConversion(focus uintptr) (handle uintptr, conversion uint32, ok bool) {
	handle, _, _ = immGetContext.Call(focus)
	if handle == 0 {
		return
	}
	defer immReleaseContext.Call(focus, handle)
	var sentence uint32
	result, _, _ := immGetConversionStatus.Call(handle, uintptr(unsafe.Pointer(&conversion)), uintptr(unsafe.Pointer(&sentence)))
	return handle, conversion, result != 0
}
