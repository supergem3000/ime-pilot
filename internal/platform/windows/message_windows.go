//go:build windows

package windows

import (
	"context"
	"fmt"
	"time"
	"unsafe"
)

// sendBoundedMessage never queues a delayed PostMessage. Each native call is
// capped at 100 ms and at the caller's remaining budget. A timeout cannot undo
// a message the target already accepted; the caller owns result verification.
func sendBoundedMessage(ctx context.Context, hwnd, message, wparam, lparam uintptr) (uintptr, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, fmt.Errorf("native message requires deadline")
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
	okValue, _, err := sendMessageTimeout.Call(hwnd, message, wparam, lparam, 0x0002|0x0020, ms, uintptr(unsafe.Pointer(&result)))
	if okValue == 0 {
		return 0, nativeError("SendMessageTimeoutW", err)
	}
	return result, nil
}

// RequestKeyboardLayout sends WM_INPUTLANGCHANGEREQUEST to the focused control.
// Acceptance is not proof that the target selected the requested layout.
func RequestKeyboardLayout(ctx context.Context, focus, layout uintptr) error {
	if _, err := sendBoundedMessage(ctx, focus, 0x0050, 0, layout); err != nil {
		return err
	}
	return ctx.Err()
}
