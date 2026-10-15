package schema

import (
	"errors"
	"strings"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

// BlueprintAssetNeed ports BlueprintAssetNeed.
type BlueprintAssetNeed struct {
	NeedID        string         `json:"need_id"`
	Kind          string         `json:"kind"`
	OwnerEntityID *string        `json:"owner_entity_id"`
	Purpose       string         `json:"purpose"`
	SearchTerms   []string       `json:"search_terms"`
	Tags          []string       `json:"tags"`
	FallbackOrder []string       `json:"fallback_order"`
	Constraints   map[string]any `json:"constraints"`
}

func (m *BlueprintAssetNeed) requiredJSONFields() []string {
	return []string{"need_id", "kind", "purpose", "search_terms", "fallback_order"}
}

// Validate ports validate_need.
func (m *BlueprintAssetNeed) Validate() error {
	if err := validateShortString("/need_id", m.NeedID); err != nil {
		return err
	}
	if err := validateLiteral("/kind", m.Kind, "sprite", "costume", "backdrop", "sound"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := validateShortString("/purpose", m.Purpose); err != nil {
		return err
	}
	if err := requireListLength("/search_terms", len(m.SearchTerms), 1, 12); err != nil {
		return err
	}
	for index, term := range m.SearchTerms {
		if err := validateShortString("/search_terms/"+itoa(index), term); err != nil {
			return err
		}
	}
	if err := validateShortStringList("/tags", m.Tags, 20); err != nil {
		return err
	}
	if err := requireListLength("/fallback_order", len(m.FallbackOrder), 1, 5); err != nil {
		return err
	}
	for index, fallback := range m.FallbackOrder {
		if err := validateLiteral("/fallback_order/"+itoa(index), fallback,
			"project_asset", "scratch_library", "user_asset", "image_generation", "audio_generation"); err != nil {
			return err
		}
	}
	if err := requireListLength("/constraints", len(m.Constraints), -1, 4); err != nil {
		return err
	}
	for _, key := range jsonx.SortedKeys(m.Constraints) {
		switch typed := m.Constraints[key].(type) {
		case bool, int, int64:
		case float64:
			if typed != float64(int64(typed)) {
				return validationError("/constraints/"+key, "Input should be a valid integer or boolean")
			}
		default:
			return validationError("/constraints/"+key, "Input should be a valid integer or boolean")
		}
	}
	if len(m.FallbackOrder) != len(stringSet(m.FallbackOrder)) {
		return errors.New("asset fallback_order entries must be unique")
	}
	if m.Kind != "sound" && containsString(m.FallbackOrder, "audio_generation") {
		return errors.New("audio_generation only applies to sound needs")
	}
	if m.Kind == "sound" && containsString(m.FallbackOrder, "image_generation") {
		return errors.New("image_generation cannot satisfy a sound need")
	}
	allowed := map[string]bool{"transparent": true, "max_bytes": true, "max_duration_ms": true, "animation_frames": true}
	for key := range m.Constraints {
		if !allowed[key] {
			return errors.New("asset need constraints contain an unknown key")
		}
	}
	return nil
}

// BlueprintEntityResolution ports BlueprintEntityResolution.
type BlueprintEntityResolution struct {
	Kind      string         `json:"kind"`
	TargetRef *TaskTargetRef `json:"target_ref"`
	NeedID    *string        `json:"need_id"`
}

func (m *BlueprintEntityResolution) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_resolution.
func (m *BlueprintEntityResolution) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "existing_target", "new_sprite"); err != nil {
		return err
	}
	if m.TargetRef != nil {
		if err := m.TargetRef.Validate(); err != nil {
			return err
		}
	}
	if err := validateOptionalShortString("/need_id", m.NeedID); err != nil {
		return err
	}
	if m.Kind == "existing_target" && (m.TargetRef == nil || m.NeedID != nil) {
		return errors.New("existing_target binding requires target_ref only")
	}
	if m.Kind == "new_sprite" && (m.NeedID == nil || m.TargetRef != nil) {
		return errors.New("new_sprite binding requires need_id only")
	}
	return nil
}

// BlueprintEntityBinding ports BlueprintEntityBinding.
type BlueprintEntityBinding struct {
	EntityID   string                    `json:"entity_id"`
	Resolution BlueprintEntityResolution `json:"resolution"`
}

