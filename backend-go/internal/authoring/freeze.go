package authoring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ai-scratch-web/backend/internal/blockcatalog"
	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/schema"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,79}$`)
var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func stableID(prefix, key string) string {
	h := sha256.Sum256([]byte(key))
	return prefix + hex.EncodeToString(h[:8])
}
func fingerprint(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return "", err
	}
	canonical, err := jsonx.Canonical(parsed)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(h[:]), nil
}

// CatalogFingerprint pins the exact currently supported compiler contract.
func CatalogFingerprint() string { return blockcatalog.CatalogHash }
func requestBindingFingerprint(sessionID, requestID string) (string, error) {
	return fingerprint(map[string]string{"session_id": sessionID, "request_id": requestID})
}
func contractFingerprint(c *FrozenContract) (string, error) {
	copy := *c
	copy.Hash = ""
	return fingerprint(&copy)
}
func short(value, path string) error {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 120 {
		return issue("SHAPE", path, "a non-empty string of at most 120 characters is required")
	}
	return nil
}
func key(value, path string) error {
	if !keyPattern.MatchString(value) {
		return issue("SYMBOL", path, "handle must start with a letter and contain at most 80 ASCII letters, digits, underscores or hyphens")
	}
	return nil
}
func set(values []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range values {
		m[v] = true
	}
	return m
}
func has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func ptr(v string) *string { return &v }
func uniqueStrings(values []string, path string, max int) error {
	if len(values) > max {
		return issue("BUDGET", path, "array exceeds the item budget")
	}
	seen := map[string]bool{}
	for i, v := range values {
		if err := short(v, fmt.Sprintf("%s/%d", path, i)); err != nil {
			return err
		}
		if seen[v] {
			return issue("DUPLICATE_SYMBOL", path, "duplicate entry: "+v)
		}
		seen[v] = true
	}
	return nil
}
func empty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
func targetFor(c *FrozenContract, handle string) *TargetBinding {
	for i := range c.Targets {
		if c.Targets[i].Handle == handle {
			return &c.Targets[i]
		}
	}
	return nil
}
func stateFor(c *FrozenContract, handle string) *StateBinding {
	for i := range c.States {
		if c.States[i].Handle == handle {
			return &c.States[i]
		}
	}
	return nil
}
func assetFor(c *FrozenContract, handle string) *AssetBinding {
	for i := range c.Assets {
		if c.Assets[i].Handle == handle {
			return &c.Assets[i]
		}
	}
	return nil
}
func slotFor(c *FrozenContract, handle string) *FrozenSlot {
	for i := range c.ScriptSlots {
		if c.ScriptSlots[i].Key == handle {
			return &c.ScriptSlots[i]
		}
	}
	return nil
}

