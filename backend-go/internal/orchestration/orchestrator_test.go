package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// ---------------------------------------------------------------------------
// Fake providers (the Go equivalent of the routed fake provider the Python
// tests build in tests/test_ai_blocks.py)
// ---------------------------------------------------------------------------

// cannedProvider returns canned tagged streams per route. It never touches the
// network.
type cannedProvider struct {
	name       string
	routes     map[string][]string
	route      func(callIndex int, lastMessage string) string
	structured map[string]bool
	jsonSchema map[string]bool
	usage      map[string]int
	failWith   error

	mu          sync.Mutex
	calls       [][]provider.ChatMessage
	jsonFlags   []bool
	jsonSchemas []map[string]any
}

func newCannedProvider(routes map[string][]string) *cannedProvider {
	return &cannedProvider{name: "mock", routes: routes}
}

func (p *cannedProvider) ProviderName() string { return p.name }

func (p *cannedProvider) SupportsStructuredOutput(segment string) bool {
	return p.structured[segment]
}

func (p *cannedProvider) SupportsJSONSchema(segment string) bool {
	return p.jsonSchema[segment]
}

func (p *cannedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *cannedProvider) callMessages(index int) []provider.ChatMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.calls) {
		return nil
	}
	return p.calls[index]
}

func (p *cannedProvider) flags() ([]bool, []map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool{}, p.jsonFlags...), append([]map[string]any{}, p.jsonSchemas...)
}

func (p *cannedProvider) StreamWithReasoning(
	ctx context.Context,
	messages []provider.ChatMessage,
	thinkingEnabled bool,
	thinkingStrength string,
	jsonOutput bool,
	jsonSchema map[string]any,
) (<-chan provider.StreamDelta, <-chan error) {
	p.mu.Lock()
	p.calls = append(p.calls, messages)
	callIndex := len(p.calls)
	last := ""
	if len(messages) > 0 {
		last = messages[len(messages)-1].Content
	}
	key := "default"
	if p.route != nil {
		key = p.route(callIndex, last)
	}
	output := ""
	if values, present := p.routes[key]; present {
		if len(values) == 1 {
			output = values[0]
		} else if len(values) > 1 {
			output = values[0]
			p.routes[key] = values[1:]
		}
	}
	p.jsonFlags = append(p.jsonFlags, jsonOutput)
	p.jsonSchemas = append(p.jsonSchemas, jsonSchema)
	usage := p.usage
	failWith := p.failWith
	p.mu.Unlock()

	deltas := make(chan provider.StreamDelta, 16)
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		defer close(deltas)
		if failWith != nil {
			errs <- failWith
			return
		}
		if usage != nil {
			select {
			case deltas <- provider.StreamDelta{Kind: provider.DeltaUsage, Usage: usage}:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if jsonOutput {
			output = taggedBody(output)
		}
		for _, chunk := range chunkRunes(output, 7) {
			select {
			case deltas <- provider.StreamDelta{Kind: provider.DeltaContent, Text: chunk}:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
	}()
	return deltas, errs
}

// taggedBody mirrors the Python fake's <tag>...</tag> extraction.
func taggedBody(output string) string {
	start := strings.Index(output, ">")
	if start < 0 {
		return output
	}
	rest := output[start+1:]
	end := strings.Index(rest, "</")
	if end < 0 {
		return output
	}
	return rest[:end]
}

func chunkRunes(value string, size int) []string {
	runes := []rune(value)
	chunks := make([]string, 0, len(runes)/size+1)
	for index := 0; index < len(runes); index += size {
		end := index + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[index:end]))
	}
	return chunks
}

// ---------------------------------------------------------------------------
// Fixtures and helpers
// ---------------------------------------------------------------------------

func fixedBuilder() *prompt.PromptBuilder {
	knowledge := "fixed knowledge"
	return prompt.NewPromptBuilder(nil, &knowledge, nil, false)
}

func singleSpriteRequest() *schema.UnifiedGenerationRequest {
	return &schema.UnifiedGenerationRequest{
		ModelTier: schema.ModelTierNormal,
		SessionID: "session-1",
		RequestID: "request-1",
		Messages:  []schema.ChatMessage{{Role: "user", Content: "make the sprite move"}},
		Target: schema.TargetContext{
			Name: "Sprite1", Mode: "append", Costumes: []string{"costume1"}, SpriteNames: []string{"Sprite1"},
		},
		Thinking:            schema.ThinkingSettings{Enabled: false, Strength: schema.ThinkingStrengthLow},
		Scope:               "single_sprite",
		AssetPolicy:         "builtin_first",
		ProceedWithDefaults: true,
	}
}

func projectContextFixture() *schema.ProjectContext {
	return &schema.ProjectContext{
		ProjectID: "project-1",
		Version:   4,
		Targets: []schema.ProjectTargetContext{
			{
				Name: "Stage", IsStage: true, Mode: "append", Costumes: []string{"costume1"},
				SpriteNames: []string{"Sprite1"}, TargetID: "stage-1", Revision: 2,
			},
			{
				Name: "Sprite1", Mode: "append", Costumes: []string{"costume1"},
				SpriteNames: []string{"Sprite1"}, TargetID: "sprite-1", Revision: 3,
			},
		},
		SharedState: map[string]any{},
	}
}

func projectRequest() *schema.UnifiedGenerationRequest {
	request := singleSpriteRequest()
	request.Scope = "project"
	request.ProjectContext = projectContextFixture()
	return request
}

func loadGolden(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		t.Fatalf("parse golden %s: %v", name, err)
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("golden %s is not an object", name)
	}
	return object
}

func goldenFixtures(t *testing.T, golden map[string]any) map[string]string {
	t.Helper()
	raw, ok := golden["fixtures"].(map[string]any)
	if !ok {
		t.Fatalf("golden has no fixtures")
	}
	fixtures := map[string]string{}
	for key, value := range raw {
		text, _ := value.(string)
		fixtures[key] = text
	}
	return fixtures
}

// collectEvents runs one generator body and returns the emitted events.
func collectEvents(t *testing.T, run func(emit emitFunc) error) []GenerationEvent {
	t.Helper()
	events := []GenerationEvent{}
	if err := run(func(event GenerationEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("generator returned an error: %v", err)
	}
	return events
}

// normalizeEvents renders events the way the Python golden does: streaming
// deltas are dropped, cache metadata is reduced to the segment name (prompt
// cache keys belong to the prompt package) and absent phases are omitted.
func normalizeEvents(t *testing.T, events []GenerationEvent) []any {
	normalized := make([]any, 0, len(events))
	for _, event := range events {
		if event.Type == "content_delta" || event.Type == "reasoning_delta" {
			continue
		}
		data := map[string]any{}
		for key, value := range event.Data {
			if event.Type == "cache_hit" && key != "segment" {
				continue
			}
			data[key] = normalizeEmbeddedJSON(key, value)
		}
		entry := map[string]any{"type": event.Type, "request_id": event.RequestID, "data": data}
		if event.Phase != "" {
			entry["phase"] = event.Phase
		}
		normalized = append(normalized, entry)
	}
	return roundTrip(t, normalized)
}

func normalizeGoldenEvents(t *testing.T, golden map[string]any, key string) []any {
	t.Helper()
	raw, ok := golden[key].([]any)
	if !ok {
		t.Fatalf("golden %s is not a list", key)
	}
	normalized := make([]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("golden %s entry is not an object", key)
		}
		eventType, _ := entry["type"].(string)
		if eventType == "content_delta" || eventType == "reasoning_delta" {
			continue
		}
		data, _ := entry["data"].(map[string]any)
		reduced := map[string]any{}
		for dataKey, value := range data {
			if eventType == "cache_hit" && dataKey != "segment" {
				continue
			}
			reduced[dataKey] = normalizeEmbeddedJSON(dataKey, value)
		}
		cleaned := map[string]any{"type": eventType, "request_id": entry["request_id"], "data": reduced}
		if phase, present := entry["phase"]; present && phase != nil {
			cleaned["phase"] = phase
		}
		normalized = append(normalized, cleaned)
	}
	return roundTrip(t, normalized)
}

// normalizeEmbeddedJSON parses the compact-JSON "detail" diagnostic so the
// comparison does not depend on Go map key order inside that string.
func normalizeEmbeddedJSON(key string, value any) any {
	if key != "detail" {
		return value
	}
	text, ok := value.(string)
	if !ok || !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return value
	}
	parsed, err := jsonx.Parse([]byte(text))
	if err != nil {
		return value
	}
	return parsed
}

