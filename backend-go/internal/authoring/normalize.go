package authoring

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/ai-scratch-web/backend/internal/blockcatalog"
	"github.com/ai-scratch-web/backend/internal/schema"
)

type normalizer struct {
	contract *FrozenContract
	target   *TargetBinding
	slot     *FrozenSlot
	options  NormalizeOptions
	count    int
}

// NormalizeSlot cannot add state, change a hat, choose a target, or authorize an
// edit. Those decisions were frozen before this model response was requested.
func NormalizeSlot(c *FrozenContract, targetHandle string, response *SlotResponse, options NormalizeOptions) (*schema.Script, error) {
	if err := ValidateFrozen(c); err != nil {
		return nil, err
	}
	if options.ContractHash != "" && options.ContractHash != c.Hash {
		return nil, issue("STALE_CONTRACT", "/contract_hash", "response belongs to another frozen interface")
	}
	if options.BaseSnapshotHash != "" && options.BaseSnapshotHash != c.BaseSnapshotHash {
		return nil, issue("STALE_SNAPSHOT", "/base_snapshot_hash", "response belongs to another base snapshot")
	}
	if response == nil {
		return nil, issue("SHAPE", "/", "slot response is required")
	}
	slot := slotFor(c, response.SlotID)
	if slot == nil || slot.Owner != targetHandle {
		return nil, issue("SLOT_UNKNOWN", "/slot_id", "slot does not belong to this target")
	}
	if response.Status == "needs_revision" {
		return nil, &Error{Code: "CONTRACT_REVISION_REQUIRED", Path: "/revision", SlotID: slot.Key, Message: "slot requested a separately authorized contract revision"}
	}
	if response.Status != "ready" || response.Revision != nil || response.Body == nil {
		return nil, issue("SHAPE", "/", "ready slot requires an array body and null revision")
	}
	n := normalizer{contract: c, target: targetFor(c, targetHandle), slot: slot, options: options, count: 1}
	blocks, err := n.sequence(response.Body, "/body", 1)
	if err != nil {
		if e, ok := err.(*Error); ok {
			e.SlotID = slot.Key
		}
		return nil, err
	}
	hat, err := hatFor(c, slot)
	if err != nil {
		return nil, err
	}
	blocks = append([]schema.Block{hat}, blocks...)
	index := 0
	for _, s := range c.ScriptSlots {
		if s.Owner == targetHandle {
			if s.Key == slot.Key {
				break
			}
			index++
		}
	}
	script := &schema.Script{ID: slot.ScriptID, Purpose: slot.Purpose, X: (index % 3) * 400, Y: (index / 3) * 300, Blocks: blocks}
	program := &schema.ProgramIR{SchemaVersion: 1, Summary: c.Title, RemoveScriptIDs: []string{}, Variables: []schema.VariableDefinition{}, Lists: []schema.ListDefinition{}, Broadcasts: []string{}, Procedures: []schema.Procedure{}, Scripts: []schema.Script{*script}}
	if c.Scope == "project" {
		program.SchemaVersion = 2
	}
	if err := program.Validate(); err != nil {
		return nil, issue("PROGRAM_IR", "/body", err.Error())
	}
	if _, err := schema.ValidateProgramContracts(program, schema.ProgramContractOptions{TargetKind: n.target.Kind, AllowedExtensions: set(c.RequiredExtensions), AllowResourceRefs: c.Scope == "project"}); err != nil {
		return nil, issue("PROGRAM_CONTRACT", "/body", err.Error())
	}
	return script, nil
}

