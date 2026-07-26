//go:build ignore

// Command generate draws the app and tray icons.
//
// The icons are generated rather than committed as opaque binaries so that a
// change to the palette is a readable diff, and so the whole set can be redrawn
// at any size without a design tool. Run it with:
//
//	go run build/icons/generate.go
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// palette holds the one colour per state that the tray needs. Each state is
// distinguishable by shape as well as colour, because a colour-only signal is
// invisible to a good number of people.
var states = map[string]struct {
	fill  color.NRGBA
	glyph glyph
}{
	"ok":      {color.NRGBA{0x22, 0x9E, 0x63, 0xFF}, glyphTick},
	"working": {color.NRGBA{0x2C, 0x7B, 0xE0, 0xFF}, glyphArrow},
	"idle":    {color.NRGBA{0x6B, 0x72, 0x80, 0xFF}, glyphShield},
	"warning": {color.NRGBA{0xD9, 0x7A, 0x0B, 0xFF}, glyphBang},
}

type glyph int

const (
	glyphShield glyph = iota
	glyphTick
	glyphArrow
	glyphBang
	glyphShieldTick
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	trayDir := filepath.Join(root, "build", "tray")
	if err := os.MkdirAll(trayDir, 0o755); err != nil {
		fail(err)
	}

	for name, state := range states {
		// 32 pixels covers both the 16-pixel tray and the 24-pixel high-DPI tray
		// without a second source.
		img := drawIcon(32, state.fill, state.glyph)
		writePNG(filepath.Join(trayDir, name+".png"), img)
		writeICO(filepath.Join(trayDir, name+".ico"), img)
	}

	// The application icon is the "protected" mark at a size the installer and
	// the window frame can use.
	appIcon := drawIcon(256, states["ok"].fill, glyphShieldTick)
	writePNG(filepath.Join(root, "build", "appicon.png"), appIcon)
	writeICO(filepath.Join(root, "build", "windows", "icon.ico"), appIcon)
	fmt.Println("icons written")
}

// drawIcon paints a rounded square with a glyph on it.
func drawIcon(size int, fill color.NRGBA, g glyph) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	radius := float64(size) * 0.28
	inset := float64(size) * 0.06

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if coverage := roundedRectCoverage(float64(x), float64(y), inset, float64(size)-inset, radius); coverage > 0 {
				img.SetNRGBA(x, y, alpha(fill, coverage))
			}
		}
	}
	drawGlyph(img, g)
	return img
}

// roundedRectCoverage antialiases the rounded square by sampling each pixel.
func roundedRectCoverage(x, y, min, max, radius float64) float64 {
	const samples = 3
	hit := 0.0
	for sy := 0; sy < samples; sy++ {
		for sx := 0; sx < samples; sx++ {
			px := x + (float64(sx)+0.5)/samples
			py := y + (float64(sy)+0.5)/samples
			if insideRoundedRect(px, py, min, max, radius) {
				hit++
			}
		}
	}
	return hit / (samples * samples)
}

func insideRoundedRect(x, y, min, max, radius float64) bool {
	if x < min || x > max || y < min || y > max {
		return false
	}
	// Only the corners need the circle test.
	cx := math.Min(math.Max(x, min+radius), max-radius)
	cy := math.Min(math.Max(y, min+radius), max-radius)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}

