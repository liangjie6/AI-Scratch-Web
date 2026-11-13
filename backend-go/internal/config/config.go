// Package config mirrors backend/app/config.py: one process-wide Settings
// value loaded from the environment (and an optional .env file).
package config

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ConfigurationError reports a missing or invalid deployment setting.
type ConfigurationError struct{ Message string }

func (e *ConfigurationError) Error() string { return e.Message }

// Settings is the Go equivalent of the pydantic Settings model.
type Settings struct {
	AccessUser     string
	AccessPassword string
	SuiXiangAPIKey string
	AgnesAPIKey    string
	DeepSeekAPIKey string
	GPTAPIKey      string

	ImageRequestTimeoutSeconds float64
	ImageMaxDownloadBytes      int
	ImageMaxRequestBytes       int
	ImageMaxConcurrency        int

	AIBlocksMaxRequestBytes         int
	AIBlocksMaxConcurrency          int
	AIBlocksProviderTimeoutSeconds  float64
	AIBlocksVerboseLogging          bool
	AIBlocksMaxRepairRounds         int
	AIBlocksSourcePipelineEnabled   bool
	AIBlocksCommitEnabled           bool
	AIProjectAssetsEnabled          bool
	AISoundAssetsEnabled            bool
	AILogicalResourceRefsEnabled    bool
	AIShortCommitEnabled            bool
	AIAudioGenerationEnabled        bool
	AIAssetAutoMatchEnabled         bool
	AIAssetAutoMatchMinScore        int
	AIAssetAutoMatchMinMargin       int
	AIRunsDatabasePath              string
	AIRunsEventRetentionHours       int
	AIRunsRetentionDays             int
	AIRunsMaxActive                 int
	AIRunsMaxActivePerScope         int
	AIDurableRunsEnabled            bool
	AIDurableRunsRolloutPercent     int
	AICrashCheckpointEnabled        bool
	AIPenExtensionEnabled           bool
	AIMusicExtensionEnabled         bool
	AITextToSpeechExtensionEnabled  bool
	AIDestructiveResourceOpsEnabled bool

	ProviderBaseURL   string
	ProviderModel     string
	AgnesAPIBase      string
	AgnesChatModel    string
	DeepSeekAPIBase   string
	DeepSeekChatModel string
	GPTAPIBase        string
	GPTChatModel      string
}

// RequireAPIKey returns the image provider key or a ConfigurationError.
func (s *Settings) RequireAPIKey() (string, error) {
	if s.SuiXiangAPIKey == "" {
		return "", &ConfigurationError{Message: "SUI_XIANG_API_KEY is required for image generation"}
	}
	return s.SuiXiangAPIKey, nil
}

// RequireAgnesAPIKey returns the Agnes key or an error.
func (s *Settings) RequireAgnesAPIKey() (string, error) {
	if s.AgnesAPIKey == "" {
		return "", &ConfigurationError{Message: "AGNES_API_KEY is required for Agnes services"}
	}
	return s.AgnesAPIKey, nil
}

// RequireDeepSeekAPIKey returns the DeepSeek key or an error.
func (s *Settings) RequireDeepSeekAPIKey() (string, error) {
	if s.DeepSeekAPIKey == "" {
		return "", &ConfigurationError{Message: "DEEPSEEK_API_KEY is required for advanced AI block generation"}
	}
	return s.DeepSeekAPIKey, nil
}

// RequireGPTAPIKey returns the GPT key or an error.
func (s *Settings) RequireGPTAPIKey() (string, error) {
	if s.GPTAPIKey == "" {
		return "", &ConfigurationError{Message: "GPT_API_KEY is required for super AI block generation"}
	}
	return s.GPTAPIKey, nil
}

// DurableRunsEnabledForScope buckets a raw session scope without retaining it.
func (s *Settings) DurableRunsEnabledForScope(scope string) bool {
	sum := sha256.Sum256([]byte(scope))
	return s.DurableRunsEnabledForScopeHash(hex.EncodeToString(sum[:]))
}

