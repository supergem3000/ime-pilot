//go:build windows

package winime

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"unsafe"

	"ime-pilot/internal/core"
)

// ProbeReport records input-mode capabilities, never editor text.
type ProbeReport struct {
	RootThread, FocusPID                           uint32
	PID                                            uint32
	Thread                                         uint32
	Root, Focus, Layout, IMEWindow, ForeignContext string
	Conversion, Open                               uintptr
	ConversionError, OpenError                     string
	DirectConversion                               uint32
	DirectConversionOK                             bool
	LabIdleAttested                                bool
	MessageSetAttempted                            bool
	MessageSetResult                               uintptr
	AfterConversion, AfterOpen                     uintptr
	Outcome                                        string
}

// LabSetEnglish is deliberately NOT a core.Backend or an automatic feature.
// The caller must have just observed a dedicated, idle test field, and pass
// that attestation plus its freshly captured identity. It tests message support
// only; it is not connected to automatic switching.
func LabSetEnglish(ctx context.Context, expected ProbeReport, idleAttested bool) (ProbeReport, error) {
	r, err := ProbeForeground(ctx, expected.PID)
	if err != nil {
		return r, err
	}
	r.LabIdleAttested = idleAttested
	if !idleAttested {
		return r, fmt.Errorf("laboratory test requires an independently observed idle test field")
	}
	if !sameProbeTarget(r, expected) {
		return r, fmt.Errorf("target changed before laboratory command")
	}
	layout, e := strconv.ParseUint(r.Layout, 16, 64)
	if e != nil || uint16(layout) != langSimplifiedChinese || r.Open != 1 || r.OpenError != "" || r.ConversionError != "" || r.Conversion&conversionNative == 0 {
		return r, fmt.Errorf("laboratory test requires readable, open Chinese mode")
	}
	imeWindow, e := strconv.ParseUint(r.IMEWindow, 16, 64)
	if e != nil || imeWindow == 0 {
		return r, fmt.Errorf("no IME window")
	}
	if err = ctx.Err(); err != nil {
		return r, err
	}
	if err = checkProbeTarget(r); err != nil {
		return r, err
	}
	// One bounded command, no open/close fallback and no simulated keys.
	r.MessageSetAttempted = true
	r.MessageSetResult, err = imeControl(ctx, uintptr(imeWindow), imeSetConversion, r.Conversion&^(conversionNative|conversionFullShape|conversionSymbol))
	if err != nil {
		return r, err
	}
	after, err := ProbeForeground(ctx, expected.PID)
	if err != nil {
		return r, err
	}
	r.AfterConversion = after.Conversion
	r.AfterOpen = after.Open
	if !sameProbeTarget(r, after) {
		return r, fmt.Errorf("target changed after laboratory command; result unconfirmed")
	}
	if after.ConversionError != "" || after.OpenError != "" || after.Conversion&(conversionNative|conversionFullShape|conversionSymbol) != 0 || after.Open != r.Open {
		return r, fmt.Errorf("message change could not be verified")
	}
	r.Outcome = "lab_mode_readback_only_actual_input_requires_manual_verification"
	return r, nil
}

func sameProbeTarget(a, b ProbeReport) bool {
	return a.PID == b.PID && a.RootThread == b.RootThread && a.FocusPID == b.FocusPID && a.Thread == b.Thread && a.Root == b.Root && a.Focus == b.Focus && a.Layout == b.Layout && a.IMEWindow == b.IMEWindow
}

// ProbeForeground is a read-only experiment. pid is required so that the
// diagnostics cannot silently retarget an unrelated foreground application.
func ProbeForeground(ctx context.Context, pid uint32) (ProbeReport, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r := ProbeReport{Outcome: "read_only"}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if pid == 0 {
		return r, fmt.Errorf("explicit target PID is required")
	}
	root, _, _ := getForegroundWindow.Call()
	var actual uint32
	thread, _, _ := getWindowThreadProcessID.Call(root, uintptr(unsafe.Pointer(&actual)))
	if thread == 0 || actual != pid {
		return r, fmt.Errorf("foreground PID %d does not match target %d", actual, pid)
	}
	r.PID = pid
	r.RootThread = uint32(thread)
	r.Root = fmt.Sprintf("%X", root)
	info := guiThreadInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, e := getGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return r, nativeError("GetGUIThreadInfo", e)
	}
	if info.Focus == 0 {
		return r, fmt.Errorf("no focused native control")
	}
	var focusPID uint32
	focusThread, _, _ := getWindowThreadProcessID.Call(info.Focus, uintptr(unsafe.Pointer(&focusPID)))
	r.Focus = fmt.Sprintf("%X", info.Focus)
	r.FocusPID = focusPID
	if focusPID != pid || focusThread == 0 {
		return r, fmt.Errorf("focus PID %d is outside selected target PID %d", focusPID, pid)
	}
	layout, _, _ := getKeyboardLayout.Call(focusThread)
	imeWindow, _, _ := immGetDefaultIMEWindow.Call(info.Focus)
	r.Thread = uint32(focusThread)
	r.Layout = fmt.Sprintf("%X", layout)
	r.IMEWindow = fmt.Sprintf("%X", imeWindow)
	if imeWindow != 0 {
		var err error
		r.Conversion, err = imeControl(ctx, imeWindow, imeGetConversion, 0)
		if err != nil {
			r.ConversionError = err.Error()
		}
		r.Open, err = imeControl(ctx, imeWindow, imeGetOpen, 0)
		if err != nil {
			r.OpenError = err.Error()
		}
	}
	himc, _, _ := immGetContext.Call(info.Focus)
	r.ForeignContext = fmt.Sprintf("%X", himc)
	if himc != 0 {
		defer immReleaseContext.Call(info.Focus, himc)
		var sentence uint32
		ok, _, _ = immGetConversionStatus.Call(himc, uintptr(unsafe.Pointer(&r.DirectConversion)), uintptr(unsafe.Pointer(&sentence)))
		r.DirectConversionOK = ok != 0

	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if err := checkProbeTarget(r); err != nil {
		return r, err
	}
	return r, nil
}

// Recheck without sending any messages. This narrows, but cannot atomically
// eliminate, the race between foreground validation and a native message.
func checkProbeTarget(r ProbeReport) error {
	root, _, _ := getForegroundWindow.Call()
	var pid uint32
	thread, _, _ := getWindowThreadProcessID.Call(root, uintptr(unsafe.Pointer(&pid)))
	if pid != r.PID || uint32(thread) != r.RootThread || fmt.Sprintf("%X", root) != r.Root {
		return core.ErrFocusChanged
	}
	info := guiThreadInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, _ := getGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&info)))
	if ok == 0 || fmt.Sprintf("%X", info.Focus) != r.Focus {
		return core.ErrFocusChanged
	}
	var focusPID uint32
	focusThread, _, _ := getWindowThreadProcessID.Call(info.Focus, uintptr(unsafe.Pointer(&focusPID)))
	layout, _, _ := getKeyboardLayout.Call(focusThread)
	imeWindow, _, _ := immGetDefaultIMEWindow.Call(info.Focus)
	if focusPID != r.FocusPID || uint32(focusThread) != r.Thread || fmt.Sprintf("%X", layout) != r.Layout || fmt.Sprintf("%X", imeWindow) != r.IMEWindow {
		return core.ErrFocusChanged
	}
	return nil
}
