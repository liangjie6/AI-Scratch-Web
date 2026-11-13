package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

// ResourceOp is the union of the resource-operation models. The Python
// implementation stores validated pydantic models; the Go port stores the
// equivalent model_dump maps so the manifest payload stays JSON-shaped.
type ResourceOp = map[string]any

// FieldValue ports FieldValue = str | float | int | bool | StateRefField |
// ResourceRefInput | ExistingAssetRefInput.
type FieldValue = any

// InputValue ports the InputValue discriminated union.
type InputValue = any

// ProjectGenerationRequest ports ProjectGenerationRequest (the output_contract
// discriminated union). Use DecodeProjectGenerationRequest to decode it.
type ProjectGenerationRequest = any

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtr(value string) *string {
	return &value
}

func validateShortStringList(path string, values []string, maxLength int) error {
	if err := requireListLength(path, len(values), -1, maxLength); err != nil {
		return err
	}
	for index, value := range values {
		if err := validateShortString(path+"/"+itoa(index), value); err != nil {
			return err
		}
	}
	return nil
}

func itoa(value int) string { return strconv.Itoa(value) }

// ChatMessage ports ChatMessage.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (m *ChatMessage) requiredJSONFields() []string { return []string{"role", "content"} }

// Validate ports ChatMessage validation.
func (m *ChatMessage) Validate() error {
	if err := validateLiteral("/role", m.Role, "user", "assistant"); err != nil {
		return err
	}
	return validateLongString("/content", m.Content)
}

// TargetContext ports TargetContext.
type TargetContext struct {
	Name           string   `json:"name"`
	IsStage        bool     `json:"is_stage"`
	HasScripts     bool     `json:"has_scripts"`
	Mode           string   `json:"mode"`
	Costumes       []string `json:"costumes"`
	Backdrops      []string `json:"backdrops"`
	Sounds         []string `json:"sounds"`
	SpriteNames    []string `json:"sprite_names"`
	CurrentProgram string   `json:"current_program"`
}

func (m *TargetContext) requiredJSONFields() []string {
	return []string{"name", "is_stage", "has_scripts", "mode"}
}

// Validate ports TargetContext validation.
func (m *TargetContext) Validate() error {
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateLiteral("/mode", m.Mode, "modify", "append", "rewrite"); err != nil {
		return err
	}
	if err := validateShortStringList("/costumes", m.Costumes, 64); err != nil {
		return err
	}
	if err := validateShortStringList("/backdrops", m.Backdrops, 64); err != nil {
		return err
	}
	if err := validateShortStringList("/sounds", m.Sounds, 64); err != nil {
		return err
	}
	if err := validateShortStringList("/sprite_names", m.SpriteNames, 128); err != nil {
		return err
	}
	if err := requireStringLength("/current_program", m.CurrentProgram, -1, MaxCurrentProgramLength); err != nil {
		return err
	}
	if m.CurrentProgram != "" {
		parsed, err := jsonx.Parse([]byte(m.CurrentProgram))
		if err != nil {
			return errors.New("current_program must be valid JSON")
		}
		if _, ok := parsed.(map[string]any); !ok {
			return errors.New("current_program must contain a JSON object")
		}
	}
	return nil
}

// ProjectTargetContext ports ProjectTargetContext.
type ProjectTargetContext struct {
	Name           string   `json:"name"`
	IsStage        bool     `json:"is_stage"`
	HasScripts     bool     `json:"has_scripts"`
	Mode           string   `json:"mode"`
	Costumes       []string `json:"costumes"`
	Backdrops      []string `json:"backdrops"`
	Sounds         []string `json:"sounds"`
	SpriteNames    []string `json:"sprite_names"`
	CurrentProgram string   `json:"current_program"`
	TargetID       string   `json:"target_id"`
	Revision       int      `json:"revision"`
}

func (m *ProjectTargetContext) requiredJSONFields() []string {
	return []string{"name", "is_stage", "has_scripts", "mode", "target_id", "revision"}
}