// roundTrip normalizes numbers so Go ints and decoded JSON numbers compare.
func roundTrip(t *testing.T, value any) []any {
	t.Helper()
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatalf("marshal normalized events: %v", err)
	}
	parsed, err := jsonx.Parse(encoded)
	if err != nil {
		t.Fatalf("parse normalized events: %v", err)
	}
	list, ok := parsed.([]any)
	if !ok {
		t.Fatalf("normalized events are not a list")
	}
	return list
}

func assertEventsMatchGolden(t *testing.T, events []GenerationEvent, golden map[string]any, key string) {
	t.Helper()
	got := normalizeEvents(t, events)
	want := normalizeGoldenEvents(t, golden, key)
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := jsonx.Marshal(got)
		wantJSON, _ := jsonx.Marshal(want)
		t.Fatalf("event stream mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func eventTypes(events []GenerationEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		if event.Type == "content_delta" || event.Type == "reasoning_delta" {
			continue
		}
		types = append(types, event.Type)
	}
	return types
}

func findEvent(events []GenerationEvent, eventType string) *GenerationEvent {
	for index := range events {
		if events[index].Type == eventType {
			return &events[index]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Golden stream tests
// ---------------------------------------------------------------------------

func TestTaggedSingleFlowMatchesPythonGolden(t *testing.T) {
	golden := loadGolden(t, "golden_single.json")
	fixtures := goldenFixtures(t, golden)
	p := newCannedProvider(map[string][]string{
		"default": {fixtures["requirements"] + fixtures["plan"] + fixtures["program"]},
	})
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(singleSpriteRequest()), p, emit)
	})

	assertEventsMatchGolden(t, events, golden, "tagged")
	if p.callCount() != 1 {
		t.Fatalf("tagged flow must use one provider call, got %d", p.callCount())
	}
	complete := findEvent(events, "complete")
	if complete == nil || complete.Data["program_count"] != 1 {
		t.Fatalf("complete event missing or wrong: %+v", complete)
	}
}

func TestSegmentedSingleFlowMatchesPythonGolden(t *testing.T) {
	golden := loadGolden(t, "golden_single.json")
	fixtures := goldenFixtures(t, golden)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"plan":         {fixtures["plan"]},
		"program":      {fixtures["program"]},
	})
	p.route = func(callIndex int, _ string) string {
		switch callIndex {
		case 1:
			return "requirements"
		case 2:
			return "plan"
		default:
			return "program"
		}
	}
	p.structured = map[string]bool{"requirements": true, "plan": true}
	p.jsonSchema = map[string]bool{"requirements": true, "plan": true}
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(singleSpriteRequest()), p, emit)
	})

	assertEventsMatchGolden(t, events, golden, "segmented")
	if p.callCount() != 3 {
		t.Fatalf("segmented single flow must use three provider calls, got %d", p.callCount())
	}
	flags, schemas := p.flags()
	if !reflect.DeepEqual(flags, []bool{true, true, false}) {
		t.Fatalf("json_output flags = %v, want [true true false]", flags)
	}
	if schemas[0] == nil || schemas[1] == nil {
		t.Fatalf("root segments must receive a JSON schema")
	}
	if schemas[2] != nil {
		t.Fatalf("the tagged program segment must not receive a JSON schema")
	}
}

func TestProjectFlowMatchesPythonGolden(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {fixtures["manifest"]},
		"program":      {fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	assertEventsMatchGolden(t, events, golden, "events")
	if p.callCount() != 6 {
		t.Fatalf("project flow must use six provider calls, got %d", p.callCount())
	}
	completed := []int{}
	for _, event := range events {
		if event.Type == "phase_complete" && event.Phase == "program" {
			if index, ok := jsonx.Int(event.Data["target_index"]); ok {
				completed = append(completed, int(index))
			}
		}
	}
	if !reflect.DeepEqual(completed, []int{0}) {
		t.Fatalf("program phase_complete target indices = %v, want [0]", completed)
	}
}

func TestResourceOpRepairMatchesPythonGolden(t *testing.T) {
	golden := loadGolden(t, "golden_resource_op.json")
	fixtures := goldenFixtures(t, golden)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {fixtures["manifest"]},
		"resource_op":  {fixtures["resource_op"]},
		"program":      {fixtures["program"]},
	})
	p.route = func(callIndex int, lastMessage string) string {
		if strings.Contains(lastMessage, "resource_op_index=") {
			return "resource_op"
		}
		if strings.Contains(lastMessage, "target_index=0 的完整") {
			return "program"
		}
		return projectRoute(callIndex, lastMessage)
	}
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	assertEventsMatchGolden(t, events, golden, "events")
	repair := findEvent(events, "repair")
	if repair == nil {
		t.Fatalf("resource op repair event missing")
	}
	if repair.Data["repair_scope"] != "resource_op" || repair.Data["code"] != "INVALID_BACKDROP_SOURCE" {
		t.Fatalf("unexpected repair event: %+v", repair.Data)
	}
	if repair.Data["path"] != "/resource_ops/0/source/asset_type" {
		t.Fatalf("repair path = %v", repair.Data["path"])
	}
	if repair.Data["operation"] != "add_backdrop" {
		t.Fatalf("repair operation = %v", repair.Data["operation"])
	}
	if p.callCount() != 7 {
		t.Fatalf("resource op repair must use seven provider calls, got %d", p.callCount())
	}
}

func projectRoute(callIndex int, lastMessage string) string {
	// Program segments (tagged and JSON-object transports) both name ProgramIR
	// in their segment instruction; manifest repair retries keep the manifest
	// route.
	if strings.Contains(lastMessage, "ProgramIR") || strings.Contains(lastMessage, "target_index=0 的完整") {
		return "program"
	}
	switch callIndex {
	case 1:
		return "requirements"
	case 2:
		return "task_spec"
	case 3:
		return "blueprint"
	case 4:
		return "plan"
	default:
		return "manifest"
	}
}

// ---------------------------------------------------------------------------
// Event-shape tests ported from tests/test_ai_blocks.py
// ---------------------------------------------------------------------------

func TestOrchestratorForwardsProviderUsageEvent(t *testing.T) {
	golden := loadGolden(t, "golden_single.json")
	fixtures := goldenFixtures(t, golden)
	p := newCannedProvider(map[string][]string{
		"default": {fixtures["requirements"] + fixtures["plan"] + fixtures["program"]},
	})
	p.usage = map[string]int{
		"input_tokens": 120, "output_tokens": 40, "total_tokens": 160, "cached_input_tokens": 72,
	}
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(singleSpriteRequest()), p, emit)
	})

	usage := findEvent(events, "usage")
	if usage == nil {
		t.Fatalf("usage event missing")
	}
	want := map[string]any{
		"input_tokens": 120, "output_tokens": 40, "total_tokens": 160, "cached_input_tokens": 72,
	}
	if !reflect.DeepEqual(roundTripOnce(t, usage.Data), roundTripOnce(t, want)) {
		t.Fatalf("usage data = %v, want %v", usage.Data, want)
	}
}

func roundTripOnce(t *testing.T, value any) any {
	t.Helper()
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := jsonx.Parse(encoded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return parsed
}

func TestTaggedRejectsPlainJSONWithoutLegacyFallback(t *testing.T) {
	p := newCannedProvider(map[string][]string{"default": {`{"schema_version": 1}`}})
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(singleSpriteRequest()), p, emit)
	})

	if len(events) == 0 {
		t.Fatalf("expected a terminal error event")
	}
	last := events[len(events)-1]
	if last.Type != "error" || last.Data["code"] != "OUTPUT_PROTOCOL_ERROR" {
		t.Fatalf("terminal event = %s %v", last.Type, last.Data)
	}
	if findEvent(events, "validation_error") != nil {
		t.Fatalf("plain JSON must not produce a validation_error event")
	}
	if p.callCount() != 1 {
		t.Fatalf("protocol failure must not retry, calls = %d", p.callCount())
	}
}

