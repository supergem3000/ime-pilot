//go:build windows

package winime

import (
	"context"
	"fmt"
	"time"
	"unsafe"
)

// Legacy WM_IME_CONTROL commands used by the scoped desktop backend and
// diagnostics. Current Microsoft documentation does not list these commands;
// support is qualified by real input tests, not assumed for every IME.
const (
	imeGetConversion = 0x0001
	imeSetConversion = 0x0002
	imeGetOpen       = 0x0005
	imeSetOpen       = 0x0006
)

var (
	immGetDefaultIMEWindow = imm32.NewProc("ImmGetDefaultIMEWnd")
	sendMessageTimeout     = user32.NewProc("SendMessageTimeoutW")
)

func imeControl(ctx context.Context, hwnd, command, value uintptr) (uintptr, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, fmt.Errorf("probe requires deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}
	ms := uintptr((remaining + time.Millisecond - 1) / time.Millisecond)
	if ms > 100 {
		ms = 100
	}
	var result uintptr
	okValue, _, err := sendMessageTimeout.Call(hwnd, 0x0283, command, value, 0x0002|0x0020, ms, uintptr(unsafe.Pointer(&result)))
	if okValue == 0 {
		return 0, nativeError("SendMessageTimeoutW", err)
	}
	return result, nil
}
