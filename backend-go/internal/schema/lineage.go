package schema

import (
	"errors"
	"slices"
)

// ValidateTaskSpecBlueprintLineage ports validate_task_spec_blueprint_lineage.
func ValidateTaskSpecBlueprintLineage(taskSpec *ScratchTaskSpec, blueprint *ProjectBlueprint) error {
	hash, err := ContractSHA256(taskSpec)
	if err != nil {
		return err
	}
	if blueprint.TaskSpecHash != hash {
		return errors.New("blueprint task_spec_hash does not match the canonical TaskSpec")
	}
	entities := map[string]*TaskEntity{}
	entitySet := map[string]bool{}
	for index := range taskSpec.Entities {
		entities[taskSpec.Entities[index].EntityID] = &taskSpec.Entities[index]
		entitySet[taskSpec.Entities[index].EntityID] = true
	}
	bindingIDs := make([]string, 0, len(blueprint.EntityBindings))
	for index := range blueprint.EntityBindings {
		bindingIDs = append(bindingIDs, blueprint.EntityBindings[index].EntityID)
	}
	if len(bindingIDs) != len(entities) {
		return errors.New("blueprint must bind every TaskSpec entity exactly once")
	}
	for entityID := range entities {
		if !containsString(bindingIDs, entityID) {
			return errors.New("blueprint must bind every TaskSpec entity exactly once")
		}
	}
	state := map[string]*TaskState{}
	for index := range taskSpec.State {
		state[taskSpec.State[index].StateID] = &taskSpec.State[index]
	}
	planRefs := make([]string, 0, len(blueprint.StatePlan))
	for index := range blueprint.StatePlan {
		planRefs = append(planRefs, blueprint.StatePlan[index].RefID)
	}
	if len(planRefs) != len(state) {
		return errors.New("blueprint state_plan must project every TaskSpec state exactly once")
	}
	for stateID := range state {
		if !containsString(planRefs, stateID) {
			return errors.New("blueprint state_plan must project every TaskSpec state exactly once")
		}
	}
	for index := range blueprint.StatePlan {
		statePlan := &blueprint.StatePlan[index]
		source := state[statePlan.RefID]
		if source == nil {
			continue
		}
		if source.Kind != statePlan.Kind || source.Scope != statePlan.Scope || stringValue(source.OwnerEntityID) != stringValue(statePlan.OwnerEntityID) {
			return errors.New("blueprint state_plan must preserve TaskSpec state semantics")
		}
	}
	behaviors := map[string]*TaskBehavior{}
	for index := range taskSpec.Behaviors {
		behaviors[taskSpec.Behaviors[index].BehaviorID] = &taskSpec.Behaviors[index]
	}
	graphBehaviors := make([]string, 0, len(blueprint.BehaviorGraph))
	for index := range blueprint.BehaviorGraph {
		graphBehaviors = append(graphBehaviors, blueprint.BehaviorGraph[index].BehaviorID)
	}
	if len(graphBehaviors) != len(behaviors) {
		return errors.New("blueprint behavior graph must project every TaskSpec behavior")
	}
	for behaviorID := range behaviors {
		if !containsString(graphBehaviors, behaviorID) {
			return errors.New("blueprint behavior graph must project every TaskSpec behavior")
		}
	}
	for index := range blueprint.BehaviorGraph {
		node := &blueprint.BehaviorGraph[index]
		behavior := behaviors[node.BehaviorID]
		if behavior == nil {
			continue
		}
		if node.OwnerEntityID != behavior.OwnerEntityID {
			return errors.New("blueprint behavior owner must preserve TaskSpec owner")
		}
	}
	graphByBehavior := map[string]*BlueprintBehaviorGraphNode{}
	for index := range blueprint.BehaviorGraph {
		graphByBehavior[blueprint.BehaviorGraph[index].BehaviorID] = &blueprint.BehaviorGraph[index]
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var hasCycle func(behaviorID string) bool
	hasCycle = func(behaviorID string) bool {
		if visiting[behaviorID] {
			return true
		}
		if visited[behaviorID] {
			return false
		}
		visiting[behaviorID] = true
		cyclic := false
		node := graphByBehavior[behaviorID]
		if node != nil {
			for _, dependency := range node.DependsOn {
				if hasCycle(dependency) {
					cyclic = true
					break
				}
			}
		}
		delete(visiting, behaviorID)
		visited[behaviorID] = true
		return cyclic
	}
	for index := range blueprint.BehaviorGraph {
		if hasCycle(blueprint.BehaviorGraph[index].BehaviorID) {
			return errors.New("blueprint behavior dependencies cannot contain a cycle")
		}
	}
	broadcasts := map[string]bool{}
	for stateID, item := range state {
		if item.Kind == "broadcast" {
			broadcasts[stateID] = true
		}
	}
	var actionBroadcasts func(actions []ActionSpec) map[string]bool
	actionBroadcasts = func(actions []ActionSpec) map[string]bool {
		result := map[string]bool{}
		for index := range actions {
			action := &actions[index]
			if action.Kind == "broadcast" && stringValue(action.StateID) != "" {
				result[stringValue(action.StateID)] = true
			}
			if action.Kind == "loop" {
				for broadcast := range actionBroadcasts(action.Actions) {
					result[broadcast] = true
				}
			}
		}
		return result
	}
	for index := range blueprint.BehaviorGraph {
		node := &blueprint.BehaviorGraph[index]
		behavior := behaviors[node.BehaviorID]
		if behavior == nil {
			continue
		}
		for _, broadcast := range node.ProducesBroadcasts {
			if !broadcasts[broadcast] {
				return errors.New("blueprint behavior graph broadcast edges must reference TaskSpec broadcasts")
			}
		}
		for _, broadcast := range node.ConsumesBroadcasts {
			if !broadcasts[broadcast] {
				return errors.New("blueprint behavior graph broadcast edges must reference TaskSpec broadcasts")
			}
		}
		expectedProduces := actionBroadcasts(behavior.Actions)
		expectedConsumes := map[string]bool{}
		if behavior.Trigger.Kind == "broadcast_received" && stringValue(behavior.Trigger.StateID) != "" {
			expectedConsumes[stringValue(behavior.Trigger.StateID)] = true
		}
		if !stringSetEqual(node.ProducesBroadcasts, expectedProduces) || !stringSetEqual(node.ConsumesBroadcasts, expectedConsumes) {
			return errors.New("blueprint behavior graph broadcast edges must match TaskSpec behavior semantics")
		}
	}
	for stateID := range broadcasts {
		produced := false
		consumed := false
		for index := range blueprint.BehaviorGraph {
			node := &blueprint.BehaviorGraph[index]
			if containsString(node.ProducesBroadcasts, stateID) {
				produced = true
			}
			if containsString(node.ConsumesBroadcasts, stateID) {
				consumed = true
			}
		}
		if !produced || !consumed {
			return errors.New("each declared broadcast requires a producer and consumer")
		}
	}
	for index := range blueprint.ProgramJobs {
		job := &blueprint.ProgramJobs[index]
		for _, behaviorID := range job.BehaviorIDs {
			behavior := behaviors[behaviorID]
			if behavior == nil || behavior.OwnerEntityID != job.OwnerEntityID {
				return errors.New("program job owner must match every assigned behavior")
			}
		}
	}
	criteria := map[string]bool{}
	for index := range taskSpec.SuccessCriteria {
		criteria[taskSpec.SuccessCriteria[index].CriterionID] = true
	}
	for index := range blueprint.AcceptanceTests {
		if !criteria[blueprint.AcceptanceTests[index].SourceCriterionID] {
			return errors.New("blueprint acceptance test references an unknown success criterion")
		}
	}
	criteriaByID := map[string]*SuccessCriterionSpec{}
	for index := range taskSpec.SuccessCriteria {
		criteriaByID[taskSpec.SuccessCriteria[index].CriterionID] = &taskSpec.SuccessCriteria[index]
	}
	needs := map[string]*BlueprintAssetNeed{}
	for index := range blueprint.AssetNeeds {
		needs[blueprint.AssetNeeds[index].NeedID] = &blueprint.AssetNeeds[index]
	}
	for index := range blueprint.AcceptanceTests {
		test := &blueprint.AcceptanceTests[index]
		criterion := criteriaByID[test.SourceCriterionID]
		if criterion == nil {
			continue
		}
		if criterion.Verification != "executable" {
			return errors.New("blueprint acceptance tests may only reference executable success criteria")
		}
		for _, behaviorID := range test.RelatedBehaviorIDs {
			if !containsString(criterion.RelatedBehaviorIDs, behaviorID) {
				return errors.New("acceptance test behaviors must belong to its source criterion")
			}
		}
		trigger := &test.Trigger
		if trigger.Kind == "sprite_click" && !entitySet[stringValue(trigger.EntityID)] {
			return errors.New("sprite_click acceptance trigger must reference a declared entity")
		}
		if trigger.Kind == "broadcast" {
			target := state[stringValue(trigger.StateID)]
			if target == nil || target.Kind != "broadcast" {
				return errors.New("broadcast acceptance trigger must reference a declared broadcast")
			}
		}
		for arrangeIndex := range test.Arrange {
			arrange := &test.Arrange[arrangeIndex]
			switch arrange.Kind {
			case "set_position":
				if !entitySet[stringValue(arrange.EntityID)] {
					return errors.New("acceptance arrangement references an unknown entity")
				}
			case "set_variable":
				target := state[stringValue(arrange.StateID)]
				if target == nil || target.Kind != "variable" {
					return errors.New("set_variable arrangement must reference a variable")
				}
			case "set_list":
				target := state[stringValue(arrange.StateID)]
				if target == nil || target.Kind != "list" {
					return errors.New("set_list arrangement must reference a list")
				}
			}
		}
		for expectationIndex := range test.Expect {
			expectation := &test.Expect[expectationIndex]
			if expectation.Kind == "variable" || expectation.Kind == "broadcast_emitted" {
				expectedKind := "variable"
				if expectation.Kind == "broadcast_emitted" {
					expectedKind = "broadcast"
				}
				target := state[stringValue(expectation.StateID)]
				if target == nil || target.Kind != expectedKind {
					return errors.New("acceptance expectation references an invalid state kind")
				}
			}
			if expectation.Kind == "list" {
				target := state[stringValue(expectation.StateID)]
				if target == nil || target.Kind != "list" {
					return errors.New("list acceptance expectation must reference a list")
				}
			}
			switch expectation.Kind {
			case "position_range", "costume", "visibility", "clone_count", "say_text", "sound_invoked":
				if !entitySet[stringValue(expectation.EntityID)] {
					return errors.New("acceptance expectation references an unknown entity")
				}
			}
			switch expectation.Kind {
			case "costume", "backdrop", "sound_invoked":
				expectedNeedKind := "costume"
				if expectation.Kind == "backdrop" {
					expectedNeedKind = "backdrop"
				} else if expectation.Kind == "sound_invoked" {
					expectedNeedKind = "sound"
				}
				need := needs[stringValue(expectation.AssetNeedID)]
				if need == nil || need.Kind != expectedNeedKind {
					return errors.New("acceptance expectation references an invalid asset need")
				}
			}
			if expectation.Kind == "scripts_stopped" && expectation.EntityID != nil && !entitySet[*expectation.EntityID] {
				return errors.New("scripts_stopped acceptance expectation references an unknown entity")
			}
		}
	}
	testedCriteria := map[string]bool{}
	for index := range blueprint.AcceptanceTests {
		testedCriteria[blueprint.AcceptanceTests[index].SourceCriterionID] = true
	}
	for index := range taskSpec.SuccessCriteria {
		criterion := &taskSpec.SuccessCriteria[index]
		if criterion.Verification == "executable" && !testedCriteria[criterion.CriterionID] {
			return errors.New("every executable success criterion must have an acceptance test")
		}
	}
	return nil
}

func stringSetEqual(values []string, set map[string]bool) bool {
	if len(stringSet(values)) != len(set) {
		return false
	}
	for _, value := range values {
		if !set[value] {
			return false
		}
	}
	return true
}

// ValidateBlueprintManifestLineage ports validate_blueprint_manifest_lineage.
func ValidateBlueprintManifestLineage(blueprint *ProjectBlueprint, manifest *ProjectManifest) error {
	needs := map[string]*BlueprintAssetNeed{}
	for index := range blueprint.AssetNeeds {
		needs[blueprint.AssetNeeds[index].NeedID] = &blueprint.AssetNeeds[index]
	}
	bindings := map[string]*BlueprintEntityBinding{}
	for index := range blueprint.EntityBindings {
		bindings[blueprint.EntityBindings[index].EntityID] = &blueprint.EntityBindings[index]
	}
	for _, operation := range manifest.ResourceOps {
		name := resourceOpName(operation)
		switch name {
		case "add_builtin_sprite", "add_sprite", "add_costume", "add_backdrop", "add_sound":
			sourceNeedID, _ := operation["source_need_id"].(string)
			if needs[sourceNeedID] == nil {
				return errors.New("manifest resource source_need_id must reference a Blueprint asset need")
			}
		}
		switch name {
		case "add_builtin_sprite", "add_sprite":
			ownerEntityID, _ := operation["owner_entity_id"].(string)
			binding := bindings[ownerEntityID]
			if binding == nil || binding.Resolution.Kind != "new_sprite" || !manifestOperationNeedMatches(operation, binding) {
				return errors.New("manifest created sprite must match its Blueprint entity binding")
			}
		}
	}
	jobs := map[string]*BlueprintProgramJob{}
	for index := range blueprint.ProgramJobs {
		jobs[blueprint.ProgramJobs[index].JobID] = &blueprint.ProgramJobs[index]
	}
	targetJobIDs := make([]string, 0, len(manifest.ProgramTargets))
	for index := range manifest.ProgramTargets {
		targetJobIDs = append(targetJobIDs, manifest.ProgramTargets[index].JobID)
	}
	if len(targetJobIDs) != len(jobs) {
		return errors.New("manifest program targets must project every Blueprint program job")
	}
	for jobID := range jobs {
		if !containsString(targetJobIDs, jobID) {
			return errors.New("manifest program targets must project every Blueprint program job")
		}
	}
	for index := range manifest.ProgramTargets {
		target := &manifest.ProgramTargets[index]
		source := jobs[target.JobID]
		if source == nil {
			continue
		}
		modeCompatible := target.Mode == source.Mode
		if !modeCompatible {
			switch target.Target.Kind {
			case "stage", "existing_sprite", "created_sprite":
				modeCompatible = target.Mode == "append"
			}
		}
		if target.OwnerEntityID != source.OwnerEntityID || !slices.Equal(target.BehaviorIDs, source.BehaviorIDs) || !modeCompatible {
			return errors.New("manifest program target must preserve Blueprint job fields")
		}
	}
	blueprintState := map[string]*BlueprintStatePlan{}
	for index := range blueprint.StatePlan {
		blueprintState[blueprint.StatePlan[index].RefID] = &blueprint.StatePlan[index]
	}
	type manifestStateEntry struct {
		refID string
		kind  string
		scope string
	}
	manifestState := make([]manifestStateEntry, 0)
	for index := range manifest.SharedState.Variables {
		manifestState = append(manifestState, manifestStateEntry{manifest.SharedState.Variables[index].RefID, "variable", "global"})
	}
	for index := range manifest.SharedState.Lists {
		manifestState = append(manifestState, manifestStateEntry{manifest.SharedState.Lists[index].RefID, "list", "global"})
	}
	for index := range manifest.SharedState.Broadcasts {
		manifestState = append(manifestState, manifestStateEntry{manifest.SharedState.Broadcasts[index].RefID, "broadcast", "global"})
	}
	for index := range manifest.LocalState {
		manifestState = append(manifestState, manifestStateEntry{manifest.LocalState[index].RefID, manifest.LocalState[index].Kind, "local"})
	}
	manifestRefs := map[string]bool{}
	for _, entry := range manifestState {
		manifestRefs[entry.refID] = true
	}
	if len(manifestRefs) != len(blueprintState) {
		return errors.New("manifest state must project every Blueprint state exactly once")
	}
	for refID := range blueprintState {
		if !manifestRefs[refID] {
			return errors.New("manifest state must project every Blueprint state exactly once")
		}
	}
	for _, entry := range manifestState {
		source := blueprintState[entry.refID]
		if source == nil {
			continue
		}
		if source.Kind != entry.kind || source.Scope != entry.scope {
			return errors.New("manifest state must preserve Blueprint state kind and scope")
		}
	}
	return nil
}

func manifestOperationNeedMatches(operation ResourceOp, binding *BlueprintEntityBinding) bool {
	sourceNeedID, ok := operation["source_need_id"].(string)
	if !ok {
		return false
	}
	return stringValue(binding.Resolution.NeedID) == sourceNeedID
}