// Validate ports ProjectTargetContext validation (base fields first).
func (m *ProjectTargetContext) Validate() error {
	base := TargetContext{
		Name: m.Name, IsStage: m.IsStage, HasScripts: m.HasScripts, Mode: m.Mode,
		Costumes: m.Costumes, Backdrops: m.Backdrops, Sounds: m.Sounds,
		SpriteNames: m.SpriteNames, CurrentProgram: m.CurrentProgram,
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if err := validateShortString("/target_id", m.TargetID); err != nil {
		return err
	}
	return requireIntMin("/revision", m.Revision, 0)
}

// ProjectContext ports ProjectContext.
type ProjectContext struct {
	ProjectID   string                 `json:"project_id"`
	Version     int                    `json:"version"`
	Targets     []ProjectTargetContext `json:"targets"`
	SharedState map[string]any         `json:"shared_state"`
}

func (m *ProjectContext) requiredJSONFields() []string {
	return []string{"project_id", "version", "targets"}
}

// Validate ports ProjectContext validation.
func (m *ProjectContext) Validate() error {
	if err := validateShortString("/project_id", m.ProjectID); err != nil {
		return err
	}
	if err := requireIntMin("/version", m.Version, 0); err != nil {
		return err
	}
	if err := requireListLength("/targets", len(m.Targets), 1, 12); err != nil {
		return err
	}
	for index := range m.Targets {
		if err := m.Targets[index].Validate(); err != nil {
			return err
		}
	}
	return requireListLength("/shared_state", len(m.SharedState), -1, 16)
}

// ThinkingSettings ports ThinkingSettings.
type ThinkingSettings struct {
	Enabled  bool             `json:"enabled" default:"true"`
	Strength ThinkingStrength `json:"strength" default:"medium"`
}

func (m *ThinkingSettings) requiredJSONFields() []string { return nil }

func (m *ThinkingSettings) setPythonDefault() {
	m.Enabled = true
	m.Strength = ThinkingStrengthMedium
}

// Validate ports ThinkingSettings validation.
func (m *ThinkingSettings) Validate() error {
	allowed := make([]string, 0, len(ThinkingStrengths))
	for _, strength := range ThinkingStrengths {
		allowed = append(allowed, string(strength))
	}
	return validateLiteral("/strength", string(m.Strength), allowed...)
}

// AIBlocksRequestBase ports AIBlocksRequestBase.
type AIBlocksRequestBase struct {
	ModelTier ModelTier        `json:"model_tier" default:"normal"`
	SessionID string           `json:"session_id"`
	RequestID string           `json:"request_id"`
	Messages  []ChatMessage    `json:"messages"`
	Target    TargetContext    `json:"target"`
	Thinking  ThinkingSettings `json:"thinking"`
}

func (m *AIBlocksRequestBase) requiredJSONFields() []string {
	return []string{"session_id", "request_id", "messages", "target"}
}

// Validate ports AIBlocksRequestBase validation.
func (m *AIBlocksRequestBase) Validate() error {
	allowedTiers := make([]string, 0, len(ModelTiers))
	for _, tier := range ModelTiers {
		allowedTiers = append(allowedTiers, string(tier))
	}
	if err := validateLiteral("/model_tier", string(m.ModelTier), allowedTiers...); err != nil {
		return err
	}
	if err := validateShortString("/session_id", m.SessionID); err != nil {
		return err
	}
	if err := validateShortString("/request_id", m.RequestID); err != nil {
		return err
	}
	if err := requireListLength("/messages", len(m.Messages), 1, 40); err != nil {
		return err
	}
	for index := range m.Messages {
		if err := m.Messages[index].Validate(); err != nil {
			return err
		}
	}
	if err := m.Target.Validate(); err != nil {
		return err
	}
	return m.Thinking.Validate()
}

// ChatRequest ports ChatRequest (an AIBlocksRequestBase subclass with no extra
// fields).
type ChatRequest = AIBlocksRequestBase

// RequirementsResponse ports RequirementsResponse.
type RequirementsResponse struct {
	Ready            bool     `json:"ready"`
	Title            string   `json:"title"`
	Goal             string   `json:"goal"`
	BehaviorSteps    []string `json:"behavior_steps"`
	Triggers         []string `json:"triggers"`
	Controls         []string `json:"controls"`
	SuccessCriteria  []string `json:"success_criteria"`
	MissingQuestions []string `json:"missing_questions"`
}

func (m *RequirementsResponse) requiredJSONFields() []string {
	return []string{"ready", "title", "goal", "behavior_steps", "triggers", "controls", "success_criteria"}
}

// Validate ports RequirementsResponse validation.
func (m *RequirementsResponse) Validate() error {
	if err := requireStringLength("/title", m.Title, -1, 200); err != nil {
		return err
	}
	if err := requireStringLength("/goal", m.Goal, -1, 2000); err != nil {
		return err
	}
	if err := validateShortStringList("/behavior_steps", m.BehaviorSteps, 40); err != nil {
		return err
	}
	if err := validateShortStringList("/triggers", m.Triggers, 30); err != nil {
		return err
	}
	if err := validateShortStringList("/controls", m.Controls, 30); err != nil {
		return err
	}
	if err := validateShortStringList("/success_criteria", m.SuccessCriteria, 30); err != nil {
		return err
	}
	return validateShortStringList("/missing_questions", m.MissingQuestions, 20)
}

// Plan ports Plan.
type Plan struct {
	Steps []string `json:"steps"`
}

func (m *Plan) requiredJSONFields() []string { return []string{"steps"} }

// Validate ports Plan validation.
func (m *Plan) Validate() error {
	return validateShortStringList("/steps", m.Steps, 40)
}

// ProjectProgramTarget ports ProjectProgramTarget.
type ProjectProgramTarget struct {
	JobID         string    `json:"job_id"`
	BehaviorIDs   []string  `json:"behavior_ids"`
	OwnerEntityID string    `json:"owner_entity_id"`
	Target        TargetRef `json:"target"`
	Mode          string    `json:"mode"`
	Intent        string    `json:"intent"`
}

func (m *ProjectProgramTarget) requiredJSONFields() []string {
	return []string{"job_id", "behavior_ids", "owner_entity_id", "target", "mode", "intent"}
}

// Validate ports ProjectProgramTarget validation.
func (m *ProjectProgramTarget) Validate() error {
	if err := validateShortString("/job_id", m.JobID); err != nil {
		return err
	}
	if err := validateShortStringList("/behavior_ids", m.BehaviorIDs, 40); err != nil {
		return err
	}
	if err := validateShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := m.Target.Validate(); err != nil {
		return err
	}
	if err := validateLiteral("/mode", m.Mode, "append", "modify", "rewrite"); err != nil {
		return err
	}
	return validateShortString("/intent", m.Intent)
}

// ProjectSharedState ports ProjectSharedState.
type ProjectSharedState struct {
	Variables  []ProjectVariableDefinition  `json:"variables"`
	Lists      []ProjectListDefinition      `json:"lists"`
	Broadcasts []ProjectBroadcastDefinition `json:"broadcasts"`
}

func (m *ProjectSharedState) requiredJSONFields() []string {
	return []string{"variables", "lists", "broadcasts"}
}

// Validate ports validate_shared_state.
func (m *ProjectSharedState) Validate() error {
	if err := requireListLength("/variables", len(m.Variables), -1, 100); err != nil {
		return err
	}
	for index := range m.Variables {
		if err := m.Variables[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/lists", len(m.Lists), -1, 50); err != nil {
		return err
	}
	for index := range m.Lists {
		if err := m.Lists[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/broadcasts", len(m.Broadcasts), -1, 100); err != nil {
		return err
	}
	for index := range m.Broadcasts {
		if err := m.Broadcasts[index].Validate(); err != nil {
			return err
		}
	}
	for _, group := range []struct {
		label  string
		names  []string
		scopes []string
	}{
		{"variables", projectVariableNames(m.Variables), projectVariableScopes(m.Variables)},
		{"lists", projectListNames(m.Lists), projectListScopes(m.Lists)},
	} {
		seen := map[string]bool{}
		for _, name := range group.names {
			key, err := LogicalKey(name)
			if err != nil {
				return err
			}
			if seen[key] {
				return errors.New(group.label + " names must be unique")
			}
			seen[key] = true
		}
		for _, scope := range group.scopes {
			if scope != "global" {
				return errors.New("shared " + group.label + " must use global scope")
			}
		}
	}
	seenBroadcasts := map[string]bool{}
	for _, broadcast := range m.Broadcasts {
		key, err := LogicalKey(broadcast.Name)
		if err != nil {
			return err
		}
		if seenBroadcasts[key] {
			return errors.New("broadcast names must be unique")
		}
		seenBroadcasts[key] = true
	}
	seenRefs := map[string]bool{}
	for _, ref := range projectSharedStateRefs(m) {
		if seenRefs[ref] {
			return errors.New("shared state ref_id values must be unique")
		}
		seenRefs[ref] = true
	}
	return nil
}

func projectVariableNames(values []ProjectVariableDefinition) []string {
	names := make([]string, 0, len(values))
	for index := range values {
		names = append(names, values[index].Name)
	}
	return names
}

func projectVariableScopes(values []ProjectVariableDefinition) []string {
	scopes := make([]string, 0, len(values))
	for index := range values {
		scopes = append(scopes, values[index].Scope)
	}
	return scopes
}

func projectListNames(values []ProjectListDefinition) []string {
	names := make([]string, 0, len(values))
	for index := range values {
		names = append(names, values[index].Name)
	}
	return names
}

func projectListScopes(values []ProjectListDefinition) []string {
	scopes := make([]string, 0, len(values))
	for index := range values {
		scopes = append(scopes, values[index].Scope)
	}
	return scopes
}

func projectSharedStateRefs(state *ProjectSharedState) []string {
	refs := make([]string, 0, len(state.Variables)+len(state.Lists)+len(state.Broadcasts))
	for index := range state.Variables {
		refs = append(refs, state.Variables[index].RefID)
	}
	for index := range state.Lists {
		refs = append(refs, state.Lists[index].RefID)
	}
	for index := range state.Broadcasts {
		refs = append(refs, state.Broadcasts[index].RefID)
	}
	return refs
}

// ManifestSchemaVersion is ProjectManifest.schema_version (Literal[3]).
const ManifestSchemaVersion = 3

// ProjectManifest ports ProjectManifest.
type ProjectManifest struct {
	SchemaVersion      int                    `json:"schema_version"`
	Scope              string                 `json:"scope"`
	Summary            string                 `json:"summary"`
	Assumptions        []string               `json:"assumptions"`
	RequiredExtensions []string               `json:"required_extensions"`
	ResourceOps        []ResourceOp           `json:"resource_ops"`
	LocalState         []ProjectLocalState    `json:"local_state"`
	ProgramTargets     []ProjectProgramTarget `json:"program_targets"`
	SharedState        ProjectSharedState     `json:"shared_state"`
}

func (m *ProjectManifest) requiredJSONFields() []string {
	return []string{"schema_version", "scope", "summary", "resource_ops", "program_targets", "shared_state"}
}

// Validate ports validate_manifest (field constraints and the after validator).
func (m *ProjectManifest) Validate() error {
	if err := validateLiteral("/schema_version", itoa(m.SchemaVersion), itoa(ManifestSchemaVersion)); err != nil {
		return err
	}
	if err := validateLiteral("/scope", m.Scope, "project"); err != nil {
		return err
	}
	if err := requireStringLength("/summary", m.Summary, 1, MaxSummaryLength); err != nil {
		return err
	}
	if err := validateShortStringList("/assumptions", m.Assumptions, 10); err != nil {
		return err
	}
	if err := requireListLength("/required_extensions", len(m.RequiredExtensions), -1, 3); err != nil {
		return err
	}
	for index, extension := range m.RequiredExtensions {
		if err := validateLiteral("/required_extensions/"+itoa(index), extension, "pen", "music", "text2speech"); err != nil {
			return err
		}
	}
	if err := requireListLength("/resource_ops", len(m.ResourceOps), -1, 24); err != nil {
		return err
	}
	for index := range m.ResourceOps {
		normalizedValue, _, err := NormalizeResourceOp(m.ResourceOps[index])
		if err != nil {
			return err
		}
		normalized, ok := normalizedValue.(map[string]any)
		if !ok {
			return validationError("/resource_ops", "Input should be a valid dictionary")
		}
		dumped, err := decodeResourceOperationDump(normalized)
		if err != nil {
			return err
		}
		m.ResourceOps[index] = dumped
	}
	if err := requireListLength("/local_state", len(m.LocalState), -1, 100); err != nil {
		return err
	}
	for index := range m.LocalState {
		if err := m.LocalState[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/program_targets", len(m.ProgramTargets), -1, 12); err != nil {
		return err
	}
	for index := range m.ProgramTargets {
		if err := m.ProgramTargets[index].Validate(); err != nil {
			return err
		}
	}
	if err := m.SharedState.Validate(); err != nil {
		return err
	}
	return m.validateManifest()
}

func (m *ProjectManifest) validateManifest() error {
	settings := GetDeploymentSettings()
	enabledExtensions := settings.EnabledAIExtensions()
	if len(m.RequiredExtensions) != len(stringSet(m.RequiredExtensions)) {
		return errors.New("required_extensions must be unique")
	}
	for _, extension := range m.RequiredExtensions {
		if !enabledExtensions[extension] {
			return errors.New("required_extensions contains an extension disabled by this deployment")
		}
	}
	if !settings.AIDestructiveResourceOpsEnabled {
		for _, operation := range m.ResourceOps {
			switch resourceOpName(operation) {
			case "delete_sprite", "delete_sound":
				return errors.New("destructive resource operations are disabled by this deployment")
			}
		}
	}
	if !settings.AIProjectAssetsEnabled && len(m.ResourceOps) > 0 {
		return errors.New("project asset operations are disabled by this deployment")
	}
	if !settings.AISoundAssetsEnabled {
		for _, operation := range m.ResourceOps {
			if resourceOpName(operation) == "add_sound" {
				return errors.New("sound asset operations are disabled by this deployment")
			}
		}
	}
	if !settings.AIAudioGenerationEnabled {
		for _, operation := range m.ResourceOps {
			if resourceOpName(operation) != "add_sound" {
				continue
			}
			if source, ok := operation["source"].(map[string]any); ok && source["kind"] == "audio_generation" {
				return errors.New("audio generation is disabled by this deployment")
			}
		}
	}
	ids := map[string]bool{}
	created := map[string]bool{}
	for _, operation := range m.ResourceOps {
		opID, _ := operation["op_id"].(string)
		if ids[opID] {
			return errors.New("resource op_id values must be unique")
		}
		ids[opID] = true
		switch resourceOpName(operation) {
		case "add_sprite", "add_builtin_sprite":
			created[opID] = true
		}
		if target, ok := operation["target"].(map[string]any); ok {
			if target["kind"] == "created_sprite" {
				targetOpID, _ := target["op_id"].(string)
				if !created[targetOpID] {
					return errors.New("created_sprite must reference a prior add_sprite or add_builtin_sprite operation")
				}
			}
		}
	}
	keys := make([]programTargetKey, 0, len(m.ProgramTargets))
	jobIDs := make([]string, 0, len(m.ProgramTargets))
	for index := range m.ProgramTargets {
		target := &m.ProgramTargets[index]
		keys = append(keys, programTargetKey{
			Kind: target.Target.Kind,
			Name: stringValue(target.Target.Name),
			OpID: stringValue(target.Target.OpID),
		})
		jobIDs = append(jobIDs, target.JobID)
	}
	if len(keys) != len(uniqueProgramTargetKeys(keys)) {
		return errors.New("program targets must be unique")
	}
	if len(jobIDs) != len(stringSet(jobIDs)) {
		return errors.New("program job_id values must be unique")
	}
	for index := range m.ProgramTargets {
		target := &m.ProgramTargets[index]
		if target.Target.Kind == "created_sprite" && !created[stringValue(target.Target.OpID)] {
			return errors.New("created_sprite must reference a prior add_sprite or add_builtin_sprite operation")
		}
	}
	for index := range m.ProgramTargets {
		target := &m.ProgramTargets[index]
		switch target.Target.Kind {
		case "stage", "existing_sprite", "created_sprite":
			if target.Mode != "append" {
				return errors.New("stage, existing_sprite and created_sprite programs must use append")
			}
		}
	}
	localRefs := make([]string, 0, len(m.LocalState))
	for index := range m.LocalState {
		localRefs = append(localRefs, m.LocalState[index].RefID)
	}
	sharedRefs := projectSharedStateRefs(&m.SharedState)
	if len(localRefs) != len(stringSet(localRefs)) {
		return errors.New("local state ref_id values must be unique and distinct from shared state")
	}
	sharedSet := map[string]bool{}
	for _, ref := range sharedRefs {
		sharedSet[ref] = true
	}
	for _, ref := range localRefs {
		if sharedSet[ref] {
			return errors.New("local state ref_id values must be unique and distinct from shared state")
		}
	}
	return nil
}

type programTargetKey struct {
	Kind string
	Name string
	OpID string
}

func uniqueProgramTargetKeys(keys []programTargetKey) []programTargetKey {
	seen := map[programTargetKey]bool{}
	result := make([]programTargetKey, 0, len(keys))
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, key)
	}
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func resourceOpName(operation ResourceOp) string {
	name, _ := operation["op"].(string)
	return name
}

// ProjectProgramResponse ports ProjectProgramResponse.
type ProjectProgramResponse struct {
	Target  ProjectProgramTarget `json:"target"`
	Program ProgramIR            `json:"program"`
}

func (m *ProjectProgramResponse) requiredJSONFields() []string {
	return []string{"target", "program"}
}

// Validate ports ProjectProgramResponse validation.
func (m *ProjectProgramResponse) Validate() error {
	if err := m.Target.Validate(); err != nil {
		return err
	}
	return m.Program.Validate()
}

// UnifiedGenerationRequest ports UnifiedGenerationRequest.
type UnifiedGenerationRequest struct {
	PipelineVersion     string           `json:"pipeline_version,omitempty"`
	ModelTier           ModelTier        `json:"model_tier" default:"normal"`
	SessionID           string           `json:"session_id"`
	RequestID           string           `json:"request_id"`
	Messages            []ChatMessage    `json:"messages"`
	Target              TargetContext    `json:"target"`
	Thinking            ThinkingSettings `json:"thinking"`
	Scope               string           `json:"scope" default:"single_sprite"`
	AssetPolicy         string           `json:"asset_policy" default:"builtin_first"`
	ProjectContext      *ProjectContext  `json:"project_context"`
	ProceedWithDefaults bool             `json:"proceed_with_defaults" default:"true"`
	PermittedExtensions []string         `json:"permitted_extensions"`

	// OutputContract carries the subclass discriminator that Python reads as
	// request.output_contract. It is deliberately NOT a JSON field here: the Python
	// UnifiedGenerationRequest is a StrictModel, so a plain unified request that
	// sends output_contract must still be rejected as an unknown field. The
	// ProjectManifestGenerationRequest / ProjectProgramGenerationRequest subtypes
	// own the wire field and copy it in through AsUnifiedGenerationRequest.
	OutputContract string `json:"-" `
}

func (m *UnifiedGenerationRequest) requiredJSONFields() []string {
	return []string{"session_id", "request_id", "messages", "target"}
}

// Validate ports UnifiedGenerationRequest validation.
func (m *UnifiedGenerationRequest) Validate() error {
	if m.PipelineVersion != "" && m.PipelineVersion != "scratch-source/1" {
		return errors.New("unsupported pipeline_version")
	}
	base := AIBlocksRequestBase{
		ModelTier: m.ModelTier, SessionID: m.SessionID, RequestID: m.RequestID,
		Messages: m.Messages, Target: m.Target, Thinking: m.Thinking,
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if err := validateUnifiedGenerationFields(m.Scope, m.AssetPolicy, m.ProjectContext, m.PermittedExtensions); err != nil {
		return err
	}
	return validateUnifiedGenerationAfter(m.Scope, m.ProjectContext)
}

func validateUnifiedGenerationFields(scope, assetPolicy string, projectContext *ProjectContext, permittedExtensions []string) error {
	if err := validateLiteral("/scope", scope, "single_sprite", "project"); err != nil {
		return err
	}
	if err := validateLiteral("/asset_policy", assetPolicy, "builtin_first", "existing_only", "allow_generation"); err != nil {
		return err
	}
	if projectContext != nil {
		if err := projectContext.Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/permitted_extensions", len(permittedExtensions), -1, 1); err != nil {
		return err
	}
	for index, extension := range permittedExtensions {
		if err := validateLiteral("/permitted_extensions/"+itoa(index), extension, "text2speech"); err != nil {
			return err
		}
	}
	return nil
}

func validateUnifiedGenerationAfter(scope string, projectContext *ProjectContext) error {
	if scope == "project" && projectContext == nil {
		return errors.New("project_context is required for project generation")
	}
	if scope == "single_sprite" && projectContext != nil {
		return errors.New("project_context is only valid for project generation")
	}
	if projectContext != nil {
		encoded, err := jsonx.Marshal(projectContext)
		if err != nil {
			return err
		}
		if len(encoded) > ProjectContextSoftBudgetBytes {
			return errors.New("project_context exceeds the 64 KiB soft budget")
		}
	}
	return nil
}

// ProjectManifestGenerationRequest ports ProjectManifestGenerationRequest.
type ProjectManifestGenerationRequest struct {
	PipelineVersion     string           `json:"pipeline_version,omitempty"`
	ModelTier           ModelTier        `json:"model_tier" default:"normal"`
	SessionID           string           `json:"session_id"`
	RequestID           string           `json:"request_id"`
	Messages            []ChatMessage    `json:"messages"`
	Target              TargetContext    `json:"target"`
	Thinking            ThinkingSettings `json:"thinking"`
	Scope               string           `json:"scope" default:"single_sprite"`
	AssetPolicy         string           `json:"asset_policy" default:"builtin_first"`
	ProjectContext      *ProjectContext  `json:"project_context"`
	ProceedWithDefaults bool             `json:"proceed_with_defaults" default:"true"`
	PermittedExtensions []string         `json:"permitted_extensions"`
	OutputContract      string           `json:"output_contract" default:"project_manifest"`
}

func (m *ProjectManifestGenerationRequest) requiredJSONFields() []string {
	return []string{"session_id", "request_id", "messages", "target"}
}

// Validate ports ProjectManifestGenerationRequest validation.
func (m *ProjectManifestGenerationRequest) Validate() error {
	if m.PipelineVersion != "" && m.PipelineVersion != "scratch-source/1" {
		return errors.New("unsupported pipeline_version")
	}
	base := AIBlocksRequestBase{
		ModelTier: m.ModelTier, SessionID: m.SessionID, RequestID: m.RequestID,
		Messages: m.Messages, Target: m.Target, Thinking: m.Thinking,
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if err := validateUnifiedGenerationFields(m.Scope, m.AssetPolicy, m.ProjectContext, m.PermittedExtensions); err != nil {
		return err
	}
	if err := validateLiteral("/output_contract", m.OutputContract, "project_manifest"); err != nil {
		return err
	}
	if err := validateUnifiedGenerationAfter(m.Scope, m.ProjectContext); err != nil {
		return err
	}
	if m.Scope != "project" {
		return errors.New("project_manifest requires project scope")
	}
	return nil
}

// ProjectProgramGenerationRequest ports ProjectProgramGenerationRequest.
type ProjectProgramGenerationRequest struct {
	ModelTier           ModelTier            `json:"model_tier" default:"normal"`
	SessionID           string               `json:"session_id"`
	RequestID           string               `json:"request_id"`
	Messages            []ChatMessage        `json:"messages"`
	Target              TargetContext        `json:"target"`
	Thinking            ThinkingSettings     `json:"thinking"`
	Scope               string               `json:"scope" default:"single_sprite"`
	AssetPolicy         string               `json:"asset_policy" default:"builtin_first"`
	ProjectContext      *ProjectContext      `json:"project_context"`
	ProceedWithDefaults bool                 `json:"proceed_with_defaults" default:"true"`
	PermittedExtensions []string             `json:"permitted_extensions"`
	OutputContract      string               `json:"output_contract" default:"project_program"`
	Requirements        RequirementsResponse `json:"requirements"`
	TaskSpec            ScratchTaskSpec      `json:"task_spec"`
	Blueprint           ProjectBlueprint     `json:"blueprint"`
	Plan                Plan                 `json:"plan"`
	Manifest            ProjectManifest      `json:"manifest"`
	ProgramTarget       ProjectProgramTarget `json:"program_target"`
	TargetIndex         int                  `json:"target_index"`
}

func (m *ProjectProgramGenerationRequest) requiredJSONFields() []string {
	return []string{
		"session_id", "request_id", "messages", "target",
		"requirements", "task_spec", "blueprint", "plan", "manifest", "program_target", "target_index",
	}
}

// Validate ports ProjectProgramGenerationRequest validation.
func (m *ProjectProgramGenerationRequest) Validate() error {
	base := AIBlocksRequestBase{
		ModelTier: m.ModelTier, SessionID: m.SessionID, RequestID: m.RequestID,
		Messages: m.Messages, Target: m.Target, Thinking: m.Thinking,
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if err := validateUnifiedGenerationFields(m.Scope, m.AssetPolicy, m.ProjectContext, m.PermittedExtensions); err != nil {
		return err
	}
	if err := validateLiteral("/output_contract", m.OutputContract, "project_program"); err != nil {
		return err
	}
	if err := m.Requirements.Validate(); err != nil {
		return err
	}
	if err := m.TaskSpec.Validate(); err != nil {
		return err
	}
	if err := m.Blueprint.Validate(); err != nil {
		return err
	}
	if err := m.Plan.Validate(); err != nil {
		return err
	}
	if err := m.Manifest.Validate(); err != nil {
		return err
	}
	if err := m.ProgramTarget.Validate(); err != nil {
		return err
	}
	if err := requireIntMin("/target_index", m.TargetIndex, 0); err != nil {
		return err
	}
	if err := validateUnifiedGenerationAfter(m.Scope, m.ProjectContext); err != nil {
		return err
	}
	if m.Scope != "project" {
		return errors.New("project_program requires project scope")
	}
	if m.TargetIndex >= len(m.Manifest.ProgramTargets) {
		return errors.New("target_index is outside manifest.program_targets")
	}
	if !reflect.DeepEqual(m.Manifest.ProgramTargets[m.TargetIndex], m.ProgramTarget) {
		return errors.New("program_target must match manifest.program_targets[target_index]")
	}
	if err := ValidateTaskSpecBlueprintLineage(&m.TaskSpec, &m.Blueprint); err != nil {
		return err
	}
	return ValidateBlueprintManifestLineage(&m.Blueprint, &m.Manifest)
}

// DecodeProjectGenerationRequest decodes the output_contract discriminated
// union (ProjectGenerationRequest).
func DecodeProjectGenerationRequest(raw []byte) (ProjectGenerationRequest, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &SchemaValidationError{Message: "Input should be a valid dictionary"}
	}
	var probe struct {
		OutputContract string `json:"output_contract"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	if probe.OutputContract == "project_program" {
		target := &ProjectProgramGenerationRequest{}
		if err := DecodeStrict(raw, target); err != nil {
			return nil, err
		}
		return target, nil
	}
	target := &ProjectManifestGenerationRequest{}
	if err := DecodeStrict(raw, target); err != nil {
		return nil, err
	}
	return target, nil
}

// ValidationRequest ports ValidationRequest.
type ValidationRequest struct {
	Kind           string         `json:"kind"`
	Payload        map[string]any `json:"payload"`
	TargetKind     string         `json:"target_kind" default:"sprite"`
	Mode           string         `json:"mode" default:"append"`
	CurrentProgram string         `json:"current_program"`
	fieldsSet      map[string]bool
}

func (m *ValidationRequest) requiredJSONFields() []string { return []string{"kind", "payload"} }

func (m *ValidationRequest) recordJSONFields(object map[string]any) {
	m.fieldsSet = make(map[string]bool, len(object))
	for key := range object {
		m.fieldsSet[key] = true
	}
}

// Validate ports validate_endpoint_contract.
func (m *ValidationRequest) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "program", "manifest"); err != nil {
		return err
	}
	if err := validateLiteral("/target_kind", m.TargetKind, "stage", "sprite"); err != nil {
		return err
	}
	if err := validateLiteral("/mode", m.Mode, "modify", "append", "rewrite"); err != nil {
		return err
	}
	if err := requireStringLength("/current_program", m.CurrentProgram, -1, MaxCurrentProgramLength); err != nil {
		return err
	}
	if m.CurrentProgram != "" {
		parsed, err := jsonx.Parse([]byte(m.CurrentProgram))
		if err != nil {
			return errors.New("current_program must be valid JSON")
		}
		if _, ok := parsed.(map[string]any); !ok {
			return errors.New("current_program must contain a JSON object")
		}
	}
	if m.Kind == "manifest" {
		if m.fieldsSet["target_kind"] || m.fieldsSet["mode"] || m.fieldsSet["current_program"] {
			return errors.New("manifest validation does not accept program-only fields")
		}
	}
	return nil
}

// GenerationRequest is implemented by every unified-generation request model.
// The Python classes form an inheritance chain (ProjectManifestGenerationRequest
// and ProjectProgramGenerationRequest extend UnifiedGenerationRequest); Go has no
// pointer covariance, so callers that need the shared request surface use this
// interface or the AsUnifiedGenerationRequest conversions.
type GenerationRequest interface {
	AsUnifiedGenerationRequest() *UnifiedGenerationRequest
}

// AsUnifiedGenerationRequest returns the request itself.
func (m *UnifiedGenerationRequest) AsUnifiedGenerationRequest() *UnifiedGenerationRequest {
	return m
}

// AsUnifiedGenerationRequest projects the manifest request onto the shared
// UnifiedGenerationRequest fields (the Python base-class view).
func (m *ProjectManifestGenerationRequest) AsUnifiedGenerationRequest() *UnifiedGenerationRequest {
	return &UnifiedGenerationRequest{
		PipelineVersion:     m.PipelineVersion,
		OutputContract:      m.OutputContract,
		ModelTier:           m.ModelTier,
		SessionID:           m.SessionID,
		RequestID:           m.RequestID,
		Messages:            m.Messages,
		Target:              m.Target,
		Thinking:            m.Thinking,
		Scope:               m.Scope,
		AssetPolicy:         m.AssetPolicy,
		ProjectContext:      m.ProjectContext,
		ProceedWithDefaults: m.ProceedWithDefaults,
		PermittedExtensions: m.PermittedExtensions,
	}
}

// AsUnifiedGenerationRequest projects the program request onto the shared
// UnifiedGenerationRequest fields (the Python base-class view).
func (m *ProjectProgramGenerationRequest) AsUnifiedGenerationRequest() *UnifiedGenerationRequest {
	return &UnifiedGenerationRequest{
		OutputContract:      m.OutputContract,
		ModelTier:           m.ModelTier,
		SessionID:           m.SessionID,
		RequestID:           m.RequestID,
		Messages:            m.Messages,
		Target:              m.Target,
		Thinking:            m.Thinking,
		Scope:               m.Scope,
		AssetPolicy:         m.AssetPolicy,
		ProjectContext:      m.ProjectContext,
		ProceedWithDefaults: m.ProceedWithDefaults,
		PermittedExtensions: m.PermittedExtensions,
	}
}

// ValidateRequestExtensionPermissions ports validate_request_extension_permissions.
func ValidateRequestExtensionPermissions(request *UnifiedGenerationRequest, manifest *ProjectManifest) error {
	if containsString(manifest.RequiredExtensions, "text2speech") && !containsString(request.PermittedExtensions, "text2speech") {
		return errors.New("text2speech requires explicit request permission")
	}
	return nil
}