// Freeze resolves all model-owned local keys against a trusted snapshot. No
// input object is retained by reference: the resulting artifact owns its data.
func Freeze(spec *IntentSpec, options FreezeOptions) (*FrozenContract, error) {
	if spec == nil {
		return nil, issue("SHAPE", "/", "IntentSpec is required")
	}
	if spec.SchemaVersion != IntentVersion {
		return nil, issue("VERSION", "/schema_version", "IntentSpec schema_version must be 1")
	}
	if (spec.Ready != nil && !*spec.Ready) || len(spec.MissingQuestions) > 0 {
		return nil, issue("NEEDS_QUESTIONS", "/missing_questions", "intent requires clarification before interfaces can be frozen")
	}
	if err := short(spec.Title, "/title"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Goal) == "" || utf8.RuneCountInString(spec.Goal) > 2000 {
		return nil, issue("SHAPE", "/goal", "goal must contain 1 to 2000 characters")
	}
	if options.Scope != "single_sprite" && options.Scope != "project" {
		return nil, issue("SCOPE", "/scope", "scope must be single_sprite or project")
	}
	if options.Mode == "" {
		options.Mode = options.Target.Mode
	}
	if options.Mode == "" {
		options.Mode = "append"
	}
	if options.Mode != "append" && options.Mode != "modify" && options.Mode != "rewrite" {
		return nil, issue("MODE", "/mode", "unknown editing mode")
	}
	if !shaPattern.MatchString(options.BaseSnapshotHash) {
		return nil, issue("SNAPSHOT", "/base_snapshot_hash", "a SHA-256 snapshot binding is required")
	}
	if options.RequestBindingHash == "" {
		// Direct pure-function users have no HTTP request identity. Request-backed
		// callers always supply the actual identity through OptionsFromRequest.
		options.RequestBindingHash, _ = requestBindingFingerprint("", "")
	}
	if !shaPattern.MatchString(options.RequestBindingHash) {
		return nil, issue("REQUEST_BINDING", "/request_binding_hash", "a SHA-256 request identity binding is required")
	}
	if options.ExpiresAtUnix == 0 {
		options.ExpiresAtUnix = time.Now().Add(30 * time.Minute).Unix()
	}
	if options.ExpiresAtUnix < 1 {
		return nil, issue("EXPIRY", "/expires_at_unix", "contract expiry must be a positive Unix timestamp")
	}
	if options.CatalogHash == "" {
		options.CatalogHash = CatalogFingerprint()
	}
	if options.CatalogHash != CatalogFingerprint() {
		return nil, issue("CATALOG_VERSION", "/catalog_hash", "catalog does not match the supported compiler contract")
	}
	if len(spec.Targets) < 1 || len(spec.Targets) > 12 || len(spec.States) > 150 || len(spec.Assets) > 24 || len(spec.ScriptSlots) < 1 || len(spec.ScriptSlots) > 80 {
		return nil, issue("BUDGET", "/", "intent target/state/asset/slot budget exceeded")
	}
	boundSpec, err := BindTrustedSymbols(spec, options)
	if err != nil {
		return nil, err
	}
	spec = boundSpec
	if err := uniqueStrings(spec.RequiredExtensions, "/required_extensions", 3); err != nil {
		return nil, err
	}
	for _, ext := range spec.RequiredExtensions {
		if (ext != "pen" && ext != "music" && ext != "text2speech") || !options.AllowedExtensions[ext] {
			return nil, issue("EXTENSION_NOT_ALLOWED", "/required_extensions", ext+" is not authorized")
		}
	}
	if err := uniqueStrings(spec.Assumptions, "/assumptions", 10); err != nil {
		return nil, err
	}
	if err := uniqueStrings(spec.SuccessCriteria, "/success_criteria", 40); err != nil {
		return nil, err
	}
	c := &FrozenContract{PipelineVersion: PipelineVersion, NormalizerVersion: NormalizerVersion, CatalogHash: options.CatalogHash, Scope: options.Scope, Mode: options.Mode, BaseSnapshotHash: options.BaseSnapshotHash, RequestBindingHash: options.RequestBindingHash, ExpiresAtUnix: options.ExpiresAtUnix, InterfaceVersion: 1, Title: spec.Title, Goal: spec.Goal, Targets: []TargetBinding{}, States: []StateBinding{}, Assets: []AssetBinding{}, ScriptSlots: []FrozenSlot{}, RequiredExtensions: empty(spec.RequiredExtensions), Assumptions: empty(spec.Assumptions), SuccessCriteria: empty(spec.SuccessCriteria), AssetIntents: empty(spec.Assets)}
	targetNames := map[string]bool{}
	existingRefs := map[string]bool{}
	for index, t := range spec.Targets {
		path := fmt.Sprintf("/targets/%d", index)
		if err := key(t.Key, path+"/key"); err != nil {
			return nil, err
		}
		if targetFor(c, t.Key) != nil {
			return nil, issue("DUPLICATE_SYMBOL", path, "target key is duplicated")
		}
		if err := short(t.Name, path+"/name"); err != nil {
			return nil, err
		}
		if t.Kind != "stage" && t.Kind != "sprite" {
			return nil, issue("TARGET_KIND", path, "target kind must be stage or sprite")
		}
		if targetNames[strings.ToLower(t.Name)] {
			return nil, issue("TARGET_AMBIGUOUS", path, "target names must be unique")
		}
		targetNames[strings.ToLower(t.Name)] = true
		b := TargetBinding{Handle: t.Key, EntityID: stableID("entity_", t.Key), Name: t.Name, Kind: t.Kind, Disposition: t.Disposition, Mode: "append"}
		switch t.Disposition {
		case "current":
			if options.Target.Name == "" || t.Name != options.Target.Name || (t.Kind == "stage") != options.Target.IsStage {
				return nil, issue("TARGET_BINDING", path, "current target must match the trusted request snapshot")
			}
			b.Ref = schema.TargetRef{Kind: "current_target"}
			if t.TargetRef != nil && t.TargetRef.Kind != "current_target" {
				return nil, issue("TARGET_BINDING", path, "current disposition must use current_target")
			}
			b.Context = options.Target
			b.Mode = options.Mode
		case "existing":
			if options.Scope != "project" || options.ProjectContext == nil {
				return nil, issue("SCOPE", path, "existing project target requires project context")
			}
			matches := 0
			for _, trusted := range options.ProjectContext.Targets {
				if trusted.Name == t.Name && trusted.IsStage == (t.Kind == "stage") {
					matches++
					b.Context = schema.TargetContext{Name: trusted.Name, IsStage: trusted.IsStage, HasScripts: trusted.HasScripts, Mode: "append", Costumes: trusted.Costumes, Backdrops: trusted.Backdrops, Sounds: trusted.Sounds, SpriteNames: trusted.SpriteNames, CurrentProgram: trusted.CurrentProgram}
				}
			}
			if matches != 1 {
				return nil, issue("TARGET_AMBIGUOUS", path, "existing target must match exactly one snapshot target")
			}
			if t.Kind == "stage" {
				b.Ref = schema.TargetRef{Kind: "stage"}
			} else {
				b.Ref = schema.TargetRef{Kind: "existing_sprite", Name: ptr(t.Name)}
			}
			if t.TargetRef != nil {
				raw, _ := json.Marshal(t.TargetRef)
				expected, _ := json.Marshal(b.Ref)
				if string(raw) != string(expected) {
					return nil, issue("TARGET_BINDING", path, "target_ref disagrees with the resolved snapshot target")
				}
			}
		case "new":
			if options.Scope != "project" || t.Kind != "sprite" || t.TargetRef != nil {
				return nil, issue("SCOPE", path, "only project sprite targets can be newly created")
			}
			if options.ProjectContext != nil {
				for _, existing := range options.ProjectContext.Targets {
					if strings.EqualFold(existing.Name, t.Name) {
						return nil, issue("TARGET_AMBIGUOUS", path, "new target name already exists")
					}
				}
			}
			b.Ref = schema.TargetRef{Kind: "created_sprite", OpID: ptr(stableID("op_", "target:"+t.Key))}
			b.Context = schema.TargetContext{Name: t.Name, Mode: "append", Costumes: []string{}, Backdrops: []string{}, Sounds: []string{}, SpriteNames: []string{}, CurrentProgram: "{}"}
		default:
			return nil, issue("TARGET_BINDING", path, "unknown target disposition")
		}
		if t.Disposition != "new" {
			identity := fmt.Sprintf("%t:%s", b.Context.IsStage, b.Context.Name)
			if existingRefs[identity] {
				return nil, issue("TARGET_AMBIGUOUS", path, "one snapshot target cannot have multiple handles")
			}
			existingRefs[identity] = true
		}
		b.Context.Mode = b.Mode
		c.Targets = append(c.Targets, b)
	}
	if options.Scope == "single_sprite" && (len(c.Targets) != 1 || c.Targets[0].Disposition != "current") {
		return nil, issue("SCOPE", "/targets", "single_sprite scope requires only the current target")
	}
	stateNames := map[string]bool{}
	for i, s := range spec.States {
		path := fmt.Sprintf("/states/%d", i)
		if err := key(s.Key, path+"/key"); err != nil {
			return nil, err
		}
		if stateFor(c, s.Key) != nil {
			return nil, issue("DUPLICATE_SYMBOL", path, "state key is duplicated")
		}
		if err := short(s.Name, path+"/name"); err != nil {
			return nil, err
		}
		if s.Kind != "variable" && s.Kind != "list" && s.Kind != "broadcast" {
			return nil, issue("STATE_KIND", path, "unknown state kind")
		}
		if s.Scope != "local" && s.Scope != "global" {
			return nil, issue("SCOPE", path, "state scope is required")
		}
		if s.Scope == "local" {
			owner := targetFor(c, s.Owner)
			if owner == nil || owner.Kind != "sprite" {
				return nil, issue("STATE_OWNER", path, "local state requires a declared sprite owner")
			}
		} else if s.Owner != "" {
			return nil, issue("STATE_OWNER", path, "global state cannot have an owner")
		}
		if s.Kind == "broadcast" && (s.Scope != "global" || s.Initial != nil) {
			return nil, issue("STATE_KIND", path, "broadcasts must be global and cannot have an initial value")
		}
		identity := s.Kind + ":" + s.Scope + ":" + s.Owner + ":" + strings.ToLower(s.Name)
		if stateNames[identity] {
			return nil, issue("STATE_AMBIGUOUS", path, "state names must be unique within their type and scope")
		}
		stateNames[identity] = true
		b := StateBinding{Handle: s.Key, RefID: stableID("state_", s.Key+":"+s.Scope+":"+s.Owner), Name: s.Name, Kind: s.Kind, Scope: s.Scope, Owner: s.Owner, Disposition: s.Disposition, Value: s.Initial}
		switch s.Disposition {
		case "new":
			if b.Value == nil {
				if s.Kind == "variable" {
					b.Value = 0
				} else if s.Kind == "list" {
					b.Value = []any{}
				}
			}
			for _, existing := range options.ExistingStates {
				if sameState(existing, b) {
					return nil, issue("STATE_ALREADY_EXISTS", path, "new state conflicts with an existing snapshot state; declare reuse")
				}
			}
		case "reuse":
			if s.Initial != nil {
				return nil, issue("STATE_REUSE", path, "reuse preserves the snapshot value; initial must be omitted")
			}
			matches := 0
			for _, existing := range options.ExistingStates {
				if sameState(existing, b) {
					matches++
					b.Value = existing.Value
				}
			}
			if matches != 1 {
				return nil, issue("STATE_AMBIGUOUS", path, "reuse must match exactly one trusted snapshot state")
			}
		default:
			return nil, issue("STATE_KIND", path, "state disposition must be new or reuse")
		}
		if err := validateStateValue(b, path); err != nil {
			return nil, err
		}
		c.States = append(c.States, b)
	}
	for i, a := range spec.Assets {
		path := fmt.Sprintf("/assets/%d", i)
		if err := key(a.Key, path+"/key"); err != nil {
			return nil, err
		}
		if assetFor(c, a.Key) != nil || targetFor(c, a.Key) != nil {
			return nil, issue("DUPLICATE_SYMBOL", path, "asset key is duplicated")
		}
		owner := targetFor(c, a.Owner)
		if owner == nil {
			return nil, issue("ASSET_OWNER", path, "asset owner is not declared")
		}
		if a.Kind != "sprite" && a.Kind != "costume" && a.Kind != "backdrop" && a.Kind != "sound" {
			return nil, issue("ASSET_KIND", path, "unknown resource kind")
		}
		if a.Kind == "backdrop" && owner.Kind != "stage" && !(options.Scope == "single_sprite" && a.Disposition == "reuse") {
			return nil, issue("ASSET_OWNER", path, "backdrops must belong to the stage")
		}
		if (a.Kind == "costume" || (a.Kind == "sprite" && a.Disposition == "new")) && owner.Kind != "sprite" {
			return nil, issue("ASSET_OWNER", path, "sprite/costume resources require a sprite owner")
		}
		if err := short(a.Name, path+"/name"); err != nil {
			return nil, err
		}
		if err := short(a.Purpose, path+"/purpose"); err != nil {
			return nil, err
		}
		b := AssetBinding{Handle: a.Key, Owner: a.Owner, Kind: a.Kind, Name: a.Name, Disposition: a.Disposition}
		switch a.Disposition {
		case "reuse":
			if a.Source != nil {
				return nil, issue("ASSET_SOURCE", path, "reused assets cannot request a new source")
			}
			if !assetExists(owner, a, options.ExistingAssets) {
				return nil, issue("ASSET_UNKNOWN", path, "reused asset is absent or ambiguous in the trusted snapshot")
			}
		case "new":
			if options.Scope != "project" {
				return nil, issue("SCOPE", path, "new resources require project scope")
			}
			if err := uniqueStrings(a.SearchTerms, path+"/search_terms", 12); err != nil {
				return nil, err
			}
			if len(a.SearchTerms) == 0 && a.Source == nil {
				return nil, issue("ASSET_SOURCE", path, "new resource requires search terms or a fully specified source")
			}
			b.NeedID = stableID("need_", a.Key)
			b.OpID = stableID("op_", "asset:"+a.Key)
			if a.Kind == "sprite" {
				if owner.Disposition != "new" || a.Name != owner.Name {
					return nil, issue("ASSET_OWNER", path, "new sprite asset must create its new owner with the same name")
				}
				b.OpID = *owner.Ref.OpID
			}
			if a.X != nil && (*a.X < -10000 || *a.X > 10000) || a.Y != nil && (*a.Y < -10000 || *a.Y > 10000) || a.Size != nil && (*a.Size < 1 || *a.Size > 1000) {
				return nil, issue("ASSET_SHAPE", path, "resource layout exceeds the supported range")
			}
		default:
			return nil, issue("ASSET_KIND", path, "resource disposition must be new or reuse")
		}
		c.Assets = append(c.Assets, b)
	}
	for _, t := range c.Targets {
		if t.Disposition == "new" {
			n := 0
			for _, a := range c.Assets {
				if a.Owner == t.Handle && a.Kind == "sprite" && a.Disposition == "new" {
					n++
				}
			}
			if n != 1 {
				return nil, issue("ASSET_BINDING", "/assets", "each new sprite requires exactly one sprite resource")
			}
		}
	}
	slotsPerTarget := map[string]int{}
	edits := map[string]bool{}
	for i, s := range spec.ScriptSlots {
		path := fmt.Sprintf("/script_slots/%d", i)
		if err := key(s.Key, path+"/key"); err != nil {
			return nil, err
		}
		if slotFor(c, s.Key) != nil {
			return nil, issue("DUPLICATE_SYMBOL", path, "slot key is duplicated")
		}
		owner := targetFor(c, s.Owner)
		if owner == nil {
			return nil, issue("SLOT_OWNER", path, "slot owner is not declared")
		}
		slotsPerTarget[s.Owner]++
		if slotsPerTarget[s.Owner] > options.Limits.defaults().MaxSlots {
			return nil, issue("BUDGET", path, "target script budget exceeded")
		}
		if err := short(s.Purpose, path+"/purpose"); err != nil {
			return nil, err
		}
		for label, refs := range map[string][]string{"reads": s.Reads, "writes": s.Writes, "emits": s.Emits, "receives": s.Receives} {
			if err := uniqueStrings(refs, path+"/"+label, 150); err != nil {
				return nil, err
			}
			for _, handle := range refs {
				state := stateFor(c, handle)
				if state == nil {
					return nil, issue("STATE_UNKNOWN", path+"/"+label, "undeclared state: "+handle)
				}
				if state.Scope == "local" && state.Owner != s.Owner {
					return nil, issue("STATE_SCOPE", path+"/"+label, "local state belongs to another target")
				}
				broadcast := label == "emits" || label == "receives"
				if broadcast != (state.Kind == "broadcast") {
					return nil, issue("STATE_KIND", path+"/"+label, "broadcast and data access interfaces must be separate")
				}
			}
		}
		if err := uniqueStrings(s.Assets, path+"/assets", 36); err != nil {
			return nil, err
		}
		for _, handle := range s.Assets {
			if assetFor(c, handle) == nil && targetFor(c, handle) == nil {
				return nil, issue("ASSET_UNKNOWN", path+"/assets", "undeclared asset or target: "+handle)
			}
		}
		b := FrozenSlot{IntentSlot: s, ScriptID: stableID("script_", s.Key)}
		b.Reads = empty(s.Reads)
		b.Writes = empty(s.Writes)
		b.Emits = empty(s.Emits)
		b.Receives = empty(s.Receives)
		b.Assets = empty(s.Assets)
		if s.EditRef != "" {
			if owner.Mode != "modify" {
				return nil, issue("EDIT_NOT_ALLOWED", path+"/edit_ref", "only modify mode accepts edit_ref")
			}
			matches := 0
			for _, e := range options.ExistingScriptRefs {
				if e.Ref == s.EditRef && e.Target == s.Owner && e.BaseSnapshotHash == c.BaseSnapshotHash && !e.Protected {
					matches++
					b.RemoveScriptID = e.ScriptID
				}
			}
			if matches != 1 || !strings.HasPrefix(b.RemoveScriptID, "script_") {
				return nil, issue("EDIT_NOT_ALLOWED", path+"/edit_ref", "edit_ref is absent, protected, or bound to another snapshot")
			}
			if edits[b.RemoveScriptID] {
				return nil, issue("EDIT_DUPLICATE", path+"/edit_ref", "a script can be replaced by only one slot")
			}
			edits[b.RemoveScriptID] = true
		}
		c.ScriptSlots = append(c.ScriptSlots, b)
		if _, err := hatFor(c, &c.ScriptSlots[len(c.ScriptSlots)-1]); err != nil {
			return nil, err
		}
	}

	if err := validateModifySelections(c); err != nil {
		return nil, err
	}
	// Deep-copy all model and snapshot values before hashing the frozen artifact.
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, issue("SHAPE", "/", err.Error())
	}
	if len(raw) > 1024*1024 {
		return nil, issue("BUDGET", "/", "frozen artifact exceeds 1 MiB")
	}
	var frozen FrozenContract
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	frozen.Hash, err = contractFingerprint(&frozen)
	if err != nil {
		return nil, err
	}
	if _, err := DeriveLegacy(&frozen); err != nil {
		return nil, err
	}
	return &frozen, nil
}

