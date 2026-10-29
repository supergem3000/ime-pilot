package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// A small PNG keyboard mark works with the Windows tray's bitmap decoder.
func appIcon() []byte {
	icon := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	blue := image.NewUniform(color.NRGBA{R: 35, G: 102, B: 205, A: 255})
	white := image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	draw.Draw(icon, image.Rect(3, 3, 61, 61), blue, image.Point{}, draw.Src)
	for row := 0; row < 2; row++ {
		for column := 0; column < 4; column++ {
			x, y := 11+column*11, 17+row*12
			draw.Draw(icon, image.Rect(x, y, x+8, y+8), white, image.Point{}, draw.Src)
		}
	}
	draw.Draw(icon, image.Rect(15, 42, 49, 48), white, image.Point{}, draw.Src)
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, icon) // bytes.Buffer writes cannot fail.
	return encoded.Bytes()
}