func TestManifestModeViolationRepairsBeforeProgram(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	badManifest := strings.Replace(fixtures["manifest"], `"mode": "append"`, `"mode": "modify"`, 1)
	if badManifest == fixtures["manifest"] {
		t.Fatalf("fixture did not contain an append mode to corrupt")
	}
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {badManifest, fixtures["manifest"]},
		"program":      {fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 1, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	repair := findEvent(events, "repair")
	if repair == nil {
		t.Fatalf("repair event missing: %v", eventTypes(events))
	}
	if repair.Data["attempt"] != 1 || repair.Data["resume"] != "manifest" {
		t.Fatalf("repair data = %v", repair.Data)
	}
	detail, _ := repair.Data["detail"].(string)
	if !strings.Contains(detail, "must use append") {
		t.Fatalf("repair detail = %q", detail)
	}
	last := events[len(events)-1]
	if last.Type != "complete" || last.Data["program_count"] != 2 && last.Data["program_count"] != 1 {
		t.Fatalf("terminal event = %s %v", last.Type, last.Data)
	}
	if p.callCount() != 7 {
		t.Fatalf("expected 7 provider calls, got %d", p.callCount())
	}
	if findEvent(events, "validation_error") != nil {
		t.Fatalf("a successful repair must not surface a validation_error event")
	}
}

func TestManifestRepairExhaustionYieldsOneTerminalError(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	badManifest := strings.Replace(fixtures["manifest"], `"mode": "append"`, `"mode": "modify"`, 1)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {badManifest, badManifest},
		"program":      {fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 1, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	repairs := 0
	validations := 0
	for _, event := range events {
		switch event.Type {
		case "repair":
			repairs++
		case "validation_error":
			validations++
		}
	}
	if repairs != 1 {
		t.Fatalf("repair events = %d, want 1", repairs)
	}
	if validations != 1 {
		t.Fatalf("validation_error events = %d, want 1", validations)
	}
	if findEvent(events, "complete") != nil {
		t.Fatalf("exhausted repair must not emit complete")
	}
	last := events[len(events)-1]
	if last.Type != "error" || last.Data["code"] != "OUTPUT_VALIDATION_ERROR" || last.Data["message"] != "AI 输出未通过协议校验" {
		t.Fatalf("terminal event = %s %v", last.Type, last.Data)
	}
	if p.callCount() != 6 {
		t.Fatalf("expected 6 provider calls, got %d", p.callCount())
	}
}

func TestManifestUnsupportedSchemaVersionIsReported(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	badManifest := strings.Replace(fixtures["manifest"], `"schema_version": 3`, `"schema_version": 1`, 1)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {badManifest, badManifest},
		"program":      {fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 0, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	validation := findEvent(events, "validation_error")
	if validation == nil {
		t.Fatalf("validation_error missing: %v", eventTypes(events))
	}
	if validation.Data["code"] != "UNSUPPORTED_SCHEMA_VERSION" || validation.Data["path"] != "/schema_version" {
		t.Fatalf("validation data = %v", validation.Data)
	}
	if validation.Phase != "plan" {
		t.Fatalf("manifest validation phase = %q, want plan", validation.Phase)
	}
}

func TestProjectManifestOutputContractStopsAfterManifest(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {fixtures["manifest"]},
		"program":      {fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	request := projectRequest()
	view := viewOfUnified(request)
	view.outputContract = "project_manifest"

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), view, p, emit)
	})

	last := events[len(events)-1]
	if last.Type != "complete" || last.Data["output_contract"] != "project_manifest" || last.Data["program_count"] != 0 {
		t.Fatalf("terminal event = %s %v", last.Type, last.Data)
	}
	if p.callCount() != 5 {
		t.Fatalf("project_manifest must stop after the manifest, calls = %d", p.callCount())
	}
}

func TestRequirementsIncompleteStopsSegmentedProject(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	incomplete := strings.Replace(fixtures["requirements"], `"ready": true`, `"ready": false`, 1)
	p := newCannedProvider(map[string][]string{"requirements": {incomplete}})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	request := projectRequest()
	request.ProceedWithDefaults = false

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(request), p, emit)
	})

	last := events[len(events)-1]
	if last.Type != "error" || last.Data["code"] != "REQUIREMENTS_INCOMPLETE" {
		t.Fatalf("terminal event = %s %v", last.Type, last.Data)
	}
	if p.callCount() != 1 {
		t.Fatalf("incomplete requirements must stop the flow, calls = %d", p.callCount())
	}
}

// ---------------------------------------------------------------------------
// generate_project_program
// ---------------------------------------------------------------------------

func TestGenerateProjectProgramStreamsValidatedProgram(t *testing.T) {
	golden := loadGolden(t, "golden_project_program.json")
	fixtures := goldenFixtures(t, golden)
	request := projectProgramRequest(t, fixtures)
	p := newCannedProvider(map[string][]string{"program": {fixtures["program"]}})
	p.route = func(int, string) string { return "program" }
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateProjectProgram(context.Background(), viewOfProjectProgram(request), request, p, emit)
	})

	if p.callCount() != 1 {
		t.Fatalf("generate_project_program must use one provider call, got %d", p.callCount())
	}
	assertEventsMatchGolden(t, events, golden, "events")
	types := eventTypes(events)
	want := []string{"phase_start", "cache_hit", "metadata", "shared_state", "script_ready", "phase_complete", "complete"}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("event types = %v, want %v", types, want)
	}
	last := events[len(events)-1]
	if last.Data["scope"] != "project" || last.Data["output_contract"] != "project_program" || last.Data["program_count"] != 1 {
		t.Fatalf("complete data = %v", last.Data)
	}
	if last.Data["target_index"] != 0 || last.Data["target_name"] != "Sprite1" {
		t.Fatalf("complete target data = %v", last.Data)
	}
}

func TestGenerateProjectProgramRejectsMismatchedTarget(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	request := projectProgramRequest(t, fixtures)
	request.Target.Name = "OtherSprite"
	p := newCannedProvider(map[string][]string{"program": {fixtures["program"]}})
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateProjectProgram(context.Background(), viewOfProjectProgram(request), request, p, emit)
	})

	last := events[len(events)-1]
	if last.Type != "error" || last.Data["code"] != "INVALID_PROJECT_PROGRAM_REQUEST" {
		t.Fatalf("terminal event = %s %v", last.Type, last.Data)
	}
	if p.callCount() != 0 {
		t.Fatalf("mismatched target must not call the provider")
	}
}

func projectProgramRequest(t *testing.T, fixtures map[string]string) *schema.ProjectProgramGenerationRequest {
	t.Helper()
	request := &schema.ProjectProgramGenerationRequest{
		ModelTier:           schema.ModelTierNormal,
		SessionID:           "session-1",
		RequestID:           "request-1",
		Messages:            []schema.ChatMessage{{Role: "user", Content: "make the project move"}},
		Target:              schema.TargetContext{Name: "Sprite1", Mode: "append", Costumes: []string{"costume1"}, SpriteNames: []string{"Sprite1"}, CurrentProgram: "{\"scripts\": []}"},
		Thinking:            schema.ThinkingSettings{Enabled: false, Strength: schema.ThinkingStrengthLow},
		Scope:               "project",
		AssetPolicy:         "builtin_first",
		ProceedWithDefaults: true,
		ProjectContext:      projectContextFixture(),
		OutputContract:      "project_program",
	}
	decodeInto(t, fixtures["requirements"], &request.Requirements)
	decodeInto(t, fixtures["task_spec"], &request.TaskSpec)
	decodeInto(t, fixtures["blueprint"], &request.Blueprint)
	decodeInto(t, fixtures["plan"], &request.Plan)
	decodeInto(t, fixtures["manifest"], &request.Manifest)
	request.ProgramTarget = request.Manifest.ProgramTargets[0]
	request.TargetIndex = 0
	return request
}

