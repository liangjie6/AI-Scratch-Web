package authoring

import (
	"testing"
	"time"
)

func TestFrozenContractBindsOriginalSessionAndRequest(t *testing.T) {
	c, request := singleFixture(t, "append")
	if c.RequestBindingHash == "" {
		t.Fatal("missing code-owned request binding")
	}
	for _, mutate := range []func(){func() { request.SessionID = "other-session" }, func() { request.RequestID = "different-original-request" }} {
		mutate()
		requireCode(t, ValidateRequestBinding(c, request), "REQUEST_BINDING")
		request.SessionID, request.RequestID = "", ""
	}
	if err := ValidateRequestBinding(c, request); err != nil {
		t.Fatal(err)
	}
	c.RequestBindingHash = ""
	c.Hash, _ = contractFingerprint(c)
	requireCode(t, ValidateFrozen(c), "REQUEST_BINDING")
}

func TestFrozenExpiryIsPinnedAndHistoricalArtifactsRemainReadable(t *testing.T) {
	c, request := singleFixture(t, "append")
	remaining := c.ExpiresAtUnix - time.Now().Unix()
	if remaining < 1798 || remaining > 1800 {
		t.Fatalf("initial contract expiry is not thirty minutes: %d", remaining)
	}
	c.ExpiresAtUnix = 1
	c.Hash, _ = contractFingerprint(c)
	if err := ValidateFrozen(c); err != nil {
		t.Fatalf("historical artifact should remain readable: %v", err)
	}
	if err := ValidateRequestBinding(c, request); err != nil {
		t.Fatalf("request reconstruction refreshed or rejected pinned expiry: %v", err)
	}
	c.ExpiresAtUnix = 0
	c.Hash, _ = contractFingerprint(c)
	requireCode(t, ValidateFrozen(c), "EXPIRY")
}

func TestModifyRequiresSelectedEditAndAllowsAuxiliarySlot(t *testing.T) {
	c, request := singleFixture(t, "modify")
	spec := &IntentSpec{SchemaVersion: 1, Title: c.Title, Goal: c.Goal, Targets: []IntentTarget{{Key: "cat", Name: "猫", Kind: "sprite", Disposition: "current"}}, States: []IntentState{{Key: "score", Name: "得分", Kind: "variable", Scope: "global", Disposition: "new", Initial: 0}}, ScriptSlots: []IntentSlot{c.ScriptSlots[0].IntentSlot}}
	opts, err := OptionsFromRequest(request, spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.ScriptSlots[0].EditRef = ""
	_, err = Freeze(spec, opts)
	requireCode(t, err, "EDIT_REF_REQUIRED")
	c.ScriptSlots[0].EditRef, c.ScriptSlots[0].RemoveScriptID = "", ""
	c.Hash, _ = contractFingerprint(c)
	requireCode(t, ValidateFrozen(c), "EDIT_REF_REQUIRED")

	spec.ScriptSlots[0].EditRef = "script_1"
	spec.ScriptSlots = append(spec.ScriptSlots, IntentSlot{Key: "helper", Owner: "cat", Trigger: Trigger{Kind: "key_pressed", Key: "space"}, Purpose: "新增辅助脚本"})
	frozen, err := Freeze(spec, opts)
	if err != nil || len(frozen.ScriptSlots) != 2 || frozen.ScriptSlots[1].RemoveScriptID != "" {
		t.Fatalf("authorized edit with auxiliary slot rejected: %v", err)
	}
	if err := ValidateRequestBinding(frozen, request); err != nil {
		t.Fatal(err)
	}
}
