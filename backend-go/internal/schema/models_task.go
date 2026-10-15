package schema

import (
	"errors"
	"reflect"
	"strings"
)

// TaskAssumption ports TaskAssumption.
type TaskAssumption struct {
	ID                   string  `json:"id"`
	Text                 string  `json:"text"`
	Confidence           float64 `json:"confidence"`
	RequiresConfirmation bool    `json:"requires_confirmation"`
}

func (m *TaskAssumption) requiredJSONFields() []string {
	return []string{"id", "text", "confidence", "requires_confirmation"}
}

// Validate ports TaskAssumption validation.
func (m *TaskAssumption) Validate() error {
	if err := validateShortString("/id", m.ID); err != nil {
		return err
	}
	if err := validateShortString("/text", m.Text); err != nil {
		return err
	}
	return requireFloatRange("/confidence", m.Confidence, 0, 1)
}

// TaskEntity ports TaskEntity.
type TaskEntity struct {
	EntityID          string         `json:"entity_id"`
	Role              string         `json:"role"`
	Description       string         `json:"description"`
	ExistingTargetRef *TaskTargetRef `json:"existing_target_ref"`
}

func (m *TaskEntity) requiredJSONFields() []string {
	return []string{"entity_id", "role", "description"}
}

// Validate ports validate_role_reference.
func (m *TaskEntity) Validate() error {
	if err := validateShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	if err := validateLiteral("/role", m.Role, "stage", "sprite"); err != nil {
		return err
	}
	if err := validateShortString("/description", m.Description); err != nil {
		return err
	}
	if m.ExistingTargetRef != nil {
		if err := m.ExistingTargetRef.Validate(); err != nil {
			return err
		}
	}
	if m.Role == "stage" && m.ExistingTargetRef != nil && m.ExistingTargetRef.Kind != "stage" {
		return errors.New("a stage entity can only bind to the stage")
	}
	return nil
}

// TaskState ports TaskState.
type TaskState struct {
	StateID       string  `json:"state_id"`
	Kind          string  `json:"kind"`
	Scope         string  `json:"scope"`
	OwnerEntityID *string `json:"owner_entity_id"`
	InitialValue  any     `json:"initial_value"`
}

func (m *TaskState) requiredJSONFields() []string { return []string{"state_id", "kind", "scope"} }

// Validate ports validate_state_semantics.
func (m *TaskState) Validate() error {
	if err := validateShortString("/state_id", m.StateID); err != nil {
		return err
	}
	if err := validateLiteral("/kind", m.Kind, "variable", "list", "broadcast"); err != nil {
		return err
	}
	if err := validateLiteral("/scope", m.Scope, "global", "local"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/initial_value", m.InitialValue, true); err != nil {
		return err
	}
	if m.Kind == "broadcast" {
		if m.Scope != "global" || m.OwnerEntityID != nil || m.InitialValue != nil {
			return errors.New("broadcast state must be global and has no owner or initial_value")
		}
	}
	if m.Scope == "local" && stringValue(m.OwnerEntityID) == "" {
		return errors.New("local state requires owner_entity_id")
	}
	if m.Scope == "global" && m.OwnerEntityID != nil {
		return errors.New("global state cannot have owner_entity_id")
	}
	if m.Kind == "list" && m.InitialValue != nil {
		if _, ok := m.InitialValue.([]any); !ok {
			return errors.New("list initial_value must be an array")
		}
	}
	if m.Kind == "variable" {
		if _, ok := m.InitialValue.([]any); ok {
			return errors.New("variable initial_value cannot be an array")
		}
	}
	return nil
}

// OperandSpec ports OperandSpec.
type OperandSpec struct {
	Kind      string   `json:"kind"`
	Value     any      `json:"value"`
	StateID   *string  `json:"state_id"`
	EntityID  *string  `json:"entity_id"`
	Attribute *string  `json:"attribute"`
	Min       *float64 `json:"min"`
	Max       *float64 `json:"max"`
}

