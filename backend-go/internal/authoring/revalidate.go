package authoring

import (
	"encoding/json"
	"fmt"

	"github.com/ai-scratch-web/backend/internal/blockcatalog"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// A repaired slot is authorized again at the merge boundary, even if a caller
// accidentally modified an earlier normalized script in place.
func validateScriptPermissions(c *FrozenContract, target *TargetBinding, slot *FrozenSlot, script *schema.Script) error {
	n := normalizer{contract: c, target: target, slot: slot}
	count := 0
	var visit func([]schema.Block, int, string) error
	visit = func(blocks []schema.Block, depth int, path string) error {
		if depth > schema.MaxBlockDepth {
			return issue("BLOCK_DEPTH", path, "merged script nesting exceeds its budget")
		}
		for i, block := range blocks {
			count++
			p := fmt.Sprintf("%s/%d", path, i)
			if count > schema.MaxBlocks {
				return issue("BLOCK_BUDGET", p, "merged script exceeds the block budget")
			}
			contract, ok := blockcatalog.BlockContracts[block.Opcode]
			if !ok {
				return issue("UNKNOWN_OPCODE", p, "merged script has an unsupported opcode")
			}
			for port, value := range block.Fields {
				if values := contract.FieldEnums[port]; len(values) > 0 {
					text, ok := value.(string)
					if !ok || !has(values, text) {
						return issue("MENU_VALUE", p+"/fields/"+port, "field must use a serialized static-menu value")
					}
				}
				if contract.FieldProviders[port] == "stop_options" && value != "all" && value != "this script" {
					return issue("CAPABILITY_UNSUPPORTED", p+"/fields/"+port, "this compiler profile supports only terminal stop variants")
				}
				if contract.ResolvedFields[port] {
					kind := "variable"
					if port == "LIST" {
						kind = "list"
					}
					if port == "BROADCAST_OPTION" {
						kind = "broadcast"
					}
					var candidate *StateBinding
					if ref, ok := schema.AsStateRefFieldValue(value); ok {
						for index := range c.States {
							if c.States[index].RefID == ref.RefID && c.States[index].Kind == kind {
								candidate = &c.States[index]
							}
						}
					} else if name, ok := value.(string); ok && c.Scope == "single_sprite" {
						for index := range c.States {
							s := &c.States[index]
							if s.Name == name && s.Kind == kind && (s.Scope == "global" || s.Owner == target.Handle) {
								if candidate != nil {
									return issue("STATE_AMBIGUOUS", p, "legacy name cannot distinguish these state bindings")
								}
								candidate = s
							}
						}
					}
					if candidate == nil {
						return issue("STATE_UNKNOWN", p, "merged script uses an unknown state")
					}
					if _, err := n.state(candidate.Handle, kind, stateAccess(block.Opcode), p); err != nil {
						return err
					}
				}
				if expected, ok := contract.ResourceFieldTypes[port]; ok {
					if err := n.authorizeResource(value, string(expected), block.Opcode, port, p); err != nil {
						return err
					}
				}
			}
			for port, value := range block.Inputs {
				if values := contract.InputEnums[port]; len(values) > 0 && contract.Inputs[port] != blockcatalog.InputColor {
					if literal, ok := value.(map[string]any); ok && literal["kind"] != "block" {
						text, ok := literal["value"].(string)
						if !ok || !has(values, text) {
							return issue("MENU_VALUE", p+"/inputs/"+port, "input must use a serialized static-menu value")
						}
					}
				}
				if expected, ok := contract.ResourceInputTypes[port]; ok {
					if err := n.authorizeResource(value, string(expected), block.Opcode, port, p); err != nil {
						return err
					}
				}
				if port == "BROADCAST_INPUT" && (block.Opcode == "event_broadcast" || block.Opcode == "event_broadcastandwait") {
					object, ok := value.(map[string]any)
					if !ok || object["kind"] != "text" {
						return issue("STATE_ACCESS", p, "broadcast interface must stay a frozen literal")
					}
					allowed := false
					for _, handle := range slot.Emits {
						state := stateFor(c, handle)
						if state != nil && state.Name == object["value"] {
							allowed = true
						}
					}
					if !allowed {
						return issue("STATE_ACCESS", p, "merged slot emits an unauthorized broadcast")
					}
				}
				if nested, ok := schema.AsBlockInputValue(value); ok {
					if err := visit([]schema.Block{nested.Block}, depth+1, p+"/inputs/"+port); err != nil {
						return err
					}
				}
			}
			for name, stack := range block.Substacks {
				if err := visit(stack, depth+1, p+"/substacks/"+name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(script.Blocks, 1, "/scripts/"+slot.Key)
}
func (n *normalizer) authorizeResource(value any, expected, opcode, port, path string) error {
	raw, _ := json.Marshal(value)
	equivalent := func(candidate any) bool { b, _ := json.Marshal(candidate); return string(raw) == string(b) }
	for _, handle := range append(append([]string{}, n.slot.Assets...), n.slot.Trigger.Asset) {
		if handle == "" {
			continue
		}
		tag := "asset"
		if targetFor(n.contract, handle) != nil {
			tag = "target"
		}
		candidate, err := n.resource(map[string]any{tag: handle}, expected, opcode, port, path)
		if err == nil && equivalent(candidate) {
			return nil
		}
	}
	for _, builtin := range []string{"mouse", "edge", "random", "myself", "stage"} {
		candidate, err := n.resource(map[string]any{"builtin": builtin}, expected, opcode, port, path)
		if err == nil && equivalent(candidate) {
			return nil
		}
	}
	return issue("ASSET_ACCESS", path, "merged slot uses an unauthorized or changed resource binding")
}