func sameState(a, b StateBinding) bool {
	return a.Name == b.Name && a.Kind == b.Kind && a.Scope == b.Scope && a.Owner == b.Owner
}
func validateStateValue(s StateBinding, path string) error {
	if s.Disposition == "reuse" && s.Value == nil {
		return nil
	}
	if s.Kind == "broadcast" {
		if s.Value != nil {
			return issue("STATE_VALUE", path, "broadcast has no value")
		}
		return nil
	}
	if s.Kind == "list" {
		list, ok := s.Value.([]any)
		if !ok {
			return issue("STATE_VALUE", path, "list value must be an array")
		}
		if len(list) > 500 {
			return issue("BUDGET", path, "list exceeds 500 values")
		}
		for _, v := range list {
			if !scalar(v) {
				return issue("STATE_VALUE", path, "list items must be finite scalar values")
			}
		}
		return nil
	}
	if !scalar(s.Value) {
		return issue("STATE_VALUE", path, "variable value must be a finite scalar")
	}
	return nil
}
func scalar(v any) bool {
	switch t := v.(type) {
	case string:
		return utf8.RuneCountInString(t) <= schema.MaxStringLength
	case bool, int, int64:
		return true
	case float64:
		return !math.IsInf(t, 0) && !math.IsNaN(t)
	default:
		return false
	}
}
func assetExists(owner *TargetBinding, a IntentAsset, existing []AssetBinding) bool {
	if a.Kind == "sprite" {
		count := 0
		for _, name := range owner.Context.SpriteNames {
			if name == a.Name {
				count++
			}
		}
		if count == 1 {
			return true
		}
		return count == 0 && owner.Disposition != "new" && a.Name == owner.Name
	}
	values := owner.Context.Costumes
	if a.Kind == "backdrop" {
		values = owner.Context.Backdrops
	}
	if a.Kind == "sound" {
		values = owner.Context.Sounds
	}
	count := 0
	for _, name := range values {
		if name == a.Name {
			count++
		}
	}
	if count == 1 {
		return true
	}
	for _, e := range existing {
		if e.Owner == a.Owner && e.Kind == a.Kind && e.Name == a.Name {
			count++
		}
	}
	return count == 1
}