func (m *BlueprintEntityBinding) requiredJSONFields() []string {
	return []string{"entity_id", "resolution"}
}

// Validate ports BlueprintEntityBinding validation.
func (m *BlueprintEntityBinding) Validate() error {
	if err := validateShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	return m.Resolution.Validate()
}

// BlueprintStatePlan ports BlueprintStatePlan.
type BlueprintStatePlan struct {
	RefID         string  `json:"ref_id"`
	Kind          string  `json:"kind"`
	Scope         string  `json:"scope"`
	OwnerEntityID *string `json:"owner_entity_id"`
	Name          string  `json:"name"`
	InitialValue  any     `json:"initial_value"`
}

func (m *BlueprintStatePlan) requiredJSONFields() []string {
	return []string{"ref_id", "kind", "scope", "name"}
}

// Validate ports validate_state_plan.
func (m *BlueprintStatePlan) Validate() error {
	if err := validateShortString("/ref_id", m.RefID); err != nil {
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
	if err := validateShortString("/name", m.Name); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/initial_value", m.InitialValue, true); err != nil {
		return err
	}
	if m.Kind == "broadcast" && (m.Scope != "global" || m.OwnerEntityID != nil || m.InitialValue != nil) {
		return errors.New("broadcast plan must be global and have no owner or initial value")
	}
	if m.Scope == "local" && stringValue(m.OwnerEntityID) == "" {
		return errors.New("local state plan requires owner_entity_id")
	}
	if m.Scope == "global" && m.OwnerEntityID != nil {
		return errors.New("global state plan cannot have owner_entity_id")
	}
	return nil
}

// BlueprintBehaviorGraphNode ports BlueprintBehaviorGraphNode.
type BlueprintBehaviorGraphNode struct {
	BehaviorID         string   `json:"behavior_id"`
	OwnerEntityID      string   `json:"owner_entity_id"`
	DependsOn          []string `json:"depends_on"`
	ProducesBroadcasts []string `json:"produces_broadcasts"`
	ConsumesBroadcasts []string `json:"consumes_broadcasts"`
}

func (m *BlueprintBehaviorGraphNode) requiredJSONFields() []string {
	return []string{"behavior_id", "owner_entity_id"}
}

// Validate ports BlueprintBehaviorGraphNode validation.
func (m *BlueprintBehaviorGraphNode) Validate() error {
	if err := validateShortString("/behavior_id", m.BehaviorID); err != nil {
		return err
	}
	if err := validateShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := validateShortStringList("/depends_on", m.DependsOn, 40); err != nil {
		return err
	}
	if err := validateShortStringList("/produces_broadcasts", m.ProducesBroadcasts, 40); err != nil {
		return err
	}
	return validateShortStringList("/consumes_broadcasts", m.ConsumesBroadcasts, 40)
}

// BlueprintProgramJob ports BlueprintProgramJob.
type BlueprintProgramJob struct {
	JobID         string   `json:"job_id"`
	OwnerEntityID string   `json:"owner_entity_id"`
	Mode          string   `json:"mode"`
	BehaviorIDs   []string `json:"behavior_ids"`
}

func (m *BlueprintProgramJob) requiredJSONFields() []string {
	return []string{"job_id", "owner_entity_id", "mode", "behavior_ids"}
}

// Validate ports BlueprintProgramJob validation.
func (m *BlueprintProgramJob) Validate() error {
	if err := validateShortString("/job_id", m.JobID); err != nil {
		return err
	}
	if err := validateShortString("/owner_entity_id", m.OwnerEntityID); err != nil {
		return err
	}
	if err := validateLiteral("/mode", m.Mode, "append", "modify", "rewrite"); err != nil {
		return err
	}
	return validateShortStringList("/behavior_ids", m.BehaviorIDs, 40)
}

// AcceptanceTrigger ports AcceptanceTrigger.
type AcceptanceTrigger struct {
	Kind     string  `json:"kind"`
	Key      *string `json:"key"`
	EntityID *string `json:"entity_id"`
	StateID  *string `json:"state_id"`
}

