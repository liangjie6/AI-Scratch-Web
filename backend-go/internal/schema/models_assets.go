package schema

import (
	"errors"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

func copyAnyMap(value map[string]any) map[string]any {
	copied := make(map[string]any, len(value))
	for key, item := range value {
		copied[key] = item
	}
	return copied
}

// ScratchLibrarySource ports ScratchLibrarySource.
type ScratchLibrarySource struct {
	Kind        string   `json:"kind"`
	AssetType   string   `json:"asset_type"`
	SearchTerms []string `json:"search_terms"`
}

func (m *ScratchLibrarySource) requiredJSONFields() []string {
	return []string{"kind", "asset_type", "search_terms"}
}

// Validate ports ScratchLibrarySource validation.
func (m *ScratchLibrarySource) Validate() error {
	return validateScratchLibrarySource("/", m.Kind, m.AssetType, m.SearchTerms,
		"sprite", "costume", "backdrop", "sound")
}

func validateScratchLibrarySource(_ string, kind, assetType string, searchTerms []string, allowedAssetTypes ...string) error {
	if err := validateLiteral("/kind", kind, "scratch_library"); err != nil {
		return err
	}
	if err := validateLiteral("/asset_type", assetType, allowedAssetTypes...); err != nil {
		return err
	}
	if err := requireListLength("/search_terms", len(searchTerms), 1, 12); err != nil {
		return err
	}
	for index, term := range searchTerms {
		if err := validateShortString("/search_terms/"+itoa(index), term); err != nil {
			return err
		}
	}
	return nil
}

// ScratchSpriteLibrarySource ports ScratchSpriteLibrarySource.
type ScratchSpriteLibrarySource struct {
	Kind        string   `json:"kind"`
	AssetType   string   `json:"asset_type"`
	SearchTerms []string `json:"search_terms"`
}

func (m *ScratchSpriteLibrarySource) requiredJSONFields() []string {
	return []string{"kind", "asset_type", "search_terms"}
}

// Validate ports ScratchSpriteLibrarySource validation.
func (m *ScratchSpriteLibrarySource) Validate() error {
	return validateScratchLibrarySource("/", m.Kind, m.AssetType, m.SearchTerms, "sprite")
}

// ScratchCostumeLibrarySource ports ScratchCostumeLibrarySource.
type ScratchCostumeLibrarySource struct {
	Kind        string   `json:"kind"`
	AssetType   string   `json:"asset_type"`
	SearchTerms []string `json:"search_terms"`
}

func (m *ScratchCostumeLibrarySource) requiredJSONFields() []string {
	return []string{"kind", "asset_type", "search_terms"}
}

// Validate ports ScratchCostumeLibrarySource validation.
func (m *ScratchCostumeLibrarySource) Validate() error {
	return validateScratchLibrarySource("/", m.Kind, m.AssetType, m.SearchTerms, "costume")
}

// ScratchBackdropLibrarySource ports ScratchBackdropLibrarySource.
type ScratchBackdropLibrarySource struct {
	Kind        string   `json:"kind"`
	AssetType   string   `json:"asset_type"`
	SearchTerms []string `json:"search_terms"`
}

func (m *ScratchBackdropLibrarySource) requiredJSONFields() []string {
	return []string{"kind", "asset_type", "search_terms"}
}

// Validate ports ScratchBackdropLibrarySource validation.
func (m *ScratchBackdropLibrarySource) Validate() error {
	return validateScratchLibrarySource("/", m.Kind, m.AssetType, m.SearchTerms, "backdrop")
}

// ScratchSoundLibrarySource ports ScratchSoundLibrarySource.
type ScratchSoundLibrarySource struct {
	Kind        string   `json:"kind"`
	AssetType   string   `json:"asset_type"`
	SearchTerms []string `json:"search_terms"`
}

func (m *ScratchSoundLibrarySource) requiredJSONFields() []string {
	return []string{"kind", "asset_type", "search_terms"}
}

// Validate ports ScratchSoundLibrarySource validation.
func (m *ScratchSoundLibrarySource) Validate() error {
	return validateScratchLibrarySource("/", m.Kind, m.AssetType, m.SearchTerms, "sound")
}

// ImageGenerationSource ports ImageGenerationSource.
type ImageGenerationSource struct {
	Kind        string  `json:"kind"`
	Reason      string  `json:"reason"`
	Prompt      string  `json:"prompt"`
	Style       string  `json:"style"`
	CustomStyle *string `json:"custom_style"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	SheetLayout string  `json:"sheet_layout"`
}

func (m *ImageGenerationSource) requiredJSONFields() []string {
	return []string{"kind", "reason", "prompt", "style", "width", "height", "sheet_layout"}
}

// Validate ports ImageGenerationSource validation.
func (m *ImageGenerationSource) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "image_generation"); err != nil {
		return err
	}
	if err := validateShortString("/reason", m.Reason); err != nil {
		return err
	}
	if err := validateLongString("/prompt", m.Prompt); err != nil {
		return err
	}
	if err := validateShortString("/style", m.Style); err != nil {
		return err
	}
	if err := validateOptionalShortString("/custom_style", m.CustomStyle); err != nil {
		return err
	}
	if err := requireIntRange("/width", m.Width, 1, 2000); err != nil {
		return err
	}
	if err := requireIntRange("/height", m.Height, 1, 2000); err != nil {
		return err
	}
	if err := validateLiteral("/sheet_layout", m.SheetLayout, "1x1"); err != nil {
		return err
	}
	if !AllowedSizes[[2]int{m.Width, m.Height}] {
		return errors.New("width and height must be an allowed image size pair")
	}
	return nil
}

// AudioGenerationSource ports AudioGenerationSource.
type AudioGenerationSource struct {
	Kind               string  `json:"kind"`
	Reason             string  `json:"reason"`
	Prompt             string  `json:"prompt"`
	AudioKind          string  `json:"audio_kind"`
	MaxDurationSeconds float64 `json:"max_duration_seconds"`
}

func (m *AudioGenerationSource) requiredJSONFields() []string {
	return []string{"kind", "reason", "prompt", "audio_kind", "max_duration_seconds"}
}

// Validate ports AudioGenerationSource validation.
func (m *AudioGenerationSource) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "audio_generation"); err != nil {
		return err
	}
	if err := validateShortString("/reason", m.Reason); err != nil {
		return err
	}
	if err := validateLongString("/prompt", m.Prompt); err != nil {
		return err
	}
	if err := validateLiteral("/audio_kind", m.AudioKind, "sound_effect", "speech", "music"); err != nil {
		return err
	}
	if m.MaxDurationSeconds <= 0 {
		return validationError("/max_duration_seconds", "Input should be greater than 0")
	}
	return requireFloatMax("/max_duration_seconds", m.MaxDurationSeconds, 60)
}

// ValidateCostumeSource ports the CostumeSource discriminated union.
func ValidateCostumeSource(path string, value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return validationError(path, "Input should be a valid dictionary")
	}
	kind, _ := object["kind"].(string)
	switch kind {
	case "scratch_library":
		target, err := decodeAny[ScratchCostumeLibrarySource](object)
		if err != nil {
			return err
		}
		return target.Validate()
	case "image_generation":
		target, err := decodeAny[ImageGenerationSource](object)
		if err != nil {
			return err
		}
		return target.Validate()
	default:
		return validationError(path, "Input tag '"+kind+"' found using 'kind' does not match any of the expected tags: 'scratch_library', 'image_generation'")
	}
}

// ValidateBackdropSource ports the BackdropSource discriminated union.
func ValidateBackdropSource(path string, value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return validationError(path, "Input should be a valid dictionary")
	}
	kind, _ := object["kind"].(string)
	switch kind {
	case "scratch_library":
		target, err := decodeAny[ScratchBackdropLibrarySource](object)
		if err != nil {
			return err
		}
		return target.Validate()
	case "image_generation":
		target, err := decodeAny[ImageGenerationSource](object)
		if err != nil {
			return err
		}
		return target.Validate()
	default:
		return validationError(path, "Input tag '"+kind+"' found using 'kind' does not match any of the expected tags: 'scratch_library', 'image_generation'")
	}
}

// ValidateSoundSource ports the SoundSource discriminated union.
func ValidateSoundSource(path string, value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return validationError(path, "Input should be a valid dictionary")
	}
	kind, _ := object["kind"].(string)
	switch kind {
	case "scratch_library":
		target, err := decodeAny[ScratchSoundLibrarySource](object)
		if err != nil {
			return err
		}
		return target.Validate()
	case "audio_generation":
		target, err := decodeAny[AudioGenerationSource](object)
		if err != nil {
			return err
		}
		return target.Validate()
	default:
		return validationError(path, "Input tag '"+kind+"' found using 'kind' does not match any of the expected tags: 'scratch_library', 'audio_generation'")
	}
}

// CostumeResource ports CostumeResource.
type CostumeResource struct {
	Name   string   `json:"name"`
	Source any      `json:"source"`
	Tags   []string `json:"tags"`
}

func (m *CostumeResource) requiredJSONFields() []string { return []string{"name", "source"} }

// Validate ports CostumeResource validation.
func (m *CostumeResource) Validate() error {
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := ValidateCostumeSource("/source", m.Source); err != nil {
		return err
	}
	return validateShortStringList("/tags", m.Tags, 20)
}

// AddBuiltinSpriteOp ports AddBuiltinSpriteOp.
type AddBuiltinSpriteOp struct {
	Op              string                     `json:"op"`
	OpID            string                     `json:"op_id"`
	SourceNeedID    string                     `json:"source_need_id"`
	OwnerEntityID   string                     `json:"owner_entity_id"`
	Name            string                     `json:"name"`
	X               int                        `json:"x"`
	Y               int                        `json:"y"`
	Size            int                        `json:"size"`
	Tags            []string                   `json:"tags"`
	Source          ScratchSpriteLibrarySource `json:"source"`
	IncludeCostumes string                     `json:"include_costumes"`
	IncludeSounds   string                     `json:"include_sounds" default:"all"`
}

func (m *AddBuiltinSpriteOp) requiredJSONFields() []string {
	return []string{"op", "op_id", "source_need_id", "owner_entity_id", "name", "x", "y", "size", "source", "include_costumes"}
}

// Validate ports AddBuiltinSpriteOp validation.
func (m *AddBuiltinSpriteOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "add_builtin_sprite"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if err := validateShortString("/source_need_id", m.SourceNeedID); err != nil {
		return err
	}
	if err := validateShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := requireIntRange("/x", m.X, -10000, 10000); err != nil {
		return err
	}
	if err := requireIntRange("/y", m.Y, -10000, 10000); err != nil {
		return err
	}
	if err := requireIntRange("/size", m.Size, 1, 1000); err != nil {
		return err
	}
	if err := validateShortStringList("/tags", m.Tags, 20); err != nil {
		return err
	}
	if err := m.Source.Validate(); err != nil {
		return err
	}
	if err := validateLiteral("/include_costumes", m.IncludeCostumes, "all"); err != nil {
		return err
	}
	return validateLiteral("/include_sounds", m.IncludeSounds, "all", "none")
}

// AddSpriteOp ports AddSpriteOp.
type AddSpriteOp struct {
	Op            string            `json:"op"`
	OpID          string            `json:"op_id"`
	SourceNeedID  string            `json:"source_need_id"`
	OwnerEntityID string            `json:"owner_entity_id"`
	Name          string            `json:"name"`
	X             int               `json:"x"`
	Y             int               `json:"y"`
	Size          int               `json:"size"`
	Tags          []string          `json:"tags"`
	Costumes      []CostumeResource `json:"costumes"`
}

func (m *AddSpriteOp) requiredJSONFields() []string {
	return []string{"op", "op_id", "source_need_id", "owner_entity_id", "name", "x", "y", "size", "costumes"}
}

// Validate ports AddSpriteOp validation.
func (m *AddSpriteOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "add_sprite"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if err := validateShortString("/source_need_id", m.SourceNeedID); err != nil {
		return err
	}
	if err := validateShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := requireIntRange("/x", m.X, -10000, 10000); err != nil {
		return err
	}
	if err := requireIntRange("/y", m.Y, -10000, 10000); err != nil {
		return err
	}
	if err := requireIntRange("/size", m.Size, 1, 1000); err != nil {
		return err
	}
	if err := validateShortStringList("/tags", m.Tags, 20); err != nil {
		return err
	}
	if err := requireListLength("/costumes", len(m.Costumes), 1, 12); err != nil {
		return err
	}
	for index := range m.Costumes {
		if err := m.Costumes[index].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DeleteSpriteOp ports DeleteSpriteOp.
type DeleteSpriteOp struct {
	Op     string    `json:"op"`
	OpID   string    `json:"op_id"`
	Name   string    `json:"name"`
	Target TargetRef `json:"target"`
}

func (m *DeleteSpriteOp) requiredJSONFields() []string {
	return []string{"op", "op_id", "name", "target"}
}

// Validate ports DeleteSpriteOp.validate_target.
func (m *DeleteSpriteOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "delete_sprite"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := m.Target.Validate(); err != nil {
		return err
	}
	if m.Target.Kind != "existing_sprite" {
		return errors.New("delete_sprite requires an existing_sprite target")
	}
	return nil
}

// AddCostumeOp ports AddCostumeOp.
type AddCostumeOp struct {
	Op           string    `json:"op"`
	OpID         string    `json:"op_id"`
	SourceNeedID string    `json:"source_need_id"`
	Target       TargetRef `json:"target"`
	Name         string    `json:"name"`
	Source       any       `json:"source"`
	Tags         []string  `json:"tags"`
	SelectAfter  bool      `json:"select_after"`
}

func (m *AddCostumeOp) requiredJSONFields() []string {
	return []string{"op", "op_id", "source_need_id", "target", "name", "source"}
}

// Validate ports AddCostumeOp.validate_target.
func (m *AddCostumeOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "add_costume"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if err := validateShortString("/source_need_id", m.SourceNeedID); err != nil {
		return err
	}
	if err := m.Target.Validate(); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := ValidateCostumeSource("/source", m.Source); err != nil {
		return err
	}
	if err := validateShortStringList("/tags", m.Tags, 20); err != nil {
		return err
	}
	if m.Target.Kind == "stage" {
		return errors.New("add_costume cannot target the stage")
	}
	return nil
}

// AddBackdropOp ports AddBackdropOp.
type AddBackdropOp struct {
	Op           string   `json:"op"`
	OpID         string   `json:"op_id"`
	SourceNeedID string   `json:"source_need_id"`
	Name         string   `json:"name"`
	Source       any      `json:"source"`
	Tags         []string `json:"tags"`
	SelectAfter  bool     `json:"select_after"`
}

func (m *AddBackdropOp) requiredJSONFields() []string {
	return []string{"op", "op_id", "source_need_id", "name", "source"}
}

// Validate ports AddBackdropOp validation.
func (m *AddBackdropOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "add_backdrop"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if err := validateShortString("/source_need_id", m.SourceNeedID); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := ValidateBackdropSource("/source", m.Source); err != nil {
		return err
	}
	return validateShortStringList("/tags", m.Tags, 20)
}

// AddSoundOp ports AddSoundOp.
type AddSoundOp struct {
	Op           string    `json:"op"`
	OpID         string    `json:"op_id"`
	SourceNeedID string    `json:"source_need_id"`
	Target       TargetRef `json:"target"`
	Name         string    `json:"name"`
	Source       any       `json:"source"`
	Tags         []string  `json:"tags"`
}

func (m *AddSoundOp) requiredJSONFields() []string {
	return []string{"op", "op_id", "source_need_id", "target", "name", "source"}
}

// Validate ports AddSoundOp validation.
func (m *AddSoundOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "add_sound"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if err := validateShortString("/source_need_id", m.SourceNeedID); err != nil {
		return err
	}
	if err := m.Target.Validate(); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := ValidateSoundSource("/source", m.Source); err != nil {
		return err
	}
	return validateShortStringList("/tags", m.Tags, 20)
}

// SelectBackdropOp ports SelectBackdropOp.
type SelectBackdropOp struct {
	Op   string `json:"op"`
	OpID string `json:"op_id"`
	Name string `json:"name"`
}

func (m *SelectBackdropOp) requiredJSONFields() []string { return []string{"op", "op_id", "name"} }

// Validate ports SelectBackdropOp validation.
func (m *SelectBackdropOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "select_backdrop"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	return validateShortString("/name", m.Name)
}

// ExistingSoundRef ports ExistingSoundRef.
type ExistingSoundRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (m *ExistingSoundRef) requiredJSONFields() []string { return []string{"kind", "name"} }

// Validate ports ExistingSoundRef validation.
func (m *ExistingSoundRef) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "existing_sound"); err != nil {
		return err
	}
	return validateShortString("/name", m.Name)
}

// DeleteSoundOp ports DeleteSoundOp.
type DeleteSoundOp struct {
	Op     string           `json:"op"`
	OpID   string           `json:"op_id"`
	Target TargetRef        `json:"target"`
	Sound  ExistingSoundRef `json:"sound"`
}

func (m *DeleteSoundOp) requiredJSONFields() []string {
	return []string{"op", "op_id", "target", "sound"}
}

// Validate ports DeleteSoundOp validation.
func (m *DeleteSoundOp) Validate() error {
	if err := validateLiteral("/op", m.Op, "delete_sound"); err != nil {
		return err
	}
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if err := m.Target.Validate(); err != nil {
		return err
	}
	return m.Sound.Validate()
}

// resourceOpTags are the discriminator tags of the ResourceOp union, in the
// Python union order.
var resourceOpTags = []string{
	"add_builtin_sprite", "add_sprite", "delete_sprite", "add_costume",
	"add_backdrop", "add_sound", "delete_sound", "select_backdrop",
}

// decodeResourceOperationDump validates one normalized resource operation and
// returns the pydantic model_dump-shaped map (defaults applied, None omitted
// because no resource operation field is optional).
func decodeResourceOperationDump(raw ResourceOp) (ResourceOp, error) {
	if raw == nil {
		return nil, validationError("/resource_ops", "Input should be a valid dictionary")
	}
	name := resourceOpName(raw)
	var target any
	switch name {
	case "add_builtin_sprite":
		target = &AddBuiltinSpriteOp{}
	case "add_sprite":
		target = &AddSpriteOp{}
	case "delete_sprite":
		target = &DeleteSpriteOp{}
	case "add_costume":
		target = &AddCostumeOp{}
	case "add_backdrop":
		target = &AddBackdropOp{}
	case "add_sound":
		target = &AddSoundOp{}
	case "delete_sound":
		target = &DeleteSoundOp{}
	case "select_backdrop":
		target = &SelectBackdropOp{}
	default:
		return nil, validationError("/op", "Input tag '"+name+"' found using 'op' does not match any of the expected tags: 'add_builtin_sprite', 'add_sprite', 'delete_sprite', 'add_costume', 'add_backdrop', 'add_sound', 'delete_sound', 'select_backdrop'")
	}
	encoded, err := jsonx.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if err := DecodeStrict(encoded, target); err != nil {
		return nil, err
	}
	dumped, err := jsonx.Marshal(target)
	if err != nil {
		return nil, err
	}
	tree, err := jsonx.Parse(dumped)
	if err != nil {
		return nil, err
	}
	object, ok := tree.(map[string]any)
	if !ok {
		return nil, validationError("/resource_ops", "Input should be a valid dictionary")
	}
	return object, nil
}

// ValidateResourceOperation validates one resource operation map against the
// discriminated ResourceOp union.
func ValidateResourceOperation(raw ResourceOp) error {
	_, err := decodeResourceOperationDump(raw)
	return err
}

// NormalizeResourceOpForValidation ports _normalize_resource_op_for_validation,
// the ResourceOp BeforeValidator.
func NormalizeResourceOpForValidation(value any) (any, error) {
	normalized, _, err := NormalizeResourceOp(value)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

// NormalizeResourceOp ports normalize_resource_op with its default path.
func NormalizeResourceOp(raw any) (any, []ResourceContractIssue, error) {
	return NormalizeResourceOpAt(raw, DefaultResourceOpPath)
}

// NormalizeResourceOpAt ports normalize_resource_op with an explicit path.
func NormalizeResourceOpAt(raw any, path string) (any, []ResourceContractIssue, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return raw, nil, nil
	}
	op, _ := object["op"].(string)
	if op != "add_backdrop" && op != "add_costume" {
		return raw, nil, nil
	}
	normalized := copyAnyMap(object)
	var diagnostics []ResourceContractIssue
	operation := pythonString(normalized["op"])
	var repairID *string
	if value, ok := normalized["op_id"].(string); ok {
		repairID = stringPtr(value)
	}
	if _, present := normalized["select_after"]; !present {
		normalized["select_after"] = false
		diagnostics = append(diagnostics, NewResourceContractIssue(
			"DEFAULT_APPLIED",
			path+"/select_after",
			operation,
			nil,
			"missing select_after is equivalent to false",
			repairID,
		))
	}
	if operation != "add_backdrop" {
		return normalized, diagnostics, nil
	}
	if target, present := normalized["target"]; present {
		if targetObject, ok := target.(map[string]any); ok && len(targetObject) == 1 && targetObject["kind"] == "stage" {
			delete(normalized, "target")
			diagnostics = append(diagnostics, NewResourceContractIssue(
				"REDUNDANT_STAGE_TARGET_REMOVED",
				path+"/target",
				operation,
				target,
				"backdrop always targets the stage; omit target",
				repairID,
			))
		} else {
			return nil, nil, resourceIssue(normalized, "INVALID_BACKDROP_TARGET", path+"/target", target,
				`target must be omitted or exactly {"kind":"stage"} as a redundant compatibility field`)
		}
	}
	for _, field := range []string{"x", "y", "size"} {
		actual, present := normalized[field]
		if !present {
			continue
		}
		if actual == nil {
			delete(normalized, field)
			diagnostics = append(diagnostics, NewResourceContractIssue(
				"NULL_FIELD_REMOVED",
				path+"/"+field,
				operation,
				nil,
				field+" must be omitted for a backdrop",
				repairID,
			))
		} else {
			return nil, nil, resourceIssue(normalized, "INVALID_BACKDROP_PLACEMENT", path+"/"+field, actual,
				"backdrop operations do not accept x, y, or size")
		}
	}
	if source, ok := normalized["source"].(map[string]any); ok && source["kind"] == "scratch_library" && source["asset_type"] != "backdrop" {
		return nil, nil, resourceIssue(normalized, "INVALID_BACKDROP_SOURCE", path+"/source/asset_type", source["asset_type"],
			"scratch_library backdrop source must use asset_type=backdrop")
	}
	return normalized, diagnostics, nil
}

// NormalizeTargetReference ports _normalize_target_reference.
func NormalizeTargetReference(value any, path string) (any, []ResourceContractIssue, error) {
	return NormalizeTargetReferenceAt(value, path)
}

// NormalizeTargetReferenceAt ports _normalize_target_reference.
func NormalizeTargetReferenceAt(value any, path string) (any, []ResourceContractIssue, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return value, nil, nil
	}
	kind, _ := object["kind"].(string)
	allowed := map[string]bool{"kind": true}
	switch kind {
	case "existing_sprite":
		allowed["name"] = true
	case "created_sprite":
		allowed["op_id"] = true
	case "current_target", "stage":
	default:
		return value, nil, nil
	}
	removed := map[string]any{}
	for _, key := range []string{"name", "op_id"} {
		if allowed[key] {
			continue
		}
		if item, present := object[key]; present {
			removed[key] = item
		}
	}
	if len(removed) == 0 {
		return value, nil, nil
	}
	normalized := copyAnyMap(object)
	var diagnostics []ResourceContractIssue
	for _, key := range []string{"name", "op_id"} {
		item, present := removed[key]
		if !present {
			continue
		}
		delete(normalized, key)
		diagnostics = append(diagnostics, NewResourceContractIssueWithScope(
			"REDUNDANT_TARGET_FIELD_REMOVED",
			path+"/"+key,
			"manifest",
			item,
			"target kind="+kind+" does not use "+key+"; the redundant field was removed",
			"manifest",
			nil,
		))
	}
	return normalized, diagnostics, nil
}

// NormalizeProjectBlueprintPayload ports normalize_project_blueprint_payload.
func NormalizeProjectBlueprintPayload(raw any) any {
	object, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	normalized := copyAnyMap(object)
	if jobs, ok := object["program_jobs"].([]any); ok {
		values := make([]any, 0, len(jobs))
		for _, job := range jobs {
			jobObject, ok := job.(map[string]any)
			if !ok {
				values = append(values, job)
				continue
			}
			copied := copyAnyMap(jobObject)
			if copied["mode"] == "rewrite" || copied["mode"] == "modify" {
				copied["mode"] = "append"
			}
			values = append(values, copied)
		}
		normalized["program_jobs"] = values
	}
	return normalized
}

// NormalizeProjectManifestPayload ports normalize_project_manifest_payload.
func NormalizeProjectManifestPayload(raw any) (any, []ResourceContractIssue, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return raw, nil, nil
	}
	normalized := copyAnyMap(object)
	var diagnostics []ResourceContractIssue
	if operations, ok := object["resource_ops"].([]any); ok {
		values := make([]any, 0, len(operations))
		for index, operation := range operations {
			operationInput := operation
			if operationObject, ok := operation.(map[string]any); ok {
				if target, present := operationObject["target"]; present {
					normalizedTarget, targetDiagnostics, err := NormalizeTargetReferenceAt(target, "/resource_ops/"+itoa(index)+"/target")
					if err != nil {
						return nil, nil, err
					}
					copied := copyAnyMap(operationObject)
					copied["target"] = normalizedTarget
					operationInput = copied
					diagnostics = append(diagnostics, targetDiagnostics...)
				}
			}
			value, operationDiagnostics, err := NormalizeResourceOpAt(operationInput, "/resource_ops/"+itoa(index))
			if err != nil {
				return nil, nil, err
			}
			values = append(values, value)
			diagnostics = append(diagnostics, operationDiagnostics...)
		}
		normalized["resource_ops"] = values
	}
	if programTargets, ok := object["program_targets"].([]any); ok {
		values := make([]any, 0, len(programTargets))
		for index, programTarget := range programTargets {
			programTargetObject, ok := programTarget.(map[string]any)
			if !ok {
				values = append(values, programTarget)
				continue
			}
			target, present := programTargetObject["target"]
			if !present {
				values = append(values, programTarget)
				continue
			}
			normalizedTarget, targetDiagnostics, err := NormalizeTargetReferenceAt(target, "/program_targets/"+itoa(index)+"/target")
			if err != nil {
				return nil, nil, err
			}
			item := copyAnyMap(programTargetObject)
			item["target"] = normalizedTarget
			values = append(values, item)
			diagnostics = append(diagnostics, targetDiagnostics...)
		}
		normalized["program_targets"] = values
	}
	return normalized, diagnostics, nil
}