// drawGlyph stamps a white mark in the middle of the icon.
func drawGlyph(img *image.NRGBA, g glyph) {
	size := img.Bounds().Dx()
	white := color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
	unit := float64(size) / 32

	stroke := func(x0, y0, x1, y1 float64) {
		width := math.Max(2*unit, 2)
		length := math.Hypot(x1-x0, y1-y0)
		steps := int(length * 4)
		for i := 0; i <= steps; i++ {
			t := float64(i) / float64(max(steps, 1))
			cx := x0 + (x1-x0)*t
			cy := y0 + (y1-y0)*t
			disc(img, cx, cy, width/2, white)
		}
	}

	switch g {
	case glyphTick:
		stroke(10*unit, 17*unit, 14*unit, 21*unit)
		stroke(14*unit, 21*unit, 22*unit, 11*unit)
	case glyphArrow:
		// An upward arrow: data leaving the machine.
		stroke(16*unit, 22*unit, 16*unit, 10*unit)
		stroke(16*unit, 10*unit, 11*unit, 15*unit)
		stroke(16*unit, 10*unit, 21*unit, 15*unit)
	case glyphBang:
		stroke(16*unit, 9*unit, 16*unit, 19*unit)
		disc(img, 16*unit, 23*unit, 1.6*unit, white)
	case glyphShield:
		shield(img, unit, white)
	case glyphShieldTick:
		// At large sizes the shield can hold a tick, which says "protected"
		// rather than merely "security software".
		shield(img, unit, white)
		green := states["ok"].fill
		strokeIn := func(x0, y0, x1, y1, width float64) {
			length := math.Hypot(x1-x0, y1-y0)
			steps := int(length * 4)
			for i := 0; i <= steps; i++ {
				t := float64(i) / float64(max(steps, 1))
				disc(img, x0+(x1-x0)*t, y0+(y1-y0)*t, width/2, green)
			}
		}
		strokeIn(13*unit, 15.5*unit, 15.4*unit, 18*unit, 2.2*unit)
		strokeIn(15.4*unit, 18*unit, 19.5*unit, 12*unit, 2.2*unit)
	}
}

// shield fills a shield shape: straight shoulders, sides that stay parallel for
// most of the height, then a taper to a point. The straight section is what makes
// it read as a shield at 16 pixels instead of as a triangle.
func shield(img *image.NRGBA, unit float64, c color.NRGBA) {
	top, bottom := 7.5*unit, 24.5*unit
	left, right := 9.5*unit, 22.5*unit
	centre := (left + right) / 2
	halfWidth := (right - left) / 2

	for y := top; y <= bottom; y += 0.25 {
		t := (y - top) / (bottom - top)
		// Full width down to two thirds, then a quick taper to the point.
		var factor float64
		switch {
		case t < 0.6:
			factor = 1 - 0.08*math.Pow(t/0.6, 2)
		default:
			factor = 0.92 * math.Sqrt(1-math.Pow((t-0.6)/0.4, 2))
		}
		half := halfWidth * factor
		for x := centre - half; x <= centre+half; x += 0.25 {
			img.SetNRGBA(int(x), int(y), c)
		}
	}
}

func disc(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := math.Hypot(dx, dy)
			if d <= r {
				img.SetNRGBA(x, y, c)
			} else if d <= r+0.7 {
				// Feather the edge so small glyphs do not look jagged.
				existing := img.NRGBAAt(x, y)
				img.SetNRGBA(x, y, blend(existing, c, 1-(d-r)/0.7))
			}
		}
	}
}

func alpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(float64(c.A) * clamp(a))
	return c
}

func blend(dst, src color.NRGBA, a float64) color.NRGBA {
	a = clamp(a)
	mix := func(d, s uint8) uint8 { return uint8(float64(d)*(1-a) + float64(s)*a) }
	return color.NRGBA{mix(dst.R, src.R), mix(dst.G, src.G), mix(dst.B, src.B),
		uint8(math.Max(float64(dst.A), float64(src.A)*a))}
}

func clamp(v float64) float64 { return math.Min(math.Max(v, 0), 1) }

func writePNG(path string, img image.Image) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		fail(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		fail(err)
	}
}

// writeICO wraps a PNG in an ICO container, which Windows has accepted since
// Vista and which avoids hand-rolling a bitmap encoder.
func writeICO(path string, src image.Image) {
	bounds := src.Bounds()
	// ICO stores 256 as 0.
	dim := byte(bounds.Dx() % 256)

	scaled := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(scaled, scaled.Bounds(), src, bounds.Min, draw.Src)

	var payload bytes.Buffer
	if err := png.Encode(&payload, scaled); err != nil {
		fail(err)
	}

	var out bytes.Buffer
	write := func(v any) {
		if err := binary.Write(&out, binary.LittleEndian, v); err != nil {
			fail(err)
		}
	}
	write(uint16(0))   // reserved
	write(uint16(1))   // type: icon
	write(uint16(1))   // one image
	out.WriteByte(dim) // width
	out.WriteByte(dim) // height
	out.WriteByte(0)   // palette size
	out.WriteByte(0)   // reserved
	write(uint16(1))   // colour planes
	write(uint16(32))  // bits per pixel
	write(uint32(payload.Len()))
	write(uint32(22)) // offset: 6 byte header + 16 byte entry

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(path, append(out.Bytes(), payload.Bytes()...), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
