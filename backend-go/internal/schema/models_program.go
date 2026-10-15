package schema

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

var (
	scriptIDPattern    = regexp.MustCompile("^script_[A-Za-z0-9_-]+$")
	argIDPattern       = regexp.MustCompile("^arg_[A-Za-z0-9_-]+$")
	procedureIDPattern = regexp.MustCompile("^procedure_[A-Za-z0-9_-]+$")
	hashPattern        = regexp.MustCompile("^[a-f0-9]{64}$")
)

// orderedKeys returns recorded JSON insertion order when available and the
// sorted key order otherwise (deterministic for programmatically built maps).
func orderedKeys[T any](order []string, values map[string]T) []string {
	if order != nil {
		return order
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

// LiteralInput ports LiteralInput.
type LiteralInput struct {
	Kind  string `json:"kind"`
	Value any    `json:"value"`
}

func (m *LiteralInput) requiredJSONFields() []string { return []string{"kind", "value"} }

// Validate ports LiteralInput.validate_value.
func (m *LiteralInput) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "number", "text", "angle", "color"); err != nil {
		return err
	}
	if m.Kind == "number" || m.Kind == "angle" {
		switch m.Value.(type) {
		case float64, int, int64:
		default:
			return errors.New(m.Kind + " input requires a numeric value")
		}
	} else {
		if _, ok := m.Value.(string); !ok {
			return errors.New(m.Kind + " input requires a string value")
		}
	}
	if text, ok := m.Value.(string); ok && runeLength(text) > MaxStringLength {
		return errors.New("input string is too long")
	}
	return nil
}

// BlockInput ports BlockInput.
type BlockInput struct {
	Kind  string `json:"kind"`
	Block Block  `json:"block"`
}

func (m *BlockInput) requiredJSONFields() []string { return []string{"kind", "block"} }

// Validate ports BlockInput validation.
func (m *BlockInput) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "block"); err != nil {
		return err
	}
	return m.Block.Validate()
}

// InstalledResourceRef ports InstalledResourceRef.
type InstalledResourceRef struct {
	OpID  string `json:"op_id"`
	Value string `json:"value"`
}

func (m *InstalledResourceRef) requiredJSONFields() []string { return []string{"op_id", "value"} }

// Validate ports InstalledResourceRef validation.
func (m *InstalledResourceRef) Validate() error {
	if err := validateShortString("/op_id", m.OpID); err != nil {
		return err
	}
	return validateLiteral("/value", m.Value, "installed_name")
}

// ResourceRefInput ports ResourceRefInput.
type ResourceRefInput struct {
	Kind         string                `json:"kind"`
	ResourceType *string               `json:"resource_type"`
	OpID         *string               `json:"op_id"`
	Ref          *InstalledResourceRef `json:"ref"`
}

func (m *ResourceRefInput) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_resource_reference.
func (m *ResourceRefInput) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "resource_ref"); err != nil {
		return err
	}
	if m.ResourceType != nil {
		if err := validateLiteral("/resource_type", *m.ResourceType, "sprite", "costume", "backdrop", "sound"); err != nil {
			return err
		}
	}
	if m.OpID != nil {
		if err := validateShortString("/op_id", *m.OpID); err != nil {
			return err
		}
	}
	if m.Ref != nil {
		if err := m.Ref.Validate(); err != nil {
			return err
		}
	}
	if m.Ref != nil && m.OpID == nil && m.ResourceType == nil {
		return nil
	}
	if m.Ref == nil && m.OpID != nil && m.ResourceType != nil {
		return nil
	}
	return errors.New("resource_ref requires ref or legacy resource_type/op_id pair")
}

// ExistingAssetRefInput ports ExistingAssetRefInput.
type ExistingAssetRefInput struct {
	Kind      string `json:"kind"`
	AssetType string `json:"asset_type"`
	Name      string `json:"name"`
}

func (m *ExistingAssetRefInput) requiredJSONFields() []string {
	return []string{"kind", "asset_type", "name"}
}