func (n *normalizer) sequence(source []CompactBlock, path string, depth int) ([]schema.Block, error) {
	if depth > n.options.Limits.defaults().MaxBlockDepth {
		return nil, issue("BLOCK_DEPTH", path, "block nesting exceeds its budget")
	}
	out := make([]schema.Block, 0, len(source))
	for i, b := range source {
		p := fmt.Sprintf("%s/%d", path, i)
		block, err := n.block(b, p, depth, false, blockcatalog.InputAny)
		if err != nil {
			return nil, err
		}
		out = append(out, block)
		if blockcatalog.BlockContracts[b.Opcode].Shape == blockcatalog.ShapeCap && i != len(source)-1 {
			return nil, issue("TERMINAL_FOLLOWED", p, "terminal block must end its stack")
		}
	}
	return out, nil
}
func (n *normalizer) block(b CompactBlock, path string, depth int, nested bool, expected blockcatalog.InputKind) (schema.Block, error) {
	out := schema.Block{Opcode: b.Opcode, Fields: map[string]schema.FieldValue{}, Inputs: map[string]schema.InputValue{}, Substacks: map[string][]schema.Block{}}
	l := n.options.Limits.defaults()
	n.count++
	if n.count > l.MaxBlocks {
		return out, issue("BLOCK_BUDGET", path, "visible block budget exceeded")
	}
	if depth > l.MaxBlockDepth {
		return out, issue("BLOCK_DEPTH", path, "block nesting exceeds its budget")
	}
	if schema.ProcedureOpcodes[b.Opcode] {
		return out, issue("PROCEDURE_LEGACY_REQUIRED", path, "procedures require the separately validated legacy path")
	}
	contract, ok := blockcatalog.BlockContracts[b.Opcode]
	if !ok {
		return out, issue("UNKNOWN_OPCODE", path+"/opcode", fmt.Sprintf("opcode %q is outside the supported compiler catalog", diagnosticName(b.Opcode)))
	}
	if contract.Shape == blockcatalog.ShapeHat {
		return out, issue("STACK_EXPECTED", path, "event hat is already supplied by the service from the frozen trigger; omit hats and output only command blocks in body")
	}
	if n.options.OpcodeProfile != nil && !n.options.OpcodeProfile[b.Opcode] {
		return out, issue("CAPABILITY_NOT_IN_PROFILE", path+"/opcode", "supported opcode is outside this frozen profile")
	}
	if ext := blockcatalog.ExtensionForOpcode(b.Opcode); ext != "" && !has(n.contract.RequiredExtensions, ext) {
		return out, issue("EXTENSION_NOT_ALLOWED", path+"/opcode", "extension is not in the frozen permission set")
	}
	if !contract.Targets[n.target.Kind] {
		return out, issue("TARGET_KIND", path+"/opcode", "opcode cannot run on this target kind")
	}
	if nested {
		if contract.Shape != blockcatalog.ShapeReporter && contract.Shape != blockcatalog.ShapeBoolean {
			return out, issue("REPORTER_EXPECTED", path, "an input requires a reporter or boolean block")
		}
		if expected == blockcatalog.InputBoolean && contract.Shape != blockcatalog.ShapeBoolean {
			return out, issue("BOOLEAN_EXPECTED", path, "boolean input requires an explicit boolean reporter")
		}
		if expected != blockcatalog.InputBoolean && expected != blockcatalog.InputAny && contract.Shape == blockcatalog.ShapeBoolean {
			return out, issue("REPORTER_TYPE", path, "boolean reporter cannot fill this input")
		}
	} else if contract.Shape != blockcatalog.ShapeStack && contract.Shape != blockcatalog.ShapeCap {
		return out, issue("STACK_EXPECTED", path, "body accepts command blocks only; hats are inserted by code")
	}
	for name, value := range b.Fields {
		p := path + "/fields/" + name
		if !contract.Fields[name] {
			return out, issue("UNKNOWN_PORT", p, fmt.Sprintf("field %q is absent from opcode %q", diagnosticName(name), b.Opcode))
		}
		if contract.ResolvedFields[name] {
			kind := "variable"
			if name == "LIST" {
				kind = "list"
			}
			if name == "BROADCAST_OPTION" {
				kind = "broadcast"
			}
			handle, err := logical(value, "state", p)
			if err != nil {
				return out, err
			}
			state, err := n.state(handle, kind, stateAccess(b.Opcode), p)
			if err != nil {
				return out, err
			}
			out.Fields[name] = stateField(n.contract, state)
			continue
		}
		if resource, ok := contract.ResourceFieldTypes[name]; ok {
			value, err := n.resource(value, string(resource), b.Opcode, name, p)
			if err != nil {
				return out, err
			}
			out.Fields[name] = value
			continue
		}
		if values := contract.FieldEnums[name]; len(values) > 0 {
			text, ok := value.(string)
			if !ok || !has(values, text) {
				return out, issue("MENU_VALUE", p, "field must use a serialized static-menu value")
			}
		}
		if provider := contract.FieldProviders[name]; provider != "" {
			if provider == "stop_options" {
				if value != "all" && value != "this script" {
					return out, issue("CAPABILITY_UNSUPPORTED", p, "this compiler profile supports only terminal stop variants")
				}
			} else {
				return out, issue("LOGICAL_REFERENCE", p, "dynamic menu requires a typed logical reference")
			}
		}
		if !scalar(value) {
			return out, issue("FIELD_TYPE", p, "ordinary fields accept only finite scalar values")
		}
		out.Fields[name] = value
	}
	for name := range contract.RequiredFields {
		if _, ok := b.Fields[name]; !ok {
			return out, issue("MISSING_PORT", path+"/fields/"+name, "required field is missing")
		}
	}
	for name, value := range b.Inputs {
		p := path + "/inputs/" + name
		kind, ok := contract.Inputs[name]
		if !ok {
			return out, issue("UNKNOWN_PORT", p, fmt.Sprintf("input %q is absent from opcode %q", diagnosticName(name), b.Opcode))
		}
		var normalized any
		var err error
		if resource, ok := contract.ResourceInputTypes[name]; ok {
			normalized, err = n.resource(value, string(resource), b.Opcode, name, p)
		} else if name == "BROADCAST_INPUT" && (b.Opcode == "event_broadcast" || b.Opcode == "event_broadcastandwait") {
			handle, e := logical(value, "state", p)
			if e != nil {
				return out, e
			}
			state, e := n.state(handle, "broadcast", "emit", p)
			if e != nil {
				return out, e
			}
			normalized = map[string]any{"kind": "text", "value": state.Name}
		} else {
			normalized, err = n.input(value, kind, p, depth+1)
		}
		if err != nil {
			return out, err
		}
		if values := contract.InputEnums[name]; len(values) > 0 && kind != blockcatalog.InputColor {
			if v, ok := normalized.(map[string]any); ok && v["kind"] != "block" {
				text, ok := v["value"].(string)
				if !ok || !has(values, text) {
					return out, issue("MENU_VALUE", p, "input must use a serialized static-menu value")
				}
			}
		}
		out.Inputs[name] = normalized
	}
	for name := range contract.RequiredInputs {
		if _, ok := b.Inputs[name]; !ok {
			return out, issue("MISSING_PORT", path+"/inputs/"+name, "required input is missing")
		}
	}
	for name, source := range b.Substacks {
		p := path + "/substacks/" + name
		if !contract.Substacks[name] {
			return out, issue("UNKNOWN_PORT", p, fmt.Sprintf("substack %q is absent from opcode %q", diagnosticName(name), b.Opcode))
		}
		stack, err := n.sequence(source, p, depth+1)
		if err != nil {
			return out, err
		}
		out.Substacks[name] = stack
	}
	for name := range contract.RequiredSubstacks {
		if _, ok := b.Substacks[name]; !ok {
			return out, issue("MISSING_PORT", path+"/substacks/"+name, "required substack is missing (an explicit empty array is permitted)")
		}
	}
	return out, nil
}
func diagnosticName(value string) string {
	name := []rune(value)
	if len(name) > 96 {
		return string(name[:96]) + "…"
	}
	return value
}
func logical(value any, tag, path string) (string, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return "", issue("LOGICAL_REFERENCE", path, "expected a single "+tag+" handle")
	}
	handle, ok := object[tag].(string)
	if !ok || handle == "" {
		return "", issue("LOGICAL_REFERENCE", path, "expected a single "+tag+" handle")
	}
	return handle, nil
}
func stateAccess(op string) string {
	switch op {
	case "data_setvariableto", "data_changevariableby", "data_addtolist", "data_deleteoflist", "data_deletealloflist", "data_insertatlist", "data_replaceitemoflist":
		return "write"
	case "event_whenbroadcastreceived":
		return "receive"
	default:
		return "read"
	}
}
func (n *normalizer) state(handle, kind, access, path string) (*StateBinding, error) {
	state := stateFor(n.contract, handle)
	if state == nil {
		return nil, issue("STATE_UNKNOWN", path, "state handle is not frozen")
	}
	if state.Kind != kind {
		return nil, issue("STATE_KIND", path, "state kind does not match the port")
	}
	if state.Scope == "local" && state.Owner != n.target.Handle {
		return nil, issue("STATE_SCOPE", path, "state belongs to another target")
	}
	allowed := n.slot.Reads
	switch access {
	case "write":
		allowed = n.slot.Writes
	case "emit":
		allowed = n.slot.Emits
	case "receive":
		allowed = n.slot.Receives
	}
	if !has(allowed, handle) {
		return nil, issue("STATE_ACCESS", path, "slot lacks "+access+" permission for state "+handle)
	}
	return state, nil
}
func stateField(c *FrozenContract, state *StateBinding) any {
	if c.Scope == "single_sprite" {
		return state.Name
	}
	return map[string]any{"kind": "state_ref", "state_kind": state.Kind, "ref_id": state.RefID}
}
func (n *normalizer) input(value any, kind blockcatalog.InputKind, path string, depth int) (any, error) {
	if object, ok := value.(map[string]any); ok {
		if _, ok := object["opcode"]; ok {
			b, err := compactFromMap(object, path)
			if err != nil {
				return nil, err
			}
			block, err := n.block(b, path, depth, true, kind)
			if err != nil {
				return nil, err
			}
			return map[string]any{"kind": "block", "block": block}, nil
		}
		handle, err := logical(value, "state", path)
		if err != nil {
			return nil, err
		}
		state := stateFor(n.contract, handle)
		if state == nil || state.Kind == "broadcast" {
			return nil, issue("STATE_KIND", path, "ordinary inputs can reference only variable or list reporters")
		}
		if kind == blockcatalog.InputBoolean {
			return nil, issue("BOOLEAN_EXPECTED", path, "use an explicit comparison to read state as a condition")
		}
		opcode := "data_variable"
		field := "VARIABLE"
		if state.Kind == "list" {
			opcode = "data_listcontents"
			field = "LIST"
		}
		block, err := n.block(CompactBlock{Opcode: opcode, Fields: map[string]any{field: map[string]any{"state": handle}}}, path, depth, true, kind)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "block", "block": block}, nil
	}
	if kind == blockcatalog.InputBoolean {
		return nil, issue("BOOLEAN_EXPECTED", path, "boolean slots require an explicit boolean reporter")
	}
	switch kind {
	case blockcatalog.InputNumber, blockcatalog.InputAngle:
		if _, ok := value.(float64); !ok {
			switch value.(type) {
			case int, int64:
			default:
				return nil, issue("LITERAL_TYPE", path, "numeric input requires a number; strings are not cast")
			}
		}
		if !scalar(value) {
			return nil, issue("LITERAL_TYPE", path, "number must be finite")
		}
	case blockcatalog.InputText, blockcatalog.InputColor:
		if _, ok := value.(string); !ok {
			return nil, issue("LITERAL_TYPE", path, "text/color input requires a string; numbers are not cast")
		}
	case blockcatalog.InputAny:
		switch value.(type) {
		case string:
			kind = blockcatalog.InputText
		case int, int64, float64:
			kind = blockcatalog.InputNumber
		default:
			return nil, issue("LITERAL_TYPE", path, "unsupported literal type")
		}
	default:
		return nil, issue("LITERAL_TYPE", path, "unsupported input kind")
	}
	if !scalar(value) {
		return nil, issue("LITERAL_TYPE", path, "literal exceeds the supported range")
	}
	return map[string]any{"kind": string(kind), "value": value}, nil
}
func compactFromMap(object map[string]any, path string) (CompactBlock, error) {
	for name := range object {
		if name != "opcode" && name != "fields" && name != "inputs" && name != "substacks" {
			return CompactBlock{}, issue("SHAPE", path+"/"+name, "unknown compact block property")
		}
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return CompactBlock{}, issue("SHAPE", path, err.Error())
	}
	var b CompactBlock
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&b); err != nil {
		return b, issue("SHAPE", path, err.Error())
	}
	return b, nil
}
func (n *normalizer) resource(value any, expected, opcode, port, path string) (any, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return nil, issue("LOGICAL_REFERENCE", path, "resource menus require a logical asset, target, or builtin reference")
	}
	if _, ok := object["builtin"]; ok {
		handle, err := logical(value, "builtin", path)
		if err != nil {
			return nil, err
		}
		v, ok := builtinValue(opcode, port, handle)
		if !ok {
			return nil, issue("BUILTIN_NOT_ALLOWED", path, "builtin is not available on this exact port")
		}
		return map[string]any{"kind": "text", "value": v}, nil
	}
	tag := "asset"
	if _, ok := object["target"]; ok {
		tag = "target"
	}
	handle, err := logical(value, tag, path)
	if err != nil {
		return nil, err
	}
	if !has(n.slot.Assets, handle) && n.slot.Trigger.Asset != handle {
		return nil, issue("ASSET_ACCESS", path, "slot lacks permission for this resource")
	}
	if tag == "target" {
		target := targetFor(n.contract, handle)
		if target == nil || target.Kind != "sprite" || expected != "sprite" {
			return nil, issue("ASSET_KIND", path, "target handle does not match a sprite port")
		}
		provider := blockcatalog.BlockContracts[opcode].InputProviders[port]
		if provider == "" {
			provider = blockcatalog.BlockContracts[opcode].FieldProviders[port]
		}
		if provider == "sprites_except_self" && target.Name == n.target.Name {
			return nil, issue("ASSET_SCOPE", path, "this menu excludes the current sprite")
		}
		if target.Disposition == "new" {
			return map[string]any{"kind": "resource_ref", "ref": map[string]any{"op_id": *target.Ref.OpID, "value": "installed_name"}}, nil
		}
		return map[string]any{"kind": "existing_asset_ref", "asset_type": "sprite", "name": target.Name}, nil
	}
	asset := assetFor(n.contract, handle)
	if asset == nil {
		return nil, issue("ASSET_UNKNOWN", path, "asset handle is not frozen")
	}
	if asset.Kind != expected {
		return nil, issue("ASSET_KIND", path, "asset kind does not match the resource port")
	}
	if asset.Kind != "sprite" && asset.Kind != "backdrop" && asset.Owner != n.target.Handle {
		return nil, issue("ASSET_SCOPE", path, "asset belongs to another target")
	}
	provider := blockcatalog.BlockContracts[opcode].InputProviders[port]
	if provider == "" {
		provider = blockcatalog.BlockContracts[opcode].FieldProviders[port]
	}
	if provider == "sprites_except_self" && asset.Kind == "sprite" && asset.Name == n.target.Name {
		return nil, issue("ASSET_SCOPE", path, "this menu excludes the current sprite")
	}
	if asset.Disposition == "new" {
		return map[string]any{"kind": "resource_ref", "ref": map[string]any{"op_id": asset.OpID, "value": "installed_name"}}, nil
	}
	return map[string]any{"kind": "existing_asset_ref", "asset_type": asset.Kind, "name": asset.Name}, nil
}
func builtinValue(opcode, port, handle string) (string, bool) {
	contract, ok := blockcatalog.BlockContracts[opcode]
	if !ok {
		return "", false
	}
	aliases := map[string]string{"mouse": "_mouse_", "edge": "_edge_", "random": "_random_", "myself": "_myself_", "stage": "_stage_"}
	value, ok := aliases[handle]
	if !ok {
		return "", false
	}
	allowed := contract.InputBuiltins[port]
	if len(allowed) == 0 {
		allowed = contract.FieldBuiltins[port]
	}
	return value, has(allowed, value)
}
func hatFor(c *FrozenContract, slot *FrozenSlot) (schema.Block, error) {
	b := schema.Block{Fields: map[string]any{}, Inputs: map[string]any{}, Substacks: map[string][]schema.Block{}}
	trigger := slot.Trigger
	path := "/script_slots/" + slot.Key + "/trigger"
	owner := targetFor(c, slot.Owner)
	if owner == nil {
		return b, issue("SLOT_OWNER", path, "unknown slot target")
	}
	forbidden := func(allow string) bool {
		return trigger.Key != "" && allow != "key" || trigger.State != "" && allow != "state" || trigger.Asset != "" && allow != "asset" || trigger.Seconds != nil && allow != "seconds"
	}
	switch trigger.Kind {
	case "green_flag":
		if forbidden("") {
			return b, issue("TRIGGER_SHAPE", path, "green_flag has no arguments")
		}
		b.Opcode = "event_whenflagclicked"
	case "key_pressed":
		if forbidden("key") || trigger.Key == "" {
			return b, issue("TRIGGER_SHAPE", path, "key_pressed requires only key")
		}
		valid := trigger.Key == "space" || trigger.Key == "up arrow" || trigger.Key == "down arrow" || trigger.Key == "left arrow" || trigger.Key == "right arrow" || trigger.Key == "any" || trigger.Key == "enter" || len([]rune(trigger.Key)) == 1
		if !valid {
			return b, issue("TRIGGER_SHAPE", path, "key is not a Scratch key menu value")
		}
		b.Opcode = "event_whenkeypressed"
		b.Fields["KEY_OPTION"] = trigger.Key
	case "sprite_clicked":
		if forbidden("") {
			return b, issue("TRIGGER_SHAPE", path, "click trigger has no arguments")
		}
		b.Opcode = "event_whenthisspriteclicked"
		if owner.Kind == "stage" {
			b.Opcode = "event_whenstageclicked"
		}
	case "broadcast_received":
		if forbidden("state") {
			return b, issue("TRIGGER_SHAPE", path, "broadcast trigger accepts only state")
		}
		state := stateFor(c, trigger.State)
		if state == nil || state.Kind != "broadcast" || !has(slot.Receives, trigger.State) {
			return b, issue("STATE_ACCESS", path, "broadcast trigger requires a frozen receive interface")
		}
		b.Opcode = "event_whenbroadcastreceived"
		b.Fields["BROADCAST_OPTION"] = stateField(c, state)
	case "backdrop_switched":
		if forbidden("asset") {
			return b, issue("TRIGGER_SHAPE", path, "backdrop trigger accepts only asset")
		}
		asset := assetFor(c, trigger.Asset)
		if asset == nil || asset.Kind != "backdrop" {
			return b, issue("ASSET_KIND", path, "backdrop trigger requires a frozen backdrop asset")
		}
		b.Opcode = "event_whenbackdropswitchesto"
		if asset.Disposition == "new" {
			b.Fields["BACKDROP"] = map[string]any{"kind": "resource_ref", "ref": map[string]any{"op_id": asset.OpID, "value": "installed_name"}}
		} else {
			b.Fields["BACKDROP"] = map[string]any{"kind": "existing_asset_ref", "asset_type": "backdrop", "name": asset.Name}
		}
	case "clone_started":
		if forbidden("") || owner.Kind != "sprite" {
			return b, issue("TRIGGER_SHAPE", path, "clone_started requires a sprite and no arguments")
		}
		b.Opcode = "control_start_as_clone"
	case "timer_reached":
		if forbidden("seconds") || trigger.Seconds == nil || *trigger.Seconds < 0 || math.IsNaN(*trigger.Seconds) || math.IsInf(*trigger.Seconds, 0) {
			return b, issue("TRIGGER_SHAPE", path, "timer_reached requires finite nonnegative seconds")
		}
		b.Opcode = "event_whengreaterthan"
		b.Fields["WHENGREATERTHANMENU"] = "TIMER"
		b.Inputs["VALUE"] = map[string]any{"kind": "number", "value": *trigger.Seconds}
	default:
		return b, issue("TRIGGER_KIND", path, "unsupported trigger; procedures require the legacy path")
	}
	return b, nil
}

