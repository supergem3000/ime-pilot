//go:build windows

package windows

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

var (
	extractAppIcon = winapi.NewLazySystemDLL("shell32.dll").NewProc("SHDefExtractIconW")
	gdi32          = winapi.NewLazySystemDLL("gdi32.dll")
	createMemoryDC = gdi32.NewProc("CreateCompatibleDC")
	deleteDC       = gdi32.NewProc("DeleteDC")
	createDIB      = gdi32.NewProc("CreateDIBSection")
	selectObject   = gdi32.NewProc("SelectObject")
	deleteObject   = gdi32.NewProc("DeleteObject")
	gdiFlush       = gdi32.NewProc("GdiFlush")
	drawIcon       = user32.NewProc("DrawIconEx")
	destroyIcon    = user32.NewProc("DestroyIcon")
)

const appIconSize = 64

// ExecutableIcon reads the first application icon as a transparent PNG.
// Extract at 64 pixels so the 28-pixel UI remains sharp on high-DPI screens.
func ExecutableIcon(path string) ([]byte, error) {
	file, err := winapi.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// A memory DC belongs to its creating OS thread; keep drawing and cleanup
	// on that thread even when the Go scheduler moves other work around.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var icon uintptr
	result, _, _ := extractAppIcon.Call(uintptr(unsafe.Pointer(file)), 0, 0,
		uintptr(unsafe.Pointer(&icon)), 0, appIconSize)
	if icon != 0 {
		defer destroyIcon.Call(icon)
	}
	if result != 0 || icon == 0 {
		return nil, fmt.Errorf("读取应用图标失败：HRESULT=0x%08X", uint32(result))
	}

	// Render against black and white to recover transparency for both alpha
	// icons and older mask-based icons, without introducing a black background.
	black, err := renderAppIcon(icon, 0)
	if err != nil {
		return nil, err
	}
	white, err := renderAppIcon(icon, 255)
	if err != nil {
		return nil, err
	}
	canvas := image.NewRGBA(image.Rect(0, 0, appIconSize, appIconSize))
	for offset := 0; offset < len(black); offset += 4 {
		alpha := byte(max(0, min(255, 255-int(white[offset])+int(black[offset]))))
		// DIB pixels are BGRA; image.RGBA expects premultiplied RGBA.
		canvas.Pix[offset] = min(black[offset+2], alpha)
		canvas.Pix[offset+1] = min(black[offset+1], alpha)
		canvas.Pix[offset+2] = min(black[offset], alpha)
		canvas.Pix[offset+3] = alpha
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// The first 40 bytes match BITMAPINFOHEADER. A negative height requests
// top-down rows, avoiding a second image flip when encoding the PNG.
type iconBitmapInfo struct {
	Size          uint32
	Width, Height int32
	Planes, Bits  uint16
	Compression   uint32
	ImageSize     uint32
	XPels, YPels  int32
	Colors, Used  uint32
	ColorTable    uint32
}

func renderAppIcon(icon uintptr, background byte) ([]byte, error) {
	dc, _, err := createMemoryDC.Call(0)
	if dc == 0 {
		return nil, nativeError("CreateCompatibleDC", err)
	}
	defer deleteDC.Call(dc)
	info := iconBitmapInfo{Size: 40, Width: appIconSize, Height: -appIconSize, Planes: 1, Bits: 32}
	var bits unsafe.Pointer
	bitmap, _, err := createDIB.Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 {
		return nil, nativeError("CreateDIBSection", err)
	}
	defer deleteObject.Call(bitmap)
	previous, _, err := selectObject.Call(dc, bitmap)
	if previous == 0 || previous == ^uintptr(0) {
		return nil, nativeError("SelectObject", err)
	}
	defer selectObject.Call(dc, previous)
	pixels := unsafe.Slice((*byte)(bits), appIconSize*appIconSize*4)
	for i := range pixels {
		pixels[i] = background
	}
	ok, _, err := drawIcon.Call(dc, 0, 0, icon, appIconSize, appIconSize, 0, 0, 3) // DI_NORMAL
	if ok == 0 {
		return nil, nativeError("DrawIconEx", err)
	}
	// Flush GDI writes before reading the bitmap memory from Go.
	gdiFlush.Call()
	return append([]byte(nil), pixels...), nil
}
