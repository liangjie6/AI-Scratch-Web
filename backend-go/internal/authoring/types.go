// Package authoring owns the versioned model-facing source contract. It never
// writes a VM graph: every result still passes the existing ProgramIR compiler.
package authoring

import (
	"encoding/json"
	"fmt"

	"github.com/ai-scratch-web/backend/internal/schema"
)

const (
	PipelineVersion   = "scratch-source/1"
	NormalizerVersion = "compact-v1"
	IntentVersion     = 1
)

// Error is bounded, script-addressable feedback for a whole-slot repair.
type Error struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	SlotID  string `json:"slot_id,omitempty"`
	Message string `json:"message"`
}

func (e *Error) Error() string               { return fmt.Sprintf("%s at %s: %s", e.Code, e.Path, e.Message) }
func issue(code, path, message string) error { return &Error{Code: code, Path: path, Message: message} }

// Limits are checked before tree decoding and again while normalizing blocks.
type Limits struct {
	MaxBytes      int
	MaxJSONDepth  int
	MaxJSONValues int
	MaxBlocks     int
	MaxBlockDepth int
	MaxSlots      int
}

func DefaultLimits() Limits {
	return Limits{256 * 1024, 64, 24000, schema.MaxBlocks, schema.MaxBlockDepth, schema.MaxScripts}
}
func (l Limits) defaults() Limits {
	d := DefaultLimits()
	if l.MaxBytes > 0 {
		d.MaxBytes = l.MaxBytes
	}
	if l.MaxJSONDepth > 0 {
		d.MaxJSONDepth = l.MaxJSONDepth
	}
	if l.MaxJSONValues > 0 {
		d.MaxJSONValues = l.MaxJSONValues
	}
	if l.MaxBlocks > 0 {
		d.MaxBlocks = l.MaxBlocks
	}
	if l.MaxBlockDepth > 0 {
		d.MaxBlockDepth = l.MaxBlockDepth
	}
	if l.MaxSlots > 0 {
		d.MaxSlots = l.MaxSlots
	}
	return d
}

type IntentSpec struct {
	SchemaVersion      int            `json:"schema_version"`
	Ready              *bool          `json:"ready,omitempty"`
	Title              string         `json:"title"`
	Goal               string         `json:"goal"`
	Targets            []IntentTarget `json:"targets"`
	States             []IntentState  `json:"states"`
	Assets             []IntentAsset  `json:"assets"`
	ScriptSlots        []IntentSlot   `json:"script_slots"`
	RequiredExtensions []string       `json:"required_extensions"`
	Assumptions        []string       `json:"assumptions"`
	SuccessCriteria    []string       `json:"success_criteria"`
	MissingQuestions   []string       `json:"missing_questions"`
}

type IntentTarget struct {
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Disposition string            `json:"disposition"`
	TargetRef   *schema.TargetRef `json:"target_ref,omitempty"`
}

type IntentState struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope"`
	Owner       string `json:"owner,omitempty"`
	Disposition string `json:"disposition"`
	Initial     any    `json:"initial,omitempty"`
}

type IntentAsset struct {
	Key         string         `json:"key"`
	Owner       string         `json:"owner"`
	Kind        string         `json:"kind"`
	Name        string         `json:"name"`
	Purpose     string         `json:"purpose"`
	Disposition string         `json:"disposition"`
	SearchTerms []string       `json:"search_terms"`
	Source      map[string]any `json:"source,omitempty"`
	X           *int           `json:"x,omitempty"`
	Y           *int           `json:"y,omitempty"`
	Size        *int           `json:"size,omitempty"`
}

type Trigger struct {
	Kind    string   `json:"kind"`
	Key     string   `json:"key,omitempty"`
	State   string   `json:"state,omitempty"`
	Asset   string   `json:"asset,omitempty"`
	Seconds *float64 `json:"seconds,omitempty"`
}

type IntentSlot struct {
	Key      string   `json:"key"`
	Owner    string   `json:"owner"`
	Trigger  Trigger  `json:"trigger"`
	Purpose  string   `json:"purpose"`
	Reads    []string `json:"reads"`
	Writes   []string `json:"writes"`
	Emits    []string `json:"emits"`
	Receives []string `json:"receives"`
	Assets   []string `json:"assets"`
	EditRef  string   `json:"edit_ref,omitempty"`
}

// EditBinding is supplied by the trusted snapshot reader, never by the model.
type EditBinding struct {
	Ref              string `json:"ref"`
	Target           string `json:"target"`
	ScriptID         string `json:"script_id"`
	BaseSnapshotHash string `json:"base_snapshot_hash"`
	Protected        bool   `json:"protected"`
}