// Validate ports ExistingAssetRefInput validation.
func (m *ExistingAssetRefInput) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "existing_asset_ref"); err != nil {
		return err
	}
	if err := validateLiteral("/asset_type", m.AssetType, "sprite", "costume", "backdrop", "sound"); err != nil {
		return err
	}
	return validateShortString("/name", m.Name)
}

// StateRefField ports StateRefField.
type StateRefField struct {
	Kind      string `json:"kind"`
	StateKind string `json:"state_kind"`
	RefID     string `json:"ref_id"`
}

func (m *StateRefField) requiredJSONFields() []string {
	return []string{"kind", "state_kind", "ref_id"}
}

// Validate ports StateRefField validation.
func (m *StateRefField) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "state_ref"); err != nil {
		return err
	}
	if err := validateLiteral("/state_kind", m.StateKind, "variable", "list", "broadcast"); err != nil {
		return err
	}
	return validateShortString("/ref_id", m.RefID)
}

func decodeAny[T any](value any) (T, error) {
	var target T
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return target, err
	}
	if err := strictUnmarshal(encoded, &target); err != nil {
		return target, err
	}
	return target, nil
}

// inputDiscriminator extracts the kind tag of an InputValue.
func inputDiscriminator(value InputValue) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	kind, ok := object["kind"].(string)
	return kind, ok
}

// ValidateInputValue ports the InputValue discriminated union.
func ValidateInputValue(path string, value InputValue) error {
	object, ok := value.(map[string]any)
	if !ok {
		return validationError(path, "Input should be a valid dictionary")
	}
	kind, present := object["kind"]
	if !present {
		return validationError(path, "Unable to extract tag using discriminator 'kind'")
	}
	tag, _ := kind.(string)
	switch tag {
	case "number", "text", "angle", "color":
		target, err := decodeAny[LiteralInput](object)
		if err != nil {
			return err
		}
		return target.Validate()
	case "block":
		target, err := decodeAny[BlockInput](object)
		if err != nil {
			return err
		}
		return target.Validate()
	case "resource_ref":
		target, err := decodeAny[ResourceRefInput](object)
		if err != nil {
			return err
		}
		return target.Validate()
	case "existing_asset_ref":
		target, err := decodeAny[ExistingAssetRefInput](object)
		if err != nil {
			return err
		}
		return target.Validate()
	default:
		return validationError(path, "Input tag '"+tag+"' found using 'kind' does not match any of the expected tags: 'number', 'text', 'angle', 'color', 'block', 'resource_ref', 'existing_asset_ref'")
	}
}

// ValidateFieldValue ports the FieldValue union.
func ValidateFieldValue(path string, value FieldValue) error {
	switch typed := value.(type) {
	case string, bool, float64, int, int64:
		return nil
	case map[string]any:
		kind, _ := typed["kind"].(string)
		switch kind {
		case "state_ref":
			target, err := decodeAny[StateRefField](typed)
			if err != nil {
				return err
			}
			return target.Validate()
		case "resource_ref":
			target, err := decodeAny[ResourceRefInput](typed)
			if err != nil {
				return err
			}
			return target.Validate()
		case "existing_asset_ref":
			target, err := decodeAny[ExistingAssetRefInput](typed)
			if err != nil {
				return err
			}
			return target.Validate()
		default:
			return validationError(path, "Input tag '"+kind+"' found using 'kind' does not match any of the expected tags: 'state_ref', 'resource_ref', 'existing_asset_ref'")
		}
	default:
		return validationError(path, "Input should be a valid string, number, boolean, state_ref, resource_ref or existing_asset_ref")
	}
}

// AsBlockInputValue converts an InputValue holding a BlockInput.
func AsBlockInputValue(value InputValue) (*BlockInput, bool) {
	object, ok := value.(map[string]any)
	if !ok || object["kind"] != "block" {
		return nil, false
	}
	target, err := decodeAny[BlockInput](object)
	if err != nil {
		return nil, false
	}
	return &target, true
}

