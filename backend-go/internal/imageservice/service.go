package imageservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"sync"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/provider"
)

// AllowedSizes lists the canvas pairs accepted by the image API.
var AllowedSizes = map[[2]int]bool{
	{480, 360}: true,
	{640, 360}: true,
	{360, 360}: true,
	{360, 480}: true,
}

// AllowedModels lists the supported image generation models.
var AllowedModels = map[string]bool{
	"agnes-image-2.5-flash":  true,
	"gpt-image-2.5-sunburst": true,
	"gpt-image-2":            true,
}

// DefaultModel is the default image generation model.
const DefaultModel = "agnes-image-2.5-flash"

// Request is a validated image generation request.
type Request struct {
	Prompt      string
	Style       string
	CustomStyle string
	Width       int
	Height      int
	Target      string
	SheetLayout string
	Model       string
}

// RequestedCanvas is the client-requested canvas size.
type RequestedCanvas struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SheetLayout is the returned sprite sheet layout.
type SheetLayout struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// ProcessingHints describes how the generated sprite background can be edited.
type ProcessingHints struct {
	BackgroundMode    string `json:"background_mode"`
	SuggestedKeyColor string `json:"suggested_key_color,omitempty"`
}

// Response is the image generation response payload.
type Response struct {
	RequestID       string           `json:"request_id"`
	MimeType        string           `json:"mime_type"`
	SheetWidth      int              `json:"sheet_width"`
	SheetHeight     int              `json:"sheet_height"`
	SheetLayout     SheetLayout      `json:"sheet_layout"`
	ImageBase64     string           `json:"image_base64"`
	Target          string           `json:"target"`
	RequestedCanvas RequestedCanvas  `json:"requested_canvas"`
	ProcessingHints *ProcessingHints `json:"processing_hints,omitempty"`
}

// Service generates and normalizes images behind a bounded concurrency gate.
type Service struct {
	settings        *config.Settings
	defaultProvider provider.ImageProvider
	providers       map[string]provider.ImageProvider
	semaphore       chan struct{}
}

// NewService builds the image generation service.
func NewService(settings *config.Settings, defaultProvider provider.ImageProvider) *Service {
	limit := settings.ImageMaxConcurrency
	if limit < 1 {
		limit = 1
	}
	s := &Service{
		settings:        settings,
		defaultProvider: defaultProvider,
		providers:       make(map[string]provider.ImageProvider),
		semaphore:       make(chan struct{}, limit),
	}
	if defaultProvider == nil {
		s.providers["agnes-image-2.5-flash"] = provider.NewAgnesImageProvider(settings)
		s.providers["gpt-image-2.5-sunburst"] = provider.NewSuiXiangProvider(settings)
		s.providers["gpt-image-2"] = s.providers["gpt-image-2.5-sunburst"]
	}
	return s
}

// RegisterProvider attaches a provider for a specific model name.
func (s *Service) RegisterProvider(model string, p provider.ImageProvider) {
	s.providers[model] = p
}

func (s *Service) providerForModel(model string) (provider.ImageProvider, error) {
	if s.defaultProvider != nil {
		return s.defaultProvider, nil
	}
	if model == "gpt-image-2" {
		model = "gpt-image-2.5-sunburst"
	}
	if p, ok := s.providers[model]; ok && p != nil {
		return p, nil
	}
	return nil, &config.ConfigurationError{Message: fmt.Sprintf("unsupported image model %q", model)}
}

// Generate validates the canvas, builds the provider prompt and returns the
// normalized PNG payload.
func (s *Service) Generate(ctx context.Context, request Request) (*Response, error) {
	if !AllowedSizes[[2]int{request.Width, request.Height}] {
		return nil, fmt.Errorf("width and height must be an allowed canvas pair")
	}
	if request.Style == "custom" && request.CustomStyle == "" {
		return nil, fmt.Errorf("custom_style is required when style is custom")
	}
	model := request.Model
	if model == "" {
		model = DefaultModel
	}
	prov, err := s.providerForModel(model)
	if err != nil {
		return nil, err
	}
	background := ""
	providerBackground := ""
	var hints *ProcessingHints
	if request.Target == "sprite" {
		background = "solid"
		hints = &ProcessingHints{BackgroundMode: "solid", SuggestedKeyColor: "#00ff00"}
		if model == "gpt-image-2.5-sunburst" || model == "gpt-image-2" {
			background = "transparent"
			providerBackground = "transparent"
			hints = &ProcessingHints{BackgroundMode: "transparent"}
		}
	}
	text, err := prompt.BuildImagePrompt(request.Style, request.CustomStyle, request.Prompt, request.Width, request.Height, request.Target, background)
	if err != nil {
		return nil, err
	}

	select {
	case s.semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var generated *provider.ProviderImage
	func() {
		defer func() { <-s.semaphore }()
		generated, err = prov.Generate(ctx, provider.ImageParams{
			Model:      model,
			Prompt:     text,
			Width:      request.Width,
			Height:     request.Height,
			Background: providerBackground,
		})
	}()
	if err != nil {
		// ProviderError -> 502 and ConfigurationError -> 503 both pass through;
		// anything else becomes a provider failure like the Python reference.
		switch err.(type) {
		case *provider.ProviderError, *config.ConfigurationError:
			return nil, err
		}
		return nil, &provider.ProviderError{Message: "provider request failed"}
	}

	pngBytes, width, height, err := s.normalizeImage(generated.Data, generated.MimeType, background)
	if err != nil {
		return nil, err
	}
	requestID := generated.RequestID
	if requestID == "" {
		requestID = newRequestID()
	}
	return &Response{
		RequestID:       requestID,
		MimeType:        "image/png",
		SheetWidth:      width,
		SheetHeight:     height,
		SheetLayout:     SheetLayout{Columns: 1, Rows: 1},
		ImageBase64:     base64.StdEncoding.EncodeToString(pngBytes),
		Target:          request.Target,
		RequestedCanvas: RequestedCanvas{Width: request.Width, Height: request.Height},
		ProcessingHints: hints,
	}, nil
}

func (s *Service) normalizeImage(data []byte, mimeType, background string) ([]byte, int, int, error) {
	if len(data) == 0 || len(data) > s.settings.ImageMaxDownloadBytes {
		return nil, 0, 0, &provider.ProviderError{Message: "image is missing or too large"}
	}
	if len(mimeType) < 6 || mimeType[:6] != "image/" {
		return nil, 0, 0, &provider.ProviderError{Message: "provider returned a non-image MIME type"}
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, &provider.ProviderError{Message: "provider returned invalid image data"}
	}
	_ = format
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > 4096 || height > 4096 {
		return nil, 0, 0, &provider.ProviderError{Message: "image dimensions exceed the safety limit"}
	}
	converted := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(converted, converted.Bounds(), decoded, bounds.Min, draw.Src)
	if background == "solid" {
		normalizeGreenScreenBackground(converted)
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&output, converted); err != nil {
		return nil, 0, 0, &provider.ProviderError{Message: "provider returned invalid image data"}
	}
	return output.Bytes(), width, height, nil
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(buffer)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

var _ = sync.Mutex{}
