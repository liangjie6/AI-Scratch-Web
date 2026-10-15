package schema

import "sync"

// DeploymentSettings carries the subset of backend/app/config.py Settings that
// the schema contract consults: enabled extensions and the resource/asset and
// logical-reference capability flags.
type DeploymentSettings struct {
	AIPenExtensionEnabled           bool
	AIMusicExtensionEnabled         bool
	AITextToSpeechExtensionEnabled  bool
	AIDestructiveResourceOpsEnabled bool
	AIProjectAssetsEnabled          bool
	AISoundAssetsEnabled            bool
	AIAudioGenerationEnabled        bool
	AILogicalResourceRefsEnabled    bool
}

// DefaultDeploymentSettings mirrors the defaults of config.Settings: project
// and sound assets plus logical resource refs are enabled, everything else is
// disabled.
func DefaultDeploymentSettings() DeploymentSettings {
	return DeploymentSettings{
		AIProjectAssetsEnabled:       true,
		AISoundAssetsEnabled:         true,
		AILogicalResourceRefsEnabled: true,
	}
}

// EnabledAIExtensions ports Settings.enabled_ai_extensions().
func (s DeploymentSettings) EnabledAIExtensions() map[string]bool {
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

var (
	deploymentSettingsMu sync.RWMutex
	deploymentSettings   = DefaultDeploymentSettings()
)

// GetDeploymentSettings ports get_settings().
func GetDeploymentSettings() DeploymentSettings {
	deploymentSettingsMu.RLock()
	defer deploymentSettingsMu.RUnlock()
	return deploymentSettings
}

// SetDeploymentSettings replaces the process-wide deployment settings. It
// exists so other packages can mirror the Python Settings environment.
func SetDeploymentSettings(settings DeploymentSettings) {
	deploymentSettingsMu.Lock()
	defer deploymentSettingsMu.Unlock()
	deploymentSettings = settings
}