// AssembleProgram revalidates all scripts together and fills model-independent
// metadata/declarations. It accepts exactly one result for every target slot.
func AssembleProgram(c *FrozenContract, targetHandle string, scripts map[string]schema.Script) (*schema.ProgramIR, error) {
	if err := ValidateFrozen(c); err != nil {
		return nil, err
	}
	target := targetFor(c, targetHandle)
	if target == nil {
		return nil, issue("TARGET_UNKNOWN", "/target", "unknown target")
	}
	program := &schema.ProgramIR{SchemaVersion: 1, Summary: c.Title, RemoveScriptIDs: []string{}, Variables: []schema.VariableDefinition{}, Lists: []schema.ListDefinition{}, Broadcasts: []string{}, Procedures: []schema.Procedure{}, Scripts: []schema.Script{}}
	if c.Scope == "project" {
		program.SchemaVersion = 2
	}
	expected := map[string]bool{}
	for _, slot := range c.ScriptSlots {
		if slot.Owner != targetHandle {
			continue
		}
		expected[slot.Key] = true
		script, ok := scripts[slot.Key]
		if !ok {
			return nil, issue("SLOT_MISSING", "/scripts/"+slot.Key, "target slot has no validated result")
		}
		if script.ID != slot.ScriptID || script.Purpose != slot.Purpose || len(script.Blocks) == 0 {
			return nil, issue("SLOT_BINDING", "/scripts/"+slot.Key, "script metadata disagrees with frozen slot")
		}
		hat, _ := hatFor(c, &slot)
		actual, _ := json.Marshal(script.Blocks[0])
		wanted, _ := json.Marshal(hat)
		if string(actual) != string(wanted) {
			return nil, issue("TRIGGER_CHANGED", "/scripts/"+slot.Key, "script changed its frozen event hat")
		}
		if err := validateScriptPermissions(c, target, &slot, &script); err != nil {
			return nil, err
		}
		program.Scripts = append(program.Scripts, script)
		if slot.RemoveScriptID != "" {
			program.RemoveScriptIDs = append(program.RemoveScriptIDs, slot.RemoveScriptID)
		}
	}
	if len(expected) != len(scripts) {
		return nil, issue("SLOT_UNKNOWN", "/scripts", "unexpected slot results are not accepted")
	}
	if c.Scope == "single_sprite" {
		for _, state := range c.States {
			if state.Scope == "local" && state.Owner != targetHandle {
				continue
			}
			switch state.Kind {
			case "variable":
				program.Variables = append(program.Variables, schema.VariableDefinition{Name: state.Name, Value: state.Value, Scope: ptr(state.Scope)})
			case "list":
				program.Lists = append(program.Lists, schema.ListDefinition{Name: state.Name, Value: state.Value, Scope: ptr(state.Scope)})
			case "broadcast":
				program.Broadcasts = append(program.Broadcasts, state.Name)
			}
		}
	}
	sort.Strings(program.RemoveScriptIDs)
	if err := program.Validate(); err != nil {
		return nil, issue("PROGRAM_IR", "/program", err.Error())
	}
	if _, err := schema.ValidateProgramContracts(program, schema.ProgramContractOptions{TargetKind: target.Kind, AllowedExtensions: set(c.RequiredExtensions), AllowResourceRefs: c.Scope == "project"}); err != nil {
		return nil, issue("PROGRAM_CONTRACT", "/program", err.Error())
	}
	if _, err := schema.ValidateProgramForMode(program, target.Mode, target.Context.CurrentProgram); err != nil {
		return nil, issue("EDIT_NOT_ALLOWED", "/remove_script_ids", err.Error())
	}
	if c.Scope == "project" {
		derived, err := DeriveLegacy(c)
		if err != nil {
			return nil, err
		}
		if _, err := schema.ValidateProjectProgramStateRefs(program, derived.Manifest); err != nil {
			return nil, err
		}
		if _, err := schema.ValidateProjectProgramResourceRefs(program, derived.Manifest, nil); err != nil {
			return nil, err
		}
	}
	return program, nil
}