func (m *OperandSpec) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_operand.
func (m *OperandSpec) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "literal", "state", "attribute", "timer", "random"); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/value", m.Value, false); err != nil {
		return err
	}
	if err := validateOptionalShortString("/state_id", m.StateID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	if m.Attribute != nil {
		if err := validateLiteral("/attribute", *m.Attribute, "x", "y", "direction", "size", "visible"); err != nil {
			return err
		}
	}
	if m.Kind == "literal" && m.Value == nil {
		return errors.New("literal operand requires value")
	}
	if m.Kind == "state" && stringValue(m.StateID) == "" {
		return errors.New("state operand requires state_id")
	}
	if m.Kind == "attribute" && (stringValue(m.EntityID) == "" || m.Attribute == nil) {
		return errors.New("attribute operand requires entity_id and attribute")
	}
	if m.Kind == "random" && (m.Min == nil || m.Max == nil || *m.Min > *m.Max) {
		return errors.New("random operand requires min <= max")
	}
	forbidden := map[string][]any{
		"literal":   {m.StateID, m.EntityID, m.Attribute, m.Min, m.Max},
		"state":     {m.Value, m.EntityID, m.Attribute, m.Min, m.Max},
		"attribute": {m.Value, m.StateID, m.Min, m.Max},
		"timer":     {m.Value, m.StateID, m.EntityID, m.Attribute, m.Min, m.Max},
		"random":    {m.Value, m.StateID, m.EntityID, m.Attribute},
	}
	for _, candidate := range forbidden[m.Kind] {
		if !isNilAny(candidate) {
			return errors.New(m.Kind + " operand contains unrelated fields")
		}
	}
	return nil
}

func isNilAny(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return reflected.IsNil()
	}
	return false
}

// TriggerSpec ports TriggerSpec.
type TriggerSpec struct {
	Kind         string   `json:"kind"`
	Key          *string  `json:"key"`
	StateID      *string  `json:"state_id"`
	AssetPurpose *string  `json:"asset_purpose"`
	Seconds      *float64 `json:"seconds"`
}

func (m *TriggerSpec) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_trigger.
func (m *TriggerSpec) Validate() error {
	if err := validateLiteral("/kind", m.Kind,
		"green_flag", "key_pressed", "sprite_clicked", "broadcast_received",
		"backdrop_switched", "clone_started", "timer_reached"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/key", m.Key); err != nil {
		return err
	}
	if err := validateOptionalShortString("/state_id", m.StateID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/asset_purpose", m.AssetPurpose); err != nil {
		return err
	}
	if m.Seconds != nil {
		if err := requireFloatMin("/seconds", *m.Seconds, 0); err != nil {
			return err
		}
	}
	required := map[string]struct {
		field string
		value any
	}{
		"key_pressed":        {"key", m.Key},
		"broadcast_received": {"state_id", m.StateID},
		"backdrop_switched":  {"asset_purpose", m.AssetPurpose},
		"timer_reached":      {"seconds", m.Seconds},
	}
	if rule, ok := required[m.Kind]; ok && isNilAny(rule.value) {
		return errors.New(m.Kind + " requires " + rule.field)
	}
	allowed := map[string]bool{}
	if rule, ok := required[m.Kind]; ok {
		allowed[rule.field] = true
	}
	candidates := []struct {
		name  string
		value any
	}{
		{"key", m.Key},
		{"state_id", m.StateID},
		{"asset_purpose", m.AssetPurpose},
		{"seconds", m.Seconds},
	}
	for _, candidate := range candidates {
		if allowed[candidate.name] {
			continue
		}
		if !isNilAny(candidate.value) {
			return errors.New(m.Kind + " contains unrelated trigger fields")
		}
	}
	return nil
}

// ConditionSpec ports ConditionSpec.
type ConditionSpec struct {
	Kind     string          `json:"kind"`
	Left     *OperandSpec    `json:"left"`
	Operator *string         `json:"operator"`
	Right    *OperandSpec    `json:"right"`
	EntityID *string         `json:"entity_id"`
	Key      *string         `json:"key"`
	Operands []ConditionSpec `json:"operands" optional:"true"`
	Operand  *ConditionSpec  `json:"operand"`
}