// DurableRunsEnabledForScopeHash evaluates a previously normalized scope hash.
func (s *Settings) DurableRunsEnabledForScopeHash(scopeHash string) bool {
	if !s.AIDurableRunsEnabled || s.AIDurableRunsRolloutPercent <= 0 {
		return false
	}
	if s.AIDurableRunsRolloutPercent >= 100 {
		return true
	}
	if len(scopeHash) < 8 {
		return false
	}
	bucket, err := strconv.ParseUint(scopeHash[:8], 16, 64)
	if err != nil {
		return false
	}
	return int(bucket%100) < s.AIDurableRunsRolloutPercent
}

// EnabledAIExtensions returns the deployment-enabled built-in extensions.
func (s *Settings) EnabledAIExtensions() map[string]bool {
	enabled := map[string]bool{}
	if s.AIPenExtensionEnabled {
		enabled["pen"] = true
	}
	if s.AIMusicExtensionEnabled {
		enabled["music"] = true
	}
	if s.AITextToSpeechExtensionEnabled {
		enabled["text2speech"] = true
	}
	return enabled
}

type fieldSpec struct {
	key      string
	fallback string
}

func boolValue(env map[string]string, spec fieldSpec, def bool) bool {
	raw, ok := lookup(env, spec)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}

func intValue(env map[string]string, spec fieldSpec, def, min, max int) (int, error) {
	raw, ok := lookup(env, spec)
	if !ok || strings.TrimSpace(raw) == "" {
		return def, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, &ConfigurationError{Message: fmt.Sprintf("%s must be an integer, got %q", spec.key, raw)}
	}
	if parsed < min || parsed > max {
		return 0, &ConfigurationError{Message: fmt.Sprintf("%s must be between %d and %d, got %d", spec.key, min, max, parsed)}
	}
	return parsed, nil
}

func floatValue(env map[string]string, spec fieldSpec, def, min, max float64) (float64, error) {
	raw, ok := lookup(env, spec)
	if !ok || strings.TrimSpace(raw) == "" {
		return def, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, &ConfigurationError{Message: fmt.Sprintf("%s must be a number, got %q", spec.key, raw)}
	}
	if parsed < min || parsed > max {
		return 0, &ConfigurationError{Message: fmt.Sprintf("%s must be between %v and %v, got %v", spec.key, min, max, parsed)}
	}
	return parsed, nil
}

func stringValue(env map[string]string, spec fieldSpec, def string) string {
	raw, ok := lookup(env, spec)
	if !ok {
		return def
	}
	return strings.TrimSpace(raw)
}

