package authoring

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ai-scratch-web/backend/internal/schema"
)

type SnapshotSymbols struct {
	BaseSnapshotHash string         `json:"base_snapshot_hash"`
	Targets          []IntentTarget `json:"targets"`
	States           []StateBinding `json:"states"`
	Assets           []AssetBinding `json:"assets"`
	Edits            []EditBinding  `json:"edits"`
}

// SnapshotBindings extracts trusted names, scopes and protected edit aliases.
// Truncated list previews are never treated as the complete current value.
func SnapshotBindings(request *schema.UnifiedGenerationRequest) (*SnapshotSymbols, error) {
	if request == nil {
		return nil, issue("SNAPSHOT", "/request", "request is required")
	}
	hash, err := fingerprint(map[string]any{"scope": request.Scope, "target": request.Target, "project_context": request.ProjectContext})
	if err != nil {
		return nil, err
	}
	out := &SnapshotSymbols{BaseSnapshotHash: hash, Targets: []IntentTarget{}, States: []StateBinding{}, Assets: []AssetBinding{}, Edits: []EditBinding{}}
	type entry struct {
		handle  string
		context schema.TargetContext
		current bool
	}
	entries := []entry{{"current", request.Target, true}}
	if request.Scope == "project" && request.ProjectContext != nil {
		for _, t := range request.ProjectContext.Targets {
			if t.Name == request.Target.Name && t.IsStage == request.Target.IsStage {
				continue
			}
			h := t.TargetID
			if !keyPattern.MatchString(h) {
				h = stableID("target_", fmt.Sprintf("%t:%s", t.IsStage, t.Name))
			}
			entries = append(entries, entry{h, schema.TargetContext{Name: t.Name, IsStage: t.IsStage, HasScripts: t.HasScripts, Mode: t.Mode, Costumes: t.Costumes, Backdrops: t.Backdrops, Sounds: t.Sounds, SpriteNames: t.SpriteNames, CurrentProgram: t.CurrentProgram}, false})
		}
	}
	stateIndex := map[string]int{}
	addState := func(name, kind, scope, owner string, value any) {
		identity := kind + ":" + scope + ":" + owner + ":" + name
		if index, ok := stateIndex[identity]; ok {
			if out.States[index].Value == nil && value != nil {
				out.States[index].Value = value
			}
			return
		}
		b := StateBinding{Handle: stableID("state_", identity), Name: name, Kind: kind, Scope: scope, Owner: owner, Disposition: "reuse", Value: value}
		stateIndex[identity] = len(out.States)
		out.States = append(out.States, b)
	}
	for _, e := range entries {
		kind := "sprite"
		ref := schema.TargetRef{Kind: "existing_sprite", Name: ptr(e.context.Name)}
		disposition := "existing"
		if e.context.IsStage {
			kind = "stage"
			ref = schema.TargetRef{Kind: "stage"}
		}
		if e.current {
			disposition = "current"
			ref = schema.TargetRef{Kind: "current_target"}
		}
		out.Targets = append(out.Targets, IntentTarget{Key: e.handle, Name: e.context.Name, Kind: kind, Disposition: disposition, TargetRef: &ref})
		if e.context.CurrentProgram != "" {
			raw := []byte(e.context.CurrentProgram)
			if err := CheckJSON(raw, Limits{MaxBytes: schema.MaxCurrentProgramLength}); err != nil {
				return nil, err
			}
			var program map[string]any
			if err := json.Unmarshal(raw, &program); err != nil {
				return nil, err
			}
			for _, section := range []string{"global_variables", "local_variables"} {
				scope := "global"
				owner := ""
				if section == "local_variables" {
					scope = "local"
					owner = e.handle
				}
				list, _ := program[section].([]any)
				seen := map[string]bool{}
				for _, item := range list {
					o, ok := item.(map[string]any)
					if !ok {
						continue
					}
					name, _ := o["name"].(string)
					if name == "" {
						continue
					}
					kind := "variable"
					switch o["type"] {
					case "list":
						kind = "list"
					case "broadcast_msg":
						kind = "broadcast"
					}
					identity := kind + ":" + name
					if seen[identity] {
						return nil, issue("STATE_AMBIGUOUS", "/snapshot/"+section, "duplicate snapshot state name within scope")
					}
					seen[identity] = true
					value := o["value"]
					itemScope, itemOwner := scope, owner
					if kind == "broadcast" {
						value = nil
						itemScope = "global"
						itemOwner = ""
					}
					addState(name, kind, itemScope, itemOwner, value)
				}
			}
			scripts, _ := program["scripts"].([]any)
			for _, v := range scripts {
				s, ok := v.(map[string]any)
				if !ok {
					continue
				}
				id, _ := s["id"].(string)
				if !strings.HasPrefix(id, "script_") {
					continue
				}
				protected, _ := s["protected"].(bool)
				out.Edits = append(out.Edits, EditBinding{Ref: id, Target: e.handle, ScriptID: id, BaseSnapshotHash: hash, Protected: protected})
			}
		}
		addAsset := func(kind, name string) {
			out.Assets = append(out.Assets, AssetBinding{Handle: stableID("asset_", e.handle+":"+kind+":"+name), Owner: e.handle, Kind: kind, Name: name, Disposition: "reuse"})
		}
		for _, name := range e.context.Costumes {
			if !e.context.IsStage {
				addAsset("costume", name)
			}
		}
		for _, name := range e.context.Backdrops {
			addAsset("backdrop", name)
		}
		for _, name := range e.context.Sounds {
			addAsset("sound", name)
		}
		for _, name := range e.context.SpriteNames {
			addAsset("sprite", name)
		}
	}
	if request.ProjectContext != nil {
		shared := request.ProjectContext.SharedState
		for _, section := range []string{"variables", "lists", "broadcasts"} {
			list, _ := shared[section].([]any)
			for _, v := range list {
				kind := "variable"
				if section == "lists" {
					kind = "list"
				}
				if section == "broadcasts" {
					kind = "broadcast"
				}
				if name, ok := v.(string); ok {
					addState(name, kind, "global", "", nil)
					continue
				}
				if object, ok := v.(map[string]any); ok {
					name, _ := object["name"].(string)
					if name != "" {
						addState(name, kind, "global", "", object["value"])
					}
				}
			}
		}
	}
	return out, nil
}