func (m *AcceptanceTrigger) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_trigger.
func (m *AcceptanceTrigger) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "green_flag", "key", "sprite_click", "broadcast"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/key", m.Key); err != nil {
		return err
	}
	if err := validateOptionalShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/state_id", m.StateID); err != nil {
		return err
	}
	required := map[string]struct {
		field string
		value any
	}{
		"key":          {"key", m.Key},
		"sprite_click": {"entity_id", m.EntityID},
		"broadcast":    {"state_id", m.StateID},
	}
	if rule, ok := required[m.Kind]; ok && isNilAny(rule.value) {
		return errors.New(m.Kind + " acceptance trigger requires " + rule.field)
	}
	return nil
}

// AcceptanceArrange ports AcceptanceArrange.
type AcceptanceArrange struct {
	Kind     string   `json:"kind"`
	EntityID *string  `json:"entity_id"`
	StateID  *string  `json:"state_id"`
	X        *float64 `json:"x"`
	Y        *float64 `json:"y"`
	Value    any      `json:"value"`
	Seconds  *float64 `json:"seconds"`
}

func (m *AcceptanceArrange) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_arrange.
func (m *AcceptanceArrange) Validate() error {
	if err := validateLiteral("/kind", m.Kind, "set_position", "set_variable", "set_list", "set_timer"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/state_id", m.StateID); err != nil {
		return err
	}
	if err := validateScalarOrListValue("/value", m.Value, true); err != nil {
		return err
	}
	required := map[string][]struct {
		field string
		value any
	}{
		"set_position": {{"entity_id", m.EntityID}, {"x", m.X}, {"y", m.Y}},
		"set_variable": {{"state_id", m.StateID}, {"value", m.Value}},
		"set_list":     {{"state_id", m.StateID}, {"value", m.Value}},
		"set_timer":    {{"seconds", m.Seconds}},
	}
	missing := make([]string, 0, 3)
	for _, rule := range required[m.Kind] {
		if isNilAny(rule.value) {
			missing = append(missing, rule.field)
		}
	}
	if len(missing) > 0 {
		return errors.New(m.Kind + " arrangement requires " + strings.Join(missing, ", "))
	}
	if m.Kind == "set_list" {
		if _, ok := m.Value.([]any); !ok {
			return errors.New("set_list arrangement requires a list value")
		}
	}
	if m.Kind == "set_variable" {
		if _, ok := m.Value.([]any); ok {
			return errors.New("set_variable arrangement requires a scalar value")
		}
	}
	if m.Kind == "set_timer" && m.Seconds != nil && (*m.Seconds < 0 || *m.Seconds > 86400) {
		return errors.New("set_timer arrangement requires seconds from 0 to 86400")
	}
	return nil
}

// AcceptanceExpectation ports AcceptanceExpectation.
type AcceptanceExpectation struct {
	Kind        string   `json:"kind"`
	StateID     *string  `json:"state_id"`
	EntityID    *string  `json:"entity_id"`
	AssetNeedID *string  `json:"asset_need_id"`
	Comparator  *string  `json:"comparator"`
	Value       any      `json:"value"`
	Axis        *string  `json:"axis"`
	Min         *float64 `json:"min"`
	Max         *float64 `json:"max"`
	Visible     *bool    `json:"visible"`
	Text        *string  `json:"text"`
	MinCount    *int     `json:"min_count"`
}

func (m *AcceptanceExpectation) requiredJSONFields() []string { return []string{"kind"} }

