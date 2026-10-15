package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-scratch-web/backend/internal/config"
)

// ProviderError reports an upstream image provider failure.
type ProviderError struct{ Message string }

func (e *ProviderError) Error() string { return e.Message }

func newProviderError(format string, args ...any) *ProviderError {
	return &ProviderError{Message: fmt.Sprintf(format, args...)}
}

// ProviderImage is one generated image returned by the upstream provider.
type ProviderImage struct {
	RequestID string
	MimeType  string
	Data      []byte
}

// ImageParams holds options for generating an image.
type ImageParams struct {
	Model  string
	Prompt string
	Width  int
	Height int
}

// ImageProvider generates one image for given parameters.
type ImageProvider interface {
	Generate(ctx context.Context, params ImageParams) (*ProviderImage, error)
}

// SuiXiangProvider calls the OpenAI-compatible image generation endpoint.
type SuiXiangProvider struct {
	Settings *config.Settings
	Client   *http.Client
}

// NewSuiXiangProvider builds the image provider from deployment settings.
func NewSuiXiangProvider(settings *config.Settings) *SuiXiangProvider {
	return &SuiXiangProvider{Settings: settings}
}

func (p *SuiXiangProvider) httpClient(timeout float64) *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: time.Duration(timeout * float64(time.Second))}
}

type imageGenerationResponse struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Data   []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
}

func decodeBase64Image(raw string) ([]byte, error) {
	rawB64 := strings.TrimSpace(raw)
	if comma := strings.Index(rawB64, ","); comma >= 0 && strings.HasPrefix(rawB64, "data:") {
		rawB64 = rawB64[comma+1:]
	}
	return base64.StdEncoding.DecodeString(rawB64)
}

func downloadImage(ctx context.Context, maxDownloadBytes int, timeout float64, requestID, url string) (*ProviderImage, error) {
	client := &http.Client{
		Timeout: time.Duration(timeout * float64(time.Second)),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// follow_redirects=False in the Python reference.
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, newProviderError("provider image download failed")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, newProviderError("provider image download failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newProviderError("provider image download failed")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return nil, newProviderError("provider URL did not return an image")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxDownloadBytes)+1))
	if err != nil {
		return nil, newProviderError("provider image download failed")
	}
	if len(data) > maxDownloadBytes {
		return nil, newProviderError("provider image is too large")
	}
	return &ProviderImage{RequestID: requestID, MimeType: contentType, Data: data}, nil
}

// Generate requests one image and returns either inline base64 or a downloaded URL.
func (p *SuiXiangProvider) Generate(ctx context.Context, params ImageParams) (*ProviderImage, error) {
	apiKey, err := p.Settings.RequireAPIKey()
	if err != nil {
		// Matches the Python reference: Settings.require_api_key() raises
		// ConfigurationError, which the route maps to 503, not 502.
		return nil, err
	}
	model := params.Model
	if model == "" || model == "gpt-image-2" {
		model = p.Settings.ProviderModel
	}
	if model == "" {
		model = "gpt-image-2.5-sunburst"
	}
	payload, err := json.Marshal(map[string]any{
		"model":  model,
		"prompt": params.Prompt,
	})
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	timeout := p.Settings.ImageRequestTimeoutSeconds
	client := p.httpClient(timeout)
	endpoint := ResolveURL(p.Settings.ProviderBaseURL, "images/generations")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(p.Settings.ImageMaxDownloadBytes)+1))
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newProviderError("provider request failed")
	}
	var decoded imageGenerationResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, newProviderError("provider returned no image")
	}
	if len(decoded.Data) == 0 {
		return nil, newProviderError("provider returned no image")
	}
	requestID := decoded.ID
	if requestID == "" {
		requestID = decoded.TaskID
	}
	if requestID == "" {
		requestID = "provider-request"
	}
	item := decoded.Data[0]
	if item.B64JSON != "" {
		data, err := decodeBase64Image(item.B64JSON)
		if err != nil {
			return nil, newProviderError("provider returned invalid base64 image")
		}
		return &ProviderImage{RequestID: requestID, MimeType: "image/png", Data: data}, nil
	}
	if !strings.HasPrefix(item.URL, "https://") {
		return nil, newProviderError("provider returned an unsafe image URL")
	}
	return downloadImage(ctx, p.Settings.ImageMaxDownloadBytes, timeout, requestID, item.URL)
}

// AgnesImageProvider calls the Agnes image generation API using AGNES_API_KEY.
type AgnesImageProvider struct {
	Settings *config.Settings
	Client   *http.Client
}

// NewAgnesImageProvider builds an Agnes image provider.
func NewAgnesImageProvider(settings *config.Settings) *AgnesImageProvider {
	return &AgnesImageProvider{Settings: settings}
}

func (p *AgnesImageProvider) httpClient(timeout float64) *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: time.Duration(timeout * float64(time.Second))}
}

// AgnesRatioForSize maps canvas dimensions to Agnes supported ratio strings.
func AgnesRatioForSize(width, height int) string {
	switch {
	case width == 480 && height == 360:
		return "4:3"
	case width == 640 && height == 360:
		return "16:9"
	case width == 360 && height == 360:
		return "1:1"
	case width == 360 && height == 480:
		return "3:4"
	default:
		if width == height {
			return "1:1"
		}
		if width > height {
			return "4:3"
		}
		return "3:4"
	}
}

// Generate requests one image from Agnes API with return_base64=true.
func (p *AgnesImageProvider) Generate(ctx context.Context, params ImageParams) (*ProviderImage, error) {
	apiKey, err := p.Settings.RequireAgnesAPIKey()
	if err != nil {
		return nil, err
	}
	model := params.Model
	if model == "" {
		model = "agnes-image-2.5-flash"
	}
	ratio := AgnesRatioForSize(params.Width, params.Height)
	payload, err := json.Marshal(map[string]any{
		"model":         model,
		"prompt":        params.Prompt,
		"size":          "1K",
		"ratio":         ratio,
		"return_base64": true,
		"extra_body": map[string]any{
			"response_format": "b64_json",
		},
	})
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	timeout := p.Settings.ImageRequestTimeoutSeconds
	client := p.httpClient(timeout)
	endpoint := ResolveURL(p.Settings.AgnesAPIBase, "images/generations")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(p.Settings.ImageMaxDownloadBytes)+1))
	if err != nil {
		return nil, newProviderError("provider request failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newProviderError("provider request failed")
	}
	var decoded imageGenerationResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, newProviderError("provider returned no image")
	}
	if len(decoded.Data) == 0 {
		return nil, newProviderError("provider returned no image")
	}
	requestID := decoded.ID
	if requestID == "" {
		requestID = decoded.TaskID
	}
	if requestID == "" {
		requestID = "agnes-image-request"
	}
	item := decoded.Data[0]
	if item.B64JSON != "" {
		data, err := decodeBase64Image(item.B64JSON)
		if err != nil {
			return nil, newProviderError("provider returned invalid base64 image")
		}
		return &ProviderImage{RequestID: requestID, MimeType: "image/png", Data: data}, nil
	}
	if strings.HasPrefix(item.URL, "https://") {
		return downloadImage(ctx, p.Settings.ImageMaxDownloadBytes, timeout, requestID, item.URL)
	}
	return nil, newProviderError("provider returned no image")
}
