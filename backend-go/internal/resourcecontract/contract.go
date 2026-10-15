// Code generated from the Python reference implementation (backend/app/resource_contract.py).
// It is the single source of truth for the AI resource-operation contract. Do not edit by hand.

package resourcecontract

// ContractVersion carries immutable Blueprint lineage into every executable operation.
// A v2 manifest has no stable need/entity IDs and cannot be preflighted safely.
const ContractVersion = 3

// DefaultValue is one ordered operation default.
type DefaultValue struct {
	Key   string
	Value any
}

// OperationSpec declares the accepted field surface of one resource operation.
type OperationSpec struct {
	Name                        string
	RequiredFields              []string
	OptionalFields              []string
	Defaults                    []DefaultValue
	ForbiddenFields             []string
	TargetKinds                 []string
	SourceAssetTypes            []string
	SourceKinds                 []string
	NestedCostumesMin           *int
	NestedCostumesMax           *int
	RequiresDestructiveApproval bool
	Description                 string
}

// OperationSpecs lists every resource operation in contract order.
var OperationSpecs = []OperationSpec{
	{
		Name:             "add_builtin_sprite",
		RequiredFields:   []string{"op", "op_id", "source_need_id", "owner_entity_id", "name", "x", "y", "size", "source", "include_costumes"},
		OptionalFields:   []string{"tags", "include_sounds"},
		Defaults:         []DefaultValue{{"include_sounds", "all"}},
		ForbiddenFields:  []string{"target", "select_after", "costumes"},
		TargetKinds:      nil,
		SourceAssetTypes: []string{"sprite"},
		SourceKinds:      []string{"scratch_library"},
		Description:      "Add a sprite from the Scratch library.",
	},
	{
		Name:              "add_sprite",
		RequiredFields:    []string{"op", "op_id", "source_need_id", "owner_entity_id", "name", "x", "y", "size", "costumes"},
		OptionalFields:    []string{"tags"},
		ForbiddenFields:   []string{"target", "source", "select_after"},
		TargetKinds:       nil,
		SourceAssetTypes:  []string{"costume"},
		SourceKinds:       []string{"scratch_library", "image_generation"},
		NestedCostumesMin: intPtr(1),
		NestedCostumesMax: intPtr(12),
		Description:       "Add a custom sprite with one or more costume resources.",
	},
	{
		Name:                        "delete_sprite",
		RequiredFields:              []string{"op", "op_id", "name", "target"},
		OptionalFields:              nil,
		ForbiddenFields:             []string{"source", "x", "y", "size", "tags", "select_after", "costumes"},
		TargetKinds:                 []string{"existing_sprite"},
		SourceAssetTypes:            nil,
		SourceKinds:                 []string{"scratch_library", "image_generation"},
		RequiresDestructiveApproval: true,
		Description:                 "Delete an existing sprite.",
	},
	{
		Name:             "add_costume",
		RequiredFields:   []string{"op", "op_id", "source_need_id", "target", "name", "source"},
		OptionalFields:   []string{"tags", "select_after"},
		Defaults:         []DefaultValue{{"select_after", false}},
		ForbiddenFields:  []string{"x", "y", "size", "costumes"},
		TargetKinds:      []string{"current_target", "existing_sprite", "created_sprite"},
		SourceAssetTypes: []string{"costume"},
		SourceKinds:      []string{"scratch_library", "image_generation"},
		Description:      "Add a costume to a non-stage target.",
	},
	{
		Name:             "add_backdrop",
		RequiredFields:   []string{"op", "op_id", "source_need_id", "name", "source"},
		OptionalFields:   []string{"tags", "select_after"},
		Defaults:         []DefaultValue{{"select_after", false}},
		ForbiddenFields:  []string{"target", "x", "y", "size", "costumes"},
		TargetKinds:      nil,
		SourceAssetTypes: []string{"backdrop"},
		SourceKinds:      []string{"scratch_library", "image_generation"},
		Description:      "Add a backdrop to the stage.",
	},
	{
		Name:             "add_sound",
		RequiredFields:   []string{"op", "op_id", "source_need_id", "target", "name", "source"},
		OptionalFields:   []string{"tags"},
		ForbiddenFields:  nil,
		TargetKinds:      []string{"current_target", "stage", "existing_sprite", "created_sprite"},
		SourceAssetTypes: []string{"sound"},
		SourceKinds:      []string{"scratch_library", "audio_generation"},
		Description:      "Add a Scratch sound to a stage or sprite.",
	},
	{
		Name:                        "delete_sound",
		RequiredFields:              []string{"op", "op_id", "target", "sound"},
		OptionalFields:              nil,
		ForbiddenFields:             nil,
		TargetKinds:                 []string{"current_target", "stage", "existing_sprite", "created_sprite"},
		SourceAssetTypes:            nil,
		SourceKinds:                 []string{"scratch_library", "image_generation"},
		RequiresDestructiveApproval: true,
		Description:                 "Delete an existing sound after an explicit destructive-edit approval.",
	},
	{
		Name:             "select_backdrop",
		RequiredFields:   []string{"op", "op_id", "name"},
		OptionalFields:   nil,
		ForbiddenFields:  nil,
		TargetKinds:      nil,
		SourceAssetTypes: nil,
		SourceKinds:      []string{"scratch_library", "image_generation"},
		Description:      "Select an existing backdrop by name.",
	},
}

func intPtr(v int) *int { return &v }

// OperationByName indexes the contract by operation name.
var OperationByName = func() map[string]*OperationSpec {
	result := make(map[string]*OperationSpec, len(OperationSpecs))
	for i := range OperationSpecs {
		result[OperationSpecs[i].Name] = &OperationSpecs[i]
	}
	return result
}()

// SourceSpec declares one accepted resource source kind.
type SourceSpec struct {
	Kind           string
	RequiredFields []string
	OptionalFields []string
	AssetTypes     []string
}

// SourceSpecs lists every accepted resource source in contract order.
var SourceSpecs = []SourceSpec{
	{"scratch_library", []string{"kind", "asset_type", "search_terms"}, nil, []string{"sprite", "costume", "backdrop", "sound"}},
	{"image_generation", []string{"kind", "reason", "prompt", "style", "width", "height", "sheet_layout"}, []string{"custom_style"}, nil},
	{"audio_generation", []string{"kind", "reason", "prompt", "audio_kind", "max_duration_seconds"}, nil, nil},
}

// DiagnosticCodes are non-fatal normalizations reported to the client.
var DiagnosticCodes = []string{"DEFAULT_APPLIED", "REDUNDANT_STAGE_TARGET_REMOVED", "REDUNDANT_TARGET_FIELD_REMOVED", "NULL_FIELD_REMOVED"}

// ErrorCodes are terminal resource-contract failures.
var ErrorCodes = []string{"INVALID_BACKDROP_TARGET", "INVALID_BACKDROP_PLACEMENT", "INVALID_BACKDROP_SOURCE", "FIELD_FORBIDDEN", "FIELD_REQUIRED", "SCHEMA_VALIDATION_ERROR", "PROGRAM_CONTRACT_INVALID"}