// Validate ports validate_expectation.
func (m *AcceptanceExpectation) Validate() error {
	if err := validateLiteral("/kind", m.Kind,
		"variable", "position_range", "costume", "backdrop", "visibility", "list",
		"clone_count", "say_text", "question_asked", "broadcast_emitted",
		"sound_invoked", "scripts_stopped"); err != nil {
		return err
	}
	if err := validateOptionalShortString("/state_id", m.StateID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/entity_id", m.EntityID); err != nil {
		return err
	}
	if err := validateOptionalShortString("/asset_need_id", m.AssetNeedID); err != nil {
		return err
	}
	if m.Comparator != nil {
		if err := validateLiteral("/comparator", *m.Comparator, "eq", "gt", "gte", "lt", "lte", "equals", "contains", "length"); err != nil {
			return err
		}
	}
	if err := validateScalarOrListValue("/value", m.Value, true); err != nil {
		return err
	}
	if m.Axis != nil {
		if err := validateLiteral("/axis", *m.Axis, "x", "y"); err != nil {
			return err
		}
	}
	if err := validateOptionalShortString("/text", m.Text); err != nil {
		return err
	}
	if m.MinCount != nil {
		if err := requireIntMin("/min_count", *m.MinCount, 0); err != nil {
			return err
		}
	}
	required := map[string][]struct {
		field string
		value any
	}{
		"variable":          {{"state_id", m.StateID}, {"comparator", m.Comparator}, {"value", m.Value}},
		"position_range":    {{"entity_id", m.EntityID}, {"axis", m.Axis}, {"min", m.Min}, {"max", m.Max}},
		"costume":           {{"entity_id", m.EntityID}, {"asset_need_id", m.AssetNeedID}},
		"backdrop":          {{"asset_need_id", m.AssetNeedID}},
		"visibility":        {{"entity_id", m.EntityID}, {"visible", m.Visible}},
		"list":              {{"state_id", m.StateID}, {"comparator", m.Comparator}, {"value", m.Value}},
		"clone_count":       {{"entity_id", m.EntityID}, {"comparator", m.Comparator}, {"value", m.Value}},
		"say_text":          {{"entity_id", m.EntityID}, {"text", m.Text}},
		"question_asked":    {{"text", m.Text}},
		"broadcast_emitted": {{"state_id", m.StateID}, {"min_count", m.MinCount}},
		"sound_invoked":     {{"entity_id", m.EntityID}, {"asset_need_id", m.AssetNeedID}, {"min_count", m.MinCount}},
		"scripts_stopped":   {},
	}
	missing := make([]string, 0, 3)
	for _, rule := range required[m.Kind] {
		if isNilAny(rule.value) {
			missing = append(missing, rule.field)
		}
	}
	if len(missing) > 0 {
		return errors.New(m.Kind + " expectation requires " + strings.Join(missing, ", "))
	}
	allowedComparators := map[string]map[string]bool{
		"variable":    {"eq": true, "gt": true, "gte": true, "lt": true, "lte": true},
		"list":        {"equals": true, "contains": true, "length": true},
		"clone_count": {"eq": true, "gte": true, "lte": true},
	}
	if allowed, ok := allowedComparators[m.Kind]; ok {
		comparator := ""
		if m.Comparator != nil {
			comparator = *m.Comparator
		}
		if !allowed[comparator] {
			return errors.New(m.Kind + " expectation has an invalid comparator")
		}
	}
	if m.Kind == "position_range" && m.Min != nil && m.Max != nil && *m.Min > *m.Max {
		return errors.New("position_range expectation min cannot exceed max")
	}
	if m.Kind == "clone_count" {
		switch m.Value.(type) {
		case float64, int, int64:
		default:
			return errors.New("clone_count expectation requires a numeric value")
		}
	}
	return nil
}

// BlueprintAcceptanceTest ports BlueprintAcceptanceTest.
type BlueprintAcceptanceTest struct {
	TestID             string                  `json:"test_id"`
	SourceCriterionID  string                  `json:"source_criterion_id"`
	RelatedBehaviorIDs []string                `json:"related_behavior_ids"`
	Trigger            AcceptanceTrigger       `json:"trigger"`
	Arrange            []AcceptanceArrange     `json:"arrange"`
	Expect             []AcceptanceExpectation `json:"expect"`
	TimeoutMS          int                     `json:"timeout_ms"`
}

func (m *BlueprintAcceptanceTest) requiredJSONFields() []string {
	return []string{"test_id", "source_criterion_id", "related_behavior_ids", "trigger", "expect", "timeout_ms"}
}