// OptionsFromRequest remaps snapshot owners to the local keys chosen in this
// planning response, while retaining the exact snapshot-bound edit aliases.
func OptionsFromRequest(request *schema.UnifiedGenerationRequest, spec *IntentSpec) (FreezeOptions, error) {
	symbols, err := SnapshotBindings(request)
	if err != nil {
		return FreezeOptions{}, err
	}
	remap := map[string]string{}
	if spec != nil {
		for _, s := range symbols.Targets {
			for _, t := range spec.Targets {
				if s.Name == t.Name && s.Kind == t.Kind && t.Disposition != "new" {
					remap[s.Key] = t.Key
				}
			}
		}
	}
	states := []StateBinding{}
	for _, s := range symbols.States {
		if s.Owner != "" {
			owner, ok := remap[s.Owner]
			if !ok {
				continue
			}
			s.Owner = owner
		}
		states = append(states, s)
	}
	assets := []AssetBinding{}
	for _, a := range symbols.Assets {
		if owner, ok := remap[a.Owner]; ok {
			a.Owner = owner
			assets = append(assets, a)
		}
	}
	edits := []EditBinding{}
	for _, e := range symbols.Edits {
		if target, ok := remap[e.Target]; ok {
			e.Target = target
			edits = append(edits, e)
		}
	}
	allowed := schema.GetDeploymentSettings().EnabledAIExtensions()
	if !has(request.PermittedExtensions, "text2speech") {
		delete(allowed, "text2speech")
	}
	requestHash, err := requestBindingFingerprint(request.SessionID, request.RequestID)
	if err != nil {
		return FreezeOptions{}, err
	}
	return FreezeOptions{Scope: request.Scope, Mode: request.Target.Mode, Target: request.Target, ProjectContext: request.ProjectContext, BaseSnapshotHash: symbols.BaseSnapshotHash, RequestBindingHash: requestHash, CatalogHash: CatalogFingerprint(), AllowedExtensions: allowed, ExistingStates: states, ExistingAssets: assets, ExistingScriptRefs: edits}, nil
}

// ProgramTargets returns stable manifest order, excluding resource-only owners.
func ProgramTargets(c *FrozenContract) []TargetBinding {
	out := []TargetBinding{}
	if c == nil {
		return out
	}
	for _, target := range c.Targets {
		for _, slot := range c.ScriptSlots {
			if slot.Owner == target.Handle {
				out = append(out, target)
				break
			}
		}
	}
	return out
}