func decodeInto(t *testing.T, tagged string, target any) {
	t.Helper()
	body := taggedBody(tagged)
	if err := schema.DecodeStrict([]byte(body), target); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Public API plumbing
// ---------------------------------------------------------------------------

func TestGenerateUnifiedExposesProviderErrorsOnTheErrorChannel(t *testing.T) {
	settings := &config.Settings{}
	textProvider := provider.NewTextProvider(settings, "agnes")
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events, errs := orchestrator.GenerateUnified(context.Background(), singleSpriteRequest(), textProvider)

	collected := []GenerationEvent{}
	for event := range events {
		collected = append(collected, event)
	}
	var gotErr error
	for err := range errs {
		gotErr = err
	}
	if gotErr == nil {
		t.Fatalf("expected the provider error on the error channel")
	}
	var configurationErr *config.ConfigurationError
	if !errors.As(gotErr, &configurationErr) {
		t.Fatalf("error = %T %v, want *config.ConfigurationError", gotErr, gotErr)
	}
	if len(collected) != 1 || collected[0].Type != "cache_hit" {
		t.Fatalf("events = %v, want a single cache_hit before the provider failure", eventTypes(collected))
	}
}

// ---------------------------------------------------------------------------
// _to_chatgpt_strict_json_schema / _structured_output_schema
// ---------------------------------------------------------------------------

func TestStructuredOutputSchemasAreChatGPTCompatible(t *testing.T) {
	p := &cannedProvider{
		name: "gpt",
		jsonSchema: map[string]bool{
			"requirements": true, "task_spec": true, "blueprint": true, "plan": true,
			"manifest": true, "resource_op": true, "program": true,
		},
	}
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	// prompt.StrictJSONSchemas pre-generates exactly the four root segments the
	// real provider advertises through supports_json_schema.
	for _, segment := range []string{"requirements", "task_spec", "blueprint", "plan"} {
		schemaValue := orchestrator.structuredOutputSchema(p, segment)
		if schemaValue == nil {
			t.Fatalf("segment %s must expose a structured schema", segment)
		}
		if schemaValue["type"] != "object" {
			t.Fatalf("segment %s schema type = %v", segment, schemaValue["type"])
		}
		assertChatGPTCompatible(t, schemaValue)
		assertEveryPropertyRequired(t, schemaValue)
	}
	// ResourceOp is a root union, which Responses structured output rejects,
	// and the remaining segments have no pre-generated strict schema.
	for _, segment := range []string{"manifest", "resource_op", "program"} {
		if schema := orchestrator.structuredOutputSchema(p, segment); schema != nil {
			t.Fatalf("segment %s must not expose a structured schema", segment)
		}
	}
	taskSpec := orchestrator.structuredOutputSchema(p, "task_spec")
	if _, present := taskSpec["properties"].(map[string]any)["assumptions"]; !present {
		t.Fatalf("task_spec schema must describe assumptions")
	}
	requirements := orchestrator.structuredOutputSchema(p, "requirements")
	if _, present := requirements["properties"].(map[string]any)["missing_questions"]; !present {
		t.Fatalf("requirements schema must describe missing_questions")
	}
	// A provider without the JSON-schema capability never receives a schema.
	plain := &cannedProvider{name: "mock"}
	if schema := orchestrator.structuredOutputSchema(plain, "requirements"); schema != nil {
		t.Fatalf("a provider without supports_json_schema must not receive a schema")
	}
}

func assertChatGPTCompatible(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if _, present := typed["oneOf"]; present {
			t.Fatalf("schema still contains oneOf: %v", typed["title"])
		}
		if _, present := typed["discriminator"]; present {
			t.Fatalf("schema still contains discriminator: %v", typed["title"])
		}
		for _, child := range typed {
			assertChatGPTCompatible(t, child)
		}
	case []any:
		for _, child := range typed {
			assertChatGPTCompatible(t, child)
		}
	}
}

func assertEveryPropertyRequired(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if properties, present := typed["properties"].(map[string]any); present {
			required, _ := typed["required"].([]any)
			requiredSet := map[string]bool{}
			for _, item := range required {
				if name, ok := item.(string); ok {
					requiredSet[name] = true
				}
			}
			if len(requiredSet) != len(properties) {
				t.Fatalf("required %v does not cover properties of %v", required, typed["title"])
			}
			for name := range properties {
				if !requiredSet[name] {
					t.Fatalf("property %s missing from required of %v", name, typed["title"])
				}
			}
		}
		for _, child := range typed {
			assertEveryPropertyRequired(t, child)
		}
	case []any:
		for _, child := range typed {
			assertEveryPropertyRequired(t, child)
		}
	}
}

// ---------------------------------------------------------------------------
// Helper ports
// ---------------------------------------------------------------------------

func TestResourceOpIndexAndPayloadAt(t *testing.T) {
	cases := []struct {
		path string
		want *int
	}{
		{"/resource_ops/0/source/asset_type", intPointer(0)},
		{"/resource_ops/2", intPointer(2)},
		{"resource_ops/3/x", intPointer(3)},
		{"/program_targets/0/target", nil},
		{"/resource_ops/x/source", nil},
		{"/resource_ops", nil},
		{"", nil},
		{"/resource_ops/", nil},
	}
	for _, item := range cases {
		got := resourceOpIndex(item.path)
		if item.want == nil {
			if got != nil {
				t.Fatalf("resourceOpIndex(%q) = %d, want nil", item.path, *got)
			}
			continue
		}
		if got == nil || *got != *item.want {
			t.Fatalf("resourceOpIndex(%q) = %v, want %d", item.path, got, *item.want)
		}
	}
	if resourceOpIndex(7) != nil {
		t.Fatalf("non-string paths must not produce an index")
	}

	manifest := map[string]any{"resource_ops": []any{map[string]any{"op": "a"}, map[string]any{"op": "b"}}}
	if payloadAt(manifest, 1).(map[string]any)["op"] != "b" {
		t.Fatalf("payloadAt did not select the second operation")
	}
	if payloadAt(manifest, 5) != nil || payloadAt(map[string]any{}, 0) != nil || payloadAt(nil, 0) != nil {
		t.Fatalf("payloadAt out-of-range lookups must be nil")
	}
	if payloadAt(manifest, -1).(map[string]any)["op"] != "b" {
		t.Fatalf("payloadAt must use Python negative indexing")
	}
}

func intPointer(value int) *int { return &value }

func TestSegmentPhaseAndPhaseFor(t *testing.T) {
	if segmentPhase("manifest") != "plan" || segmentPhase("resource_op") != "plan" {
		t.Fatalf("manifest and resource_op report the plan phase")
	}
	if segmentPhase("program") != "program" || segmentPhase("requirements") != "requirements" {
		t.Fatalf("unexpected segment phase")
	}
	if phaseFor(StreamFrame{Kind: "manifest"}) != "plan" {
		t.Fatalf("manifest frames belong to the plan phase")
	}
	for _, kind := range []string{"program_start", "program_end", "metadata", "shared_state", "script"} {
		if phaseFor(StreamFrame{Kind: kind}) != "program" {
			t.Fatalf("frame %s must report the program phase", kind)
		}
	}
	if phaseFor(StreamFrame{Kind: "requirements"}) != "requirements" {
		t.Fatalf("requirements frames keep their own phase")
	}
}

func TestSingleSegmentFrameAndProgramFrames(t *testing.T) {
	if _, err := singleSegmentFrame([]StreamFrame{}, "plan"); err == nil {
		t.Fatalf("a missing segment frame must fail")
	}
	if _, err := singleSegmentFrame([]StreamFrame{{Kind: "plan"}, {Kind: "plan"}}, "plan"); err == nil {
		t.Fatalf("duplicate segment frames must fail")
	}
	frame, err := singleSegmentFrame([]StreamFrame{{Kind: "plan", Content: "{}"}}, "plan")
	if err != nil || frame.Content != "{}" {
		t.Fatalf("single segment frame lookup failed: %v", err)
	}

	frames := []StreamFrame{
		{Kind: "program_start", Attrs: map[string]string{"target_index": "0"}},
		{Kind: "metadata", Content: "{}"},
		{Kind: "shared_state", Content: "{}"},
		{Kind: "script", Attrs: map[string]string{"target_index": "0", "index": "0", "total": "1"}},
		{Kind: "program_end", Attrs: map[string]string{"target_index": "0"}},
	}
	selected, err := programSegmentFrames(frames, 0)
	if err != nil || len(selected) != 5 {
		t.Fatalf("program segment selection failed: %v %d", err, len(selected))
	}
	if _, err := programSegmentFrames(frames, 1); err == nil {
		t.Fatalf("an incomplete program segment must fail")
	}
	nested := []StreamFrame{
		{Kind: "program_start", Attrs: map[string]string{"target_index": "0"}},
		{Kind: "program_start", Attrs: map[string]string{"target_index": "1"}},
	}
	if _, err := programSegmentFrames(nested, 0); err == nil {
		t.Fatalf("nested programs must fail")
	}
}

