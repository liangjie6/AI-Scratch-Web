package imageservice

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func greenScreenFixture(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 8), G: uint8(150 + (x+y)%30), B: uint8(45 + y%30), A: 255})
		}
	}
	return img
}

func TestNormalizeGreenScreenBackgroundFlattensTextureAndPreservesSubjects(t *testing.T) {
	img := greenScreenFixture(20, 20)
	subject := color.NRGBA{R: 240, G: 120, B: 20, A: 255}
	detail := color.NRGBA{G: 160, B: 60, A: 255}
	for y := 6; y < 14; y++ {
		for x := 6; x < 14; x++ {
			img.SetNRGBA(x, y, subject)
		}
	}
	// This detail matches the background color but is enclosed by the subject.
	img.SetNRGBA(9, 9, detail)
	if changed := normalizeGreenScreenBackground(img); changed != 20*20-8*8 {
		t.Fatalf("changed %d pixels, want %d", changed, 20*20-8*8)
	}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			want := color.NRGBA{G: 255, A: 255}
			if x >= 6 && x < 14 && y >= 6 && y < 14 {
				want = subject
			}
			if x == 9 && y == 9 {
				want = detail
			}
			if got := img.NRGBAAt(x, y); got != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestNormalizeGreenScreenBackgroundRequiresConsistentGreenBorder(t *testing.T) {
	for _, name := range []string{"scene border", "inconsistent green border"} {
		t.Run(name, func(t *testing.T) {
			img := greenScreenFixture(20, 20)
			for y := 0; y < 20; y++ {
				for x := 0; x < 10; x++ {
					pixel := color.NRGBA{R: 30, G: 100, B: 220, A: 255}
					if name == "inconsistent green border" {
						pixel = color.NRGBA{G: 255, A: 255}
					}
					img.SetNRGBA(x, y, pixel)
				}
			}
			before := append([]byte(nil), img.Pix...)
			if changed := normalizeGreenScreenBackground(img); changed != 0 || !bytes.Equal(before, img.Pix) {
				t.Fatal("normalizer changed an image without a consistent green border")
			}
		})
	}
}

func TestNormalizeGreenScreenBackgroundPreservesAlpha(t *testing.T) {
	img := greenScreenFixture(11, 11)
	transparent := color.NRGBA{R: 123, G: 45, B: 67}
	translucent := color.NRGBA{G: 165, B: 63, A: 128}
	img.SetNRGBA(0, 0, transparent)
	img.SetNRGBA(1, 0, translucent)
	img.SetNRGBA(5, 5, translucent)
	if changed := normalizeGreenScreenBackground(img); changed != 11*11-3 {
		t.Fatalf("changed %d pixels, want %d", changed, 11*11-3)
	}
	if img.NRGBAAt(0, 0) != transparent || img.NRGBAAt(1, 0) != translucent || img.NRGBAAt(5, 5) != translucent {
		t.Fatal("transparent or translucent pixel channels changed")
	}
}

func TestNormalizeGreenScreenBackgroundSupportsOffsetStride(t *testing.T) {
	parent := greenScreenFixture(12, 12)
	img := parent.SubImage(image.Rect(2, 3, 10, 9)).(*image.NRGBA)
	outside := parent.NRGBAAt(1, 3)
	if changed := normalizeGreenScreenBackground(img); changed != 8*6 {
		t.Fatalf("changed %d pixels, want %d", changed, 8*6)
	}
	if parent.NRGBAAt(1, 3) != outside || img.NRGBAAt(2, 3) != (color.NRGBA{G: 255, A: 255}) {
		t.Fatal("subimage normalization ignored its stride or bounds")
	}
	if changed := normalizeGreenScreenBackground(img); changed != 0 {
		t.Fatalf("already pure green image changed %d pixels", changed)
	}
}
