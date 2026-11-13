package authoring

import "fmt"

// BindTrustedSymbols supplies omitted reuse declarations only for exact trusted
// state handles already referenced by a slot. It never guesses from a display
// name, introduces new state, changes a value, or widens local ownership. The
// returned state slice belongs to the copy; the caller's spec is not modified.
// Resource declarations remain explicit because they carry preparation intent.
func BindTrustedSymbols(spec *IntentSpec, options FreezeOptions) (*IntentSpec, error) {
	if spec == nil {
		return nil, issue("SHAPE", "/", "IntentSpec is required")
	}
	if len(spec.States) > 150 || len(spec.ScriptSlots) > 80 {
		return nil, issue("BUDGET", "/", "intent state/slot budget exceeded")
	}
	out := *spec
	out.States = append([]IntentState{}, spec.States...)
	declared := map[string]bool{}
	for _, state := range spec.States {
		declared[state.Key] = true
	}
	trusted := map[string]StateBinding{}
	ambiguous := map[string]bool{}
	for _, state := range options.ExistingStates {
		if _, exists := trusted[state.Handle]; exists {
			ambiguous[state.Handle] = true
		}
		trusted[state.Handle] = state
	}
	bind := func(handle, owner, path string) error {
		if handle == "" || declared[handle] {
			return nil
		}
		state, exists := trusted[handle]
		if !exists {
			return issue("STATE_UNKNOWN", path, "undeclared state: "+diagnosticName(handle))
		}
		if ambiguous[handle] {
			return issue("STATE_AMBIGUOUS", path, "trusted state handle must identify exactly one snapshot state")
		}
		if state.Scope == "local" && state.Owner != owner {
			return issue("STATE_SCOPE", path, "local state belongs to another target")
		}
		if len(out.States) >= 150 {
			return issue("BUDGET", "/states", "trusted reuse declarations exceed the state budget")
		}
		out.States = append(out.States, IntentState{Key: handle, Name: state.Name, Kind: state.Kind, Scope: state.Scope, Owner: state.Owner, Disposition: "reuse"})
		declared[handle] = true
		return nil
	}
	for index, slot := range spec.ScriptSlots {
		// Fixed order makes the frozen state order and hash reproducible.
		for _, group := range []struct {
			name string
			refs []string
		}{{"reads", slot.Reads}, {"writes", slot.Writes}, {"emits", slot.Emits}, {"receives", slot.Receives}} {
			if len(group.refs) > 150 {
				return nil, issue("BUDGET", fmt.Sprintf("/script_slots/%d/%s", index, group.name), "array exceeds the item budget")
			}
			for _, handle := range group.refs {
				if err := bind(handle, slot.Owner, fmt.Sprintf("/script_slots/%d/%s", index, group.name)); err != nil {
					return nil, err
				}
			}
		}
		if slot.Trigger.State != "" {
			if err := bind(slot.Trigger.State, slot.Owner, fmt.Sprintf("/script_slots/%d/trigger/state", index)); err != nil {
				return nil, err
			}
		}
	}
	return &out, nil
}