func TestSegmentPayloadAndErrorFrame(t *testing.T) {
	programFrame := StreamFrame{Kind: "program", Content: `{"schema_version": 1, "summary": "s", "scripts": [{"id": "script_1"}]}`}
	targetIndex := 0
	payload, ok := segmentPayload([]StreamFrame{programFrame}, "program", &targetIndex).(map[string]any)
	if !ok {
		t.Fatalf("single program frame payload missing")
	}
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata["schema_version"] != int64(1) || metadata["summary"] != "s" {
		t.Fatalf("program payload metadata = %v", metadata)
	}
	if scripts, _ := payload["scripts"].([]any); len(scripts) != 1 {
		t.Fatalf("program payload scripts = %v", payload["scripts"])
	}

	tagged := []StreamFrame{
		{Kind: "program_start", Attrs: map[string]string{"target_index": "0"}},
		{Kind: "metadata", Content: `{"summary": "m"}`},
		{Kind: "shared_state", Content: `{"variables": []}`},
		{Kind: "script", Content: `{"id": "script_1"}`},
		{Kind: "program_end"},
	}
	taggedPayload, ok := segmentPayload(tagged, "program", &targetIndex).(map[string]any)
	if !ok {
		t.Fatalf("tagged program payload missing")
	}
	if taggedPayload["metadata"].(map[string]any)["summary"] != "m" {
		t.Fatalf("tagged program metadata = %v", taggedPayload["metadata"])
	}
	if scripts, _ := taggedPayload["scripts"].([]any); len(scripts) != 1 {
		t.Fatalf("tagged program scripts = %v", taggedPayload["scripts"])
	}

	manifestPayload := segmentPayload([]StreamFrame{{Kind: "manifest", Content: `{"scope": "project"}`}}, "manifest", nil)
	if manifestPayload.(map[string]any)["scope"] != "project" {
		t.Fatalf("manifest payload = %v", manifestPayload)
	}
	if segmentPayload([]StreamFrame{{Kind: "plan", Content: "{oops"}}, "plan", nil) != nil {
		t.Fatalf("malformed frame payloads must be nil")
	}
	if segmentPayload([]StreamFrame{}, "plan", nil) != nil {
		t.Fatalf("missing segment payloads must be nil")
	}

	errorFrameValue := errorFrame(tagged, "program", &targetIndex)
	if errorFrameValue.Kind != "script" || errorFrameValue.Content != `{"id": "script_1"}` {
		t.Fatalf("program error frame = %+v", errorFrameValue)
	}
	fallback := errorFrame([]StreamFrame{}, "program", nil)
	if fallback.Kind != "program_start" || fallback.Attrs["target_index"] != "0" {
		t.Fatalf("program error frame fallback = %+v", fallback)
	}
	if errorFrame([]StreamFrame{}, "plan", nil).Kind != "plan" {
		t.Fatalf("non-program error frame must be an empty segment frame")
	}
}

func TestRepairEventCarriesFailureFields(t *testing.T) {
	request := &requestView{requestID: "request-1"}
	failure := GenerationEvent{
		Type: "validation_error", RequestID: "request-1", Phase: "plan",
		Data: map[string]any{
			"code": "INVALID_BACKDROP_SOURCE", "path": "/resource_ops/0/source/asset_type",
			"operation": "add_backdrop", "actual": "costume", "expected": "asset_type=backdrop",
			"repair_scope": "resource_op", "repair_id": "backdrop_1", "message": "asset_type=backdrop",
			"detail": "detail", "recoverable": false,
		},
	}
	targetIndex := 0
	operationIndex := 0
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	event := orchestrator.repairEvent(request, failure, "resource_op", 2, 3, &targetIndex, &operationIndex)

	if event.Type != "repair" || event.Phase != "plan" {
		t.Fatalf("repair event = %+v", event)
	}
	want := map[string]any{
		"attempt": 2, "max_rounds": 3, "phase": "plan", "path": "/resource_ops/0/source/asset_type",
		"code": "INVALID_BACKDROP_SOURCE", "operation": "add_backdrop", "actual": "costume",
		"expected": "asset_type=backdrop", "repair_scope": "resource_op", "repair_id": "backdrop_1",
		"message": "asset_type=backdrop", "detail": "detail", "resume": "resource_op",
		"target_index": 0, "resource_op_index": 0,
	}
	if !reflect.DeepEqual(roundTripOnce(t, event.Data), roundTripOnce(t, want)) {
		t.Fatalf("repair data = %v, want %v", event.Data, want)
	}

	scopeless := GenerationEvent{Phase: "program", Data: map[string]any{}}
	fallback := orchestrator.repairEvent(request, scopeless, "program", 1, 3, nil, nil)
	if fallback.Data["repair_scope"] != "program" || fallback.Data["resume"] != "program" {
		t.Fatalf("repair scope fallback = %v", fallback.Data)
	}
	if _, present := fallback.Data["target_index"]; present {
		t.Fatalf("target_index must be omitted when unset")
	}
}

func TestResumeStateKeepsPassedPhases(t *testing.T) {
	state := newGenerationState()
	state.requirements = &schema.RequirementsResponse{Ready: true}
	state.plan = &schema.Plan{Steps: []string{"a"}}
	state.manifest = &schema.ProjectManifest{Scope: "project"}
	state.programs[0] = &schema.ProgramIR{}

	restarted := resumeState(state, "requirements")
	if restarted.requirements != nil || len(restarted.phaseStarted) != 0 {
		t.Fatalf("requirements resume must start from scratch")
	}
	fromPlan := resumeState(state, "plan")
	if fromPlan.requirements == nil || fromPlan.plan != nil {
		t.Fatalf("plan resume keeps only requirements")
	}
	fromManifest := resumeState(state, "manifest")
	if fromManifest.requirements == nil || fromManifest.plan == nil || fromManifest.manifest != nil {
		t.Fatalf("manifest resume keeps requirements and plan but drops the manifest")
	}
	fromProgram := resumeState(state, "program")
	if fromProgram.manifest != state.manifest {
		t.Fatalf("program resume keeps the manifest")
	}
	// The Python reference passes the same set object for both phase fields.
	fromManifest.phaseStarted["program"] = true
	if !fromManifest.phaseCompleted["program"] {
		t.Fatalf("resumed phase sets must alias like the Python reference")
	}
}

func TestRepairMessagesStayBounded(t *testing.T) {
	promptMessages := []prompt.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "hello"}}
	failure := &GenerationEvent{
		Type: "validation_error", RequestID: "request-1", Phase: "plan",
		Data: map[string]any{
			"path": "/program_targets/0/mode", "detail": "mode violation", "message": "mode",
		},
	}
	repaired := repairMessages(promptMessages, failure, "manifest")
	if len(repaired) != 3 || repaired[2].Role != "user" {
		t.Fatalf("repair messages = %+v", repaired)
	}
	content := repaired[2].Content
	if !strings.Contains(content, "mode violation") {
		t.Fatalf("repair message must quote the failure detail: %q", content)
	}
	if !strings.Contains(content, "mode 只能为") {
		t.Fatalf("repair message must include the repair hint: %q", content)
	}
	if !strings.Contains(content, "从 <manifest> 标签开始重新输出") {
		t.Fatalf("repair message must include the resume instruction: %q", content)
	}
	if strings.Contains(content, "hello") {
		t.Fatalf("repair message must not replay user text")
	}

	empty := repairMessages(promptMessages, &GenerationEvent{Phase: "program", Data: map[string]any{}}, "program")
	if !strings.Contains(empty[2].Content, "未知校验错误") {
		t.Fatalf("missing diagnostics must fall back to the default detail")
	}
}