func DecodeFrozen(raw []byte, limits ...Limits) (*FrozenContract, error) {
	l := Limits{MaxBytes: 1024 * 1024}
	if len(limits) > 0 {
		l = limits[0]
	}
	var c FrozenContract
	if err := decodeBounded(raw, &c, l); err != nil {
		return nil, err
	}
	if err := ValidateFrozen(&c); err != nil {
		return nil, err
	}
	return &c, nil
}
func ValidateFrozen(c *FrozenContract) error {
	if c == nil {
		return issue("SHAPE", "/", "frozen contract is required")
	}
	if c.PipelineVersion != PipelineVersion || c.NormalizerVersion != NormalizerVersion || c.InterfaceVersion != 1 {
		return issue("VERSION", "/pipeline_version", "unsupported frozen authoring version")
	}
	if c.CatalogHash != CatalogFingerprint() {
		return issue("CATALOG_VERSION", "/catalog_hash", "the pinned catalog is unavailable")
	}
	if !shaPattern.MatchString(c.BaseSnapshotHash) {
		return issue("SNAPSHOT", "/base_snapshot_hash", "invalid snapshot hash")
	}
	if !shaPattern.MatchString(c.RequestBindingHash) {
		return issue("REQUEST_BINDING", "/request_binding_hash", "invalid request identity hash")
	}
	if c.ExpiresAtUnix < 1 {
		return issue("EXPIRY", "/expires_at_unix", "invalid contract expiry")
	}
	hash, err := contractFingerprint(c)
	if err != nil || hash != c.Hash {
		return issue("ARTIFACT_HASH", "/hash", "frozen artifact was modified")
	}
	if c.Scope != "project" && c.Scope != "single_sprite" {
		return issue("SCOPE", "/scope", "unknown frozen scope")
	}
	if c.Mode != "append" && c.Mode != "modify" && c.Mode != "rewrite" {
		return issue("MODE", "/mode", "unknown frozen mode")
	}
	if len(c.Targets) < 1 || len(c.Targets) > 12 || len(c.ScriptSlots) < 1 || len(c.ScriptSlots) > 80 || len(c.States) > 150 || len(c.Assets) > 24 || len(c.AssetIntents) != len(c.Assets) {
		return issue("BUDGET", "/", "frozen artifact budget exceeded")
	}
	if err := short(c.Title, "/title"); err != nil {
		return err
	}
	if len(c.Goal) == 0 || utf8.RuneCountInString(c.Goal) > 2000 {
		return issue("SHAPE", "/goal", "invalid frozen goal")
	}
	if err := uniqueStrings(c.RequiredExtensions, "/required_extensions", 3); err != nil {
		return err
	}
	for _, ext := range c.RequiredExtensions {
		if ext != "pen" && ext != "music" && ext != "text2speech" {
			return issue("EXTENSION_NOT_ALLOWED", "/required_extensions", "unknown extension")
		}
	}
	targets := map[string]bool{}
	states := map[string]bool{}
	assets := map[string]bool{}
	slots := map[string]bool{}
	refs := map[string]bool{}
	for _, t := range c.Targets {
		if err := key(t.Handle, "/targets/handle"); err != nil {
			return err
		}
		if targets[t.Handle] || t.EntityID != stableID("entity_", t.Handle) {
			return issue("TARGET_BINDING", "/targets", "duplicate handle or altered entity id")
		}
		targets[t.Handle] = true
		if t.Kind != "sprite" && t.Kind != "stage" {
			return issue("TARGET_KIND", "/targets", "unknown target kind")
		}
		if t.Mode != "append" && t.Mode != "modify" && t.Mode != "rewrite" {
			return issue("MODE", "/targets", "unknown target mode")
		}
		if t.Disposition != "current" && t.Disposition != "existing" && t.Disposition != "new" {
			return issue("TARGET_BINDING", "/targets", "unknown disposition")
		}
		if t.Disposition != "current" && t.Mode != "append" {
			return issue("MODE", "/targets", "only current target can modify/rewrite")
		}
		if err := t.Ref.Validate(); err != nil {
			return err
		}
		if err := t.Context.Validate(); err != nil {
			return err
		}
		if t.Context.Name != t.Name || t.Context.IsStage != (t.Kind == "stage") || t.Context.Mode != t.Mode {
			return issue("TARGET_BINDING", "/targets", "target context disagrees with frozen binding")
		}
		if t.Disposition == "new" && (t.Kind != "sprite" || t.Ref.Kind != "created_sprite" || t.Ref.OpID == nil || *t.Ref.OpID != stableID("op_", "target:"+t.Handle)) {
			return issue("TARGET_BINDING", "/targets", "new sprite binding is invalid")
		}
	}
	if c.Scope == "single_sprite" && (len(c.Targets) != 1 || c.Targets[0].Disposition != "current") {
		return issue("SCOPE", "/targets", "single target artifact must bind only current target")
	}
	names := map[string]bool{}
	for _, s := range c.States {
		if err := key(s.Handle, "/states/handle"); err != nil {
			return err
		}
		if states[s.Handle] || refs[s.RefID] || s.RefID != stableID("state_", s.Handle+":"+s.Scope+":"+s.Owner) {
			return issue("STATE_BINDING", "/states", "duplicate or altered state identity")
		}
		states[s.Handle] = true
		refs[s.RefID] = true
		if s.Kind != "variable" && s.Kind != "list" && s.Kind != "broadcast" {
			return issue("STATE_KIND", "/states", "unknown state kind")
		}
		if s.Disposition != "new" && s.Disposition != "reuse" {
			return issue("STATE_KIND", "/states", "unknown state disposition")
		}
		if s.Scope == "local" {
			t := targetFor(c, s.Owner)
			if t == nil || t.Kind != "sprite" || s.Kind == "broadcast" {
				return issue("STATE_OWNER", "/states", "invalid local state owner")
			}
		} else if s.Scope != "global" || s.Owner != "" {
			return issue("STATE_SCOPE", "/states", "invalid global state scope")
		}
		if err := short(s.Name, "/states/name"); err != nil {
			return err
		}
		identity := s.Kind + ":" + s.Scope + ":" + s.Owner + ":" + s.Name
		if c.Scope == "single_sprite" {
			identity = s.Kind + ":" + s.Name
		}
		if names[identity] {
			return issue("STATE_AMBIGUOUS", "/states", "this output profile cannot distinguish duplicate state names")
		}
		names[identity] = true
		if err := validateStateValue(s, "/states"); err != nil {
			return err
		}
	}
	for _, a := range c.Assets {
		if err := key(a.Handle, "/assets/handle"); err != nil {
			return err
		}
		if assets[a.Handle] || targets[a.Handle] || !targets[a.Owner] {
			return issue("ASSET_BINDING", "/assets", "duplicate asset or unknown owner")
		}
		assets[a.Handle] = true
		if a.Kind != "sprite" && a.Kind != "costume" && a.Kind != "backdrop" && a.Kind != "sound" {
			return issue("ASSET_KIND", "/assets", "unknown resource kind")
		}
		if a.Disposition != "new" && a.Disposition != "reuse" {
			return issue("ASSET_BINDING", "/assets", "unknown resource disposition")
		}
		intent := assetIntent(c, a.Handle)
		if intent == nil || intent.Owner != a.Owner || intent.Kind != a.Kind || intent.Name != a.Name || intent.Disposition != a.Disposition {
			return issue("ASSET_BINDING", "/assets", "resource source and logical binding disagree")
		}
		if a.Disposition == "new" {
			if c.Scope != "project" || a.NeedID != stableID("need_", a.Handle) {
				return issue("ASSET_BINDING", "/assets", "new resource need identity changed")
			}
			op := stableID("op_", "asset:"+a.Handle)
			if a.Kind == "sprite" {
				target := targetFor(c, a.Owner)
				if target.Disposition != "new" {
					return issue("ASSET_BINDING", "/assets", "sprite resource does not create a new owner")
				}
				op = *target.Ref.OpID
			}
			if a.OpID != op {
				return issue("ASSET_BINDING", "/assets", "resource operation identity changed")
			}
		}
	}
	slotCounts := map[string]int{}
	edits := map[string]bool{}
	for _, s := range c.ScriptSlots {
		if err := key(s.Key, "/script_slots/key"); err != nil {
			return err
		}
		owner := targetFor(c, s.Owner)
		if slots[s.Key] || owner == nil || s.ScriptID != stableID("script_", s.Key) {
			return issue("SLOT_OWNER", "/script_slots", "duplicate slot, unknown owner or altered script id")
		}
		slots[s.Key] = true
		slotCounts[s.Owner]++
		if slotCounts[s.Owner] > schema.MaxScripts {
			return issue("BUDGET", "/script_slots", "target has too many slots")
		}
		if err := short(s.Purpose, "/script_slots/purpose"); err != nil {
			return err
		}
		for label, handles := range map[string][]string{"reads": s.Reads, "writes": s.Writes, "emits": s.Emits, "receives": s.Receives} {
			if err := uniqueStrings(handles, "/script_slots/"+label, 150); err != nil {
				return err
			}
			for _, h := range handles {
				state := stateFor(c, h)
				if state == nil {
					return issue("STATE_UNKNOWN", "/script_slots/"+label, "unknown state handle")
				}
				if state.Scope == "local" && state.Owner != s.Owner {
					return issue("STATE_SCOPE", "/script_slots/"+label, "foreign local state")
				}
				if (label == "emits" || label == "receives") != (state.Kind == "broadcast") {
					return issue("STATE_KIND", "/script_slots/"+label, "wrong state kind for access")
				}
			}
		}
		if err := uniqueStrings(s.Assets, "/script_slots/assets", 36); err != nil {
			return err
		}
		for _, h := range s.Assets {
			if !assets[h] && !targets[h] {
				return issue("ASSET_UNKNOWN", "/script_slots/assets", "unknown resource handle")
			}
		}
		if s.RemoveScriptID != "" || s.EditRef != "" {
			if owner.Mode != "modify" || s.EditRef == "" || s.RemoveScriptID == "" || edits[s.RemoveScriptID] || !strings.HasPrefix(s.RemoveScriptID, "script_") {
				return issue("EDIT_NOT_ALLOWED", "/script_slots/edit_ref", "invalid or repeated authorized edit")
			}
			edits[s.RemoveScriptID] = true
		}
		if _, err := hatFor(c, &s); err != nil {
			return err
		}
	}
	return validateModifySelections(c)
}

