//go:build windows

package windows

// EnablePerMonitorDPI must run before creating any windows, including startup
// error dialogs. Per-monitor V2 lets Windows scale native dialog controls and
// fonts directly instead of stretching a low-resolution bitmap.
func EnablePerMonitorDPI() error {
	const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	// Packaged builds declare this in the manifest before Go starts. Avoid
	// setting it twice; Windows rejects changing a manifest-defined context.
	current, _, _ := user32.NewProc("GetThreadDpiAwarenessContext").Call()
	equal, _, _ := user32.NewProc("AreDpiAwarenessContextsEqual").Call(current, perMonitorAwareV2)
	if equal != 0 {
		return nil
	}
	// Keep direct go build / go run usable without the packaged manifest.
	ok, _, err := user32.NewProc("SetProcessDpiAwarenessContext").Call(perMonitorAwareV2)
	if ok == 0 {
		return nativeError("SetProcessDpiAwarenessContext", err)
	}
	return nil
}