func TestStructuredIssueCodesAndPaths(t *testing.T) {
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	request := &requestView{requestID: "request-1"}

	protocol := orchestrator.validationEvent(request, "program", &StreamProtocolError{Message: "bad frame"}, StreamFrame{Kind: "program_start", Content: "abc"})
	if protocol.Data["code"] != "STREAM_PROTOCOL_ERROR" || protocol.Data["path"] != "/output" {
		t.Fatalf("stream protocol issue = %v", protocol.Data)
	}
	if protocol.Data["operation"] != "program_start" || protocol.Data["repair_scope"] != "program" {
		t.Fatalf("stream protocol issue scope = %v", protocol.Data)
	}

	resourceErr := &schema.ResourceContractError{Issue: schema.NewResourceContractIssue(
		"INVALID_BACKDROP_SOURCE", "/resource_ops/0/source/asset_type", "add_backdrop", "costume", "asset_type=backdrop", nil,
	)}
	resource := orchestrator.validationEvent(request, "plan", resourceErr, StreamFrame{Kind: "manifest"})
	if resource.Data["code"] != "INVALID_BACKDROP_SOURCE" || resource.Data["repair_id"] != nil {
		t.Fatalf("resource issue = %v", resource.Data)
	}
	if resource.Data["repair_scope"] != "resource_op" {
		t.Fatalf("resource issue repair scope = %v", resource.Data["repair_scope"])
	}

	programErr := &schema.ProgramContractError{Issues: []schema.ContractIssue{{Path: "scripts[0]", Message: "bad block"}}}
	program := orchestrator.validationEvent(request, "program", programErr, StreamFrame{Kind: "script"})
	if program.Data["code"] != "PROGRAM_CONTRACT_INVALID" || program.Data["expected"] != "bad block" {
		t.Fatalf("program contract issue = %v", program.Data)
	}
	if program.Data["path"] != "/scripts/0" {
		t.Fatalf("program contract path = %v", program.Data["path"])
	}

	unsupported := orchestrator.validationEvent(request, "plan", &schema.SchemaValidationError{Path: "/schema_version", Message: "Input should be '3'"}, StreamFrame{Kind: "manifest", Content: `{"schema_version": 1}`})
	if unsupported.Data["code"] != "UNSUPPORTED_SCHEMA_VERSION" || unsupported.Data["expected"] != "schema_version must equal 3" {
		t.Fatalf("unsupported version issue = %v", unsupported.Data)
	}
	if unsupported.Data["actual"] != int64(1) {
		t.Fatalf("unsupported version actual = %v", unsupported.Data["actual"])
	}

	missing := orchestrator.validationEvent(request, "plan", &schema.SchemaValidationError{Path: "/schema_version", Message: "Field required"}, StreamFrame{Kind: "manifest", Content: `{}`})
	if missing.Data["code"] != "SCHEMA_VALIDATION_ERROR" || missing.Data["expected"] != "Field required" {
		t.Fatalf("missing version issue = %v", missing.Data)
	}
	if missing.Data["actual"] != nil {
		t.Fatalf("missing version actual = %v", missing.Data["actual"])
	}

	modelErr := &modelValidationError{err: errors.New("stage, existing_sprite and created_sprite programs must use append")}
	model := orchestrator.validationEvent(request, "plan", modelErr, StreamFrame{Kind: "manifest", Content: `{"scope": "project"}`})
	if model.Data["code"] != "SCHEMA_VALIDATION_ERROR" || model.Data["path"] != "" {
		t.Fatalf("model validator issue = %v", model.Data)
	}
	if !strings.Contains(model.Data["detail"].(string), "must use append") {
		t.Fatalf("model validator detail = %v", model.Data["detail"])
	}
}

func TestDiagnosticsStayBounded(t *testing.T) {
	if requestLogRef("request-1") != "19f1064b619d49d3" {
		t.Fatalf("requestLogRef = %q", requestLogRef("request-1"))
	}
	bounded := boundedLogText("line\nline", 12_000)
	if strings.Contains(bounded, "\n") || !strings.Contains(bounded, "\\n") {
		t.Fatalf("boundedLogText must escape newlines: %q", bounded)
	}
	if !strings.HasSuffix(boundedLogText(strings.Repeat("x", 30), 10), "...") {
		t.Fatalf("boundedLogText must truncate with an ellipsis")
	}
	formatted := formattedModelOutput("{\n  \"schema_version\": 2\n}", 12_000)
	if !strings.HasPrefix(formatted, "{\n  \"schema_version\": 2") || strings.Contains(formatted, "\\n") {
		t.Fatalf("formattedModelOutput = %q", formatted)
	}
	if formattedModelOutput("not json", 12_000) != "not json" {
		t.Fatalf("malformed output must be returned as text")
	}
	shape := diagnosticShape(map[string]any{"a": 1, "b": "x"}, 0)
	if !strings.Contains(shape, `"type":"object"`) || !strings.Contains(shape, `"key_count":2`) {
		t.Fatalf("diagnosticShape object = %q", shape)
	}
	if !strings.Contains(diagnosticShape([]any{1, "a"}, 0), `"item_types":["int","str"]`) {
		t.Fatalf("diagnosticShape array = %q", diagnosticShape([]any{1, "a"}, 0))
	}
	if diagnosticShape(nil, 0) != "null" {
		t.Fatalf("diagnosticShape nil = %q", diagnosticShape(nil, 0))
	}
	if diagnosticJSON(map[string]any{"a": 1}, 2_048) != `{"a":1}` {
		t.Fatalf("diagnosticJSON = %q", diagnosticJSON(map[string]any{"a": 1}, 2_048))
	}
}

func TestProgramGenerationLayersRespectsBlueprintDependencies(t *testing.T) {
	manifest := &schema.ProjectManifest{
		ProgramTargets: []schema.ProjectProgramTarget{
			{JobID: "job_1", BehaviorIDs: []string{"behavior_1"}, OwnerEntityID: "e1", Target: schema.TargetRef{Kind: "stage"}, Mode: "append", Intent: "i"},
			{JobID: "job_2", BehaviorIDs: []string{"behavior_2"}, OwnerEntityID: "e2", Target: schema.TargetRef{Kind: "existing_sprite", Name: stringPointer("Sprite1")}, Mode: "append", Intent: "i"},
		},
	}
	blueprint := &schema.ProjectBlueprint{
		BehaviorGraph: []schema.BlueprintBehaviorGraphNode{
			{BehaviorID: "behavior_1", OwnerEntityID: "e1"},
			{BehaviorID: "behavior_2", OwnerEntityID: "e2", DependsOn: []string{"behavior_1"}},
		},
	}
	layers, err := programGenerationLayers(blueprint, manifest)
	if err != nil {
		t.Fatalf("programGenerationLayers: %v", err)
	}
	if !reflect.DeepEqual(layers, [][]int{{0}, {1}}) {
		t.Fatalf("layers = %v, want [[0] [1]]", layers)
	}

	duplicate := &schema.ProjectManifest{
		ProgramTargets: []schema.ProjectProgramTarget{
			{JobID: "job_1", BehaviorIDs: []string{"behavior_1"}},
			{JobID: "job_2", BehaviorIDs: []string{"behavior_1"}},
		},
	}
	if _, err := programGenerationLayers(blueprint, duplicate); err == nil {
		t.Fatalf("behaviors assigned twice must fail")
	}
	unknown := &schema.ProjectBlueprint{BehaviorGraph: []schema.BlueprintBehaviorGraphNode{{BehaviorID: "behavior_9"}}}
	if _, err := programGenerationLayers(unknown, manifest); err == nil {
		t.Fatalf("unknown blueprint behavior must fail")
	}
}

func stringPointer(value string) *string { return &value }

func TestFinishAndResetState(t *testing.T) {
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	request := &requestView{requestID: "request-1", scope: "single_sprite", proceedWithDefaults: true}
	state := newGenerationState()
	if failure := orchestrator.finish(request, state); failure == nil || failure.Data["code"] != "OUTPUT_INCOMPLETE" {
		t.Fatalf("empty state must report OUTPUT_INCOMPLETE")
	}
	state.requirements = &schema.RequirementsResponse{Ready: true}
	state.plan = &schema.Plan{Steps: []string{"a"}}
	state.activeProgram = &programBuffer{targetIndex: 0}
	if failure := orchestrator.finish(request, state); failure == nil || failure.Type != "validation_error" || failure.Phase != "program" {
		t.Fatalf("incomplete program must report a program validation error")
	}
	state.activeProgram = nil
	if failure := orchestrator.finish(request, state); failure == nil || failure.Data["code"] != "PROGRAM_MISSING" {
		t.Fatalf("missing program must report PROGRAM_MISSING")
	}
	state.programs[0] = &schema.ProgramIR{}
	if failure := orchestrator.finish(request, state); failure != nil {
		t.Fatalf("complete single target state must finish cleanly: %v", failure.Data)
	}

	project := &requestView{requestID: "request-1", scope: "project", proceedWithDefaults: true}
	projectState := newGenerationState()
	projectState.requirements = &schema.RequirementsResponse{Ready: true}
	projectState.plan = &schema.Plan{}
	if failure := orchestrator.finish(project, projectState); failure == nil || failure.Data["code"] != "MANIFEST_MISSING" {
		t.Fatalf("project scope without a manifest must report MANIFEST_MISSING")
	}
	projectState.manifest = &schema.ProjectManifest{ProgramTargets: []schema.ProjectProgramTarget{{}}}
	if failure := orchestrator.finish(project, projectState); failure == nil || failure.Data["code"] != "PROGRAMS_INCOMPLETE" {
		t.Fatalf("missing project programs must report PROGRAMS_INCOMPLETE")
	}
	projectState.programs[0] = &schema.ProgramIR{}
	if failure := orchestrator.finish(project, projectState); failure != nil {
		t.Fatalf("complete project state must finish cleanly: %v", failure.Data)
	}

	projectState.phaseStarted["program"] = true
	resetState(projectState)
	if projectState.manifest != nil || len(projectState.programs) != 0 || len(projectState.phaseStarted) != 0 {
		t.Fatalf("resetState must clear the generation state")
	}
}