// Validate ports BlueprintAcceptanceTest validation.
func (m *BlueprintAcceptanceTest) Validate() error {
	if err := validateShortString("/test_id", m.TestID); err != nil {
		return err
	}
	if err := validateShortString("/source_criterion_id", m.SourceCriterionID); err != nil {
		return err
	}
	if err := validateShortStringList("/related_behavior_ids", m.RelatedBehaviorIDs, 40); err != nil {
		return err
	}
	if err := m.Trigger.Validate(); err != nil {
		return err
	}
	if err := requireListLength("/arrange", len(m.Arrange), -1, 20); err != nil {
		return err
	}
	for index := range m.Arrange {
		if err := m.Arrange[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/expect", len(m.Expect), 1, 20); err != nil {
		return err
	}
	for index := range m.Expect {
		if err := m.Expect[index].Validate(); err != nil {
			return err
		}
	}
	return requireIntRange("/timeout_ms", m.TimeoutMS, 1, 4000)
}

// ProjectBlueprint ports ProjectBlueprint.
type ProjectBlueprint struct {
	SchemaVersion   int                          `json:"schema_version"`
	TaskSpecHash    string                       `json:"task_spec_hash"`
	AssetNeeds      []BlueprintAssetNeed         `json:"asset_needs"`
	EntityBindings  []BlueprintEntityBinding     `json:"entity_bindings"`
	StatePlan       []BlueprintStatePlan         `json:"state_plan"`
	BehaviorGraph   []BlueprintBehaviorGraphNode `json:"behavior_graph"`
	ProgramJobs     []BlueprintProgramJob        `json:"program_jobs"`
	AcceptanceTests []BlueprintAcceptanceTest    `json:"acceptance_tests"`
}

func (m *ProjectBlueprint) requiredJSONFields() []string {
	return []string{"schema_version", "task_spec_hash", "entity_bindings", "behavior_graph", "program_jobs"}
}

// Validate ports validate_blueprint.
func (m *ProjectBlueprint) Validate() error {
	if m.SchemaVersion != 2 {
		return validationError("/schema_version", "Input should be 2")
	}
	if !hashPattern.MatchString(m.TaskSpecHash) {
		return validationError("/task_spec_hash", "String should match pattern '^[a-f0-9]{64}$'")
	}
	if err := requireListLength("/asset_needs", len(m.AssetNeeds), -1, 24); err != nil {
		return err
	}
	for index := range m.AssetNeeds {
		if err := m.AssetNeeds[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/entity_bindings", len(m.EntityBindings), 1, 12); err != nil {
		return err
	}
	for index := range m.EntityBindings {
		if err := m.EntityBindings[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/state_plan", len(m.StatePlan), -1, 150); err != nil {
		return err
	}
	for index := range m.StatePlan {
		if err := m.StatePlan[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/behavior_graph", len(m.BehaviorGraph), 1, 80); err != nil {
		return err
	}
	for index := range m.BehaviorGraph {
		if err := m.BehaviorGraph[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/program_jobs", len(m.ProgramJobs), 1, 12); err != nil {
		return err
	}
	for index := range m.ProgramJobs {
		if err := m.ProgramJobs[index].Validate(); err != nil {
			return err
		}
	}
	if err := requireListLength("/acceptance_tests", len(m.AcceptanceTests), -1, 40); err != nil {
		return err
	}
	for index := range m.AcceptanceTests {
		if err := m.AcceptanceTests[index].Validate(); err != nil {
			return err
		}
	}
	return m.validateBlueprint()
}

func (m *ProjectBlueprint) validateBlueprint() error {
	unique := func(values []string, label string) error {
		if len(values) != len(stringSet(values)) {
			return errors.New(label + " values must be unique")
		}
		return nil
	}
	needIDs := make([]string, 0, len(m.AssetNeeds))
	for index := range m.AssetNeeds {
		needIDs = append(needIDs, m.AssetNeeds[index].NeedID)
	}
	bindingEntities := make([]string, 0, len(m.EntityBindings))
	for index := range m.EntityBindings {
		bindingEntities = append(bindingEntities, m.EntityBindings[index].EntityID)
	}
	stateRefs := make([]string, 0, len(m.StatePlan))
	for index := range m.StatePlan {
		stateRefs = append(stateRefs, m.StatePlan[index].RefID)
	}
	graphBehaviors := make([]string, 0, len(m.BehaviorGraph))
	for index := range m.BehaviorGraph {
		graphBehaviors = append(graphBehaviors, m.BehaviorGraph[index].BehaviorID)
	}
	jobIDs := make([]string, 0, len(m.ProgramJobs))
	for index := range m.ProgramJobs {
		jobIDs = append(jobIDs, m.ProgramJobs[index].JobID)
	}
	testIDs := make([]string, 0, len(m.AcceptanceTests))
	for index := range m.AcceptanceTests {
		testIDs = append(testIDs, m.AcceptanceTests[index].TestID)
	}
	for _, group := range []struct {
		values []string
		label  string
	}{
		{needIDs, "need_id"},
		{bindingEntities, "entity binding"},
		{stateRefs, "state ref_id"},
		{graphBehaviors, "behavior graph"},
		{jobIDs, "job_id"},
		{testIDs, "acceptance test"},
	} {
		if err := unique(group.values, group.label); err != nil {
			return err
		}
	}
	needs := map[string]*BlueprintAssetNeed{}
	for index := range m.AssetNeeds {
		needs[m.AssetNeeds[index].NeedID] = &m.AssetNeeds[index]
	}
	for index := range m.EntityBindings {
		binding := &m.EntityBindings[index]
		if binding.Resolution.Kind == "new_sprite" {
			need := needs[stringValue(binding.Resolution.NeedID)]
			if need == nil || need.Kind != "sprite" || stringValue(need.OwnerEntityID) != binding.EntityID {
				return errors.New("new_sprite binding must reference its entity sprite need")
			}
		}
	}
	graph := stringSet(graphBehaviors)
	for index := range m.BehaviorGraph {
		node := &m.BehaviorGraph[index]
		for _, dependency := range node.DependsOn {
			if !graph[dependency] {
				return errors.New("behavior dependency must reference a declared behavior")
			}
		}
	}
	assigned := make([]string, 0)
	for index := range m.ProgramJobs {
		assigned = append(assigned, m.ProgramJobs[index].BehaviorIDs...)
	}
	if len(assigned) != len(stringSet(assigned)) || len(stringSet(assigned)) != len(graph) {
		return errors.New("program jobs must cover every behavior exactly once")
	}
	for behaviorID := range graph {
		if !containsString(assigned, behaviorID) {
			return errors.New("program jobs must cover every behavior exactly once")
		}
	}
	behaviorToJob := map[string]string{}
	for index := range m.ProgramJobs {
		job := &m.ProgramJobs[index]
		for _, behaviorID := range job.BehaviorIDs {
			behaviorToJob[behaviorID] = job.JobID
		}
	}
	jobDepOrder := make([]string, 0, len(m.ProgramJobs))
	jobDeps := map[string][]string{}
	for index := range m.ProgramJobs {
		jobID := m.ProgramJobs[index].JobID
		jobDepOrder = append(jobDepOrder, jobID)
		jobDeps[jobID] = nil
	}
	for index := range m.BehaviorGraph {
		node := &m.BehaviorGraph[index]
		nodeJob := behaviorToJob[node.BehaviorID]
		if nodeJob == "" {
			continue
		}
		for _, dependency := range node.DependsOn {
			dependencyJob := behaviorToJob[dependency]
			if dependencyJob != "" && dependencyJob != nodeJob && !containsString(jobDeps[nodeJob], dependencyJob) {
				jobDeps[nodeJob] = append(jobDeps[nodeJob], dependencyJob)
			}
		}
	}
	visitingJobs := map[string]bool{}
	visitedJobs := map[string]bool{}
	var hasJobCycle func(jobID string) bool
	hasJobCycle = func(jobID string) bool {
		if visitingJobs[jobID] {
			return true
		}
		if visitedJobs[jobID] {
			return false
		}
		visitingJobs[jobID] = true
		cyclic := false
		for _, dependency := range jobDeps[jobID] {
			if hasJobCycle(dependency) {
				cyclic = true
				break
			}
		}
		delete(visitingJobs, jobID)
		visitedJobs[jobID] = true
		return cyclic
	}
	for _, jobID := range jobDepOrder {
		if hasJobCycle(jobID) {
			return errors.New("blueprint program jobs dependencies cannot contain a cycle")
		}
	}
	totalTimeout := 0
	for index := range m.AcceptanceTests {
		totalTimeout += m.AcceptanceTests[index].TimeoutMS
	}
	if totalTimeout > 4000 {
		return errors.New("acceptance test total timeout must not exceed 4000ms")
	}
	return nil
}
