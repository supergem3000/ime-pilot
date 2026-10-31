//go:build windows

package windows

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

var ErrKeysHeld = errors.New("键盘或鼠标按键尚未松开")

var (
	getAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	sendInput        = user32.NewProc("SendInput")
)

// KEYBDINPUT uses pointer alignment for ExtraInfo. INPUT's union must also fit
// MOUSEINPUT, which is eight bytes larger. This gives 28 bytes on 32-bit Windows
// and 40 bytes on 64-bit Windows, including the padding after Type.
// https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-input
type keyboardInput struct {
	VirtualKey, ScanCode uint16
	Flags, Time          uint32
	ExtraInfo            uintptr
}

type keyboardEvent struct {
	Type     uint32
	Keyboard keyboardInput
	_        [8]byte
}

const keyboardEventSize = 16 + 3*unsafe.Sizeof(uintptr(0))

// Compile-time ABI checks catch an incorrect union size or alignment.
var (
	_ [keyboardEventSize - unsafe.Sizeof(keyboardEvent{})]byte
	_ [unsafe.Sizeof(keyboardEvent{}) - keyboardEventSize]byte
	_ [unsafe.Sizeof(uintptr(0)) - unsafe.Offsetof(keyboardEvent{}.Keyboard)]byte
	_ [unsafe.Offsetof(keyboardEvent{}.Keyboard) - unsafe.Sizeof(uintptr(0))]byte
)

// AnyKeyDown includes mouse buttons and modifiers. Only the current pressed
// bit is read; this is not a keyboard hook and does not record key history.
func AnyKeyDown() bool {
	for key := uintptr(1); key < 255; key++ {
		state, _, _ := getAsyncKeyState.Call(key)
		if state&0x8000 != 0 {
			return true
		}
	}
	return false
}

// TapKey sends one down/up pair into the foreground input stream. The caller's
// guard rechecks focus and cancellation immediately before SendInput. Windows
// cannot atomically bind this operation to an HWND; the caller must also check
// the target afterwards. It never retries a toggle on failure.
func TapKey(virtualKey, scanCode uint16, guard func() error) error {
	if virtualKey == 0 || virtualKey > 254 || guard == nil {
		return fmt.Errorf("invalid guarded key request")
	}
	if AnyKeyDown() {
		return ErrKeysHeld
	}
	if err := guard(); err != nil {
		return err
	}
	inputs := [2]keyboardEvent{
		{Type: 1, Keyboard: keyboardInput{VirtualKey: virtualKey, ScanCode: scanCode}},
		{Type: 1, Keyboard: keyboardInput{VirtualKey: virtualKey, ScanCode: scanCode, Flags: 2}},
	}
	count, _, err := sendInput.Call(2, uintptr(unsafe.Pointer(&inputs[0])), keyboardEventSize)
	runtime.KeepAlive(inputs)
	if count == 2 {
		return nil
	}
	if count == 1 {
		// If only key-down was inserted, release that synthetic key even after
		// cancellation. This is cleanup, never a second down/up toggle attempt.
		released, _, releaseErr := sendInput.Call(1, uintptr(unsafe.Pointer(&inputs[1])), keyboardEventSize)
		runtime.KeepAlive(inputs)
		if released != 1 {
			return fmt.Errorf("SendInput 只发送了按下事件，释放失败：%w", nativeError("SendInput key-up", releaseErr))
		}
	}
	return fmt.Errorf("SendInput 仅发送 %d/2 个事件：%w", count, nativeError("SendInput", err))
}