func TestTargetContractSelection(t *testing.T) {
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	single := &requestView{
		requestID: "request-1", scope: "single_sprite",
		target: schema.TargetContext{Name: "Sprite1", Mode: "append", CurrentProgram: "{}"},
	}
	kind, mode, current, err := orchestrator.targetContract(single, newGenerationState(), 0)
	if err != nil || kind != "sprite" || mode != "append" || current != "{}" {
		t.Fatalf("single target contract = %s %s %s %v", kind, mode, current, err)
	}

	manifest := &schema.ProjectManifest{ProgramTargets: []schema.ProjectProgramTarget{
		{Target: schema.TargetRef{Kind: "stage"}, Mode: "append"},
		{Target: schema.TargetRef{Kind: "created_sprite", OpID: stringPointer("op_1")}, Mode: "append"},
		{Target: schema.TargetRef{Kind: "existing_sprite", Name: stringPointer("Sprite1")}, Mode: "append"},
	}}
	project := &requestView{
		requestID: "request-1", scope: "project", projectContext: projectContextFixture(),
		target: schema.TargetContext{Name: "Sprite1", Mode: "append", CurrentProgram: "{}"},
	}
	state := newGenerationState()
	state.manifest = manifest
	if _, _, _, err := orchestrator.targetContract(project, state, len(manifest.ProgramTargets)); err == nil {
		t.Fatalf("out-of-range target index must fail")
	}
	if kind, _, _, err := orchestrator.targetContract(project, state, 0); err != nil || kind != "stage" {
		t.Fatalf("stage target contract = %s %v", kind, err)
	}
	if kind, _, current, err := orchestrator.targetContract(project, state, 1); err != nil || kind != "sprite" || current != "" {
		t.Fatalf("created sprite contract = %s %q %v", kind, current, err)
	}
	if kind, _, current, err := orchestrator.targetContract(project, state, 2); err != nil || kind != "sprite" || current != "" {
		t.Fatalf("existing sprite contract = %s %q %v", kind, current, err)
	}
	project.outputContract = "project_program"
	if _, _, current, err := orchestrator.targetContract(project, state, 1); err != nil || current != "{}" {
		t.Fatalf("project_program must use the request current program: %q %v", current, err)
	}
}

func TestTargetContextForManifestTargetProjectsManifestNames(t *testing.T) {
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	manifest := &schema.ProjectManifest{
		ResourceOps: []schema.ResourceOp{
			{"op": "add_sprite", "op_id": "op_1", "name": "NewSprite", "costumes": []any{map[string]any{"name": "c1"}}},
		},
		ProgramTargets: []schema.ProjectProgramTarget{
			{Target: schema.TargetRef{Kind: "created_sprite", OpID: stringPointer("op_1")}, Mode: "modify"},
			{Target: schema.TargetRef{Kind: "existing_sprite", Name: stringPointer("Sprite1")}, Mode: "append"},
		},
	}
	request := &requestView{
		requestID: "request-1", scope: "project", projectContext: projectContextFixture(),
		target: schema.TargetContext{Name: "Sprite1", Mode: "append", CurrentProgram: "{}"},
	}
	created, err := orchestrator.targetContextForManifestTarget(request, manifest, 0, nil)
	if err != nil {
		t.Fatalf("created sprite context: %v", err)
	}
	if created.Name != "NewSprite" || created.Mode != "append" || created.CurrentProgram != "{}" {
		t.Fatalf("created sprite context = %+v", created)
	}
	if !reflect.DeepEqual(created.SpriteNames, []string{"Sprite1", "NewSprite"}) {
		t.Fatalf("projected sprite names = %v", created.SpriteNames)
	}
	if !reflect.DeepEqual(created.Costumes, []string{"c1"}) {
		t.Fatalf("created sprite costumes = %v", created.Costumes)
	}
	existing, err := orchestrator.targetContextForManifestTarget(request, manifest, 1, stringPointer("append"))
	if err != nil {
		t.Fatalf("existing sprite context: %v", err)
	}
	if existing.TargetID != "sprite-1" || existing.Revision != 3 {
		t.Fatalf("existing sprite context = %+v", existing)
	}

	assets := orchestrator.existingAssetsForTarget(request, existing)
	if !reflect.DeepEqual(assets["sprite"], []string{"Sprite1"}) || !reflect.DeepEqual(assets["costume"], []string{"costume1"}) {
		t.Fatalf("existing assets = %v", assets)
	}
	missing := &schema.ProjectManifest{ProgramTargets: []schema.ProjectProgramTarget{{Target: schema.TargetRef{Kind: "existing_sprite", Name: stringPointer("Nope")}}}}
	if _, err := orchestrator.targetContextForManifestTarget(request, missing, 0, nil); err == nil {
		t.Fatalf("a target missing from the project context must fail")
	}
	noContext := &requestView{requestID: "request-1", scope: "project", target: request.target}
	if _, err := orchestrator.targetContextForManifestTarget(noContext, manifest, 0, nil); err == nil {
		t.Fatalf("a missing project context must fail")
	}
}

// ---------------------------------------------------------------------------
// promptRequest projections
// ---------------------------------------------------------------------------

func TestProjectContextUsesLogicalTargetIDs(t *testing.T) {
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)
	project := orchestrator.projectContext(viewOfUnified(projectRequest()))
	if project == nil || len(project.Targets) != 2 {
		t.Fatalf("project context = %+v", project)
	}
	if project.Targets[0]["target_id"] != "logical_target_1" || project.Targets[1]["target_id"] != "logical_target_2" {
		t.Fatalf("logical target ids = %v %v", project.Targets[0]["target_id"], project.Targets[1]["target_id"])
	}
	if orchestrator.projectContext(viewOfUnified(singleSpriteRequest())) != nil {
		t.Fatalf("a request without project context must project nil")
	}
	target, err := orchestrator.targetContext(viewOfUnified(projectRequest()))
	if err != nil {
		t.Fatalf("target context: %v", err)
	}
	if target.TargetID != "sprite-1" || target.Revision != 3 {
		t.Fatalf("target context = %+v", target)
	}
}

// ---------------------------------------------------------------------------
// Structured project transport and extra repair rounds
// ---------------------------------------------------------------------------

// structuredProgramPayload mirrors structured_program_payload in the Python
// tests: the tagged program frames merged into one ProgramIR object.
func structuredProgramPayload(t *testing.T, tagged string) string {
	t.Helper()
	metadata := substringBetween(t, tagged, "<metadata>", "</metadata>")
	state := substringBetween(t, tagged, "<shared_state>", "</shared_state>")
	scriptStart := strings.Index(tagged, "<script")
	if scriptStart < 0 {
		t.Fatalf("tagged program has no script frame")
	}
	scriptOpenEnd := strings.Index(tagged[scriptStart:], ">")
	if scriptOpenEnd < 0 {
		t.Fatalf("tagged program has a malformed script frame")
	}
	scriptOpenEnd += scriptStart
	scriptEnd := strings.Index(tagged[scriptOpenEnd:], "</script>")
	if scriptEnd < 0 {
		t.Fatalf("tagged program has an unterminated script frame")
	}
	scriptEnd += scriptOpenEnd

	merged := map[string]any{}
	for _, raw := range []string{metadata, state} {
		parsed, err := jsonx.Parse([]byte(raw))
		if err != nil {
			t.Fatalf("parse program part: %v", err)
		}
		object, ok := parsed.(map[string]any)
		if !ok {
			t.Fatalf("program part is not an object")
		}
		for key, value := range object {
			merged[key] = value
		}
	}
	script, err := jsonx.Parse([]byte(tagged[scriptOpenEnd+1 : scriptEnd]))
	if err != nil {
		t.Fatalf("parse program script: %v", err)
	}
	merged["scripts"] = []any{script}
	encoded, err := jsonx.Marshal(merged)
	if err != nil {
		t.Fatalf("marshal structured program: %v", err)
	}
	return string(encoded)
}