func optionalValue(env map[string]string, spec fieldSpec) string {
	raw, ok := lookup(env, spec)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func lookup(env map[string]string, spec fieldSpec) (string, bool) {
	if value, ok := env[spec.key]; ok {
		return value, true
	}
	if spec.fallback != "" {
		if value, ok := env[spec.fallback]; ok {
			return value, true
		}
	}
	return "", false
}

// Load reads .env (when present) plus the process environment and validates ranges.
func Load() (*Settings, error) {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		if index := strings.IndexByte(entry, '='); index > 0 {
			env[entry[:index]] = entry[index+1:]
		}
	}
	path := os.Getenv("AI_SCRATCH_ENV_FILE")
	if path == "" {
		path = ".env"
	}
	fileEnv, err := readEnvFile(path)
	if err != nil {
		return nil, err
	}
	for key, value := range fileEnv {
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}

	settings := &Settings{
		AccessUser:        optionalValue(env, fieldSpec{"AI_SCRATCH_ACCESS_USER", ""}),
		AccessPassword:    env["AI_SCRATCH_ACCESS_PASSWORD"],
		SuiXiangAPIKey:    optionalValue(env, fieldSpec{"SUI_XIANG_API_KEY", ""}),
		AgnesAPIKey:       optionalValue(env, fieldSpec{"AGNES_API_KEY", ""}),
		DeepSeekAPIKey:    optionalValue(env, fieldSpec{"DEEPSEEK_API_KEY", ""}),
		GPTAPIKey:         optionalValue(env, fieldSpec{"GPT_API_KEY", ""}),
		ProviderBaseURL:   stringValue(env, fieldSpec{"PROVIDER_BASE_URL", ""}, "https://sui-xiang.com/v1"),
		ProviderModel:     stringValue(env, fieldSpec{"PROVIDER_MODEL", ""}, "gpt-image-2.5-sunburst"),
		AgnesAPIBase:      stringValue(env, fieldSpec{"AGNES_API_BASE", ""}, "https://api.agnes-ai.cn/v1/"),
		AgnesChatModel:    stringValue(env, fieldSpec{"AGNES_CHAT_MODEL", ""}, "agnes-2.5-flash"),
		DeepSeekAPIBase:   stringValue(env, fieldSpec{"DEEPSEEK_API_BASE", ""}, "https://api.deepseek.com"),
		DeepSeekChatModel: stringValue(env, fieldSpec{"DEEPSEEK_CHAT_MODEL", ""}, "deepseek-v4.1-flash-expires-on-0910"),
		GPTAPIBase:        stringValue(env, fieldSpec{"GPT_API_BASE", ""}, "https://sui-xiang.com/v1"),
		GPTChatModel:      stringValue(env, fieldSpec{"GPT_CHAT_MODEL", ""}, "gpt-5.6-sol"),
	}

	if settings.ImageRequestTimeoutSeconds, err = floatValue(env, fieldSpec{"IMAGE_REQUEST_TIMEOUT_SECONDS", ""}, 90, 5, 180); err != nil {
		return nil, err
	}
	if settings.ImageMaxDownloadBytes, err = intValue(env, fieldSpec{"IMAGE_MAX_DOWNLOAD_BYTES", ""}, 12_582_912, 1_048_576, 50_000_000); err != nil {
		return nil, err
	}
	if settings.ImageMaxRequestBytes, err = intValue(env, fieldSpec{"IMAGE_MAX_REQUEST_BYTES", ""}, 16_384, 2_048, 1_048_576); err != nil {
		return nil, err
	}
	if settings.ImageMaxConcurrency, err = intValue(env, fieldSpec{"IMAGE_MAX_CONCURRENCY", ""}, 2, 1, 8); err != nil {
		return nil, err
	}
	if settings.AIBlocksMaxRequestBytes, err = intValue(env, fieldSpec{"AI_BLOCKS_MAX_REQUEST_BYTES", ""}, 262_144, 16_384, 1_048_576); err != nil {
		return nil, err
	}
	if settings.AIBlocksMaxConcurrency, err = intValue(env, fieldSpec{"AI_BLOCKS_MAX_CONCURRENCY", ""}, 2, 1, 8); err != nil {
		return nil, err
	}
	if settings.AIBlocksProviderTimeoutSeconds, err = floatValue(env, fieldSpec{"AI_BLOCKS_PROVIDER_TIMEOUT_SECONDS", ""}, 480, 10, 720); err != nil {
		return nil, err
	}
	if settings.AIBlocksMaxRepairRounds, err = intValue(env, fieldSpec{"AI_BLOCKS_MAX_REPAIR_ROUNDS", ""}, 3, 0, 3); err != nil {
		return nil, err
	}
	if settings.AIAssetAutoMatchMinScore, err = intValue(env, fieldSpec{"AI_ASSET_AUTO_MATCH_MIN_SCORE", ""}, 8, 8, 100); err != nil {
		return nil, err
	}
	if settings.AIAssetAutoMatchMinMargin, err = intValue(env, fieldSpec{"AI_ASSET_AUTO_MATCH_MIN_MARGIN", ""}, 5, 5, 100); err != nil {
		return nil, err
	}
	if settings.AIRunsEventRetentionHours, err = intValue(env, fieldSpec{"AI_RUNS_EVENT_RETENTION_HOURS", ""}, 24, 1, 24*30); err != nil {
		return nil, err
	}
	if settings.AIRunsMaxActive, err = intValue(env, fieldSpec{"AI_RUNS_MAX_ACTIVE", ""}, 32, 1, 256); err != nil {
		return nil, err
	}
	if settings.AIRunsMaxActivePerScope, err = intValue(env, fieldSpec{"AI_RUNS_MAX_ACTIVE_PER_SCOPE", ""}, 8, 1, 64); err != nil {
		return nil, err
	}
	if settings.AIRunsRetentionDays, err = intValue(env, fieldSpec{"AI_RUNS_RETENTION_DAYS", ""}, 7, 1, 90); err != nil {
		return nil, err
	}
	if settings.AIDurableRunsRolloutPercent, err = intValue(env, fieldSpec{"AI_DURABLE_RUNS_ROLLOUT_PERCENT", ""}, 100, 0, 100); err != nil {
		return nil, err
	}

	settings.AIBlocksVerboseLogging = boolValue(env, fieldSpec{"AI_BLOCKS_VERBOSE_LOGGING", ""}, false)
	settings.AIBlocksSourcePipelineEnabled = boolValue(env, fieldSpec{"AI_BLOCKS_SOURCE_PIPELINE_ENABLED", ""}, true)
	settings.AIBlocksCommitEnabled = boolValue(env, fieldSpec{"AI_BLOCKS_COMMIT_ENABLED", ""}, true)
	settings.AIProjectAssetsEnabled = boolValue(env, fieldSpec{"AI_PROJECT_ASSETS_ENABLED", ""}, true)
	settings.AISoundAssetsEnabled = boolValue(env, fieldSpec{"AI_SOUND_ASSETS_ENABLED", ""}, true)
	settings.AILogicalResourceRefsEnabled = boolValue(env, fieldSpec{"AI_LOGICAL_RESOURCE_REFS_ENABLED", ""}, true)
	settings.AIShortCommitEnabled = boolValue(env, fieldSpec{"AI_SHORT_COMMIT_ENABLED", ""}, true)
	settings.AIAudioGenerationEnabled = boolValue(env, fieldSpec{"AI_AUDIO_GENERATION_ENABLED", ""}, false)
	settings.AIAssetAutoMatchEnabled = boolValue(env, fieldSpec{"AI_ASSET_AUTO_MATCH_ENABLED", ""}, false)
	settings.AIDurableRunsEnabled = boolValue(env, fieldSpec{"AI_DURABLE_RUNS_ENABLED", ""}, false)
	settings.AICrashCheckpointEnabled = boolValue(env, fieldSpec{"AI_CRASH_CHECKPOINT_ENABLED", ""}, true)
	settings.AIPenExtensionEnabled = boolValue(env, fieldSpec{"AI_PEN_EXTENSION_ENABLED", ""}, false)
	settings.AIMusicExtensionEnabled = boolValue(env, fieldSpec{"AI_MUSIC_EXTENSION_ENABLED", ""}, false)
	settings.AITextToSpeechExtensionEnabled = boolValue(env, fieldSpec{"AI_TEXT_TO_SPEECH_EXTENSION_ENABLED", ""}, false)
	settings.AIDestructiveResourceOpsEnabled = boolValue(env, fieldSpec{"AI_DESTRUCTIVE_RESOURCE_OPS_ENABLED", ""}, false)

	settings.AIRunsDatabasePath = stringValue(env, fieldSpec{"AI_RUNS_DATABASE_PATH", ""}, filepath.Join("data", "ai-runs.sqlite3"))
	return settings, nil
}

func readEnvFile(path string) (map[string]string, error) {
	result := map[string]string{}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		index := strings.IndexByte(line, '=')
		if index <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:index])
		value := strings.TrimSpace(line[index+1:])
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		result[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

var (
	settingsOnce sync.Once
	settingsVal  *Settings
	settingsErr  error
)

// Get returns the process-wide Settings, loading it once.
func Get() *Settings {
	settingsOnce.Do(func() {
		settingsVal, settingsErr = Load()
		if settingsErr != nil {
			panic(settingsErr)
		}
	})
	return settingsVal
}

// TryGet returns the process-wide Settings and any load error.
func TryGet() (*Settings, error) {
	settingsOnce.Do(func() {
		settingsVal, settingsErr = Load()
	})
	return settingsVal, settingsErr
}