func (m *ConditionSpec) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_condition.
func (m *ConditionSpec) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "compare", "touching", "touching_edge", "key_down", "logical", "not"); err != nil {
		return err
	}
	if m.Left != nil {
		if err := m.Left.Validate(); err != nil {
			return err
		}
	}
	if m.Operator != nil {
		if err := validateLiteral("/operator", *m.Operator, "eq", "gt", "gte", "lt", "lte", "and", "or"); err != nil {
			return err
		}
	}
	if m.Right != nil {
		if err := m.Right.Validate(); err != nil {
			return err
		}
	}
	if err := validateOptionalShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/key", m.Key); err != nil {
		return err
	}
	if m.Operands != nil {
		if err := requireListLength("/operands", len(m.Operands), 2, 12); err != nil {
			return err
		}
	}
	for index := range m.Operands {
		if err := m.Operands[index].Validate(); err != nil {
			return err
		}
	}
	if m.Operand != nil {
		if err := m.Operand.Validate(); err != nil {
			return err
		}
	}
	if m.Kind == "compare" {
		if m.Left == nil || m.Right == nil || m.Operator == nil || !stringIn(*m.Operator, "eq", "gt", "gte", "lt", "lte") {
			return errors.New("compare condition requires left, comparison operator, and right")
		}
	}
	if m.Kind == "touching" && stringValue(m.EntityID) == "" {
		return errors.New("touching condition requires entity_id")
	}
	if m.Kind == "key_down" && stringValue(m.Key) == "" {
		return errors.New("key_down condition requires key")
	}
	if m.Kind == "logical" {
		operator := ""
		if m.Operator != nil {
			operator = *m.Operator
		}
		if (operator != "and" && operator != "or") || len(m.Operands) == 0 {
			return errors.New("logical condition requires and/or and operands")
		}
	}
	if m.Kind == "not" && m.Operand == nil {
		return errors.New("not condition requires operand")
	}
	return nil
}

func stringIn(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// ActionSpec ports ActionSpec.
type ActionSpec struct {
	Kind      string         `json:"kind"`
	Steps     *OperandSpec   `json:"steps"`
	Axis      *string        `json:"axis"`
	Value     *OperandSpec   `json:"value"`
	Delta     *OperandSpec   `json:"delta"`
	EntityID  *string        `json:"entity_id"`
	Degrees   *OperandSpec   `json:"degrees"`
	StateID   *string        `json:"state_id"`
	AssetKind *string        `json:"asset_kind"`
	Purpose   *string        `json:"purpose"`
	Wait      *bool          `json:"wait"`
	Visible   *bool          `json:"visible"`
	Text      *string        `json:"text"`
	Seconds   *OperandSpec   `json:"seconds"`
	Question  *string        `json:"question"`
	Mode      *string        `json:"mode"`
	Count     *OperandSpec   `json:"count"`
	Until     *ConditionSpec `json:"until"`
	Actions   []ActionSpec   `json:"actions" optional:"true"`
	Scope     *string        `json:"scope"`
}

func (m *ActionSpec) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_action.
func (m *ActionSpec) Validate() error {
	if err := validateLiteral("/kind", m.Kind,
		"move_steps", "set_axis", "change_axis", "go_to_entity", "turn", "point_towards",
		"set_state", "change_state", "broadcast", "use_asset", "set_visibility", "say",
		"ask", "create_clone", "delete_this_clone", "wait", "loop", "stop"); err != nil {
		return err
	}
	for _, operand := range []*OperandSpec{m.Steps, m.Value, m.Delta, m.Degrees, m.Seconds, m.Count} {
		if operand != nil {
			if err := operand.Validate(); err != nil {
				return err
			}
		}
	}
	if m.Axis != nil {
		if err := validateLiteral("/axis", *m.Axis, "x", "y"); err != nil {
			return err
		}
	}
	if err := validateOptionalShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/state_id", m.StateID); err != nil {
		return err
	}
	if m.AssetKind != nil {
		if err := validateLiteral("/asset_kind", *m.AssetKind, "costume", "backdrop", "sound"); err != nil {
			return err
		}
	}
	if err := validateOptionalShortString("/purpose", m.Purpose); err != nil {
		return err
	}
	if err := validateOptionalShortString("/text", m.Text); err != nil {
		return err
	}
	if err := validateOptionalShortString("/question", m.Question); err != nil {
		return err
	}
	if m.Mode != nil {
		if err := validateLiteral("/mode", *m.Mode, "forever", "repeat", "repeat_until"); err != nil {
			return err
		}
	}
	if m.Until != nil {
		if err := m.Until.Validate(); err != nil {
			return err
		}
	}
	if m.Actions != nil {
		if err := requireListLength("/actions", len(m.Actions), 1, 40); err != nil {
			return err
		}
	}
	for index := range m.Actions {
		if err := m.Actions[index].Validate(); err != nil {
			return err
		}
	}
	if m.Scope != nil {
		if err := validateLiteral("/scope", *m.Scope, "this_script", "all"); err != nil {
			return err
		}
	}
	required := map[string][]struct {
		field string
		value any
	}{
		"move_steps":        {{"steps", m.Steps}},
		"set_axis":          {{"axis", m.Axis}, {"value", m.Value}},
		"change_axis":       {{"axis", m.Axis}, {"delta", m.Delta}},
		"go_to_entity":      {{"entity_id", m.EntityID}},
		"turn":              {{"degrees", m.Degrees}},
		"point_towards":     {{"entity_id", m.EntityID}},
		"set_state":         {{"state_id", m.StateID}, {"value", m.Value}},
		"change_state":      {{"state_id", m.StateID}, {"delta", m.Delta}},
		"broadcast":         {{"state_id", m.StateID}},
		"use_asset":         {{"asset_kind", m.AssetKind}, {"purpose", m.Purpose}},
		"set_visibility":    {{"visible", m.Visible}},
		"say":               {{"text", m.Text}},
		"ask":               {{"question", m.Question}},
		"create_clone":      {{"entity_id", m.EntityID}},
		"delete_this_clone": {},
		"wait":              {{"seconds", m.Seconds}},
		"loop":              {{"mode", m.Mode}, {"actions", actionsRequired(m.Actions)}},
		"stop":              {{"scope", m.Scope}},
	}
	missing := make([]string, 0, 2)
	for _, rule := range required[m.Kind] {
		if isNilAny(rule.value) {
			missing = append(missing, rule.field)
		}
	}
	if len(missing) > 0 {
		return errors.New(m.Kind + " requires " + strings.Join(missing, ", "))
	}
	if m.Kind == "loop" {
		mode := ""
		if m.Mode != nil {
			mode = *m.Mode
		}
		if mode == "repeat" && m.Count == nil {
			return errors.New("repeat loop requires count")
		}
		if mode == "repeat_until" && m.Until == nil {
			return errors.New("repeat_until loop requires until")
		}
		if mode == "forever" && (m.Count != nil || m.Until != nil) {
			return errors.New("forever loop cannot contain count or until")
		}
	}
	return nil
}