func substringBetween(t *testing.T, value, open, closeTag string) string {
	t.Helper()
	start := strings.Index(value, open)
	end := strings.Index(value, closeTag)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("missing %s..%s in tagged output", open, closeTag)
	}
	return value[start+len(open) : end]
}

func TestStructuredProjectTransportForwardsSchemasAndDecodesPrograms(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {fixtures["manifest"]},
		"program":      {structuredProgramPayload(t, fixtures["program"])},
	})
	p.route = projectRoute
	p.structured = map[string]bool{
		"requirements": true, "task_spec": true, "blueprint": true,
		"plan": true, "manifest": true, "program": true,
	}
	p.jsonSchema = map[string]bool{
		"requirements": true, "task_spec": true, "blueprint": true, "plan": true,
	}
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	last := events[len(events)-1]
	if last.Type != "complete" || last.Data["program_count"] != 1 {
		t.Fatalf("terminal event = %s %v (%v)", last.Type, last.Data, eventTypes(events))
	}
	if findEvent(events, "validation_error") != nil {
		t.Fatalf("structured transport must not produce validation errors")
	}
	flags, schemas := p.flags()
	if len(flags) != 6 {
		t.Fatalf("provider calls = %d, want 6", len(flags))
	}
	for index, flag := range flags {
		if !flag {
			t.Fatalf("call %d did not request json_output", index+1)
		}
	}
	for index := 0; index < 4; index++ {
		if schemas[index] == nil {
			t.Fatalf("call %d must receive a JSON schema", index+1)
		}
	}
	if schemas[4] != nil || schemas[5] != nil {
		t.Fatalf("manifest and program segments have no pre-generated schema")
	}
}

func TestProgramValidationErrorRepairsOnce(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	badProgram := strings.Replace(fixtures["program"], `"opcode": "event_whenflagclicked"`, `"opcode": "motion_movesteps"`, 1)
	if badProgram == fixtures["program"] {
		t.Fatalf("fixture did not contain the hat block to corrupt")
	}
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {fixtures["manifest"]},
		"program":      {badProgram, fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 1, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	repair := findEvent(events, "repair")
	if repair == nil || repair.Data["resume"] != "program" || repair.Data["attempt"] != 1 {
		t.Fatalf("program repair event = %+v", repair)
	}
	detail, _ := repair.Data["detail"].(string)
	if !strings.Contains(detail, "hat block") && !strings.Contains(detail, "unknown opcode") {
		t.Fatalf("program repair detail = %q", detail)
	}
	last := events[len(events)-1]
	if last.Type != "complete" {
		t.Fatalf("terminal event = %s %v", last.Type, last.Data)
	}
	// The batch sink streams every failed attempt live, including the one that
	// is repaired; only the final attempt's failure would be duplicated by the
	// returned event list.
	failures := 0
	for _, event := range events {
		if event.Type == "validation_error" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("live validation_error events = %d, want 1", failures)
	}
	if p.callCount() != 7 {
		t.Fatalf("provider calls = %d, want 7", p.callCount())
	}
}

func TestProgramRepairSecondRoundWhenFirstRepairFails(t *testing.T) {
	golden := loadGolden(t, "golden_project.json")
	fixtures := goldenFixtures(t, golden)
	badProgram := strings.Replace(fixtures["program"], `"opcode": "event_whenflagclicked"`, `"opcode": "motion_movesteps"`, 1)
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {fixtures["manifest"]},
		"program":      {badProgram, badProgram, fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 2, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	attempts := []any{}
	for _, event := range events {
		if event.Type == "repair" {
			attempts = append(attempts, event.Data["attempt"])
		}
	}
	if !reflect.DeepEqual(attempts, []any{1, 2}) {
		t.Fatalf("repair attempts = %v, want [1 2]", attempts)
	}
	if events[len(events)-1].Type != "complete" {
		t.Fatalf("terminal event = %s", events[len(events)-1].Type)
	}
	if p.callCount() != 8 {
		t.Fatalf("provider calls = %d, want 8", p.callCount())
	}
}

func TestResourceOpRepairHasTwoLocalRounds(t *testing.T) {
	golden := loadGolden(t, "golden_resource_op.json")
	fixtures := goldenFixtures(t, golden)
	badOp := strings.Replace(fixtures["resource_op"], `"asset_type": "backdrop"`, `"asset_type": "costume"`, 1)
	if badOp == fixtures["resource_op"] {
		t.Fatalf("fixture did not contain a backdrop source to corrupt")
	}
	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {fixtures["manifest"]},
		"resource_op":  {badOp, fixtures["resource_op"]},
		"program":      {fixtures["program"]},
	})
	p.route = func(callIndex int, lastMessage string) string {
		if strings.Contains(lastMessage, "resource_op_index=") {
			return "resource_op"
		}
		return projectRoute(callIndex, lastMessage)
	}
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	attempts := []any{}
	scopes := map[any]bool{}
	for _, event := range events {
		if event.Type == "repair" {
			attempts = append(attempts, event.Data["attempt"])
			scopes[event.Data["repair_scope"]] = true
		}
	}
	if !reflect.DeepEqual(attempts, []any{1, 2}) {
		t.Fatalf("repair attempts = %v, want [1 2]", attempts)
	}
	if len(scopes) != 1 || !scopes["resource_op"] {
		t.Fatalf("repair scopes = %v, want only resource_op", scopes)
	}
	if events[len(events)-1].Type != "complete" {
		t.Fatalf("terminal event = %s", events[len(events)-1].Type)
	}
	if p.callCount() != 8 {
		t.Fatalf("provider calls = %d, want 8", p.callCount())
	}
}

func TestManifestPhaseReportsBackdropNormalizations(t *testing.T) {
	golden := loadGolden(t, "golden_resource_op.json")
	fixtures := goldenFixtures(t, golden)
	body := taggedBody(fixtures["manifest"])
	parsed, err := jsonx.Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse manifest fixture: %v", err)
	}
	manifest, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("manifest fixture is not an object")
	}
	manifest["resource_ops"] = []any{map[string]any{
		"op":             "add_backdrop",
		"op_id":          "backdrop_1",
		"source_need_id": "need_backdrop",
		"name":           "forest",
		"source":         map[string]any{"kind": "scratch_library", "asset_type": "backdrop", "search_terms": []any{"forest"}},
		"target":         map[string]any{"kind": "stage"},
	}}
	encoded, err := jsonx.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	normalizingManifest := "<manifest>" + string(encoded) + "</manifest>"

	p := newCannedProvider(map[string][]string{
		"requirements": {fixtures["requirements"]},
		"task_spec":    {fixtures["task_spec"]},
		"blueprint":    {fixtures["blueprint"]},
		"plan":         {fixtures["plan"]},
		"manifest":     {normalizingManifest},
		"program":      {fixtures["program"]},
	})
	p.route = projectRoute
	orchestrator := NewGenerationOrchestrator(fixedBuilder(), 3, 4)

	events := collectEvents(t, func(emit emitFunc) error {
		return orchestrator.generateUnified(context.Background(), viewOfUnified(projectRequest()), p, emit)
	})

	var planComplete *GenerationEvent
	for index := range events {
		if events[index].Type == "phase_complete" && events[index].Phase == "plan" {
			planComplete = &events[index]
		}
	}
	if planComplete == nil {
		t.Fatalf("plan phase_complete event missing")
	}
	codes := []string{}
	for _, item := range planComplete.Data["normalizations"].([]map[string]any) {
		codes = append(codes, item["code"].(string))
	}
	want := []string{"DEFAULT_APPLIED", "REDUNDANT_STAGE_TARGET_REMOVED"}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("normalization codes = %v, want %v", codes, want)
	}
	manifestDump := planComplete.Data["manifest"].(map[string]any)
	operation := manifestDump["resource_ops"].([]any)[0].(map[string]any)
	if operation["select_after"] != false {
		t.Fatalf("select_after was not defaulted: %v", operation["select_after"])
	}
	if _, present := operation["target"]; present {
		t.Fatalf("the redundant stage target was not removed")
	}
}