type StateBinding struct {
	Handle      string `json:"handle"`
	RefID       string `json:"ref_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope"`
	Owner       string `json:"owner,omitempty"`
	Disposition string `json:"disposition"`
	Value       any    `json:"value"`
}

type AssetBinding struct {
	Handle      string `json:"handle"`
	Owner       string `json:"owner"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Disposition string `json:"disposition"`
	OpID        string `json:"op_id,omitempty"`
	NeedID      string `json:"need_id,omitempty"`
}

type TargetBinding struct {
	Handle      string               `json:"handle"`
	EntityID    string               `json:"entity_id"`
	Name        string               `json:"name"`
	Kind        string               `json:"kind"`
	Disposition string               `json:"disposition"`
	Ref         schema.TargetRef     `json:"ref"`
	Context     schema.TargetContext `json:"context"`
	Mode        string               `json:"mode"`
}

type FrozenSlot struct {
	IntentSlot
	ScriptID       string `json:"script_id"`
	RemoveScriptID string `json:"remove_script_id,omitempty"`
}

type FrozenContract struct {
	PipelineVersion    string          `json:"pipeline_version"`
	NormalizerVersion  string          `json:"normalizer_version"`
	CatalogHash        string          `json:"catalog_hash"`
	Scope              string          `json:"scope"`
	Mode               string          `json:"mode"`
	BaseSnapshotHash   string          `json:"base_snapshot_hash"`
	RequestBindingHash string          `json:"request_binding_hash"`
	ExpiresAtUnix      int64           `json:"expires_at_unix"`
	InterfaceVersion   int             `json:"interface_version"`
	Hash               string          `json:"hash"`
	Title              string          `json:"title"`
	Goal               string          `json:"goal"`
	Targets            []TargetBinding `json:"targets"`
	States             []StateBinding  `json:"states"`
	Assets             []AssetBinding  `json:"assets"`
	ScriptSlots        []FrozenSlot    `json:"script_slots"`
	RequiredExtensions []string        `json:"required_extensions"`
	Assumptions        []string        `json:"assumptions"`
	SuccessCriteria    []string        `json:"success_criteria"`
	AssetIntents       []IntentAsset   `json:"asset_intents"`
}

type FreezeOptions struct {
	Scope              string
	Mode               string
	Target             schema.TargetContext
	ProjectContext     *schema.ProjectContext
	BaseSnapshotHash   string
	RequestBindingHash string
	ExpiresAtUnix      int64
	CatalogHash        string
	AllowedExtensions  map[string]bool
	ExistingStates     []StateBinding
	ExistingAssets     []AssetBinding
	ExistingScriptRefs []EditBinding
	Limits             Limits
}

type CompactBlock struct {
	Opcode    string                    `json:"opcode"`
	Fields    map[string]any            `json:"fields,omitempty"`
	Inputs    map[string]any            `json:"inputs,omitempty"`
	Substacks map[string][]CompactBlock `json:"substacks,omitempty"`
}

type Revision struct {
	Reason           string   `json:"reason"`
	RequestedOpcodes []string `json:"requested_opcodes"`
	Explanation      string   `json:"explanation"`
}

type SlotResponse struct {
	SlotID   string         `json:"slot_id"`
	Status   string         `json:"status"`
	Body     []CompactBlock `json:"body"`
	Revision *Revision      `json:"revision"`
}

type NormalizeOptions struct {
	Limits Limits
	// A nil profile permits the full policy catalog. A non-nil empty profile
	// permits no model-authored blocks; deterministic hats are unaffected.
	OpcodeProfile    map[string]bool
	BaseSnapshotHash string
	ContractHash     string
}

type DerivedContracts struct {
	Blueprint *schema.ProjectBlueprint `json:"blueprint"`
	Manifest  *schema.ProjectManifest  `json:"manifest,omitempty"`
	Plan      *schema.Plan             `json:"plan"`
}

type ProgramContext struct {
	PipelineVersion    string          `json:"pipeline_version"`
	ContractHash       string          `json:"contract_hash"`
	BaseSnapshotHash   string          `json:"base_snapshot_hash"`
	Target             TargetBinding   `json:"target"`
	Slots              []FrozenSlot    `json:"script_slots"`
	States             []StateBinding  `json:"states"`
	Assets             []AssetBinding  `json:"assets"`
	Targets            []TargetBinding `json:"targets"`
	RequiredExtensions []string        `json:"required_extensions"`
}

// Marshal is useful to persist the exact frozen source artifact in an envelope.
func (c *FrozenContract) Marshal() ([]byte, error) { return json.Marshal(c) }
