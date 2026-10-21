// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"strings"
)

func hex(c string) color.RGBA {
	var r, g, b uint8
	_, _ = fmt.Sscanf(c, "#%02x%02x%02x", &r, &g, &b)
	return color.RGBA{r, g, b, 255}
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	_ = enc.Encode(&buf, img)
	return buf.Bytes()
}

func lerp(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 255}
}

// brandPNG draws a small generated logo: a gradient field (or transparent
// background when bg is nil) with a disc and a few bars standing in for the
// product name.
func brandPNG(w, h int, bg *color.RGBA, fg color.RGBA, accent color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if bg != nil {
		for y := 0; y < h; y++ {
			c := lerp(*bg, color.RGBA{bg.R / 2, bg.G / 2, bg.B / 2, 255}, float64(y)/float64(h))
			for x := 0; x < w; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	d := h * 7 / 10
	cx, cy := h/2, h/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= d*d/4 {
				img.SetRGBA(x, y, fg)
			}
			if dx*dx+dy*dy <= d*d/16 {
				img.SetRGBA(x, y, accent)
			}
		}
	}
	if w > h*2 { // wide logo: "text" bars
		x0 := h + h/6
		for i, bw := range []int{w/2 - h/2, w/3 - h/3, w/2 - h/2} {
			y0 := h/4 + i*h/4
			for y := y0; y < y0+h/8 && y < h; y++ {
				for x := x0; x < x0+bw && x < w; x++ {
					img.SetRGBA(x, y, fg)
				}
			}
		}
	}
	return encodePNG(img)
}

// backgroundPNG is the login background: a soft diagonal gradient.
func backgroundPNG(w, h int, a, b color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, lerp(a, b, float64(x+y)/float64(w+h)))
		}
	}
	return encodePNG(img)
}

// screenshotPNG is a small fake application window, deterministic per seed.
func screenshotPNG(seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 7))
	const w, h = 320, 200
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	fill(0, 0, w, h, color.RGBA{245, 246, 248, 255})
	fill(0, 0, w, 24, color.RGBA{40, 60, 90, 255})
	fill(0, 24, 70, h, color.RGBA{225, 229, 236, 255})
	for i := 0; i < 5; i++ {
		fill(8, 36+i*22, 62, 46+i*22, color.RGBA{170, 178, 192, 255})
	}
	fill(90, 44, 300, 124, color.RGBA{255, 255, 255, 255})
	fill(100, 60, 100+r.IntN(120)+60, 68, color.RGBA{200, 205, 214, 255})
	fill(100, 80, 100+r.IntN(120)+60, 88, color.RGBA{200, 205, 214, 255})
	fill(100, 100, 100+r.IntN(80)+40, 108, color.RGBA{200, 205, 214, 255})
	fill(90, 140, 300, 176, color.RGBA{253, 236, 234, 255}) // error banner
	fill(98, 152, 98+r.IntN(100)+80, 160, color.RGBA{190, 60, 50, 255})
	return encodePNG(img)
}

// logText is a plausible short log excerpt, deterministic per seed.
func logText(seed uint64, service string) []byte {
	r := rand.New(rand.NewPCG(seed, 11))
	var b strings.Builder
	levels := []string{"INFO", "INFO", "WARN", "ERROR"}
	msgs := []string{"connection established", "retrying request (attempt %d)", "certificate chain incomplete", "timeout after %d ms", "session refreshed"}
	for i := 0; i < 12; i++ {
		lv := levels[r.IntN(len(levels))]
		m := msgs[r.IntN(len(msgs))]
		if strings.Contains(m, "%d") {
			m = fmt.Sprintf(m, 1+r.IntN(30)*100)
		}
		fmt.Fprintf(&b, "2026-10-%02dT%02d:%02d:%02dZ %-5s %s: %s\n", 1+r.IntN(20), r.IntN(24), r.IntN(60), r.IntN(60), lv, service, m)
	}
	return []byte(b.String())
}