func validateModifySelections(c *FrozenContract) error {
	for _, target := range c.Targets {
		if target.Mode != "modify" {
			continue
		}
		hasSlot, hasEdit := false, false
		for _, slot := range c.ScriptSlots {
			if slot.Owner == target.Handle {
				hasSlot = true
				hasEdit = hasEdit || slot.EditRef != ""
			}
		}
		if hasSlot && !hasEdit {
			return issue("EDIT_REF_REQUIRED", "/script_slots", "modify target "+target.Handle+" must select at least one authorized edit_ref")
		}
	}
	return nil
}

func ContextForTarget(c *FrozenContract, handle string) (*ProgramContext, error) {
	if err := ValidateFrozen(c); err != nil {
		return nil, err
	}
	target := targetFor(c, handle)
	if target == nil {
		return nil, issue("TARGET_UNKNOWN", "/target", "unknown target handle")
	}
	out := &ProgramContext{PipelineVersion: c.PipelineVersion, ContractHash: c.Hash, BaseSnapshotHash: c.BaseSnapshotHash, Target: promptTarget(c, *target, true), Slots: []FrozenSlot{}, States: []StateBinding{}, Assets: []AssetBinding{}, Targets: []TargetBinding{}, RequiredExtensions: c.RequiredExtensions}
	states := map[string]bool{}
	assets := map[string]bool{}
	for _, slot := range c.ScriptSlots {
		if slot.Owner == handle {
			out.Slots = append(out.Slots, slot)
			for _, h := range append(append(append(append([]string{}, slot.Reads...), slot.Writes...), slot.Emits...), slot.Receives...) {
				states[h] = true
			}
			for _, h := range slot.Assets {
				assets[h] = true
			}
			if slot.Trigger.State != "" {
				states[slot.Trigger.State] = true
			}
			if slot.Trigger.Asset != "" {
				assets[slot.Trigger.Asset] = true
			}
		}
	}
	for _, state := range c.States {
		if states[state.Handle] {
			out.States = append(out.States, state)
		}
	}
	for _, asset := range c.Assets {
		if assets[asset.Handle] {
			out.Assets = append(out.Assets, asset)
		}
	}
	for _, t := range c.Targets {
		if assets[t.Handle] {
			out.Targets = append(out.Targets, promptTarget(c, t, false))
		}
	}
	return out, nil
}

// EnvelopeHash hashes the complete JSON artifact, preserving explicit nulls.
// It differs from legacy contract hashes which intentionally omit None values.
func EnvelopeHash(value any) (string, error) { return fingerprint(value) }
