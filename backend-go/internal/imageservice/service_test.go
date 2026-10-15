package imageservice

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/provider"
)

type captureImageProvider struct {
	params provider.ImageParams
	data   []byte
}

func (p *captureImageProvider) Generate(_ context.Context, params provider.ImageParams) (*provider.ProviderImage, error) {
	p.params = params
	return &provider.ProviderImage{RequestID: "image-test", MimeType: "image/png", Data: p.data}, nil
}

func TestSpriteBackgroundAndAlphaPreservation(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	source.SetNRGBA(0, 0, color.NRGBA{A: 0})
	source.SetNRGBA(1, 0, color.NRGBA{R: 240, G: 120, B: 20, A: 128})
	source.SetNRGBA(2, 0, color.NRGBA{R: 80, G: 40, B: 20, A: 255})
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, model, target, background, hint, key string
	}{
		{"default green sprite", "", "sprite", "", "solid", "#00ff00"},
		{"agnes green sprite", "agnes-image-2.5-flash", "sprite", "", "solid", "#00ff00"},
		{"sunburst alpha sprite", "gpt-image-2.5-sunburst", "sprite", "transparent", "transparent", ""},
		{"legacy alpha sprite", "gpt-image-2", "sprite", "transparent", "transparent", ""},
		{"sunburst backdrop", "gpt-image-2.5-sunburst", "backdrop", "", "", ""},
		{"agnes backdrop", "agnes-image-2.5-flash", "backdrop", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := &captureImageProvider{data: input.Bytes()}
			service := NewService(&config.Settings{ImageMaxConcurrency: 1, ImageMaxDownloadBytes: 1024 * 1024}, captured)
			result, err := service.Generate(context.Background(), Request{
				Prompt: "three cats", Style: "cartoon", Width: 480, Height: 360,
				Target: tc.target, SheetLayout: "1x1", Model: tc.model,
			})
			if err != nil {
				t.Fatal(err)
			}
			if captured.params.Background != tc.background {
				t.Errorf("provider background = %q, want %q", captured.params.Background, tc.background)
			}
			if tc.hint == "" {
				if result.ProcessingHints != nil || strings.Contains(captured.params.Prompt, "#00ff00") {
					t.Fatal("backdrop received sprite background instructions")
				}
			} else {
				if result.ProcessingHints == nil || result.ProcessingHints.BackgroundMode != tc.hint || result.ProcessingHints.SuggestedKeyColor != tc.key {
					t.Fatalf("unexpected processing hints: %+v", result.ProcessingHints)
				}
				if tc.hint == "solid" && !strings.Contains(captured.params.Prompt, "#00ff00") {
					t.Fatal("green sprite prompt does not specify key color")
				}
				if tc.hint == "transparent" && strings.Contains(captured.params.Prompt, "#00ff00") {
					t.Fatal("transparent sprite prompt contains green screen instructions")
				}
			}
			data, err := base64.StdEncoding.DecodeString(result.ImageBase64)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			for x, want := range []uint32{0, 128 * 257, 65535} {
				_, _, _, alpha := decoded.At(x, 0).RGBA()
				if alpha != want {
					t.Errorf("pixel %d alpha = %d, want %d", x, alpha, want)
				}
			}
		})
	}
}
