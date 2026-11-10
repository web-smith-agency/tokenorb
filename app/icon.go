package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Tray-Icon: Ring, der sich je nach Session-Auslastung fuellt und einfaerbt.
// Wird zur Laufzeit erzeugt – keine Bilddateien noetig.

func usageRGB(pct float64, known bool) color.NRGBA {
	if !known {
		return color.NRGBA{0x6B, 0x7F, 0xA8, 0xFF}
	}
	green := [3]float64{0x34, 0xD3, 0x99}
	amber := [3]float64{0xFB, 0xBF, 0x24}
	red := [3]float64{0xF8, 0x71, 0x71}
	lerp := func(a, b [3]float64, t float64) color.NRGBA {
		t = math.Max(0, math.Min(1, t))
		return color.NRGBA{uint8(a[0] + (b[0]-a[0])*t), uint8(a[1] + (b[1]-a[1])*t), uint8(a[2] + (b[2]-a[2])*t), 0xFF}
	}
	switch {
	case pct < 50:
		return lerp(green, green, 0)
	case pct < 80:
		return lerp(green, amber, (pct-50)/30)
	case pct < 95:
		return lerp(amber, red, (pct-80)/15)
	default:
		return lerp(red, red, 0)
	}
}

// renderIcon zeichnet das Icon mit 4x4-Supersampling (weiche Kanten).
func renderIcon(size int, pct float64, known bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	fill := usageRGB(pct, known)
	track := color.NRGBA{0x3A, 0x40, 0x4A, 0xFF}
	dot := color.NRGBA{0xF5, 0xF7, 0xFA, 0xFF}

	s := float64(size)
	c := s / 2
	rOut := s * 0.47
	rIn := s * 0.30
	frac := math.Max(0, math.Min(1, pct/100))
	if !known {
		frac = 0
	}
	const ss = 4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss - c
					py := float64(y) + (float64(sy)+0.5)/ss - c
					d := math.Hypot(px, py)
					var col color.NRGBA
					hit := false
					if d >= rIn && d <= rOut {
						// Winkel ab 12 Uhr im Uhrzeigersinn, 0..1
						ang := math.Atan2(px, -py)
						if ang < 0 {
							ang += 2 * math.Pi
						}
						if ang/(2*math.Pi) <= frac {
							col = fill
						} else {
							col = track
						}
						hit = true
					} else if d <= s*0.14 {
						col = dot
						if known {
							col = fill
						}
						hit = true
					}
					if hit {
						r += float64(col.R)
						g += float64(col.G)
						b += float64(col.B)
						a += 255
					}
				}
			}
			n := float64(ss * ss)
			if a > 0 {
				cov := a / 255
				img.SetNRGBA(x, y, color.NRGBA{uint8(r / cov), uint8(g / cov), uint8(b / cov), uint8(a / n)})
			}
		}
	}
	return img
}

// buildICO verpackt PNG-Bilder in eine .ico-Datei (ab Windows Vista erlaubt).
func buildICO(imgs ...*image.NRGBA) []byte {
	var pngs [][]byte
	for _, im := range imgs {
		var buf bytes.Buffer
		_ = png.Encode(&buf, im)
		pngs = append(pngs, buf.Bytes())
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint16(0))         // reserviert
	binary.Write(&out, binary.LittleEndian, uint16(1))         // Typ: Icon
	binary.Write(&out, binary.LittleEndian, uint16(len(imgs))) // Anzahl
	offset := 6 + 16*len(imgs)
	for i, im := range imgs {
		w := im.Bounds().Dx()
		bw := byte(w)
		if w >= 256 {
			bw = 0
		}
		out.WriteByte(bw)
		out.WriteByte(bw)
		out.WriteByte(0) // Palette
		out.WriteByte(0)
		binary.Write(&out, binary.LittleEndian, uint16(1))  // Farbebenen
		binary.Write(&out, binary.LittleEndian, uint16(32)) // Bit pro Pixel
		binary.Write(&out, binary.LittleEndian, uint32(len(pngs[i])))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes()
}

func trayIcon(pct float64, known bool) []byte {
	return buildICO(renderIcon(16, pct, known), renderIcon(32, pct, known), renderIcon(64, pct, known))
}
