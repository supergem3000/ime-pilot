//go:build windows

package main

import (
	"fmt"
	"os"

	"ime-pilot/internal/bootstrap"
	"ime-pilot/internal/lifecycle"
	native "ime-pilot/internal/platform/windows"
)

func main() {
	// Duplicate-instance errors can show a dialog before Wails starts.
	// Set DPI awareness first so these dialogs render at the monitor's scale.
	if err := native.EnablePerMonitorDPI(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = native.ShowMessage("IME Pilot", err.Error())
	}
}

func run() error {
	release, err := lifecycle.AcquireInstance()
	if err != nil {
		return err
	}
	defer release()
	return bootstrap.Run()
}
