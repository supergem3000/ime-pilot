//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"ime-pilot/internal/desktop"
	"ime-pilot/internal/lifecycle"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		text, _ := windows.UTF16PtrFromString(err.Error())
		title, _ := windows.UTF16PtrFromString("ime-pilot")
		_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(
			0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x40)
	}
}

func run() error {
	release, err := lifecycle.AcquireInstance()
	if err != nil {
		return err
	}
	defer release()
	return desktop.Run()
}
