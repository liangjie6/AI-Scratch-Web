package authoring

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ai-scratch-web/backend/internal/schema"
)

// DeriveLegacy preserves Blueprint v2 / Manifest v3 wire shapes. The hash binds
// this source artifact in the NEW path; do not apply old ActionSpec lineage.
func DeriveLegacy(c *FrozenContract) (*DerivedContracts, error) {
	if err := ValidateFrozen(c); err != nil {
		return nil, err
	}
	return derive(c)
}
func derive(c *FrozenContract) (*DerivedContracts, error) {
	blueprint := &schema.ProjectBlueprint{SchemaVersion: 2, TaskSpecHash: c.Hash, AssetNeeds: []schema.BlueprintAssetNeed{}, EntityBindings: []schema.BlueprintEntityBinding{}, StatePlan: []schema.BlueprintStatePlan{}, BehaviorGraph: []schema.BlueprintBehaviorGraphNode{}, ProgramJobs: []schema.BlueprintProgramJob{}, AcceptanceTests: []schema.BlueprintAcceptanceTest{}}
	manifest := &schema.ProjectManifest{SchemaVersion: 3, Scope: "project", Summary: c.Title, Assumptions: c.Assumptions, RequiredExtensions: c.RequiredExtensions, ResourceOps: []schema.ResourceOp{}, LocalState: []schema.ProjectLocalState{}, ProgramTargets: []schema.ProjectProgramTarget{}, SharedState: schema.ProjectSharedState{Variables: []schema.ProjectVariableDefinition{}, Lists: []schema.ProjectListDefinition{}, Broadcasts: []schema.ProjectBroadcastDefinition{}}}
	plan := &schema.Plan{Steps: []string{}}
	for _, target := range c.Targets {
		resolution := schema.BlueprintEntityResolution{Kind: "existing_target", TargetRef: &schema.TaskTargetRef{Kind: target.Ref.Kind, Name: target.Ref.Name}}
		if target.Disposition == "new" {
			for _, asset := range c.Assets {
				if asset.Owner == target.Handle && asset.Kind == "sprite" && asset.Disposition == "new" {
					resolution = schema.BlueprintEntityResolution{Kind: "new_sprite", NeedID: ptr(asset.NeedID)}
					break
				}
			}
		}
		blueprint.EntityBindings = append(blueprint.EntityBindings, schema.BlueprintEntityBinding{EntityID: target.EntityID, Resolution: resolution})
		behaviors := []string{}
		purposes := []string{}
		for _, slot := range c.ScriptSlots {
			if slot.Owner != target.Handle {
				continue
			}
			id := stableID("behavior_", slot.Key)
			behaviors = append(behaviors, id)
			purposes = append(purposes, slot.Purpose)
			produces := []string{}
			consumes := []string{}
			for _, h := range slot.Emits {
				produces = append(produces, stateFor(c, h).RefID)
			}
			for _, h := range slot.Receives {
				consumes = append(consumes, stateFor(c, h).RefID)
			}
			blueprint.BehaviorGraph = append(blueprint.BehaviorGraph, schema.BlueprintBehaviorGraphNode{BehaviorID: id, OwnerEntityID: target.EntityID, DependsOn: []string{}, ProducesBroadcasts: produces, ConsumesBroadcasts: consumes})
		}
		if len(behaviors) == 0 {
			continue
		}
		job := schema.BlueprintProgramJob{JobID: stableID("job_", target.Handle), OwnerEntityID: target.EntityID, Mode: target.Mode, BehaviorIDs: behaviors}
		blueprint.ProgramJobs = append(blueprint.ProgramJobs, job)
		purpose := trimText(strings.Join(purposes, "；"), 120)
		manifest.ProgramTargets = append(manifest.ProgramTargets, schema.ProjectProgramTarget{JobID: job.JobID, OwnerEntityID: target.EntityID, BehaviorIDs: behaviors, Target: target.Ref, Mode: target.Mode, Intent: purpose})
		plan.Steps = append(plan.Steps, trimText(target.Name+"："+purpose, 120))
	}
	for _, state := range c.States {
		var owner *string
		if state.Owner != "" {
			owner = ptr(targetFor(c, state.Owner).EntityID)
		}
		blueprint.StatePlan = append(blueprint.StatePlan, schema.BlueprintStatePlan{RefID: state.RefID, Kind: state.Kind, Scope: state.Scope, OwnerEntityID: owner, Name: state.Name, InitialValue: state.Value})
		if state.Scope == "local" {
			manifest.LocalState = append(manifest.LocalState, schema.ProjectLocalState{RefID: state.RefID, Kind: state.Kind, Target: targetFor(c, state.Owner).Ref, Name: state.Name, Value: state.Value})
			continue
		}
		switch state.Kind {
		case "variable":
			manifest.SharedState.Variables = append(manifest.SharedState.Variables, schema.ProjectVariableDefinition{RefID: state.RefID, Name: state.Name, Scope: "global", Value: state.Value})
		case "list":
			manifest.SharedState.Lists = append(manifest.SharedState.Lists, schema.ProjectListDefinition{RefID: state.RefID, Name: state.Name, Scope: "global", Value: state.Value})
		case "broadcast":
			manifest.SharedState.Broadcasts = append(manifest.SharedState.Broadcasts, schema.ProjectBroadcastDefinition{RefID: state.RefID, Name: state.Name})
		}
	}
	for _, asset := range c.Assets {
		if asset.Disposition != "new" {
			continue
		}
		intent := assetIntent(c, asset.Handle)
		if intent == nil {
			return nil, issue("ASSET_BINDING", "/asset_intents", "missing frozen resource source")
		}
		owner := targetFor(c, asset.Owner)
		source := intent.Source
		if source == nil {
			source = map[string]any{"kind": "scratch_library", "asset_type": asset.Kind, "search_terms": intent.SearchTerms}
		}
		sourceKind, _ := source["kind"].(string)
		fallback := []string{sourceKind}
		if sourceKind == "scratch_library" {
			fallback = []string{"project_asset", "scratch_library"}
		}
		if sourceKind == "audio_generation" {
			return nil, issue("CAPABILITY_UNSUPPORTED", "/assets/"+asset.Handle, "audio generation is not executable by the current resource preparer")
		}
		terms := empty(intent.SearchTerms)
		if len(terms) == 0 {
			terms = []string{asset.Name}
		}
		blueprint.AssetNeeds = append(blueprint.AssetNeeds, schema.BlueprintAssetNeed{NeedID: asset.NeedID, Kind: asset.Kind, OwnerEntityID: ptr(owner.EntityID), Purpose: intent.Purpose, SearchTerms: terms, Tags: []string{}, FallbackOrder: fallback, Constraints: map[string]any{}})
		op := schema.ResourceOp{"op_id": asset.OpID, "source_need_id": asset.NeedID, "name": asset.Name, "tags": []string{}}
		switch asset.Kind {
		case "sprite":
			x, y, size := 0, 0, 100
			if intent.X != nil {
				x = *intent.X
			}
			if intent.Y != nil {
				y = *intent.Y
			}
			if intent.Size != nil {
				size = *intent.Size
			}
			op["owner_entity_id"] = owner.EntityID
			op["x"] = x
			op["y"] = y
			op["size"] = size
			if sourceKind == "scratch_library" {
				op["op"] = "add_builtin_sprite"
				op["source"] = source
				op["include_costumes"] = "all"
				op["include_sounds"] = "all"
			} else {
				op["op"] = "add_sprite"
				op["costumes"] = []any{map[string]any{"name": asset.Name, "source": source, "tags": []string{}}}
			}
		case "costume":
			op["op"] = "add_costume"
			op["target"] = owner.Ref
			op["source"] = source
			op["select_after"] = false
		case "backdrop":
			op["op"] = "add_backdrop"
			op["source"] = source
			op["select_after"] = false
		case "sound":
			op["op"] = "add_sound"
			op["target"] = owner.Ref
			op["source"] = source
		default:
			return nil, issue("ASSET_KIND", "/assets/"+asset.Handle, "unknown resource kind")
		}
		manifest.ResourceOps = append(manifest.ResourceOps, op)
	}
	if err := validateOldShape(blueprint); err != nil {
		return nil, issue("BLUEPRINT_DERIVATION", "/blueprint", err.Error())
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	result := &DerivedContracts{Blueprint: blueprint, Plan: plan}
	if c.Scope == "project" {
		if err := validateOldShape(manifest); err != nil {
			return nil, issue("MANIFEST_DERIVATION", "/manifest", err.Error())
		}
		if err := schema.ValidateBlueprintManifestLineage(blueprint, manifest); err != nil {
			return nil, issue("LINEAGE", "/manifest", err.Error())
		}
		result.Manifest = manifest
	}
	return result, nil
}
func assetIntent(c *FrozenContract, handle string) *IntentAsset {
	for i := range c.AssetIntents {
		if c.AssetIntents[i].Key == handle {
			return &c.AssetIntents[i]
		}
	}
	return nil
}
func trimText(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}
func validateOldShape(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	switch v := value.(type) {
	case *schema.ProjectBlueprint:
		return schema.DecodeStrict(raw, v)
	case *schema.ProjectManifest:
		return schema.DecodeStrict(raw, v)
	default:
		return fmt.Errorf("unsupported derived contract type")
	}
}

// ValidateDerivedContracts checks projections without inventing ActionSpecs.
func ValidateDerivedContracts(c *FrozenContract, blueprint *schema.ProjectBlueprint, manifest *schema.ProjectManifest) error {
	expected, err := DeriveLegacy(c)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(expected.Blueprint)
	b, _ := json.Marshal(blueprint)
	if string(a) != string(b) {
		return issue("LINEAGE", "/blueprint", "Blueprint differs from the frozen source projection")
	}
	if c.Scope == "project" {
		a, _ = json.Marshal(expected.Manifest)
		b, _ = json.Marshal(manifest)
		if string(a) != string(b) {
			return issue("LINEAGE", "/manifest", "Manifest differs from the frozen source projection")
		}
	} else if manifest != nil {
		return issue("SCOPE", "/manifest", "single target source contracts do not have a Manifest")
	}
	return nil
}
