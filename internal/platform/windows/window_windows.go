//go:build windows

// Package windows provides native Windows operations shared by schemes and
// application adapters. It does not decide when or why to switch input modes.
package windows

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

var (
	user32                   = winapi.NewLazySystemDLL("user32.dll")
	imm32                    = winapi.NewLazySystemDLL("imm32.dll")
	getForegroundWindow      = user32.NewProc("GetForegroundWindow")
	getWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	getGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	isChild                  = user32.NewProc("IsChild")
	getKeyboardLayout        = user32.NewProc("GetKeyboardLayout")
	sendMessageTimeout       = user32.NewProc("SendMessageTimeoutW")
	immGetDescription        = imm32.NewProc("ImmGetDescriptionW")
)

type GUIThreadInfo struct {
	Size, Flags                                        uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	CaretRect                                          [4]int32
}

func ForegroundWindow() uintptr {
	hwnd, _, _ := getForegroundWindow.Call()
	return hwnd
}

func WindowThreadProcessID(hwnd uintptr) (thread, pid uint32) {
	value, _, _ := getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return uint32(value), pid
}

func ThreadInfo(thread uint32) (GUIThreadInfo, error) {
	var info GUIThreadInfo
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, err := getGUIThreadInfo.Call(uintptr(thread), uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return info, nativeError("GetGUIThreadInfo", err)
	}
	return info, nil
}

func IsChild(parent, child uintptr) bool {
	result, _, _ := isChild.Call(parent, child)
	return result != 0
}

func KeyboardLayout(thread uint32) uintptr {
	handle, _, _ := getKeyboardLayout.Call(uintptr(thread))
	return handle
}

// ShowMessage displays a native informational dialog before the desktop UI exists.
func ShowMessage(title, message string) error {
	caption, err := winapi.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	text, err := winapi.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	result, _, err := user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), 0x40)
	if result == 0 {
		return nativeError("MessageBoxW", err)
	}
	return nil
}

func nativeError(operation string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return fmt.Errorf("%s rejected the operation (no Windows error code)", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