func actionsRequired(actions []ActionSpec) any {
	if len(actions) == 0 {
		return nil
	}
	return actions
}

// TaskBehavior ports TaskBehavior.
type TaskBehavior struct {
	BehaviorID    string          `json:"behavior_id"`
	OwnerEntityID string          `json:"owner_entity_id"`
	Trigger       TriggerSpec     `json:"trigger"`
	Conditions    []ConditionSpec `json:"conditions"`
	Actions       []ActionSpec    `json:"actions"`
	Reads         []string        `json:"reads"`
	Writes        []string        `json:"writes"`
}

func (m *TaskBehavior) requiredJSONFields() []string {
	return []string{"behavior_id", "owner_entity_id", "trigger", "actions"}
}

// Validate ports TaskBehavior validation.
func (m *TaskBehavior) Validate() error {
	if err := validateShortString("/behavior_id", m.BehaviorID); err != nil {
		return err
	}
	if err := validateShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := m.Trigger.Validate(); err != nil {
		return err
	}
	if err := requireListLength("/conditions", len(m.Conditions), -1, 12); err != nil {
		return err
	}
	for index := range m.Conditions {
		if err := m.Conditions[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/actions", len(m.Actions), 1, 40); err != nil {
		return err
	}
	for index := range m.Actions {
		if err := m.Actions[index].Validate(); err != nil {
			return err
		}
	}
	if err := validateShortStringList("/reads", m.Reads, 100); err != nil {
		return err
	}
	return validateShortStringList("/writes", m.Writes, 100)
}

// SuccessCriterionSpec ports SuccessCriterionSpec.
type SuccessCriterionSpec struct {
	CriterionID        string   `json:"criterion_id"`
	Description        string   `json:"description"`
	RelatedBehaviorIDs []string `json:"related_behavior_ids"`
	Priority           string   `json:"priority"`
	Verification       string   `json:"verification"`
}

func (m *SuccessCriterionSpec) requiredJSONFields() []string {
	return []string{"criterion_id", "description", "related_behavior_ids", "priority", "verification"}
}

// Validate ports SuccessCriterionSpec validation.
func (m *SuccessCriterionSpec) Validate() error {
	if err := validateShortString("/criterion_id", m.CriterionID); err != nil {
		return err
	}
	if err := validateShortString("/description", m.Description); err != nil {
		return err
	}
	if err := validateShortStringList("/related_behavior_ids", m.RelatedBehaviorIDs, 40); err != nil {
		return err
	}
	if err := validateLiteral("/priority", m.Priority, "must", "should"); err != nil {
		return err
	}
	return validateLiteral("/verification", m.Verification, "executable", "structural", "manual", "unsupported")
}

// UnsupportedRequirement ports UnsupportedRequirement.
type UnsupportedRequirement struct {
	Requirement string `json:"requirement"`
	ReasonCode  string `json:"reason_code"`
}

func (m *UnsupportedRequirement) requiredJSONFields() []string {
	return []string{"requirement", "reason_code"}
}

// Validate ports UnsupportedRequirement validation.
func (m *UnsupportedRequirement) Validate() error {
	if err := validateShortString("/requirement", m.Requirement); err != nil {
		return err
	}
	return validateShortString("/reason_code", m.ReasonCode)
}

// ScratchTaskSpec ports ScratchTaskSpec.
type ScratchTaskSpec struct {
	SchemaVersion   int                      `json:"schema_version"`
	Title           string                   `json:"title"`
	Goal            string                   `json:"goal"`
	Assumptions     []TaskAssumption         `json:"assumptions"`
	Entities        []TaskEntity             `json:"entities"`
	State           []TaskState              `json:"state"`
	Behaviors       []TaskBehavior           `json:"behaviors"`
	SuccessCriteria []SuccessCriterionSpec   `json:"success_criteria"`
	Unsupported     []UnsupportedRequirement `json:"unsupported"`
}

func (m *ScratchTaskSpec) requiredJSONFields() []string {
	return []string{"schema_version", "title", "goal", "entities", "behaviors", "success_criteria"}
}

// Validate ports validate_task_lineage.
func (m *ScratchTaskSpec) Validate() error {
	if m.SchemaVersion != 2 {
		return validationError("/schema_version", "Input should be 2")
	}
	if err := validateShortString("/title", m.Title); err != nil {
		return err
	}
	if err := validateLongString("/goal", m.Goal); err != nil {
		return err
	}
	if err := requireListLength("/assumptions", len(m.Assumptions), -1, 10); err != nil {
		return err
	}
	for index := range m.Assumptions {
		if err := m.Assumptions[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/entities", len(m.Entities), 1, 12); err != nil {
		return err
	}
	for index := range m.Entities {
		if err := m.Entities[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/state", len(m.State), -1, 150); err != nil {
		return err
	}
	for index := range m.State {
		if err := m.State[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/behaviors", len(m.Behaviors), 1, 80); err != nil {
		return err
	}
	for index := range m.Behaviors {
		if err := m.Behaviors[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/success_criteria", len(m.SuccessCriteria), 1, 40); err != nil {
		return err
	}
	for index := range m.SuccessCriteria {
		if err := m.SuccessCriteria[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/unsupported", len(m.Unsupported), -1, 40); err != nil {
		return err
	}
	for index := range m.Unsupported {
		if err := m.Unsupported[index].Validate(); err != nil {
			return err
		}
	}
	return m.validateTaskLineage()
}

func (m *ScratchTaskSpec) validateTaskLineage() error {
	unique := func(values []string, label string) error {
		if len(values) != len(stringSet(values)) {
			return errors.New(label + " values must be unique")
		}
		return nil
	}
	entityIDs := make([]string, 0, len(m.Entities))
	for index := range m.Entities {
		entityIDs = append(entityIDs, m.Entities[index].EntityID)
	}
	stateIDs := make([]string, 0, len(m.State))
	for index := range m.State {
		stateIDs = append(stateIDs, m.State[index].StateID)
	}
	behaviorIDs := make([]string, 0, len(m.Behaviors))
	for index := range m.Behaviors {
		behaviorIDs = append(behaviorIDs, m.Behaviors[index].BehaviorID)
	}
	criterionIDs := make([]string, 0, len(m.SuccessCriteria))
	for index := range m.SuccessCriteria {
		criterionIDs = append(criterionIDs, m.SuccessCriteria[index].CriterionID)
	}
	for _, group := range []struct {
		values []string
		label  string
	}{
		{entityIDs, "entity_id"},
		{stateIDs, "state_id"},
		{behaviorIDs, "behavior_id"},
		{criterionIDs, "criterion_id"},
	} {
		if err := unique(group.values, group.label); err != nil {
			return err
		}
	}
	entities := stringSet(entityIDs)
	states := stringSet(stateIDs)
	behaviors := stringSet(behaviorIDs)
	stateByID := map[string]*TaskState{}
	for index := range m.State {
		stateByID[m.State[index].StateID] = &m.State[index]
	}
	var validateOperand func(operand *OperandSpec) error
	validateOperand = func(operand *OperandSpec) error {
		if operand.Kind == "state" && !states[stringValue(operand.StateID)] {
			return errors.New("operand state_id must reference declared state")
		}
		if operand.Kind == "attribute" && !entities[stringValue(operand.EntityID)] {
			return errors.New("operand entity_id must reference declared entity")
		}
		return nil
	}
	var validateCondition func(condition *ConditionSpec) error
	validateCondition = func(condition *ConditionSpec) error {
		switch condition.Kind {
		case "compare":
			if condition.Left != nil {
				if err := validateOperand(condition.Left); err != nil {
					return err
				}
			}
			if condition.Right != nil {
				if err := validateOperand(condition.Right); err != nil {
					return err
				}
			}
		case "touching":
			if !entities[stringValue(condition.EntityID)] {
				return errors.New("touching condition entity_id must reference declared entity")
			}
		case "logical":
			for index := range condition.Operands {
				if err := validateCondition(&condition.Operands[index]); err != nil {
					return err
				}
			}
		case "not":
			if condition.Operand != nil {
				if err := validateCondition(condition.Operand); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var validateAction func(action *ActionSpec) error
	validateAction = func(action *ActionSpec) error {
		for _, operand := range []*OperandSpec{action.Steps, action.Value, action.Delta, action.Degrees, action.Seconds, action.Count} {
			if operand == nil {
				continue
			}
			if err := validateOperand(operand); err != nil {
				return err
			}
		}
		if action.Until != nil {
			if err := validateCondition(action.Until); err != nil {
				return err
			}
		}
		if action.EntityID != nil && !entities[*action.EntityID] {
			return errors.New("action entity_id must reference declared entity")
		}
		if action.StateID != nil && !states[*action.StateID] {
			return errors.New("action state_id must reference declared state")
		}
		if action.Kind == "broadcast" {
			state := stateByID[stringValue(action.StateID)]
			if state == nil || state.Kind != "broadcast" {
				return errors.New("broadcast action must reference a broadcast state")
			}
		}
		for index := range action.Actions {
			if err := validateAction(&action.Actions[index]); err != nil {
				return err
			}
		}
		return nil
	}
	for index := range m.State {
		item := &m.State[index]
		if item.OwnerEntityID != nil && *item.OwnerEntityID != "" && !entities[*item.OwnerEntityID] {
			return errors.New("state owner_entity_id must reference an entity")
		}
	}
	for index := range m.Behaviors {
		behavior := &m.Behaviors[index]
		if !entities[behavior.OwnerEntityID] {
			return errors.New("behavior owner_entity_id must reference an entity")
		}
		references := append(append([]string{}, behavior.Reads...), behavior.Writes...)
		for _, stateID := range references {
			if !states[stateID] {
				return errors.New("behavior reads and writes must reference declared state")
			}
		}
		for _, stateID := range references {
			localState := stateByID[stateID]
			if localState.Scope == "local" && stringValue(localState.OwnerEntityID) != behavior.OwnerEntityID {
				return errors.New("behavior cannot read or write another entity local state")
			}
		}
		if behavior.Trigger.Kind == "broadcast_received" {
			broadcast := stateByID[stringValue(behavior.Trigger.StateID)]
			if broadcast == nil || broadcast.Kind != "broadcast" {
				return errors.New("broadcast trigger must reference a broadcast state")
			}
		}
		for conditionIndex := range behavior.Conditions {
			if err := validateCondition(&behavior.Conditions[conditionIndex]); err != nil {
				return err
			}
		}
		for actionIndex := range behavior.Actions {
			if err := validateAction(&behavior.Actions[actionIndex]); err != nil {
				return err
			}
		}
	}
	for index := range m.SuccessCriteria {
		criterion := &m.SuccessCriteria[index]
		for _, behaviorID := range criterion.RelatedBehaviorIDs {
			if !behaviors[behaviorID] {
				return errors.New("success criterion must reference declared behaviors")
			}
		}
	}
	return nil
}