// AsLiteralInputValue converts an InputValue holding a LiteralInput.
func AsLiteralInputValue(value InputValue) (*LiteralInput, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	switch object["kind"] {
	case "number", "text", "angle", "color":
	default:
		return nil, false
	}
	target, err := decodeAny[LiteralInput](object)
	if err != nil {
		return nil, false
	}
	return &target, true
}

// AsResourceRefInputValue converts a union value holding a ResourceRefInput.
func AsResourceRefInputValue(value any) (*ResourceRefInput, bool) {
	object, ok := value.(map[string]any)
	if !ok || object["kind"] != "resource_ref" {
		return nil, false
	}
	target, err := decodeAny[ResourceRefInput](object)
	if err != nil {
		return nil, false
	}
	return &target, true
}

// AsExistingAssetRefInputValue converts a union value holding an
// ExistingAssetRefInput.
func AsExistingAssetRefInputValue(value any) (*ExistingAssetRefInput, bool) {
	object, ok := value.(map[string]any)
	if !ok || object["kind"] != "existing_asset_ref" {
		return nil, false
	}
	target, err := decodeAny[ExistingAssetRefInput](object)
	if err != nil {
		return nil, false
	}
	return &target, true
}

// AsStateRefFieldValue converts a FieldValue holding a StateRefField.
func AsStateRefFieldValue(value FieldValue) (*StateRefField, bool) {
	object, ok := value.(map[string]any)
	if !ok || object["kind"] != "state_ref" {
		return nil, false
	}
	target, err := decodeAny[StateRefField](object)
	if err != nil {
		return nil, false
	}
	return &target, true
}

// Block ports Block.
type Block struct {
	Opcode      string                `json:"opcode"`
	ProcedureID *string               `json:"procedure_id"`
	ArgumentID  *string               `json:"argument_id"`
	Fields      map[string]FieldValue `json:"fields"`
	Inputs      map[string]InputValue `json:"inputs"`
	Substacks   map[string][]Block    `json:"substacks"`

	fieldOrder    []string
	inputOrder    []string
	substackOrder []string
}

func (m *Block) requiredJSONFields() []string { return []string{"opcode"} }

