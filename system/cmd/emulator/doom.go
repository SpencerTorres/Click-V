package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

const (
	DOOMGENERIC_RESX = 320
	DOOMGENERIC_RESY = 200
)

func SaveDoomFrame(framebuffer []byte, filename string) error {
	width := DOOMGENERIC_RESX
	height := DOOMGENERIC_RESY

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*width + x) * 4

			if offset+3 >= len(framebuffer) {
				continue
			}

			b := framebuffer[offset+0]
			g := framebuffer[offset+1]
			r := framebuffer[offset+2]
			a := uint8(0xFF)

			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	return png.Encode(file, img)
}