// ValidateRequestBinding re-derives the client-returned artifact from the
// trusted original request, including edit aliases and extension permissions.
// A public digest provides integrity, not authorization, so hash checking alone
// is insufficient at this boundary.
func ValidateRequestBinding(c *FrozenContract, request *schema.UnifiedGenerationRequest) error {
	if err := ValidateFrozen(c); err != nil {
		return err
	}
	if request == nil || request.PipelineVersion != PipelineVersion {
		return issue("VERSION", "/request/pipeline_version", "source pipeline must be explicitly requested")
	}
	spec := &IntentSpec{SchemaVersion: 1, Title: c.Title, Goal: c.Goal, Targets: []IntentTarget{}, States: []IntentState{}, Assets: c.AssetIntents, ScriptSlots: []IntentSlot{}, RequiredExtensions: c.RequiredExtensions, Assumptions: c.Assumptions, SuccessCriteria: c.SuccessCriteria, MissingQuestions: []string{}}
	for _, t := range c.Targets {
		ref := t.Ref
		var targetRef *schema.TargetRef
		if t.Disposition != "new" {
			targetRef = &ref
		}
		spec.Targets = append(spec.Targets, IntentTarget{Key: t.Handle, Name: t.Name, Kind: t.Kind, Disposition: t.Disposition, TargetRef: targetRef})
	}
	for _, state := range c.States {
		initial := state.Value
		if state.Disposition == "reuse" {
			initial = nil
		}
		spec.States = append(spec.States, IntentState{Key: state.Handle, Name: state.Name, Kind: state.Kind, Scope: state.Scope, Owner: state.Owner, Disposition: state.Disposition, Initial: initial})
	}
	for _, slot := range c.ScriptSlots {
		spec.ScriptSlots = append(spec.ScriptSlots, slot.IntentSlot)
	}
	if request.AssetPolicy == "existing_only" {
		for _, target := range spec.Targets {
			if target.Disposition == "new" {
				return issue("ASSET_POLICY", "/targets", "existing_only forbids newly created targets")
			}
		}
		for _, asset := range spec.Assets {
			if asset.Disposition == "new" {
				return issue("ASSET_POLICY", "/asset_intents", "existing_only forbids newly added resources")
			}
		}
	}
	options, err := OptionsFromRequest(request, spec)
	if err != nil {
		return err
	}
	if options.RequestBindingHash != c.RequestBindingHash {
		return issue("REQUEST_BINDING", "/request_binding_hash", "frozen contract belongs to a different original session or request")
	}
	// Read/reconstruction preserves the original lifetime. Runtime generation
	// applies the clock check; historical artifacts must remain inspectable.
	options.ExpiresAtUnix = c.ExpiresAtUnix
	rebuilt, err := Freeze(spec, options)
	if err != nil {
		return err
	}
	if rebuilt.Hash != c.Hash {
		return issue("SNAPSHOT_BINDING", "/source_contract", "frozen interfaces, permissions or edit aliases disagree with the original request snapshot")
	}
	return nil
}

func promptTarget(c *FrozenContract, target TargetBinding, own bool) TargetBinding {
	if !own || target.Mode == "append" {
		target.Context.CurrentProgram = "{}"
		return target
	}
	var source map[string]any
	if json.Unmarshal([]byte(target.Context.CurrentProgram), &source) != nil {
		target.Context.CurrentProgram = "{}"
		return target
	}
	wanted := map[string]bool{}
	for _, slot := range c.ScriptSlots {
		if slot.Owner == target.Handle && slot.RemoveScriptID != "" {
			wanted[slot.RemoveScriptID] = true
		}
	}
	scripts := []any{}
	raw, _ := source["scripts"].([]any)
	for _, value := range raw {
		script, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id, _ := script["id"].(string)
		protected, _ := script["protected"].(bool)
		if target.Mode == "modify" && !wanted[id] {
			continue
		}
		if protected {
			scripts = append(scripts, map[string]any{"id": id, "protected": true, "purpose": script["purpose"]})
			continue
		}
		scripts = append(scripts, script)
	}
	rawJSON, _ := json.Marshal(map[string]any{"scripts": scripts})
	target.Context.CurrentProgram = string(rawJSON)
	return target
}