// UnmarshalJSON records the field/input/substack key order so validators emit
// issues in the same order as the Python dict iteration.
func (m *Block) UnmarshalJSON(data []byte) error {
	type alias Block
	var decoded alias
	if err := strictUnmarshal(data, &decoded); err != nil {
		return err
	}
	*m = Block(decoded)
	var raw struct {
		Fields    json.RawMessage `json:"fields"`
		Inputs    json.RawMessage `json:"inputs"`
		Substacks json.RawMessage `json:"substacks"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Fields) > 0 {
		if keys, err := jsonObjectKeys(raw.Fields); err == nil {
			m.fieldOrder = keys
		}
	}
	if len(raw.Inputs) > 0 {
		if keys, err := jsonObjectKeys(raw.Inputs); err == nil {
			m.inputOrder = keys
		}
	}
	if len(raw.Substacks) > 0 {
		if keys, err := jsonObjectKeys(raw.Substacks); err == nil {
			m.substackOrder = keys
		}
	}
	return nil
}

// FieldNames returns the field keys in JSON document order.
func (m *Block) FieldNames() []string { return orderedKeys(m.fieldOrder, m.Fields) }

// InputNames returns the input keys in JSON document order.
func (m *Block) InputNames() []string { return orderedKeys(m.inputOrder, m.Inputs) }

// SubstacksNames returns the substack keys in JSON document order.
func (m *Block) SubstacksNames() []string { return orderedKeys(m.substackOrder, m.Substacks) }

// Validate ports Block's field validators.
func (m *Block) Validate() error {
	if !IsNativeOpcode(m.Opcode) && !ProcedureOpcodes[m.Opcode] {
		return errors.New("opcode is not an allowed native Scratch opcode")
	}
	if err := validateOptionalShortString("/procedure_id", m.ProcedureID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/argument_id", m.ArgumentID); err != nil {
		return err
	}
	if err := requireListLength("/fields", len(m.Fields), -1, 32); err != nil {
		return err
	}
	for _, name := range m.FieldNames() {
		if runeLength(name) > MaxShortStringLength {
			return errors.New("field name is too long")
		}
	}
	for _, name := range m.FieldNames() {
		if text, ok := m.Fields[name].(string); ok && runeLength(text) > MaxStringLength {
			return errors.New("field string is too long")
		}
	}
	for _, name := range m.FieldNames() {
		if err := ValidateFieldValue("/fields/"+name, m.Fields[name]); err != nil {
			return err
		}
	}
	if err := requireListLength("/inputs", len(m.Inputs), -1, 32); err != nil {
		return err
	}
	for _, name := range m.InputNames() {
		if err := ValidateInputValue("/inputs/"+name, m.Inputs[name]); err != nil {
			return err
		}
	}
	if err := requireListLength("/substacks", len(m.Substacks), -1, 4); err != nil {
		return err
	}
	for _, name := range m.SubstacksNames() {
		if name != "SUBSTACK" && name != "SUBSTACK2" {
			return errors.New("invalid substack name")
		}
		for index := range m.Substacks[name] {
			if err := m.Substacks[name][index].Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// VariableDefinition ports VariableDefinition.
type VariableDefinition struct {
	Name  string  `json:"name"`
	Value any     `json:"value"`
	Scope *string `json:"scope"`
}

func (m *VariableDefinition) requiredJSONFields() []string { return []string{"name"} }

// Validate ports VariableDefinition validation.
func (m *VariableDefinition) Validate() error {
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/value", m.Value, false); err != nil {
		return err
	}
	if m.Scope != nil {
		return validateLiteral("/scope", *m.Scope, "local", "global")
	}
	return nil
}

// ListDefinition ports ListDefinition.
type ListDefinition struct {
	Name  string  `json:"name"`
	Value any     `json:"value"`
	Scope *string `json:"scope"`
}

func (m *ListDefinition) requiredJSONFields() []string { return []string{"name"} }

// Validate ports ListDefinition validation.
func (m *ListDefinition) Validate() error {
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/value", m.Value, true); err != nil {
		return err
	}
	if list, ok := m.Value.([]any); ok {
		if err := requireListLength("/value", len(list), -1, 500); err != nil {
			return err
		}
	}
	if m.Scope != nil {
		return validateLiteral("/scope", *m.Scope, "local", "global")
	}
	return nil
}

// ProjectVariableDefinition ports ProjectVariableDefinition.
type ProjectVariableDefinition struct {
	RefID string `json:"ref_id"`
	Name  string `json:"name"`
	Value any    `json:"value"`
	Scope string `json:"scope"`
}

func (m *ProjectVariableDefinition) requiredJSONFields() []string {
	return []string{"ref_id", "name", "scope"}
}

// Validate ports ProjectVariableDefinition validation.
func (m *ProjectVariableDefinition) Validate() error {
	if err := validateShortString("/ref_id", m.RefID); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/value", m.Value, false); err != nil {
		return err
	}
	return validateLiteral("/scope", m.Scope, "global")
}

// ProjectListDefinition ports ProjectListDefinition.
type ProjectListDefinition struct {
	RefID string `json:"ref_id"`
	Name  string `json:"name"`
	Value any    `json:"value"`
	Scope string `json:"scope"`
}

func (m *ProjectListDefinition) requiredJSONFields() []string {
	return []string{"ref_id", "name", "scope"}
}

// Validate ports ProjectListDefinition validation.
func (m *ProjectListDefinition) Validate() error {
	if err := validateShortString("/ref_id", m.RefID); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/value", m.Value, true); err != nil {
		return err
	}
	if list, ok := m.Value.([]any); ok {
		if err := requireListLength("/value", len(list), -1, 500); err != nil {
			return err
		}
	}
	return validateLiteral("/scope", m.Scope, "global")
}

// ProjectBroadcastDefinition ports ProjectBroadcastDefinition.
type ProjectBroadcastDefinition struct {
	RefID string `json:"ref_id"`
	Name  string `json:"name"`
}

func (m *ProjectBroadcastDefinition) requiredJSONFields() []string {
	return []string{"ref_id", "name"}
}

// Validate ports ProjectBroadcastDefinition validation.
func (m *ProjectBroadcastDefinition) Validate() error {
	if err := validateShortString("/ref_id", m.RefID); err != nil {
		return err
	}
	return validateShortString("/name", m.Name)
}

// ProjectLocalState ports ProjectLocalState.
type ProjectLocalState struct {
	RefID  string    `json:"ref_id"`
	Kind   string    `json:"kind"`
	Target TargetRef `json:"target"`
	Name   string    `json:"name"`
	Value  any       `json:"value"`
}

func (m *ProjectLocalState) requiredJSONFields() []string {
	return []string{"ref_id", "kind", "target", "name"}
}

// Validate ports validate_local_target_and_value.
func (m *ProjectLocalState) Validate() error {
	if err := validateShortString("/ref_id", m.RefID); err != nil {
		return err
	}
	if err := validateLiteral("/kind", m.Kind, "variable", "list"); err != nil {
		return err
	}
	if err := m.Target.Validate(); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/value", m.Value, true); err != nil {
		return err
	}
	if m.Target.Kind == "stage" {
		return errors.New("local state cannot target the stage")
	}
	if m.Kind == "list" && m.Value != nil {
		if _, ok := m.Value.([]any); !ok {
			return errors.New("local list value must be an array")
		}
	}
	if m.Kind == "variable" {
		if _, ok := m.Value.([]any); ok {
			return errors.New("local variable value cannot be an array")
		}
	}
	return nil
}

// TargetRef ports TargetRef.
type TargetRef struct {
	Kind string  `json:"kind"`
	Name *string `json:"name"`
	OpID *string `json:"op_id"`
}

func (m *TargetRef) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_reference.
func (m *TargetRef) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "current_target", "stage", "existing_sprite", "created_sprite"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateOptionalShortString("/op_id", m.OpID); err != nil {
		return err
	}
	if m.Kind == "existing_sprite" && stringValue(m.Name) == "" {
		return errors.New("existing_sprite requires name")
	}
	if m.Kind == "created_sprite" && stringValue(m.OpID) == "" {
		return errors.New("created_sprite requires op_id")
	}
	if (m.Kind == "current_target" || m.Kind == "stage") && (stringValue(m.Name) != "" || stringValue(m.OpID) != "") {
		return errors.New(m.Kind + " cannot contain name or op_id")
	}
	return nil
}

// TaskTargetRef ports TaskTargetRef.
type TaskTargetRef struct {
	Kind string  `json:"kind"`
	Name *string `json:"name"`
}

func (m *TaskTargetRef) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports TaskTargetRef.validate_reference.
func (m *TaskTargetRef) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "current_target", "stage", "existing_sprite"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/name", m.Name); err != nil {
		return err
	}
	if m.Kind == "existing_sprite" && stringValue(m.Name) == "" {
		return errors.New("existing_sprite requires name")
	}
	if m.Kind != "existing_sprite" && m.Name != nil {
		return errors.New(m.Kind + " cannot contain name")
	}
	return nil
}

// Script ports Script.
type Script struct {
	ID      string  `json:"id"`
	Purpose string  `json:"purpose"`
	X       int     `json:"x"`
	Y       int     `json:"y"`
	Blocks  []Block `json:"blocks"`
}

func (m *Script) requiredJSONFields() []string { return []string{"id", "purpose", "x", "y", "blocks"} }

// Validate ports Script validation.
func (m *Script) Validate() error {
	if !scriptIDPattern.MatchString(m.ID) {
		return validationError("/id", "String should match pattern '^script_[A-Za-z0-9_-]+$'")
	}
	if err := requireStringLength("/id", m.ID, -1, 100); err != nil {
		return err
	}
	if err := validateShortString("/purpose", m.Purpose); err != nil {
		return err
	}
	if err := requireIntRange("/x", m.X, -10000, 10000); err != nil {
		return err
	}
	if err := requireIntRange("/y", m.Y, -10000, 10000); err != nil {
		return err
	}
	if err := requireListLength("/blocks", len(m.Blocks), 1, MaxBlocks); err != nil {
		return err
	}
	for index := range m.Blocks {
		if err := m.Blocks[index].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ProcedureParameter ports ProcedureParameter.
type ProcedureParameter struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Default any    `json:"default"`
}

func (m *ProcedureParameter) requiredJSONFields() []string { return []string{"id", "name", "kind"} }

// Validate ports ProcedureParameter validation.
func (m *ProcedureParameter) Validate() error {
	if !argIDPattern.MatchString(m.ID) {
		return validationError("/id", "String should match pattern '^arg_[A-Za-z0-9_-]+$'")
	}
	if err := requireStringLength("/id", m.ID, -1, 100); err != nil {
		return err
	}
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateLiteral("/kind", m.Kind, "string_number", "boolean"); err != nil {
		return err
	}
	return validateScalarOrListValue("/default", m.Default, false)
}

// Procedure ports Procedure.
type Procedure struct {
	ID         string               `json:"id"`
	Label      string               `json:"label"`
	Parameters []ProcedureParameter `json:"parameters"`
	Warp       bool                 `json:"warp"`
	Body       []Block              `json:"body"`
}

func (m *Procedure) requiredJSONFields() []string { return []string{"id", "label", "body"} }

// Validate ports validate_procedure.
func (m *Procedure) Validate() error {
	if !procedureIDPattern.MatchString(m.ID) {
		return validationError("/id", "String should match pattern '^procedure_[A-Za-z0-9_-]+$'")
	}
	if err := requireStringLength("/id", m.ID, -1, 100); err != nil {
		return err
	}
	if err := validateShortString("/label", m.Label); err != nil {
		return err
	}
	if err := requireListLength("/parameters", len(m.Parameters), -1, 8); err != nil {
		return err
	}
	for index := range m.Parameters {
		if err := m.Parameters[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/body", len(m.Body), 1, MaxBlocks); err != nil {
		return err
	}
	for index := range m.Body {
		if err := m.Body[index].Validate(); err != nil {
			return err
		}
	}
	if strings.Contains(m.Label, "%") {
		return errors.New("procedure label cannot contain Scratch placeholder characters")
	}
	seen := map[string]bool{}
	for index := range m.Parameters {
		id := m.Parameters[index].ID
		if seen[id] {
			return errors.New("procedure parameter ids must be unique")
		}
		seen[id] = true
	}
	return nil
}

// ProgramMetadata ports ProgramMetadata.
type ProgramMetadata struct {
	SchemaVersion   int         `json:"schema_version" default:"1"`
	Summary         string      `json:"summary"`
	RemoveScriptIDs []string    `json:"remove_script_ids"`
	Procedures      []Procedure `json:"procedures"`
}

func (m *ProgramMetadata) requiredJSONFields() []string { return []string{"summary"} }

// Validate ports ProgramMetadata validation.
func (m *ProgramMetadata) Validate() error {
	if m.SchemaVersion != 1 && m.SchemaVersion != 2 {
		return validationError("/schema_version", "Input should be 1 or 2")
	}
	if err := requireStringLength("/summary", m.Summary, 1, MaxSummaryLength); err != nil {
		return err
	}
	if err := requireListLength("/remove_script_ids", len(m.RemoveScriptIDs), -1, MaxScripts); err != nil {
		return err
	}
	if err := validateRemovedScriptIDs(m.RemoveScriptIDs); err != nil {
		return err
	}
	if err := requireListLength("/procedures", len(m.Procedures), -1, 12); err != nil {
		return err
	}
	for index := range m.Procedures {
		if err := m.Procedures[index].Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateRemovedScriptIDs(values []string) error {
	for _, value := range values {
		if !scriptIDPattern.MatchString(value) {
			return errors.New("remove_script_ids must use script_N style ids")
		}
	}
	if len(values) != len(stringSet(values)) {
		return errors.New("remove_script_ids must be unique")
	}
	return nil
}

// ProgramState ports ProgramState.
type ProgramState struct {
	Variables  []VariableDefinition `json:"variables"`
	Lists      []ListDefinition     `json:"lists"`
	Broadcasts []string             `json:"broadcasts"`
}

func (m *ProgramState) requiredJSONFields() []string { return nil }

// Validate ports ProgramState.validate_names.
func (m *ProgramState) Validate() error {
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
	for index, broadcast := range m.Broadcasts {
		if err := validateShortString("/broadcasts/"+itoa(index), broadcast); err != nil {
			return err
		}
	}
	for _, group := range []struct {
		label string
		names []string
	}{
		{"variables", variableNames(m.Variables)},
		{"lists", definitionNames(m.Lists)},
	} {
		if len(group.names) != len(stringSet(group.names)) {
			return errors.New(group.label + " names must be unique")
		}
	}
	if len(m.Broadcasts) != len(stringSet(m.Broadcasts)) {
		return errors.New("broadcast names must be unique")
	}
	return nil
}

func variableNames(values []VariableDefinition) []string {
	names := make([]string, 0, len(values))
	for index := range values {
		names = append(names, values[index].Name)
	}
	return names
}

func definitionNames(values []ListDefinition) []string {
	names := make([]string, 0, len(values))
	for index := range values {
		names = append(names, values[index].Name)
	}
	return names
}

// ProgramIR ports ProgramIR.
type ProgramIR struct {
	SchemaVersion   int                  `json:"schema_version"`
	Summary         string               `json:"summary"`
	RemoveScriptIDs []string             `json:"remove_script_ids"`
	Variables       []VariableDefinition `json:"variables"`
	Lists           []ListDefinition     `json:"lists"`
	Broadcasts      []string             `json:"broadcasts"`
	Procedures      []Procedure          `json:"procedures"`
	Scripts         []Script             `json:"scripts"`
}

func (m *ProgramIR) requiredJSONFields() []string {
	return []string{"schema_version", "summary", "scripts"}
}

// Validate ports ProgramIR validation (field and model validators).
func (m *ProgramIR) Validate() error {
	if m.SchemaVersion != 1 && m.SchemaVersion != 2 {
		return validationError("/schema_version", "Input should be 1 or 2")
	}
	if err := requireStringLength("/summary", m.Summary, 1, MaxSummaryLength); err != nil {
		return err
	}
	if err := requireListLength("/remove_script_ids", len(m.RemoveScriptIDs), -1, MaxScripts); err != nil {
		return err
	}
	if err := validateRemovedScriptIDs(m.RemoveScriptIDs); err != nil {
		return err
	}
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
	for index, broadcast := range m.Broadcasts {
		if err := validateShortString("/broadcasts/"+itoa(index), broadcast); err != nil {
			return err
		}
	}
	if err := requireListLength("/procedures", len(m.Procedures), -1, 12); err != nil {
		return err
	}
	for index := range m.Procedures {
		if err := m.Procedures[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/scripts", len(m.Scripts), 1, MaxScripts); err != nil {
		return err
	}
	for index := range m.Scripts {
		if err := m.Scripts[index].Validate(); err != nil {
			return err
		}
	}
	return m.validateProgramSize()
}

func (m *ProgramIR) validateProgramSize() error {
	for _, group := range []struct {
		label string
		names []string
	}{
		{"variables", variableNames(m.Variables)},
		{"lists", definitionNames(m.Lists)},
	} {
		if len(group.names) != len(stringSet(group.names)) {
			return errors.New(group.label + " names must be unique")
		}
	}
	if len(m.Broadcasts) != len(stringSet(m.Broadcasts)) {
		return errors.New("broadcast names must be unique")
	}
	procedureIDs := make([]string, 0, len(m.Procedures))
	for index := range m.Procedures {
		procedureIDs = append(procedureIDs, m.Procedures[index].ID)
	}
	if len(procedureIDs) != len(stringSet(procedureIDs)) {
		return errors.New("procedure ids must be unique")
	}
	procedureCodes := map[string]bool{}
	for index := range m.Procedures {
		procedure := &m.Procedures[index]
		parts := make([]string, 0, len(procedure.Parameters))
		for parameterIndex := range procedure.Parameters {
			if procedure.Parameters[parameterIndex].Kind == "boolean" {
				parts = append(parts, "%b")
			} else {
				parts = append(parts, "%s")
			}
		}
		code := StripPythonSpace(procedure.Label + " " + strings.Join(parts, " "))
		procedureCodes[code] = true
	}
	if len(procedureCodes) != len(m.Procedures) {
		return errors.New("procedure proccodes must be unique")
	}
	count := 0
	var visit func(block *Block, depth int) error
	visit = func(block *Block, depth int) error {
		if depth > MaxBlockDepth {
			return errors.New("block nesting is too deep")
		}
		count++
		if count > MaxBlocks {
			return errors.New("program has too many blocks")
		}
		for _, name := range block.InputNames() {
			blockInput, ok := AsBlockInputValue(block.Inputs[name])
			if !ok {
				continue
			}
			if err := visit(&blockInput.Block, depth+1); err != nil {
				return err
			}
		}
		for _, stackName := range block.SubstacksNames() {
			for index := range block.Substacks[stackName] {
				if err := visit(&block.Substacks[stackName][index], depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for scriptIndex := range m.Scripts {
		for blockIndex := range m.Scripts[scriptIndex].Blocks {
			if err := visit(&m.Scripts[scriptIndex].Blocks[blockIndex], 1); err != nil {
				return err
			}
		}
	}
	for procedureIndex := range m.Procedures {
		for blockIndex := range m.Procedures[procedureIndex].Body {
			if err := visit(&m.Procedures[procedureIndex].Body[blockIndex], 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// AssetMatchCandidate ports AssetMatchCandidate.
type AssetMatchCandidate struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func (m *AssetMatchCandidate) requiredJSONFields() []string { return []string{"name"} }

// Validate ports AssetMatchCandidate validation.
func (m *AssetMatchCandidate) Validate() error {
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	return validateShortStringList("/tags", m.Tags, 20)
}

// AssetMatchEvidence ports AssetMatchEvidence.
type AssetMatchEvidence struct {
	RequirementID   string                `json:"requirement_id"`
	AssetType       string                `json:"asset_type"`
	ManifestVersion string                `json:"manifest_version"`
	MatchCount      int                   `json:"match_count"`
	Candidates      []AssetMatchCandidate `json:"candidates"`
}

func (m *AssetMatchEvidence) requiredJSONFields() []string {
	return []string{"requirement_id", "asset_type", "manifest_version", "match_count"}
}

// Validate ports validate_count.
func (m *AssetMatchEvidence) Validate() error {
	if err := validateShortString("/requirement_id", m.RequirementID); err != nil {
		return err
	}
	if err := validateLiteral("/asset_type", m.AssetType, "sprite", "costume", "backdrop", "sound"); err != nil {
		return err
	}
	if err := validateLiteral("/manifest_version", m.ManifestVersion, "scratch-gui-15.1.1"); err != nil {
		return err
	}
	if err := requireIntRange("/match_count", m.MatchCount, 0, 1000); err != nil {
		return err
	}
	if err := requireListLength("/candidates", len(m.Candidates), -1, 5); err != nil {
		return err
	}
	for index := range m.Candidates {
		if err := m.Candidates[index].Validate(); err != nil {
			return err
		}
	}
	if m.MatchCount < len(m.Candidates) {
		return errors.New("match_count cannot be smaller than candidates length")
	}
	return nil
}
