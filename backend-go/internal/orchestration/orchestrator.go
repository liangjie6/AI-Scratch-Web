// Package orchestration ports backend/app/orchestration/orchestrator.py.
//
// The Python async generators become emit callbacks: every method that yields
// GenerationEvent takes an emit function and returns an error only for the
// failures the Python reference lets escape the generator. The public
// GenerateUnified and GenerateProjectProgram wrappers surface those failures on
// a second channel, mirroring how the FastAPI route consumes them: the event
// channel carries cache_hit/usage/phase_start/.../complete/error events and is
// closed first, the buffered error channel is closed after it.
package orchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"

	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/resourcecontract"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// logger mirrors the module logger of the Python reference. Tests may replace
// the process logger through slog.SetDefault.
var logger = slog.Default()

// boundedActualLimit is the default limit of the Python bounded_actual helper.
const boundedActualLimit = 2048

// ---------------------------------------------------------------------------
// Module-level helpers
// ---------------------------------------------------------------------------

// requestLogRef ports _request_log_ref.
func requestLogRef(requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	return hex.EncodeToString(sum[:])[:16]
}

// pythonString approximates Python str() for the values that reach diagnostics.
func pythonString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return jsonx.FormatFloat(typed)
	default:
		return schema.PythonRepr(value)
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

// boundedLogText ports _bounded_log_text.
func boundedLogText(value any, limit int) string {
	text := strings.ReplaceAll(strings.ReplaceAll(pythonString(value), "\r", "\\r"), "\n", "\\n")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:maxInt(0, limit-3)]) + "..."
}

// formattedModelOutput ports _formatted_model_output.
func formattedModelOutput(value string, limit int) string {
	var text string
	parsed, err := jsonx.Parse([]byte(strings.TrimSpace(value)))
	if err != nil {
		text = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	} else {
		text = marshalIndentJSON(parsed)
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:maxInt(0, limit-3)]) + "..."
}

// marshalIndentJSON mirrors json.dumps(value, ensure_ascii=False, indent=2).
func marshalIndentJSON(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return pythonString(value)
	}
	return strings.TrimRight(buffer.String(), "\n")
}

// diagnosticText ports _diagnostic_text.
func diagnosticText(value any, limit int) string {
	return boundedLogText(value, limit)
}

// diagnosticJSON ports _diagnostic_json.
func diagnosticJSON(value any, limit int) string {
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return boundedLogText(pythonString(value), limit)
	}
	return boundedLogText(string(encoded), limit)
}

// pythonTypeName mirrors type(value).__name__ for decoded contract values.
func pythonTypeName(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NoneType"
	case string:
		return "str"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case map[string]any:
		return "dict"
	case []any:
		return "list"
	case *schema.RequirementsResponse:
		return "RequirementsResponse"
	case *schema.ScratchTaskSpec:
		return "ScratchTaskSpec"
	case *schema.ProjectBlueprint:
		return "ProjectBlueprint"
	case *schema.Plan:
		return "Plan"
	case *schema.ProjectManifest:
		return "ProjectManifest"
	case *schema.ProgramIR:
		return "ProgramIR"
	default:
		return fmt.Sprintf("%T", typed)
	}
}

// diagnosticShape ports _diagnostic_shape. Go maps have no insertion order, so
// object keys are reported in sorted order.
func diagnosticShape(value any, depth int) string {
	if depth > 1 {
		return pythonTypeName(value)
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := jsonx.SortedKeys(typed)
		shown := keys
		if len(shown) > 40 {
			shown = shown[:40]
		}
		return compactJSON(map[string]any{"type": "object", "keys": shown, "key_count": len(keys)})
	case []any:
		limit := len(typed)
		if limit > 20 {
			limit = 20
		}
		seen := map[string]bool{}
		for _, item := range typed[:limit] {
			seen[pythonTypeName(item)] = true
		}
		names := make([]string, 0, len(seen))
		for name := range seen {
			names = append(names, name)
		}
		sort.Strings(names)
		return compactJSON(map[string]any{"type": "array", "length": len(typed), "item_types": names})
	case string:
		return compactJSON(map[string]any{"type": "string", "length": utf8.RuneCountInString(typed)})
	case nil:
		return "null"
	default:
		return compactJSON(map[string]any{"type": pythonTypeName(typed)})
	}
}

func compactJSON(value any) string {
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return pythonString(value)
	}
	return string(encoded)
}

// toChatGPTStrictJSONSchema ports _to_chatgpt_strict_json_schema. The strict
// schema comes from the pre-generated prompt.StrictJSONSchemas map, which is
// byte-identical to the Python to_strict_json_schema output for every ported
// model; the ChatGPT Responses dialect adaptation then rewrites the residual
// oneOf/discriminator pairs the same way the Python helper does.
func toChatGPTStrictJSONSchema(segment string) map[string]any {
	raw, ok := prompt.StrictJSONSchemas[segment]
	if !ok {
		return nil
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil
	}
	schema, ok := parsed.(map[string]any)
	if !ok {
		return nil
	}
	normalizeChatGPTSchema(schema)
	return schema
}

func normalizeChatGPTSchema(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if alternatives, present := typed["oneOf"]; present {
			delete(typed, "oneOf")
			existing, _ := typed["anyOf"].([]any)
			merged := make([]any, 0, len(existing)+len(alternatives.([]any)))
			merged = append(merged, existing...)
			merged = append(merged, alternatives.([]any)...)
			typed["anyOf"] = merged
		}
		delete(typed, "discriminator")
		for _, key := range jsonx.SortedKeys(typed) {
			normalizeChatGPTSchema(typed[key])
		}
	case []any:
		for _, child := range typed {
			normalizeChatGPTSchema(child)
		}
	}
}

// caseFold mirrors Python str.casefold.
func caseFold(value string) string {
	return cases.Fold().String(value)
}

func orString(value any) string {
	text, _ := value.(string)
	return text
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalString(value any) string {
	text, _ := value.(string)
	return text
}

func cloneAnyMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func mergeAnyMaps(values ...map[string]any) map[string]any {
	result := map[string]any{}
	for _, value := range values {
		for key, item := range value {
			result[key] = item
		}
	}
	return result
}

// modelDump ports pydantic model_dump(mode='json') for the ported models.
func modelDump(value any) map[string]any {
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	parsed, err := jsonx.Parse(encoded)
	if err != nil {
		return map[string]any{}
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return object
}

// modelValidationError wraps a plain error returned by a ported model validator
// so it is treated exactly like a pydantic ValidationError (loc=(), msg=error).
type modelValidationError struct{ err error }

func (e *modelValidationError) Error() string { return e.err.Error() }

func (e *modelValidationError) Unwrap() error { return e.err }

// decodeModel ports Model.model_validate(parse_json(...)).
func decodeModel[T any](raw any) (*T, error) {
	encoded, err := jsonx.Marshal(raw)
	if err != nil {
		return nil, &modelValidationError{err: err}
	}
	var value T
	if err := schema.DecodeStrict(encoded, &value); err != nil {
		var schemaErr *schema.SchemaValidationError
		if errors.As(err, &schemaErr) {
			return nil, err
		}
		return nil, &modelValidationError{err: err}
	}
	return &value, nil
}

// catchableSegmentError reports whether an error behaves like the Python
// (ValidationError, ValueError, StreamProtocolError) tuple: the ported
// ProgramContractError and ResourceContractError both subclass ValueError.
func catchableSegmentError(err error) bool {
	var streamErr *StreamProtocolError
	if errors.As(err, &streamErr) {
		return true
	}
	var schemaErr *schema.SchemaValidationError
	if errors.As(err, &schemaErr) {
		return true
	}
	var programErr *schema.ProgramContractError
	if errors.As(err, &programErr) {
		return true
	}
	var resourceErr *schema.ResourceContractError
	if errors.As(err, &resourceErr) {
		return true
	}
	var modelErr *modelValidationError
	if errors.As(err, &modelErr) {
		return true
	}
	return false
}

// resourceOpTags are the discriminator tags of the ResourceOp union.
var resourceOpTags = []string{
	"add_builtin_sprite", "add_sprite", "delete_sprite", "add_costume",
	"add_backdrop", "add_sound", "delete_sound", "select_backdrop",
}

// decodeResourceOp ports TypeAdapter(ResourceOp).validate_python followed by
// model_dump(mode='json'): the discriminated union member is validated through
// the schema package and returned in its dumped shape.
func decodeResourceOp(value any) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, &schema.SchemaValidationError{Path: "/resource_ops", Message: "Input should be a valid dictionary"}
	}
	tag, _ := object["op"].(string)
	var target any
	switch tag {
	case "add_builtin_sprite":
		target = &schema.AddBuiltinSpriteOp{}
	case "add_sprite":
		target = &schema.AddSpriteOp{}
	case "delete_sprite":
		target = &schema.DeleteSpriteOp{}
	case "add_costume":
		target = &schema.AddCostumeOp{}
	case "add_backdrop":
		target = &schema.AddBackdropOp{}
	case "add_sound":
		target = &schema.AddSoundOp{}
	case "delete_sound":
		target = &schema.DeleteSoundOp{}
	case "select_backdrop":
		target = &schema.SelectBackdropOp{}
	default:
		tags := make([]string, 0, len(resourceOpTags))
		for _, candidate := range resourceOpTags {
			tags = append(tags, "'"+candidate+"'")
		}
		return nil, &schema.SchemaValidationError{
			Path:    "/op",
			Message: "Input tag '" + tag + "' found using 'op' does not match any of the expected tags: " + strings.Join(tags, ", "),
		}
	}
	encoded, err := jsonx.Marshal(object)
	if err != nil {
		return nil, &modelValidationError{err: err}
	}
	if err := schema.DecodeStrict(encoded, target); err != nil {
		return nil, err
	}
	return modelDump(target), nil
}

// ---------------------------------------------------------------------------
// Events and state
// ---------------------------------------------------------------------------

// GenerationEvent ports the frozen GenerationEvent dataclass.
type GenerationEvent struct {
	Type      string
	RequestID string
	Phase     string
	Data      map[string]any
}

// Payload ports GenerationEvent.payload: request_id, an optional phase and the
// optional data object.
func (e GenerationEvent) Payload() map[string]any {
	payload := map[string]any{"request_id": e.RequestID}
	if e.Phase != "" {
		payload["phase"] = e.Phase
	}
	if len(e.Data) > 0 {
		payload["data"] = e.Data
	}
	return payload
}

// programBuffer ports _ProgramBuffer.
type programBuffer struct {
	targetIndex int
	attrs       map[string]string
	metadata    *schema.ProgramMetadata
	sharedState *schema.ProgramState
	scripts     []schema.Script
	total       *int
}

// generationState ports _GenerationState.
type generationState struct {
	requirements           *schema.RequirementsResponse
	taskSpec               *schema.ScratchTaskSpec
	blueprint              *schema.ProjectBlueprint
	plan                   *schema.Plan
	manifest               *schema.ProjectManifest
	programs               map[int]*schema.ProgramIR
	activeProgram          *programBuffer
	phaseStarted           map[string]bool
	phaseCompleted         map[string]bool
	expectedProgramIndices map[int]bool
}

func newGenerationState() *generationState {
	return &generationState{
		programs:       map[int]*schema.ProgramIR{},
		phaseStarted:   map[string]bool{},
		phaseCompleted: map[string]bool{},
	}
}

// segmentAttempt ports _SegmentAttempt.
type segmentAttempt struct {
	value   any
	events  []GenerationEvent
	failure *GenerationEvent
	payload any
}

// segmentValidationFailure ports _SegmentValidationFailure.
type segmentValidationFailure struct{ event *GenerationEvent }

func (e *segmentValidationFailure) Error() string {
	if e.event == nil {
		return "segment validation failed"
	}
	if message, ok := e.event.Data["message"].(string); ok && message != "" {
		return message
	}
	return "segment validation failed"
}

// programDecodeResult is the Go form of the Python decode return value
// tuple[ProgramIR, list[GenerationEvent]].
type programDecodeResult struct {
	program *schema.ProgramIR
	events  []GenerationEvent
}

// manifestDecodeResult is the Go form of the Python decode return value
// tuple[ProjectManifest, list[dict[str, Any]]].
type manifestDecodeResult struct {
	manifest       *schema.ProjectManifest
	normalizations []map[string]any
}

// requestView is the common projection of UnifiedGenerationRequest and
// ProjectProgramGenerationRequest used by the ported helpers. outputContract is
// empty when the attribute is absent, matching getattr(request,
// 'output_contract', None).
type requestView struct {
	requestID           string
	scope               string
	assetPolicy         string
	proceedWithDefaults bool
	permittedExtensions []string
	messages            []prompt.Message
	projectContext      *schema.ProjectContext
	target              schema.TargetContext
	thinkingEnabled     bool
	thinkingStrength    string
	outputContract      string
}

func viewOfUnified(request *schema.UnifiedGenerationRequest) *requestView {
	return &requestView{
		requestID:           request.RequestID,
		scope:               request.Scope,
		assetPolicy:         request.AssetPolicy,
		proceedWithDefaults: request.ProceedWithDefaults,
		permittedExtensions: request.PermittedExtensions,
		messages:            toPromptMessages(request.Messages),
		projectContext:      request.ProjectContext,
		target:              request.Target,
		thinkingEnabled:     request.Thinking.Enabled,
		thinkingStrength:    string(request.Thinking.Strength),
		outputContract:      request.OutputContract,
	}
}

func viewOfProjectProgram(request *schema.ProjectProgramGenerationRequest) *requestView {
	return &requestView{
		requestID:           request.RequestID,
		scope:               request.Scope,
		assetPolicy:         request.AssetPolicy,
		proceedWithDefaults: request.ProceedWithDefaults,
		permittedExtensions: request.PermittedExtensions,
		messages:            toPromptMessages(request.Messages),
		projectContext:      request.ProjectContext,
		target:              request.Target,
		thinkingEnabled:     request.Thinking.Enabled,
		thinkingStrength:    string(request.Thinking.Strength),
		outputContract:      request.OutputContract,
	}
}

func (v *requestView) promptRequest() prompt.Request {
	return prompt.Request{
		Scope:               v.scope,
		AssetPolicy:         v.assetPolicy,
		ProceedWithDefaults: v.proceedWithDefaults,
		PermittedExtensions: v.permittedExtensions,
		TargetMode:          v.target.Mode,
		Messages:            v.messages,
	}
}

func toProviderMessages(messages []prompt.Message) []provider.ChatMessage {
	result := make([]provider.ChatMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, provider.ChatMessage{Role: message.Role, Content: message.Content})
	}
	return result
}

func toPromptMessages(messages []schema.ChatMessage) []prompt.Message {
	result := make([]prompt.Message, 0, len(messages))
	for _, message := range messages {
		result = append(result, prompt.Message{Role: message.Role, Content: message.Content})
	}
	return result
}

// ---------------------------------------------------------------------------
// Provider capability probes
// ---------------------------------------------------------------------------

// segmentProvider is the subset of the text provider the orchestrator uses.
// Python probes the same surface dynamically through getattr.
type segmentProvider interface {
	StreamWithReasoning(
		ctx context.Context,
		messages []provider.ChatMessage,
		thinkingEnabled bool,
		thinkingStrength string,
		jsonOutput bool,
		jsonSchema map[string]any,
	) (<-chan provider.StreamDelta, <-chan error)
	ProviderName() string
}

// structuredOutputProbe mirrors provider.supports_structured_output(segment).
type structuredOutputProbe interface {
	SupportsStructuredOutput(segment string) bool
}

// structuredOutputNoArgProbe mirrors the zero-argument capability fallback the
// Python probe allows when the callable rejects the segment argument.
type structuredOutputNoArgProbe interface {
	SupportsStructuredOutputNoArgs() bool
}

// jsonSchemaProbe mirrors provider.supports_json_schema(segment).
type jsonSchemaProbe interface {
	SupportsJSONSchema(segment string) bool
}

// jsonSchemaNoArgProbe mirrors the zero-argument JSON-schema capability.
type jsonSchemaNoArgProbe interface {
	SupportsJSONSchemaNoArgs() bool
}

// verboseLoggingProbe reports get_settings().ai_blocks_verbose_logging.
type verboseLoggingProbe interface {
	VerboseLogging() bool
}

// textProviderAdapter exposes *provider.TextProvider through segmentProvider.
type textProviderAdapter struct{ provider *provider.TextProvider }

func (a textProviderAdapter) StreamWithReasoning(
	ctx context.Context,
	messages []provider.ChatMessage,
	thinkingEnabled bool,
	thinkingStrength string,
	jsonOutput bool,
	jsonSchema map[string]any,
) (<-chan provider.StreamDelta, <-chan error) {
	return a.provider.StreamWithReasoning(ctx, messages, thinkingEnabled, thinkingStrength, jsonOutput, jsonSchema)
}

func (a textProviderAdapter) ProviderName() string {
	if a.provider == nil {
		return ""
	}
	return a.provider.Provider
}

func (a textProviderAdapter) SupportsStructuredOutput(segment string) bool {
	if a.provider == nil {
		return false
	}
	return a.provider.SupportsStructuredOutput(segment)
}

func (a textProviderAdapter) SupportsJSONSchema(segment string) bool {
	if a.provider == nil {
		return false
	}
	return a.provider.SupportsJSONSchema(segment)
}

func (a textProviderAdapter) VerboseLogging() bool {
	if a.provider == nil || a.provider.Settings == nil {
		return false
	}
	return a.provider.Settings.AIBlocksVerboseLogging
}

// ---------------------------------------------------------------------------
// Orchestrator
// ---------------------------------------------------------------------------

// GenerationOrchestrator ports GenerationOrchestrator: it runs segmented
// project generation and the single-target tagged flow.
type GenerationOrchestrator struct {
	promptBuilder   *prompt.PromptBuilder
	maxRepairRounds int
	maxConcurrency  int
	sourceMu        sync.Mutex
	sourceBudgets   map[string]*sourceBudget
}

// NewGenerationOrchestrator ports GenerationOrchestrator.__init__.
func NewGenerationOrchestrator(promptBuilder *prompt.PromptBuilder, maxRepairRounds int, maxConcurrency int) *GenerationOrchestrator {
	if promptBuilder == nil {
		promptBuilder = prompt.NewPromptBuilder(nil, nil, nil, false)
	}
	if maxRepairRounds < 0 {
		maxRepairRounds = 0
	}
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	return &GenerationOrchestrator{
		promptBuilder:   promptBuilder,
		maxRepairRounds: maxRepairRounds,
		maxConcurrency:  maxConcurrency,
	}
}

// PromptBuilder exposes the builder the orchestrator was constructed with.
func (o *GenerationOrchestrator) PromptBuilder() *prompt.PromptBuilder {
	return o.promptBuilder
}

// MaxRepairRounds reports the configured repair budget.
func (o *GenerationOrchestrator) MaxRepairRounds() int { return o.maxRepairRounds }

// MaxConcurrency reports the configured provider concurrency bound.
func (o *GenerationOrchestrator) MaxConcurrency() int { return o.maxConcurrency }

// emitFunc is one yield of a Python async generator.
type emitFunc func(GenerationEvent) error

// eventSink is a live event sink; it mirrors asyncio.Queue.put_nowait and has
// no failure mode of its own.
type eventSink func(GenerationEvent)

func recordEvent(events *[]GenerationEvent, event GenerationEvent, emitEvent eventSink) {
	*events = append(*events, event)
	if emitEvent != nil {
		emitEvent(event)
	}
}

// streamEvents runs body in a goroutine and exposes the yielded events and the
// escaping error, closing the event channel before the buffered error channel.
func streamEvents(ctx context.Context, body func(emit emitFunc) error) (<-chan GenerationEvent, <-chan error) {
	events := make(chan GenerationEvent)
	errs := make(chan error, 1)
	go func() {
		err := body(func(event GenerationEvent) error {
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil {
			errs <- err
		}
		close(events)
		close(errs)
	}()
	return events, errs
}

// GenerateUnified ports generate_unified.
func (o *GenerationOrchestrator) GenerateUnified(
	ctx context.Context,
	request *schema.UnifiedGenerationRequest,
	textProvider *provider.TextProvider,
) (<-chan GenerationEvent, <-chan error) {
	view := viewOfUnified(request)
	p := textProviderAdapter{provider: textProvider}
	return streamEvents(ctx, func(emit emitFunc) error {
		if request.PipelineVersion == "scratch-source/1" {
			if textProvider == nil || textProvider.Settings == nil || !textProvider.Settings.AIBlocksSourcePipelineEnabled {
				return sourceFailure(emit, request, "requirements", errors.New("source pipeline disabled"))
			}
			return o.generateSourceUnified(ctx, request, textProviderAdapter{provider: textProvider.ForSourceAuthoring()}, emit)
		}
		return o.generateUnified(ctx, view, p, emit)
	})
}

// GenerateProjectProgram ports generate_project_program.
func (o *GenerationOrchestrator) GenerateProjectProgram(
	ctx context.Context,
	request *schema.ProjectProgramGenerationRequest,
	textProvider *provider.TextProvider,
) (<-chan GenerationEvent, <-chan error) {
	view := viewOfProjectProgram(request)
	p := textProviderAdapter{provider: textProvider}
	return streamEvents(ctx, func(emit emitFunc) error {
		return o.generateProjectProgram(ctx, view, request, p, emit)
	})
}

// _repair_budget ports _repair_budget: every segment gets the configured repair
// budget, counted as rounds after the initial request.
func (o *GenerationOrchestrator) repairBudget(_ string) int {
	return o.maxRepairRounds
}

// generateUnified ports generate_unified.
func (o *GenerationOrchestrator) generateUnified(
	ctx context.Context,
	request *requestView,
	p segmentProvider,
	emit emitFunc,
) error {
	if request.scope == "project" {
		return o.generateSegmentedProject(ctx, request, p, emit)
	}
	// Providers with a JSON transport get the same phase isolation for a single
	// target as project mode. Keeping Agnes on the legacy tagged stream
	// preserves its existing one-request compatibility path.
	if o.structuredOutputSupported(p, "requirements") {
		return o.generateSegmentedSingle(ctx, request, p, emit)
	}
	return o.generateTagged(ctx, request, p, emit)
}

// generateSegmentedSingle ports _generate_segmented_single.
func (o *GenerationOrchestrator) generateSegmentedSingle(
	ctx context.Context,
	request *requestView,
	p segmentProvider,
	emit emitFunc,
) error {
	project := o.projectContext(request)
	target, err := o.targetContext(request)
	if err != nil {
		return err
	}

	requirementsContext := map[string]any{"scope": request.scope, "contract_version": 3}
	requirementsValue, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "requirements",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "requirements", context, nil, nil)
		},
		func(frames []StreamFrame) (any, error) {
			frame, err := singleSegmentFrame(frames, "requirements")
			if err != nil {
				return nil, err
			}
			parsed, err := ParseJSON(frame.Content)
			if err != nil {
				return nil, err
			}
			return decodeModel[schema.RequirementsResponse](parsed)
		},
		requirementsContext,
		o.repairBudget("requirements"),
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	requirements, ok := requirementsValue.(*schema.RequirementsResponse)
	if failure != nil || !ok {
		return emit(o.terminalValidationError(request, failure))
	}
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "requirements"}); err != nil {
		return err
	}
	if err := emit(GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "requirements", Data: modelDump(requirements)}); err != nil {
		return err
	}
	if !request.proceedWithDefaults && !requirements.Ready {
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "REQUIREMENTS_INCOMPLETE", "message": "需求信息还不完整",
		}})
	}

	planContext := map[string]any{
		"requirements":     modelDump(requirements),
		"scope":            request.scope,
		"contract_version": 1,
	}
	planValue, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "plan",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "plan", context, nil, nil)
		},
		func(frames []StreamFrame) (any, error) {
			frame, err := singleSegmentFrame(frames, "plan")
			if err != nil {
				return nil, err
			}
			parsed, err := ParseJSON(frame.Content)
			if err != nil {
				return nil, err
			}
			return decodeModel[schema.Plan](parsed)
		},
		planContext,
		o.repairBudget("plan"),
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	plan, ok := planValue.(*schema.Plan)
	if failure != nil || !ok {
		return emit(o.terminalValidationError(request, failure))
	}
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "plan"}); err != nil {
		return err
	}
	if err := emit(GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "plan", Data: modelDump(plan)}); err != nil {
		return err
	}

	targetData := map[string]any{"target_index": 0, "target_name": target.Name, "target_count": 1}
	programContext := map[string]any{
		"requirements": modelDump(requirements),
		"plan":         modelDump(plan),
		"target_index": 0,
	}

	decodeProgram := func(frames []StreamFrame) (any, error) {
		if len(frames) == 1 && frames[0].Kind == "program" {
			parsed, err := ParseJSON(frames[0].Content)
			if err != nil {
				return nil, err
			}
			output, err := decodeModel[schema.ProgramIR](parsed)
			if err != nil {
				return nil, err
			}
			targetKind := "sprite"
			if request.target.IsStage {
				targetKind = "stage"
			}
			if _, err := schema.ValidateProgramContracts(output, schema.ProgramContractOptions{
				TargetKind:        targetKind,
				AllowedExtensions: map[string]bool{},
				AllowResourceRefs: false,
			}); err != nil {
				return nil, err
			}
			if _, err := schema.ValidateProgramForMode(output, request.target.Mode, request.target.CurrentProgram); err != nil {
				return nil, err
			}
			dumped := modelDump(output)
			childEvents := []GenerationEvent{
				{Type: "metadata", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
					"schema_version":    dumped["schema_version"],
					"summary":           dumped["summary"],
					"remove_script_ids": dumped["remove_script_ids"],
					"procedures":        dumped["procedures"],
				})},
				{Type: "shared_state", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
					"variables":  dumped["variables"],
					"lists":      dumped["lists"],
					"broadcasts": dumped["broadcasts"],
				})},
			}
			scripts, _ := dumped["scripts"].([]any)
			for index, scriptValue := range scripts {
				childEvents = append(childEvents, GenerationEvent{Type: "script_ready", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
					"index":  index,
					"total":  len(output.Scripts),
					"script": scriptValue,
				})})
			}
			childEvents = append(childEvents, GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
				"script_count": len(output.Scripts),
				"summary":      output.Summary,
			})})
			return programDecodeResult{program: output, events: childEvents}, nil
		}

		state := newGenerationState()
		state.requirements = requirements
		state.plan = plan
		state.phaseStarted = map[string]bool{"requirements": true, "plan": true}
		state.phaseCompleted = map[string]bool{"requirements": true, "plan": true}
		state.expectedProgramIndices = map[int]bool{0: true}
		childEvents := []GenerationEvent{}
		selected, err := programSegmentFrames(frames, 0)
		if err != nil {
			return nil, err
		}
		for _, frame := range selected {
			frameEvents, frameFailure, err := o.consumeFrame(request, state, frame, false)
			if err != nil {
				return nil, err
			}
			if frameFailure != nil {
				return nil, &segmentValidationFailure{event: frameFailure}
			}
			childEvents = append(childEvents, frameEvents...)
		}
		if _, present := state.programs[0]; !present {
			return nil, newStreamProtocolError("program segment did not produce a complete target")
		}
		return programDecodeResult{program: state.programs[0], events: childEvents}, nil
	}

	index := 0
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "program", Data: targetData}); err != nil {
		return err
	}
	programValue, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "program",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "program", context, &index, nil)
		},
		decodeProgram,
		programContext,
		o.repairBudget("program"),
		&index, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type == "phase_start" {
			continue
		}
		if err := emit(GenerationEvent{Type: event.Type, RequestID: event.RequestID, Phase: event.Phase, Data: mergeAnyMaps(targetData, event.Data)}); err != nil {
			return err
		}
	}
	if failure != nil {
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: mergeAnyMaps(map[string]any{
			"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
		}, targetData)})
	}
	program, ok := programValue.(*schema.ProgramIR)
	if !ok {
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: mergeAnyMaps(map[string]any{
			"code": "PROGRAM_MISSING", "message": "AI 未完成当前目标程序输出",
		}, targetData)})
	}
	return emit(GenerationEvent{Type: "complete", RequestID: request.requestID, Data: mergeAnyMaps(targetData, map[string]any{
		"scope":         "single_sprite",
		"program_count": 1,
		"program":       modelDump(program),
	})})
}

// generateTagged ports _generate_tagged.
func (o *GenerationOrchestrator) generateTagged(
	ctx context.Context,
	request *requestView,
	p segmentProvider,
	emit emitFunc,
) error {
	project := o.projectContext(request)
	target, err := o.targetContext(request)
	if err != nil {
		return err
	}
	messages, cache := o.promptBuilder.BuildUnifiedPrompt(project, target, request.promptRequest(), "tagged", "")
	if err := emit(GenerationEvent{Type: "cache_hit", RequestID: request.requestID, Data: cache.AsDict()}); err != nil {
		return err
	}
	state := newGenerationState()
	parser := NewUnifiedFrameParser()
	contentChunkCount := 0
	var repairFailure *GenerationEvent
	resumeKind := "requirements"
	rounds := 1 + o.maxRepairRounds
	failed := false

	for attempt := 0; attempt < rounds; attempt++ {
		var attemptMessages []prompt.Message
		if attempt > 0 {
			if repairFailure == nil {
				return errors.New("repair failure was not recorded")
			}
			if err := emit(GenerationEvent{Type: "repair", RequestID: request.requestID, Data: map[string]any{
				"attempt":    attempt,
				"max_rounds": o.maxRepairRounds,
				"phase":      repairFailure.Phase,
				"path":       repairFailure.Data["path"],
				"message":    repairFailure.Data["message"],
				"detail":     repairFailure.Data["detail"],
				"resume":     resumeKind,
			}}); err != nil {
				return err
			}
			state = resumeState(state, resumeKind)
			parser = NewUnifiedFrameParser()
			attemptMessages = repairMessages(messages, repairFailure, resumeKind)
		} else {
			attemptMessages = messages
		}
		failed = false
		streamFailed := false

		handleFrames := func(frames []StreamFrame) error {
			for _, frame := range frames {
				events, failure, err := o.consumeFrame(request, state, frame, attempt > 0)
				if err != nil {
					return err
				}
				for _, event := range events {
					if err := emit(event); err != nil {
						return err
					}
				}
				if failure != nil {
					repairFailure = failure
					resumeKind = resumeTag(frame.Kind)
					failed = true
					break
				}
			}
			return nil
		}

		streamFailed = false
		streamErr := func() error {
			streamCtx, streamCancel := context.WithCancel(ctx)
			defer streamCancel()
			stream, errs := p.StreamWithReasoning(
				streamCtx, toProviderMessages(attemptMessages), request.thinkingEnabled, request.thinkingStrength, false, nil,
			)
			for delta := range stream {
				switch {
				case delta.Kind == provider.DeltaReasoning && delta.Text != "":
					if err := emit(GenerationEvent{Type: "reasoning_delta", RequestID: request.requestID, Data: map[string]any{"text": delta.Text}}); err != nil {
						return err
					}
					continue
				case delta.Kind == provider.DeltaUsage && len(delta.Usage) > 0:
					if err := emit(GenerationEvent{Type: "usage", RequestID: request.requestID, Data: usageData(delta.Usage)}); err != nil {
						return err
					}
					continue
				case delta.Kind != provider.DeltaContent || delta.Text == "":
					continue
				}
				contentChunkCount++
				frames, err := parser.Feed(delta.Text)
				if err != nil {
					streamFailed = true
					if emitErr := emit(failureEvent(request.requestID, "OUTPUT_PROTOCOL_ERROR")); emitErr != nil {
						return emitErr
					}
					break
				}
				if err := handleFrames(frames); err != nil {
					return err
				}
				if failed {
					break
				}
				if len(state.phaseStarted) > 0 {
					if err := emit(GenerationEvent{Type: "content_delta", RequestID: request.requestID, Data: map[string]any{"text": delta.Text}}); err != nil {
						return err
					}
				}
			}
			if streamFailed || failed {
				// The attempt is abandoned mid-stream: cancel the provider
				// request and drain it so its goroutine can close both
				// channels.
				abandonProviderStream(streamCancel, stream)
				return nil
			}
			return drainProviderError(errs)
		}()
		if streamErr != nil {
			return streamErr
		}
		if streamFailed {
			return nil
		}
		if !failed {
			trailing, err := parser.Finish()
			if err != nil {
				return emit(failureEvent(request.requestID, "OUTPUT_INCOMPLETE"))
			}
			if err := handleFrames(trailing); err != nil {
				return err
			}
		}
		if failed {
			if repairFailure != nil {
				logger.Warn("统一生成 校验失败 触发修复",
					"attempt", attempt, "resume", resumeKind, "phase", repairFailure.Phase)
			}
			continue
		}
		break
	}

	if !failed {
		failure := o.finish(request, state)
		if failure != nil {
			if err := emit(*failure); err != nil {
				return err
			}
			if failure.Type == "validation_error" {
				return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
					"code": "OUTPUT_INCOMPLETE", "message": "AI 输出未完成",
				}})
			}
			return nil
		}
		if state.phaseStarted["program"] && !state.phaseCompleted["program"] {
			if err := emit(GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "program", Data: map[string]any{
				"program_count":  len(state.programs),
				"target_indices": sortedProgramIndices(state.programs),
			}}); err != nil {
				return err
			}
		}
		logger.Info("统一生成 完成", "scope", request.scope, "program_count", len(state.programs), "content_chunks", contentChunkCount)
		return emit(GenerationEvent{Type: "complete", RequestID: request.requestID, Data: map[string]any{
			"scope":         request.scope,
			"program_count": len(state.programs),
		}})
	}

	if repairFailure == nil {
		return errors.New("repair failure was not recorded")
	}
	if err := emit(*repairFailure); err != nil {
		return err
	}
	return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
		"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
	}})
}

// generateProjectProgram ports generate_project_program.
func (o *GenerationOrchestrator) generateProjectProgram(
	ctx context.Context,
	request *requestView,
	contract *schema.ProjectProgramGenerationRequest,
	p segmentProvider,
	emit emitFunc,
) error {
	project := o.projectContext(request)
	// The Python reference reads these fields with getattr on a
	// UnifiedGenerationRequest and reports INVALID_PROJECT_PROGRAM_REQUEST when
	// the manifest context is absent. Go's typed request always carries the
	// fields, so the guard degenerates to the nil envelope check performed by
	// the public wrapper; a malformed target selection is still caught below.
	manifest := &contract.Manifest
	requirements := &contract.Requirements
	taskSpec := &contract.TaskSpec
	blueprint := &contract.Blueprint
	plan := &contract.Plan
	targetIndex := contract.TargetIndex
	programTarget := &contract.ProgramTarget

	if targetIndex < 0 || targetIndex >= len(manifest.ProgramTargets) {
		// The Python reference indexes manifest.program_targets directly here,
		// so an out-of-range index escapes the generator instead of becoming an
		// INVALID_PROJECT_PROGRAM_REQUEST event.
		return newStreamProtocolError("program target_index is not present in the manifest")
	}
	target, err := o.targetContextForManifestTarget(request, manifest, targetIndex, &programTarget.Mode)
	if err != nil {
		return err
	}
	if request.target.Name != target.Name || request.target.IsStage != target.IsStage {
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "INVALID_PROJECT_PROGRAM_REQUEST", "message": "项目程序请求目标与清单目标不一致",
		}})
	}
	targetCount := len(manifest.ProgramTargets)
	targetData := map[string]any{
		"target_index": targetIndex,
		"target_name":  target.Name,
		"target_count": targetCount,
	}
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "program", Data: targetData}); err != nil {
		return err
	}
	state := newGenerationState()
	state.requirements = requirements
	state.taskSpec = taskSpec
	state.blueprint = blueprint
	state.plan = plan
	state.manifest = manifest
	state.phaseStarted = map[string]bool{"requirements": true, "plan": true}
	state.phaseCompleted = map[string]bool{"requirements": true, "plan": true}
	state.expectedProgramIndices = map[int]bool{targetIndex: true}

	taskSpecHash, err := schema.ContractSHA256(taskSpec)
	if err != nil {
		return err
	}
	blueprintHash, err := schema.ContractSHA256(blueprint)
	if err != nil {
		return err
	}
	programContext := map[string]any{
		"requirements":   modelDump(requirements),
		"task_spec":      modelDump(taskSpec),
		"task_spec_hash": taskSpecHash,
		"blueprint":      modelDump(blueprint),
		"blueprint_hash": blueprintHash,
		"plan":           modelDump(plan),
		"manifest":       modelDump(manifest),
		"target":         modelDump(programTarget),
	}

	decodeProgram := func(frames []StreamFrame) (any, error) {
		if len(frames) == 1 && frames[0].Kind == "program" {
			parsed, err := ParseJSON(frames[0].Content)
			if err != nil {
				return nil, err
			}
			output, err := decodeModel[schema.ProgramIR](parsed)
			if err != nil {
				return nil, err
			}
			targetKind, mode, currentProgram, err := o.targetContract(request, state, targetIndex)
			if err != nil {
				return nil, err
			}
			if _, err := schema.ValidateProgramContracts(output, schema.ProgramContractOptions{
				TargetKind:        targetKind,
				AllowedExtensions: stringSet(manifest.RequiredExtensions),
			}); err != nil {
				return nil, err
			}
			if _, err := schema.ValidateProgramForMode(output, mode, currentProgram); err != nil {
				return nil, err
			}
			if _, err := schema.ValidateProjectProgramSharedState(output, &manifest.SharedState, targetKind); err != nil {
				return nil, err
			}
			if _, err := schema.ValidateProjectProgramResourceRefs(output, manifest, o.existingAssetsForTarget(request, target)); err != nil {
				return nil, err
			}
			if _, err := schema.ValidateProjectProgramStateRefs(output, manifest); err != nil {
				return nil, err
			}
			state.programs[targetIndex] = output
			dumped := modelDump(output)
			childEvents := []GenerationEvent{
				{Type: "metadata", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
					"schema_version":    dumped["schema_version"],
					"summary":           dumped["summary"],
					"remove_script_ids": dumped["remove_script_ids"],
					"procedures":        dumped["procedures"],
				})},
				{Type: "shared_state", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
					"variables":  dumped["variables"],
					"lists":      dumped["lists"],
					"broadcasts": dumped["broadcasts"],
				})},
			}
			scripts, _ := dumped["scripts"].([]any)
			for index, scriptValue := range scripts {
				childEvents = append(childEvents, GenerationEvent{Type: "script_ready", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
					"index":  index,
					"total":  len(output.Scripts),
					"script": scriptValue,
				})})
			}
			childEvents = append(childEvents, GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(targetData, map[string]any{
				"script_count": len(output.Scripts),
				"summary":      output.Summary,
			})})
			return programDecodeResult{program: output, events: childEvents}, nil
		}

		local := newGenerationState()
		local.requirements = requirements
		local.taskSpec = taskSpec
		local.blueprint = blueprint
		local.plan = plan
		local.manifest = manifest
		local.phaseStarted = map[string]bool{"requirements": true, "plan": true}
		local.phaseCompleted = map[string]bool{"requirements": true, "plan": true}
		local.expectedProgramIndices = map[int]bool{targetIndex: true}
		childEvents := []GenerationEvent{}
		selected, err := programSegmentFrames(frames, targetIndex)
		if err != nil {
			return nil, err
		}
		for _, frame := range selected {
			frameEvents, frameFailure, err := o.consumeFrame(request, local, frame, false)
			if err != nil {
				return nil, err
			}
			if frameFailure != nil {
				return nil, &segmentValidationFailure{event: frameFailure}
			}
			childEvents = append(childEvents, frameEvents...)
		}
		if _, present := local.programs[targetIndex]; !present {
			return nil, newStreamProtocolError("program segment did not produce a complete target")
		}
		return programDecodeResult{program: local.programs[targetIndex], events: childEvents}, nil
	}

	value, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "program",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "program", context, &targetIndex, nil)
		},
		decodeProgram,
		programContext,
		o.repairBudget("program"),
		&targetIndex, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type == "phase_start" {
			continue
		}
		if err := emit(GenerationEvent{Type: event.Type, RequestID: event.RequestID, Phase: event.Phase, Data: mergeAnyMaps(targetData, event.Data)}); err != nil {
			return err
		}
	}
	if failure != nil {
		if err := emit(GenerationEvent{Type: failure.Type, RequestID: failure.RequestID, Phase: failure.Phase, Data: mergeAnyMaps(targetData, failure.Data)}); err != nil {
			return err
		}
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: mergeAnyMaps(map[string]any{
			"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
		}, targetData)})
	}
	program, ok := value.(*schema.ProgramIR)
	if !ok {
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: mergeAnyMaps(map[string]any{
			"code": "PROGRAM_MISSING", "message": "AI 未完成当前目标程序输出",
		}, targetData)})
	}
	return emit(GenerationEvent{Type: "complete", RequestID: request.requestID, Data: mergeAnyMaps(targetData, map[string]any{
		"scope":           "project",
		"output_contract": "project_program",
		"program_count":   1,
		"program":         modelDump(program),
	})})
}

// generateSegmentedProject ports _generate_segmented_project.
func (o *GenerationOrchestrator) generateSegmentedProject(
	ctx context.Context,
	request *requestView,
	p segmentProvider,
	emit emitFunc,
) error {
	project := o.projectContext(request)
	target, err := o.targetContext(request)
	if err != nil {
		return err
	}
	state := newGenerationState()

	requirementsContext := map[string]any{"scope": request.scope, "contract_version": 3}
	requirementsValue, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "requirements",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "requirements", context, nil, nil)
		},
		func(frames []StreamFrame) (any, error) {
			frame, err := singleSegmentFrame(frames, "requirements")
			if err != nil {
				return nil, err
			}
			parsed, err := ParseJSON(frame.Content)
			if err != nil {
				return nil, err
			}
			return decodeModel[schema.RequirementsResponse](parsed)
		},
		requirementsContext,
		o.repairBudget("requirements"),
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	requirements, ok := requirementsValue.(*schema.RequirementsResponse)
	if failure != nil || !ok {
		if failure != nil {
			if err := emit(*failure); err != nil {
				return err
			}
		}
		return emit(o.terminalValidationError(request, failure))
	}
	state.requirements = requirements
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "requirements"}); err != nil {
		return err
	}
	if err := emit(GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "requirements", Data: modelDump(requirements)}); err != nil {
		return err
	}
	if !request.proceedWithDefaults && !requirements.Ready {
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "REQUIREMENTS_INCOMPLETE", "message": "需求信息还不完整",
		}})
	}

	taskContext := map[string]any{
		"requirements":     modelDump(requirements),
		"scope":            request.scope,
		"contract_version": 1,
	}
	taskSpecValue, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "task_spec",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "task_spec", context, nil, nil)
		},
		func(frames []StreamFrame) (any, error) {
			frame, err := singleSegmentFrame(frames, "task_spec")
			if err != nil {
				return nil, err
			}
			parsed, err := ParseJSON(frame.Content)
			if err != nil {
				return nil, err
			}
			return decodeModel[schema.ScratchTaskSpec](parsed)
		},
		taskContext,
		o.repairBudget("task_spec"),
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	taskSpec, ok := taskSpecValue.(*schema.ScratchTaskSpec)
	if failure != nil || !ok {
		if failure != nil {
			if err := emit(*failure); err != nil {
				return err
			}
		}
		return emit(o.terminalValidationError(request, failure))
	}
	state.taskSpec = taskSpec
	taskHash, err := schema.ContractSHA256(taskSpec)
	if err != nil {
		return err
	}
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "task_spec"}); err != nil {
		return err
	}
	if err := emit(GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "task_spec", Data: map[string]any{
		"task_spec":      modelDump(taskSpec),
		"task_spec_hash": taskHash,
	}}); err != nil {
		return err
	}

	blueprintContext := map[string]any{
		"task_spec":      modelDump(taskSpec),
		"task_spec_hash": taskHash,
		"requirements":   modelDump(requirements),
	}

	// decodeBlueprint keeps lineage validation inside the segment retry
	// boundary. A schema-valid blueprint can still reference the wrong
	// TaskSpec; that is model-repairable output, not a terminal orchestrator
	// failure.
	decodeBlueprint := func(frames []StreamFrame) (any, error) {
		frame, err := singleSegmentFrame(frames, "blueprint")
		if err != nil {
			return nil, err
		}
		rawBlueprint, err := ParseJSON(frame.Content)
		if err != nil {
			return nil, err
		}
		normalizedBlueprint := schema.NormalizeProjectBlueprintPayload(rawBlueprint)
		blueprintValue, err := decodeModel[schema.ProjectBlueprint](normalizedBlueprint)
		if err != nil {
			return nil, err
		}
		if err := schema.ValidateTaskSpecBlueprintLineage(taskSpec, blueprintValue); err != nil {
			return nil, err
		}
		return blueprintValue, nil
	}

	blueprintValue, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "blueprint",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "blueprint", context, nil, nil)
		},
		decodeBlueprint,
		blueprintContext,
		o.repairBudget("blueprint"),
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	blueprint, ok := blueprintValue.(*schema.ProjectBlueprint)
	if failure != nil || !ok {
		if failure != nil {
			if err := emit(*failure); err != nil {
				return err
			}
		}
		return emit(o.terminalValidationError(request, failure))
	}
	state.blueprint = blueprint
	blueprintHash, err := schema.ContractSHA256(blueprint)
	if err != nil {
		return err
	}
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "blueprint"}); err != nil {
		return err
	}
	if err := emit(GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "blueprint", Data: map[string]any{
		"blueprint":      modelDump(blueprint),
		"task_spec_hash": taskHash,
		"blueprint_hash": blueprintHash,
	}}); err != nil {
		return err
	}

	planContext := map[string]any{"requirements": modelDump(requirements), "task_spec": modelDump(taskSpec)}
	planValue, events, failure, _, err := o.runSegmentWithRepair(
		ctx, request, p, "plan",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "plan", context, nil, nil)
		},
		func(frames []StreamFrame) (any, error) {
			frame, err := singleSegmentFrame(frames, "plan")
			if err != nil {
				return nil, err
			}
			parsed, err := ParseJSON(frame.Content)
			if err != nil {
				return nil, err
			}
			return decodeModel[schema.Plan](parsed)
		},
		planContext,
		o.repairBudget("plan"),
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	plan, ok := planValue.(*schema.Plan)
	if failure != nil || !ok {
		if failure != nil {
			if err := emit(*failure); err != nil {
				return err
			}
		}
		return emit(o.terminalValidationError(request, failure))
	}
	state.plan = plan
	if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "plan"}); err != nil {
		return err
	}

	manifestContext := map[string]any{
		"requirements":   modelDump(requirements),
		"task_spec":      modelDump(taskSpec),
		"task_spec_hash": taskHash,
		"blueprint":      modelDump(blueprint),
		"blueprint_hash": blueprintHash,
		"plan":           modelDump(plan),
	}
	manifestBudget := o.repairBudget("manifest")

	decodeManifest := func(frames []StreamFrame) (any, error) {
		frame, err := singleSegmentFrame(frames, "manifest")
		if err != nil {
			return nil, err
		}
		rawManifest, err := ParseJSON(frame.Content)
		if err != nil {
			return nil, err
		}
		normalizedManifest, normalizations, err := schema.NormalizeProjectManifestPayload(rawManifest)
		if err != nil {
			return nil, err
		}
		manifest, err := decodeModel[schema.ProjectManifest](normalizedManifest)
		if err != nil {
			return nil, err
		}
		if err := schema.ValidateRequestExtensionPermissions(request.extensionPermissionRequest(), manifest); err != nil {
			return nil, err
		}
		if err := schema.ValidateBlueprintManifestLineage(blueprint, manifest); err != nil {
			return nil, err
		}
		return manifestDecodeResult{manifest: manifest, normalizations: normalizationsAsDict(normalizations)}, nil
	}

	manifestValue, events, failure, failedManifestPayload, err := o.runSegmentWithRepair(
		ctx, request, p, "manifest",
		func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
			return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "manifest", context, nil, nil)
		},
		decodeManifest,
		manifestContext,
		0,
		nil, nil, nil,
	)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}

	var manifestNormalizations []map[string]any
	if failure != nil {
		repairScope := orString(failure.Data["repair_scope"])
		var operationIndex *int
		if repairScope == "resource_op" {
			operationIndex = resourceOpIndex(failure.Data["path"])
		}
		failedManifest, isManifestPayload := failedManifestPayload.(map[string]any)
		if operationIndex != nil && isManifestPayload {
			operationFailure := failure
			repaired := false
			resourceBudget := o.repairBudget("resource_op")
			operationContext := map[string]any{
				"resource_op_index": *operationIndex,
				"resource_op":       payloadAt(failedManifest, *operationIndex),
				"failure":           operationFailure.Data,
			}
			for attempt := 1; attempt <= resourceBudget; attempt++ {
				if err := emit(o.repairEvent(request, *operationFailure, "resource_op", attempt, resourceBudget, nil, operationIndex)); err != nil {
					return err
				}
				opIndex := *operationIndex
				repairedOpValue, opEvents, opFailure, failedOpPayload, err := o.runSegmentWithRepair(
					ctx, request, p, "resource_op",
					func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
						return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "resource_op", context, nil, &opIndex)
					},
					func(frames []StreamFrame) (any, error) {
						frame, err := singleSegmentFrame(frames, "resource_op")
						if err != nil {
							return nil, err
						}
						parsed, err := ParseJSON(frame.Content)
						if err != nil {
							return nil, err
						}
						return decodeResourceOp(parsed)
					},
					operationContext,
					0,
					nil, &opIndex, nil,
				)
				if err != nil {
					return err
				}
				repairedOp, isOp := repairedOpValue.(map[string]any)
				if opFailure != nil || !isOp {
					if opFailure != nil {
						operationFailure = opFailure
					} else {
						operationFailure = failure
					}
					operationContext = mergeAnyMaps(operationContext, map[string]any{"failure": operationFailure.Data})
					if failedOp, ok := failedOpPayload.(map[string]any); ok {
						operationContext["resource_op"] = failedOp
					}
					continue
				}
				for _, event := range opEvents {
					if err := emit(event); err != nil {
						return err
					}
				}
				mergedPayload := cloneAnyMap(failedManifest)
				operations, _ := mergedPayload["resource_ops"].([]any)
				copied := make([]any, len(operations))
				copy(copied, operations)
				if *operationIndex >= len(copied) || *operationIndex < 0 {
					return errors.New("list assignment index out of range")
				}
				copied[*operationIndex] = repairedOp
				mergedPayload["resource_ops"] = copied
				normalizedManifest, normalizations, normalizeErr := schema.NormalizeProjectManifestPayload(mergedPayload)
				var mergeErr error
				var repairedManifest *schema.ProjectManifest
				if normalizeErr != nil {
					mergeErr = normalizeErr
				} else {
					repairedManifest, mergeErr = decodeModel[schema.ProjectManifest](normalizedManifest)
				}
				if mergeErr == nil {
					mergeErr = schema.ValidateRequestExtensionPermissions(request.extensionPermissionRequest(), repairedManifest)
				}
				if mergeErr == nil {
					mergeErr = schema.ValidateBlueprintManifestLineage(blueprint, repairedManifest)
				}
				if mergeErr != nil {
					operationFailure = o.validationEvent(request, "plan", mergeErr, StreamFrame{Kind: "manifest"})
					operationContext = mergeAnyMaps(operationContext, map[string]any{"failure": operationFailure.Data})
					continue
				}
				manifestValue = manifestDecodeResult{manifest: repairedManifest, normalizations: normalizationsAsDict(normalizations)}
				repaired = true
				break
			}
			if !repaired {
				if err := emit(*operationFailure); err != nil {
					return err
				}
				return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
					"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
				}})
			}
		} else {
			if manifestBudget <= 0 {
				if err := emit(*failure); err != nil {
					return err
				}
				return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
					"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
				}})
			}
			if err := emit(o.repairEvent(request, *failure, "manifest", 1, manifestBudget, nil, nil)); err != nil {
				return err
			}
			retryContext := mergeAnyMaps(manifestContext, map[string]any{"failure": failure.Data})
			retryValue, retryEvents, retryFailure, _, err := o.runSegmentWithRepair(
				ctx, request, p, "manifest",
				func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
					return o.promptBuilder.BuildSegmentPrompt(project, target, request.promptRequest(), "manifest", context, nil, nil)
				},
				decodeManifest,
				retryContext,
				0,
				nil, nil, nil,
			)
			if err != nil {
				return err
			}
			for _, event := range retryEvents {
				if err := emit(event); err != nil {
					return err
				}
			}
			if retryFailure != nil {
				if err := emit(*retryFailure); err != nil {
					return err
				}
				return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
					"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
				}})
			}
			manifestValue = retryValue
		}
	}
	decodedManifest, isManifest := manifestValue.(manifestDecodeResult)
	if !isManifest {
		return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "MANIFEST_MISSING", "message": "AI 未完成项目清单输出",
		}})
	}
	state.manifest = decodedManifest.manifest
	manifestNormalizations = decodedManifest.normalizations
	if err := emit(GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "plan", Data: map[string]any{
		"steps":          state.plan.Steps,
		"task_spec":      modelDump(taskSpec),
		"task_spec_hash": taskHash,
		"blueprint":      modelDump(blueprint),
		"blueprint_hash": blueprintHash,
		"manifest":       modelDump(state.manifest),
		"normalizations": manifestNormalizations,
	}}); err != nil {
		return err
	}
	if request.outputContract == "project_manifest" {
		return emit(GenerationEvent{Type: "complete", RequestID: request.requestID, Data: map[string]any{
			"scope":           "project",
			"output_contract": "project_manifest",
			"program_count":   0,
		}})
	}

	targetCount := len(state.manifest.ProgramTargets)
	if targetCount == 0 {
		return emit(GenerationEvent{Type: "complete", RequestID: request.requestID, Data: map[string]any{
			"scope": request.scope, "program_count": 0,
		}})
	}

	state.phaseStarted["program"] = true
	semaphore := make(chan struct{}, o.maxConcurrency)

	type targetResult struct {
		index   int
		value   any
		failure *GenerationEvent
	}

	runTarget := func(batchCtx context.Context, index int, emitEvent eventSink) (targetResult, error) {
		select {
		case semaphore <- struct{}{}:
		case <-batchCtx.Done():
			return targetResult{}, batchCtx.Err()
		}
		defer func() { <-semaphore }()

		targetContextValue, err := o.targetContextForManifestTarget(request, state.manifest, index, nil)
		if err != nil {
			return targetResult{}, err
		}

		emitTargetEvent := func(event GenerationEvent) {
			if emitEvent == nil || event.Type == "phase_start" {
				return
			}
			emitEvent(GenerationEvent{
				Type:      event.Type,
				RequestID: event.RequestID,
				Phase:     event.Phase,
				Data: mergeAnyMaps(map[string]any{
					"target_index": index,
					"target_name":  targetContextValue.Name,
					"target_count": targetCount,
				}, event.Data),
			})
		}

		targetHash, err := schema.ContractSHA256(taskSpec)
		if err != nil {
			return targetResult{}, err
		}
		blueprintTargetHash, err := schema.ContractSHA256(blueprint)
		if err != nil {
			return targetResult{}, err
		}
		programContext := map[string]any{
			"requirements":   modelDump(requirements),
			"task_spec":      modelDump(taskSpec),
			"task_spec_hash": targetHash,
			"blueprint":      modelDump(blueprint),
			"blueprint_hash": blueprintTargetHash,
			"plan":           modelDump(plan),
			"manifest":       modelDump(state.manifest),
			"target":         modelDump(&state.manifest.ProgramTargets[index]),
		}

		decodeProgram := func(frames []StreamFrame) (any, error) {
			if len(frames) == 1 && frames[0].Kind == "program" {
				parsed, err := ParseJSON(frames[0].Content)
				if err != nil {
					return nil, err
				}
				output, err := decodeModel[schema.ProgramIR](parsed)
				if err != nil {
					return nil, err
				}
				targetKind, mode, currentProgram, err := o.targetContract(request, state, index)
				if err != nil {
					return nil, err
				}
				if _, err := schema.ValidateProgramContracts(output, schema.ProgramContractOptions{
					TargetKind:        targetKind,
					AllowedExtensions: stringSet(state.manifest.RequiredExtensions),
				}); err != nil {
					return nil, err
				}
				if _, err := schema.ValidateProgramForMode(output, mode, currentProgram); err != nil {
					return nil, err
				}
				if _, err := schema.ValidateProjectProgramSharedState(output, &state.manifest.SharedState, targetKind); err != nil {
					return nil, err
				}
				if _, err := schema.ValidateProjectProgramResourceRefs(output, state.manifest, o.existingAssetsForTarget(request, targetContextValue)); err != nil {
					return nil, err
				}
				if _, err := schema.ValidateProjectProgramStateRefs(output, state.manifest); err != nil {
					return nil, err
				}
				dumped := modelDump(output)
				total := len(output.Scripts)
				childEvents := []GenerationEvent{
					{Type: "metadata", RequestID: request.requestID, Phase: "program", Data: map[string]any{
						"target_index":      index,
						"target_name":       targetContextValue.Name,
						"target_count":      targetCount,
						"schema_version":    dumped["schema_version"],
						"summary":           dumped["summary"],
						"remove_script_ids": dumped["remove_script_ids"],
						"procedures":        dumped["procedures"],
					}},
					{Type: "shared_state", RequestID: request.requestID, Phase: "program", Data: map[string]any{
						"target_index": index,
						"target_name":  targetContextValue.Name,
						"target_count": targetCount,
						"variables":    dumped["variables"],
						"lists":        dumped["lists"],
						"broadcasts":   dumped["broadcasts"],
					}},
				}
				scripts, _ := dumped["scripts"].([]any)
				for scriptIndex := range output.Scripts {
					var scriptValue any
					if scriptIndex < len(scripts) {
						scriptValue = scripts[scriptIndex]
					}
					childEvents = append(childEvents, GenerationEvent{Type: "script_ready", RequestID: request.requestID, Phase: "program", Data: map[string]any{
						"target_index": index,
						"target_name":  targetContextValue.Name,
						"target_count": targetCount,
						"index":        scriptIndex,
						"total":        total,
						"script":       scriptValue,
					}})
				}
				childEvents = append(childEvents, GenerationEvent{Type: "phase_complete", RequestID: request.requestID, Phase: "program", Data: map[string]any{
					"target_index": index,
					"target_name":  targetContextValue.Name,
					"target_count": targetCount,
					"script_count": total,
					"summary":      output.Summary,
				}})
				return programDecodeResult{program: output, events: childEvents}, nil
			}
			local := newGenerationState()
			local.requirements = requirements
			local.taskSpec = taskSpec
			local.blueprint = blueprint
			local.plan = plan
			local.manifest = state.manifest
			local.phaseStarted = map[string]bool{"requirements": true, "plan": true}
			local.phaseCompleted = map[string]bool{"requirements": true, "plan": true}
			local.expectedProgramIndices = map[int]bool{index: true}
			childEvents := []GenerationEvent{}
			selected, err := programSegmentFrames(frames, index)
			if err != nil {
				return nil, err
			}
			for _, frame := range selected {
				frameEvents, frameFailure, err := o.consumeFrame(request, local, frame, false)
				if err != nil {
					return nil, err
				}
				if frameFailure != nil {
					return nil, &segmentValidationFailure{event: frameFailure}
				}
				childEvents = append(childEvents, frameEvents...)
			}
			if _, present := local.programs[index]; !present {
				return nil, newStreamProtocolError("program segment did not produce a complete target")
			}
			return programDecodeResult{program: local.programs[index], events: childEvents}, nil
		}

		var sink eventSink
		if emitEvent != nil {
			sink = emitTargetEvent
		}
		segmentIndex := index
		value, _, segmentFailure, _, err := o.runSegmentWithRepair(
			batchCtx, request, p, "program",
			func(context map[string]any) ([]prompt.Message, prompt.CacheMetadata) {
				return o.promptBuilder.BuildSegmentPrompt(project, targetContextValue, request.promptRequest(), "program", context, &segmentIndex, nil)
			},
			decodeProgram,
			programContext,
			o.repairBudget("program"),
			&segmentIndex, nil, sink,
		)
		if err != nil {
			return targetResult{}, err
		}
		return targetResult{index: index, value: value, failure: segmentFailure}, nil
	}

	type queuedTargetEvent struct {
		index int
		event *GenerationEvent
		err   error
	}

	// Generate dependency-ready targets in bounded batches. Events are
	// multiplexed as they arrive, while validated programs are committed to
	// state in manifest order after the whole batch succeeds.
	layers, err := programGenerationLayers(state.blueprint, state.manifest)
	if err != nil {
		return err
	}
	for _, layer := range layers {
		for offset := 0; offset < len(layer); offset += 2 {
			end := offset + 2
			if end > len(layer) {
				end = len(layer)
			}
			batch := layer[offset:end]
			for _, index := range batch {
				targetContextValue, err := o.targetContextForManifestTarget(request, state.manifest, index, nil)
				if err != nil {
					return err
				}
				if err := emit(GenerationEvent{Type: "phase_start", RequestID: request.requestID, Phase: "program", Data: map[string]any{
					"target_index": index,
					"target_name":  targetContextValue.Name,
					"target_count": targetCount,
				}}); err != nil {
					return err
				}
			}
			eventQueue := make(chan queuedTargetEvent, 16)
			targetResults := map[int]targetResult{}
			var resultsMu sync.Mutex
			batchCtx, batchCancel := context.WithCancel(ctx)
			var waitGroup sync.WaitGroup
			for _, index := range batch {
				waitGroup.Add(1)
				go func(index int) {
					defer waitGroup.Done()
					send := func(item queuedTargetEvent) {
						select {
						case eventQueue <- item:
						case <-batchCtx.Done():
						}
					}
					result, err := runTarget(batchCtx, index, func(event GenerationEvent) {
						send(queuedTargetEvent{index: index, event: &event})
					})
					if err != nil {
						// Surface failures immediately so the caller can stop
						// other requests in this dependency layer.
						send(queuedTargetEvent{index: index, err: err})
						return
					}
					resultsMu.Lock()
					targetResults[index] = result
					resultsMu.Unlock()
					send(queuedTargetEvent{index: index})
				}(index)
			}
			finished := 0
			var batchErr error
			for finished < len(batch) {
				item := <-eventQueue
				if item.err != nil {
					batchErr = item.err
					break
				}
				if item.event == nil {
					finished++
					resultsMu.Lock()
					completed, present := targetResults[item.index]
					resultsMu.Unlock()
					if present && completed.failure != nil {
						// A validation failure is a completed task, not an
						// exception, but it still invalidates the whole
						// dependency layer. The live sink already emitted the
						// terminal validation event.
						batchCancel()
						waitGroup.Wait()
						return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
							"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
						}})
					}
					continue
				}
				if err := emit(*item.event); err != nil {
					batchErr = err
					break
				}
			}
			batchCancel()
			waitGroup.Wait()
			if batchErr != nil {
				return batchErr
			}
			for _, index := range batch {
				result, present := targetResults[index]
				if !present {
					continue
				}
				if result.failure != nil {
					return emit(GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
						"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
					}})
				}
				program, ok := result.value.(*schema.ProgramIR)
				if !ok {
					return newStreamProtocolError("program segment did not produce a complete target")
				}
				state.programs[result.index] = program
			}
		}
	}

	return emit(GenerationEvent{Type: "complete", RequestID: request.requestID, Data: map[string]any{
		"scope": request.scope, "program_count": len(state.programs),
	}})
}

// runSegmentWithRepair ports _run_segment_with_repair.
func (o *GenerationOrchestrator) runSegmentWithRepair(
	ctx context.Context,
	request *requestView,
	p segmentProvider,
	segment string,
	buildMessages func(map[string]any) ([]prompt.Message, prompt.CacheMetadata),
	decode func([]StreamFrame) (any, error),
	baseContext map[string]any,
	maxRounds int,
	targetIndex *int,
	resourceOpIndex *int,
	emitEvent eventSink,
) (any, []GenerationEvent, *GenerationEvent, any, error) {
	events := []GenerationEvent{}
	var failure *GenerationEvent
	var failedPayload any
	for attempt := 0; attempt <= maxRounds; attempt++ {
		context := cloneAnyMap(baseContext)
		if _, present := context["transport"]; !present {
			transport := "tagged"
			if o.structuredOutputSupported(p, segment) {
				transport = "json_object"
			}
			context["transport"] = transport
		}
		if failure != nil {
			context["failure"] = failure.Data
		}
		builtMessages, cache := buildMessages(context)
		messages := toProviderMessages(builtMessages)
		logger.Debug("ai_segment_start",
			"request_ref", requestLogRef(request.requestID),
			"phase", segmentPhase(segment),
			"segment", segment,
			"attempt", attempt+1,
			"max_attempts", maxRounds+1,
			"provider", p.ProviderName(),
			"transport", context["transport"],
			"target_index", optionalInt(targetIndex),
			"resource_op_index", optionalInt(resourceOpIndex),
			"message_count", len(messages),
			"prompt_chars", promptCharCount(messages),
			"cache_hit", cache.Hits[segment],
		)
		cacheEvent := GenerationEvent{
			Type:      "cache_hit",
			RequestID: request.requestID,
			Phase:     segmentPhase(segment),
			Data:      mergeAnyMaps(map[string]any{"segment": segment}, cache.AsDict()),
		}
		recordEvent(&events, cacheEvent, emitEvent)
		result, err := o.runSegmentOnce(ctx, request, p, segment, messages, decode, targetIndex, resourceOpIndex, emitEvent)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if result.failure == nil {
			events = append(events, result.events...)
			logger.Debug("ai_segment_success",
				"request_ref", requestLogRef(request.requestID),
				"phase", segmentPhase(segment),
				"segment", segment,
				"attempt", attempt+1,
				"max_attempts", maxRounds+1,
				"target_index", optionalInt(targetIndex),
				"payload_shape", diagnosticShape(result.value, 0),
			)
			return result.value, events, nil, result.payload, nil
		}
		if attempt == maxRounds {
			events = append(events, result.events...)
		} else {
			for _, event := range result.events {
				if event.Type != "validation_error" && event.Type != "content_delta" {
					events = append(events, event)
				}
			}
		}
		failure = result.failure
		failedPayload = result.payload
		logger.Debug("ai_segment_retry",
			"request_ref", requestLogRef(request.requestID),
			"phase", segmentPhase(segment),
			"segment", segment,
			"attempt", attempt+1,
			"max_attempts", maxRounds+1,
			"target_index", optionalInt(targetIndex),
			"failure_code", failure.Data["code"],
			"failure_path", failure.Data["path"],
			"will_retry", attempt < maxRounds,
		)
		if emitEvent != nil {
			emitEvent(*failure)
		}
		if attempt < maxRounds {
			repairEventValue := o.repairEvent(request, *failure, segment, attempt+1, maxRounds, targetIndex, resourceOpIndex)
			recordEvent(&events, repairEventValue, emitEvent)
		}
	}
	return nil, events, failure, failedPayload, nil
}

// runSegmentOnce ports _run_segment_once.
func (o *GenerationOrchestrator) runSegmentOnce(
	ctx context.Context,
	request *requestView,
	p segmentProvider,
	segment string,
	messages []provider.ChatMessage,
	decode func([]StreamFrame) (any, error),
	targetIndex *int,
	resourceOpIndex *int,
	emitEvent eventSink,
) (segmentAttempt, error) {
	result := segmentAttempt{}
	rawOutput := ""
	parser := NewUnifiedFrameParser()
	frames := []StreamFrame{}
	reasoningChars := 0
	structuredOutput := o.structuredOutputSupported(p, segment)
	var jsonSchema map[string]any
	if structuredOutput {
		jsonSchema = o.structuredOutputSchema(p, segment)
	}
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	stream, errs := p.StreamWithReasoning(
		streamCtx, messages, request.thinkingEnabled, request.thinkingStrength, structuredOutput, jsonSchema,
	)
	streamErr := consumeProviderStream(stream, errs, streamCancel, func(delta provider.StreamDelta) error {
		switch {
		case delta.Kind == provider.DeltaReasoning && delta.Text != "":
			reasoningChars += utf8.RuneCountInString(delta.Text)
			recordEvent(&result.events, GenerationEvent{
				Type:      "reasoning_delta",
				RequestID: request.requestID,
				Data:      withTargetIndex(map[string]any{"text": delta.Text}, targetIndex),
			}, emitEvent)
		case delta.Kind == provider.DeltaUsage && len(delta.Usage) > 0:
			recordEvent(&result.events, GenerationEvent{
				Type:      "usage",
				RequestID: request.requestID,
				Phase:     segmentPhase(segment),
				Data:      withTargetIndex(usageData(delta.Usage), targetIndex),
			}, emitEvent)
		case delta.Kind == provider.DeltaContent && delta.Text != "":
			rawOutput += delta.Text
			recordEvent(&result.events, GenerationEvent{
				Type:      "content_delta",
				RequestID: request.requestID,
				Phase:     segmentPhase(segment),
				Data:      withTargetIndex(map[string]any{"text": delta.Text}, targetIndex),
			}, emitEvent)
			if !structuredOutput {
				parsedFrames, err := parser.Feed(delta.Text)
				if err != nil {
					return err
				}
				frames = append(frames, parsedFrames...)
			}
		}
		return nil
	})
	logger.Debug("ai_segment_stream_complete",
		"request_ref", requestLogRef(request.requestID),
		"phase", segmentPhase(segment),
		"segment", segment,
		"provider", p.ProviderName(),
		"structured", structuredOutput,
		"raw_chars", utf8.RuneCountInString(rawOutput),
		"reasoning_chars", reasoningChars,
		"frame_count", len(frames),
		"target_index", optionalInt(targetIndex),
	)
	if streamErr != nil {
		if catchableSegmentError(streamErr) {
			result.failure = o.validationEvent(request, segmentPhase(segment), streamErr, StreamFrame{Kind: segment, Content: rawOutput})
			return result, nil
		}
		return result, streamErr
	}
	if verboseLogging(p) && rawOutput != "" {
		logger.Debug("ai_segment_output",
			"request_ref", requestLogRef(request.requestID),
			"phase", segmentPhase(segment),
			"segment", segment,
			"target_index", optionalInt(targetIndex),
			"output", formattedModelOutput(rawOutput, 12_000),
		)
	}
	if structuredOutput {
		parsed, err := RobustJSONParse(rawOutput, 5)
		if err != nil {
			result.failure = o.validationEvent(request, segmentPhase(segment), err, StreamFrame{Kind: segment, Content: rawOutput})
			return result, nil
		}
		// robust_json_parse only ever returns an object; the Python isinstance
		// guard is therefore structural.
		encoded, err := jsonx.Marshal(parsed)
		if err != nil {
			result.failure = o.validationEvent(request, segmentPhase(segment), &modelValidationError{err: err}, StreamFrame{Kind: segment, Content: rawOutput})
			return result, nil
		}
		frames = []StreamFrame{{Kind: segment, Content: string(encoded)}}
	} else {
		trailing, err := parser.Finish()
		if err != nil {
			result.failure = o.validationEvent(request, segmentPhase(segment), err, StreamFrame{Kind: segment, Content: rawOutput})
			return result, nil
		}
		frames = append(frames, trailing...)
	}
	decoded, decodeErr := decode(frames)
	if decodeErr != nil {
		var validationFailure *segmentValidationFailure
		if errors.As(decodeErr, &validationFailure) {
			result.failure = validationFailure.event
			result.payload = segmentPayload(frames, segment, targetIndex)
			return result, nil
		}
		if catchableSegmentError(decodeErr) {
			result.failure = o.validationEvent(request, segmentPhase(segment), decodeErr, errorFrame(frames, segment, targetIndex))
			result.payload = segmentPayload(frames, segment, targetIndex)
			return result, nil
		}
		return result, decodeErr
	}
	if tuple, ok := decoded.(programDecodeResult); ok {
		result.value = tuple.program
		for _, event := range tuple.events {
			recordEvent(&result.events, event, emitEvent)
		}
	} else {
		result.value = decoded
	}
	result.payload = segmentPayload(frames, segment, targetIndex)
	return result, nil
}

// consumeProviderStream drains one provider stream. The delta channel closes
// after the buffered error channel, so the provider error is read once the
// deltas are exhausted. When handle aborts early the stream is cancelled and
// drained so the provider goroutine can close its channels.
func consumeProviderStream(
	stream <-chan provider.StreamDelta,
	errs <-chan error,
	cancel context.CancelFunc,
	handle func(provider.StreamDelta) error,
) error {
	for delta := range stream {
		if err := handle(delta); err != nil {
			abandonProviderStream(cancel, stream)
			return err
		}
	}
	return drainProviderError(errs)
}

// drainProviderError reads the buffered provider error. Safe to call once the
// delta channel is closed: the provider closes the error channel first.
func drainProviderError(errs <-chan error) error {
	if err, ok := <-errs; ok && err != nil {
		return err
	}
	return nil
}

// abandonProviderStream cancels an in-flight provider request and drains the
// remaining deltas in the background so the provider goroutine can finish.
func abandonProviderStream(cancel context.CancelFunc, stream <-chan provider.StreamDelta) {
	cancel()
	go func() {
		for range stream {
		}
	}()
}

// ---------------------------------------------------------------------------
// Static helpers
// ---------------------------------------------------------------------------

// structuredOutputSupported ports _structured_output_supported.
func (o *GenerationOrchestrator) structuredOutputSupported(p segmentProvider, segment string) bool {
	if probe, ok := p.(structuredOutputProbe); ok {
		return probe.SupportsStructuredOutput(segment)
	}
	if probe, ok := p.(structuredOutputNoArgProbe); ok {
		return probe.SupportsStructuredOutputNoArgs()
	}
	return false
}

// structuredOutputSchema ports _structured_output_schema.
func (o *GenerationOrchestrator) structuredOutputSchema(p segmentProvider, segment string) map[string]any {
	supported := false
	if probe, ok := p.(jsonSchemaProbe); ok {
		supported = probe.SupportsJSONSchema(segment)
	} else if probe, ok := p.(jsonSchemaNoArgProbe); ok {
		supported = probe.SupportsJSONSchemaNoArgs()
	} else {
		return nil
	}
	if !supported {
		return nil
	}
	// Apex validates the ChatGPT strict Responses schema dialect; the strict
	// schemas are pre-generated per segment.
	schemas := map[string]map[string]any{
		"requirements": toChatGPTStrictJSONSchema("requirements"),
		"task_spec":    toChatGPTStrictJSONSchema("task_spec"),
		"blueprint":    toChatGPTStrictJSONSchema("blueprint"),
		"plan":         toChatGPTStrictJSONSchema("plan"),
		"manifest":     toChatGPTStrictJSONSchema("manifest"),
		"resource_op":  toChatGPTStrictJSONSchema("resource_op"),
		"program":      toChatGPTStrictJSONSchema("program"),
	}
	schemaValue := schemas[segment]
	// A root anyOf is outside the Responses structured-output subset.
	if schemaValue == nil {
		return nil
	}
	if schemaType, _ := schemaValue["type"].(string); schemaType != "object" {
		return nil
	}
	return schemaValue
}

func verboseLogging(p segmentProvider) bool {
	if probe, ok := p.(verboseLoggingProbe); ok {
		return probe.VerboseLogging()
	}
	return false
}

// singleSegmentFrame ports _single_segment_frame.
func singleSegmentFrame(frames []StreamFrame, kind string) (StreamFrame, error) {
	var matches []StreamFrame
	for _, frame := range frames {
		if frame.Kind == kind {
			matches = append(matches, frame)
		}
	}
	if len(matches) != 1 {
		return StreamFrame{}, newStreamProtocolError("%s segment must contain exactly one frame", kind)
	}
	return matches[0], nil
}

// programSegmentFrames ports _program_segment_frames.
func programSegmentFrames(frames []StreamFrame, targetIndex int) ([]StreamFrame, error) {
	selected := []StreamFrame{}
	active := false
	for _, frame := range frames {
		if frame.Kind == "program_start" {
			if active {
				return nil, newStreamProtocolError("program segment contains nested programs")
			}
			active = frame.Attrs["target_index"] == strconv.Itoa(targetIndex)
			if active {
				selected = append(selected, frame)
			}
			continue
		}
		if active {
			selected = append(selected, frame)
			if frame.Kind == "program_end" {
				return selected, nil
			}
		}
	}
	return nil, newStreamProtocolError("program segment for target_index=%d is incomplete", targetIndex)
}

// segmentPayload ports _segment_payload.
func segmentPayload(frames []StreamFrame, segment string, targetIndex *int) any {
	if segment == "program" && len(frames) == 1 && frames[0].Kind == "program" {
		parsed, err := ParseJSON(frames[0].Content)
		if err != nil {
			return nil
		}
		program, ok := parsed.(map[string]any)
		if !ok {
			return nil
		}
		return map[string]any{
			"target_index": optionalInt(targetIndex),
			"metadata": map[string]any{
				"schema_version":    program["schema_version"],
				"summary":           program["summary"],
				"remove_script_ids": orEmptyList(program["remove_script_ids"]),
				"procedures":        orEmptyList(program["procedures"]),
			},
			"shared_state": map[string]any{
				"variables":  orEmptyList(program["variables"]),
				"lists":      orEmptyList(program["lists"]),
				"broadcasts": orEmptyList(program["broadcasts"]),
			},
			"scripts": orEmptyList(program["scripts"]),
		}
	}
	if segment == "program" {
		index := 0
		if targetIndex != nil {
			index = *targetIndex
		}
		selected, err := programSegmentFrames(frames, index)
		if err != nil {
			return nil
		}
		var metadata *StreamFrame
		var sharedState *StreamFrame
		scripts := []StreamFrame{}
		for position := range selected {
			switch selected[position].Kind {
			case "metadata":
				if metadata == nil {
					metadata = &selected[position]
				}
			case "shared_state":
				if sharedState == nil {
					sharedState = &selected[position]
				}
			case "script":
				scripts = append(scripts, selected[position])
			}
		}
		if metadata == nil && sharedState == nil && len(scripts) == 0 {
			return nil
		}
		var metadataValue any
		if metadata != nil {
			metadataValue = parseOrNil(metadata.Content)
		}
		var sharedStateValue any
		if sharedState != nil {
			sharedStateValue = parseOrNil(sharedState.Content)
		}
		scriptValues := make([]any, 0, len(scripts))
		for _, item := range scripts {
			scriptValues = append(scriptValues, parseOrNil(item.Content))
		}
		return map[string]any{
			"target_index": optionalInt(targetIndex),
			"metadata":     metadataValue,
			"shared_state": sharedStateValue,
			"scripts":      scriptValues,
		}
	}
	for _, frame := range frames {
		if frame.Kind == segment {
			return parseOrNil(frame.Content)
		}
	}
	return nil
}

func parseOrNil(content string) any {
	parsed, err := ParseJSON(content)
	if err != nil {
		return nil
	}
	return parsed
}

func orEmptyList(value any) any {
	if list, ok := value.([]any); ok {
		return list
	}
	return []any{}
}

// errorFrame ports _error_frame.
func errorFrame(frames []StreamFrame, segment string, targetIndex *int) StreamFrame {
	if segment == "program" {
		index := 0
		if targetIndex != nil {
			index = *targetIndex
		}
		selected, err := programSegmentFrames(frames, index)
		if err == nil {
			for _, frame := range selected {
				if frame.Kind == "script" {
					return frame
				}
			}
		}
		return StreamFrame{Kind: "program_start", Attrs: map[string]string{"target_index": strconv.Itoa(index)}}
	}
	for _, frame := range frames {
		if frame.Kind == segment {
			return frame
		}
	}
	return StreamFrame{Kind: segment}
}

// resourceOpIndex ports _resource_op_index.
func resourceOpIndex(path any) *int {
	text, ok := path.(string)
	if !ok {
		return nil
	}
	parts := strings.Split(strings.Trim(text, "/"), "/")
	if len(parts) < 2 || parts[0] != "resource_ops" || !isPythonDigits(parts[1]) {
		return nil
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil
	}
	return &index
}

// isPythonDigits mirrors str.isdigit for the ASCII digits the tagged protocol
// uses; non-ASCII digit strings cannot be converted by strconv.Atoi anyway.
func isPythonDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !unicode.IsDigit(char) {
			return false
		}
	}
	return true
}

// payloadAt ports _payload_at.
func payloadAt(manifest any, index int) any {
	object, ok := manifest.(map[string]any)
	if !ok {
		return nil
	}
	operations, ok := object["resource_ops"].([]any)
	if !ok {
		return nil
	}
	if index < 0 {
		index += len(operations)
	}
	if index >= 0 && index < len(operations) {
		return operations[index]
	}
	return nil
}

// opString reads a string field from a dumped resource operation.
func opString(operation schema.ResourceOp, key string) string {
	text, _ := operation[key].(string)
	return text
}

// targetRefMapEqual compares a dumped target reference with a parsed TargetRef.
func targetRefMapEqual(value any, target *schema.TargetRef) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	kind, _ := object["kind"].(string)
	return kind == target.Kind &&
		optionalString(object["name"]) == stringValue(target.Name) &&
		optionalString(object["op_id"]) == stringValue(target.OpID)
}

func derefMode(mode *string, fallback string) string {
	if mode != nil {
		return *mode
	}
	return fallback
}

// targetContextForManifestTarget ports _target_context_for_manifest_target.
func (o *GenerationOrchestrator) targetContextForManifestTarget(
	request *requestView,
	manifest *schema.ProjectManifest,
	index int,
	mode *string,
) (prompt.TargetContext, error) {
	if index < 0 || index >= len(manifest.ProgramTargets) {
		return prompt.TargetContext{}, newStreamProtocolError("program target_index is not present in the manifest")
	}
	targetRef := manifest.ProgramTargets[index].Target
	if request.projectContext == nil {
		return prompt.TargetContext{}, newStreamProtocolError("project context is required for a manifest target")
	}

	// Program generation happens before browser commit. Project the names
	// declared by the manifest so ProgramIR can use logical resource refs
	// without guessing the VM's eventual duplicate-name suffixes.
	createdNames := []string{}
	for _, operation := range manifest.ResourceOps {
		switch opString(operation, "op") {
		case "add_sprite", "add_builtin_sprite":
			if name, ok := operation["name"].(string); ok {
				createdNames = append(createdNames, name)
			}
		}
	}
	projectedSpriteNames := []string{}
	for _, item := range request.projectContext.Targets {
		if !item.IsStage {
			projectedSpriteNames = append(projectedSpriteNames, item.Name)
		}
	}
	projectedSpriteNames = append(projectedSpriteNames, createdNames...)

	projectedAssetsFor := func(target schema.TargetRef) ([]string, []string, []string) {
		costumes := []string{}
		sounds := []string{}
		backdrops := []string{}
		for _, operation := range manifest.ResourceOps {
			operationName := opString(operation, "op")
			switch {
			case operationName == "add_sprite" && target.Kind == "created_sprite" && opString(operation, "op_id") == stringValue(target.OpID):
				if items, ok := operation["costumes"].([]any); ok {
					for _, item := range items {
						if object, ok := item.(map[string]any); ok {
							if name, ok := object["name"].(string); ok {
								costumes = append(costumes, name)
							}
						}
					}
				}
			case operationName == "add_costume" && targetRefMapEqual(operation["target"], &target):
				if name, ok := operation["name"].(string); ok {
					costumes = append(costumes, name)
				}
			case operationName == "add_sound" && targetRefMapEqual(operation["target"], &target):
				if name, ok := operation["name"].(string); ok {
					sounds = append(sounds, name)
				}
			case operationName == "add_backdrop" && target.Kind == "stage":
				if name, ok := operation["name"].(string); ok {
					backdrops = append(backdrops, name)
				}
			}
		}
		return costumes, sounds, backdrops
	}

	if targetRef.Kind == "current_target" {
		if request.outputContract != "project_program" {
			target, err := o.targetContext(request)
			if err != nil {
				return prompt.TargetContext{}, err
			}
			costumes, sounds, backdrops := projectedAssetsFor(targetRef)
			target.Mode = derefMode(mode, target.Mode)
			target.Costumes = append(append([]string{}, target.Costumes...), costumes...)
			target.Backdrops = append(append([]string{}, target.Backdrops...), backdrops...)
			target.Sounds = append(append([]string{}, target.Sounds...), sounds...)
			target.SpriteNames = projectedSpriteNames
			return target, nil
		}
		var matches []schema.ProjectTargetContext
		for _, item := range request.projectContext.Targets {
			if item.TargetID == "current_target" {
				matches = append(matches, item)
			}
		}
		if len(matches) != 1 {
			return prompt.TargetContext{}, newStreamProtocolError("project context must contain exactly one current target")
		}
		item := matches[0]
		costumes, sounds, backdrops := projectedAssetsFor(targetRef)
		result := prompt.DefaultTargetContext()
		result.TargetID = item.TargetID
		result.Revision = item.Revision
		result.Name = item.Name
		result.IsStage = item.IsStage
		result.HasScripts = item.HasScripts
		result.Mode = derefMode(mode, item.Mode)
		result.Costumes = append(append([]string{}, item.Costumes...), costumes...)
		result.Backdrops = append(append([]string{}, item.Backdrops...), backdrops...)
		result.Sounds = append(append([]string{}, item.Sounds...), sounds...)
		result.SpriteNames = projectedSpriteNames
		result.CurrentProgram = request.target.CurrentProgram
		return result, nil
	}
	targetMode := derefMode(mode, "append")
	if targetRef.Kind == "created_sprite" {
		name := stringValue(targetRef.OpID)
		for _, operation := range manifest.ResourceOps {
			if opString(operation, "op_id") == stringValue(targetRef.OpID) {
				if candidate, ok := operation["name"].(string); ok {
					name = candidate
				}
				break
			}
		}
		costumes, sounds, backdrops := projectedAssetsFor(targetRef)
		result := prompt.DefaultTargetContext()
		result.Name = name
		result.Mode = targetMode
		result.Costumes = costumes
		result.Backdrops = backdrops
		result.Sounds = sounds
		result.SpriteNames = projectedSpriteNames
		return result, nil
	}
	var matches []schema.ProjectTargetContext
	if targetRef.Kind == "stage" {
		for _, item := range request.projectContext.Targets {
			if item.IsStage {
				matches = append(matches, item)
			}
		}
	} else {
		wanted := caseFold(stringValue(targetRef.Name))
		for _, item := range request.projectContext.Targets {
			if !item.IsStage && caseFold(item.Name) == wanted {
				matches = append(matches, item)
			}
		}
	}
	if len(matches) != 1 {
		return prompt.TargetContext{}, newStreamProtocolError("manifest target is not represented exactly once in project context")
	}
	item := matches[0]
	costumes, sounds, backdrops := projectedAssetsFor(targetRef)
	result := prompt.DefaultTargetContext()
	result.TargetID = item.TargetID
	result.Revision = item.Revision
	result.Name = item.Name
	result.IsStage = item.IsStage
	result.HasScripts = item.HasScripts
	result.Mode = targetMode
	result.Costumes = append(append([]string{}, item.Costumes...), costumes...)
	result.Backdrops = append(append([]string{}, item.Backdrops...), backdrops...)
	result.Sounds = append(append([]string{}, item.Sounds...), sounds...)
	result.SpriteNames = projectedSpriteNames
	if request.outputContract == "project_program" {
		result.CurrentProgram = request.target.CurrentProgram
	} else {
		result.CurrentProgram = item.CurrentProgram
	}
	return result, nil
}

// existingAssetsForTarget ports _existing_assets_for_target.
func (o *GenerationOrchestrator) existingAssetsForTarget(request *requestView, target prompt.TargetContext) map[string][]string {
	project := request.projectContext
	if project == nil {
		return map[string][]string{}
	}
	spriteNames := []string{}
	for _, item := range project.Targets {
		if !item.IsStage {
			spriteNames = append(spriteNames, item.Name)
		}
	}
	wantedName := caseFold(target.Name)
	var owner *schema.ProjectTargetContext
	for index := range project.Targets {
		item := project.Targets[index]
		if item.IsStage == target.IsStage && caseFold(item.Name) == wantedName {
			if owner == nil {
				copyOfItem := item
				owner = &copyOfItem
			} else {
				owner = nil
				break
			}
		}
	}
	if owner == nil && target.IsStage {
		var stages []schema.ProjectTargetContext
		for _, item := range project.Targets {
			if item.IsStage {
				stages = append(stages, item)
			}
		}
		if len(stages) == 1 {
			copyOfStage := stages[0]
			owner = &copyOfStage
		}
	}
	result := map[string][]string{
		"sprite":   spriteNames,
		"costume":  {},
		"sound":    {},
		"backdrop": {},
	}
	if owner != nil {
		if !owner.IsStage {
			result["costume"] = append([]string{}, owner.Costumes...)
		}
		result["sound"] = append([]string{}, owner.Sounds...)
		if owner.IsStage {
			if len(owner.Backdrops) > 0 {
				result["backdrop"] = append([]string{}, owner.Backdrops...)
			} else {
				result["backdrop"] = append([]string{}, owner.Costumes...)
			}
		}
	}
	return result
}

// repairEvent ports _repair_event.
func (o *GenerationOrchestrator) repairEvent(
	request *requestView,
	failure GenerationEvent,
	segment string,
	attempt int,
	maxRounds int,
	targetIndex *int,
	resourceOpIndex *int,
) GenerationEvent {
	data := map[string]any{
		"attempt":      attempt,
		"max_rounds":   maxRounds,
		"phase":        failure.Phase,
		"path":         failure.Data["path"],
		"code":         failure.Data["code"],
		"operation":    failure.Data["operation"],
		"actual":       failure.Data["actual"],
		"expected":     failure.Data["expected"],
		"repair_scope": defaultRepairScope(failure.Data["repair_scope"], segment),
		"repair_id":    failure.Data["repair_id"],
		"message":      failure.Data["message"],
		"detail":       failure.Data["detail"],
		"resume":       segment,
	}
	if targetIndex != nil {
		data["target_index"] = *targetIndex
	}
	if resourceOpIndex != nil {
		data["resource_op_index"] = *resourceOpIndex
	}
	return GenerationEvent{Type: "repair", RequestID: request.requestID, Phase: failure.Phase, Data: data}
}

func defaultRepairScope(value any, fallback string) any {
	if text, ok := value.(string); ok && text != "" {
		return text
	}
	return fallback
}

// segmentPhase ports _segment_phase.
func segmentPhase(segment string) string {
	switch segment {
	case "manifest", "resource_op":
		return "plan"
	case "program":
		return "program"
	default:
		return segment
	}
}

// validationEvent ports _validation_event.
func (o *GenerationOrchestrator) validationEvent(request *requestView, phase string, err error, frame StreamFrame) *GenerationEvent {
	issue := structuredIssue(err, frame)
	detail := errorDetail(err)
	logger.Warn("ai_segment_validation",
		"request_ref", requestLogRef(request.requestID),
		"phase", phase,
		"code", issue["code"],
		"path", issue["path"],
		"operation", issue["operation"],
		"error_type", errorTypeName(err),
		"actual", diagnosticJSON(issue["actual"], 2_048),
		"expected", diagnosticText(issue["expected"], 1_200),
		"detail", diagnosticText(detail, 1_200),
		"frame_kind", frame.Kind,
		"frame_chars", utf8.RuneCountInString(frame.Content),
	)
	return &GenerationEvent{
		Type:      "validation_error",
		RequestID: request.requestID,
		Phase:     phase,
		Data: mergeAnyMaps(issue, map[string]any{
			"message":     issue["expected"],
			"detail":      detail,
			"recoverable": false,
		}),
	}
}

// terminalValidationError ports _terminal_validation_error.
func (o *GenerationOrchestrator) terminalValidationError(request *requestView, failure *GenerationEvent) GenerationEvent {
	if failure != nil {
		return GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "OUTPUT_VALIDATION_ERROR", "message": "AI 输出未通过协议校验",
		}}
	}
	return GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
		"code": "OUTPUT_INCOMPLETE", "message": "AI 未完成分段输出",
	}}
}

// projectContext ports _project_context.
func (o *GenerationOrchestrator) projectContext(request *requestView) *prompt.ProjectContext {
	if request.projectContext == nil {
		return nil
	}
	// The browser uses VM target IDs for optimistic concurrency only. Model
	// prompts receive stable logical placeholders, never VM internals.
	targets := make([]map[string]any, 0, len(request.projectContext.Targets))
	for index := range request.projectContext.Targets {
		value := modelDump(&request.projectContext.Targets[index])
		value["target_id"] = fmt.Sprintf("logical_target_%d", index+1)
		targets = append(targets, value)
	}
	return &prompt.ProjectContext{
		ProjectID:   request.projectContext.ProjectID,
		Version:     request.projectContext.Version,
		Targets:     targets,
		SharedState: request.projectContext.SharedState,
	}
}

// targetContext ports _target_context.
func (o *GenerationOrchestrator) targetContext(request *requestView) (prompt.TargetContext, error) {
	targetID := "current-target"
	revision := 0
	if request.projectContext != nil {
		var matches []schema.ProjectTargetContext
		for _, item := range request.projectContext.Targets {
			if item.IsStage == request.target.IsStage && item.Name == request.target.Name {
				matches = append(matches, item)
			}
		}
		if len(matches) != 1 {
			return prompt.TargetContext{}, newStreamProtocolError("project context must contain exactly one current target")
		}
		targetID = matches[0].TargetID
		revision = matches[0].Revision
	}
	return prompt.TargetContext{
		TargetID:       targetID,
		Revision:       revision,
		Name:           request.target.Name,
		IsStage:        request.target.IsStage,
		HasScripts:     request.target.HasScripts,
		Mode:           request.target.Mode,
		Costumes:       append([]string{}, request.target.Costumes...),
		Backdrops:      append([]string{}, request.target.Backdrops...),
		Sounds:         append([]string{}, request.target.Sounds...),
		SpriteNames:    append([]string{}, request.target.SpriteNames...),
		CurrentProgram: request.target.CurrentProgram,
	}, nil
}

func (v *requestView) extensionPermissionRequest() *schema.UnifiedGenerationRequest {
	return &schema.UnifiedGenerationRequest{PermittedExtensions: v.permittedExtensions}
}

// consumeFrame ports _consume_frame.
func (o *GenerationOrchestrator) consumeFrame(
	request *requestView,
	state *generationState,
	frame StreamFrame,
	allowFullRestart bool,
) ([]GenerationEvent, *GenerationEvent, error) {
	events, failure, err := o.consumeFrameInner(request, state, frame, allowFullRestart)
	if err != nil {
		if catchableSegmentError(err) {
			phase := phaseFor(frame)
			issue := structuredIssue(err, frame)
			detail := errorDetail(err)
			logger.Warn("统一生成 校验失败", "phase", phase, "path", issue["path"], "code", issue["code"])
			return []GenerationEvent{}, &GenerationEvent{
				Type:      "validation_error",
				RequestID: request.requestID,
				Phase:     phase,
				Data: mergeAnyMaps(issue, map[string]any{
					"message":     issue["expected"],
					"detail":      detail,
					"recoverable": false,
				}),
			}, nil
		}
		return nil, nil, err
	}
	return events, failure, nil
}

func (o *GenerationOrchestrator) consumeFrameInner(
	request *requestView,
	state *generationState,
	frame StreamFrame,
	allowFullRestart bool,
) ([]GenerationEvent, *GenerationEvent, error) {
	switch frame.Kind {
	case "requirements":
		if len(state.phaseStarted) > 0 {
			if !allowFullRestart {
				return nil, nil, newStreamProtocolError("requirements must be the first generation frame")
			}
			// 修复轮中模型选择从头重出：接受并重置为全新生成状态
			resetState(state)
		}
		if err := expectNew(state, "requirements"); err != nil {
			return nil, nil, err
		}
		startPhase(state, "requirements")
		parsed, err := ParseJSON(frame.Content)
		if err != nil {
			return nil, nil, err
		}
		value, err := decodeModel[schema.RequirementsResponse](parsed)
		if err != nil {
			return nil, nil, err
		}
		state.requirements = value
		completePhase(state, "requirements")
		logger.Info("统一生成 需求完成",
			"ready", value.Ready, "behavior_steps", len(value.BehaviorSteps), "triggers", len(value.Triggers))
		return []GenerationEvent{
			{Type: "phase_start", RequestID: request.requestID, Phase: "requirements"},
			{Type: "phase_complete", RequestID: request.requestID, Phase: "requirements", Data: modelDump(value)},
		}, nil, nil
	case "plan":
		if state.requirements == nil {
			return nil, nil, newStreamProtocolError("plan appeared before requirements")
		}
		if err := expectNew(state, "plan"); err != nil {
			return nil, nil, err
		}
		startPhase(state, "plan")
		parsed, err := ParseJSON(frame.Content)
		if err != nil {
			return nil, nil, err
		}
		value, err := decodeModel[schema.Plan](parsed)
		if err != nil {
			return nil, nil, err
		}
		state.plan = value
		if request.scope == "single_sprite" {
			completePhase(state, "plan")
			logger.Info("统一生成 计划完成", "steps", len(value.Steps))
			return []GenerationEvent{
				{Type: "phase_start", RequestID: request.requestID, Phase: "plan"},
				{Type: "phase_complete", RequestID: request.requestID, Phase: "plan", Data: modelDump(value)},
			}, nil, nil
		}
		return []GenerationEvent{{Type: "phase_start", RequestID: request.requestID, Phase: "plan"}}, nil, nil
	case "manifest":
		if request.scope != "project" || state.plan == nil {
			return nil, nil, newStreamProtocolError("manifest is only valid after a project plan")
		}
		if state.phaseStarted["program"] {
			return nil, nil, newStreamProtocolError("manifest must precede program frames")
		}
		if state.manifest != nil {
			return nil, nil, newStreamProtocolError("duplicate project manifest")
		}
		rawManifest, err := ParseJSON(frame.Content)
		if err != nil {
			return nil, nil, err
		}
		normalizedManifest, normalizations, err := schema.NormalizeProjectManifestPayload(rawManifest)
		if err != nil {
			return nil, nil, err
		}
		manifest, err := decodeModel[schema.ProjectManifest](normalizedManifest)
		if err != nil {
			return nil, nil, err
		}
		if err := schema.ValidateRequestExtensionPermissions(request.extensionPermissionRequest(), manifest); err != nil {
			return nil, nil, err
		}
		state.manifest = manifest
		completePhase(state, "plan")
		logger.Info("统一生成 计划完成",
			"steps", len(state.plan.Steps),
			"program_targets", len(manifest.ProgramTargets),
			"resource_ops", len(manifest.ResourceOps))
		return []GenerationEvent{{Type: "phase_complete", RequestID: request.requestID, Phase: "plan", Data: map[string]any{
			"steps":          state.plan.Steps,
			"manifest":       modelDump(manifest),
			"normalizations": normalizationsAsDict(normalizations),
		}}}, nil, nil
	case "program_start":
		events, err := o.programStart(request, state, frame)
		return events, nil, err
	case "metadata", "shared_state", "script":
		events, err := o.programChild(request, state, frame)
		return events, nil, err
	case "program_end":
		events, err := o.programEnd(request, state)
		return events, nil, err
	default:
		return nil, nil, newStreamProtocolError("unknown generation frame")
	}
}

// programStart ports _program_start.
func (o *GenerationOrchestrator) programStart(request *requestView, state *generationState, frame StreamFrame) ([]GenerationEvent, error) {
	if state.requirements == nil || state.plan == nil {
		return nil, newStreamProtocolError("program appeared before plan")
	}
	if request.scope == "project" && state.manifest == nil {
		return nil, newStreamProtocolError("project program appeared before manifest")
	}
	if state.activeProgram != nil {
		return nil, newStreamProtocolError("nested program frame")
	}
	if len(frame.Attrs) != 1 {
		return nil, newStreamProtocolError("program requires only target_index")
	}
	rawIndex, present := frame.Attrs["target_index"]
	if !present {
		return nil, newStreamProtocolError("program requires only target_index")
	}
	targetIndex, err := strconv.Atoi(strings.TrimSpace(rawIndex))
	if err != nil {
		return nil, newStreamProtocolError("target_index must be an integer")
	}
	if targetIndex < 0 {
		return nil, newStreamProtocolError("target_index must be non-negative")
	}
	if request.scope == "single_sprite" && targetIndex != 0 {
		return nil, newStreamProtocolError("single target program must use target_index 0")
	}
	if request.scope == "project" && state.manifest != nil && targetIndex >= len(state.manifest.ProgramTargets) {
		return nil, newStreamProtocolError("program target_index is outside the manifest")
	}
	if state.expectedProgramIndices != nil {
		if !state.expectedProgramIndices[targetIndex] || state.programs[targetIndex] != nil {
			return nil, newStreamProtocolError("program target_index is not assigned to this segment")
		}
	} else if state.programs[targetIndex] != nil || targetIndex != len(state.programs) {
		return nil, newStreamProtocolError("program target_index must be unique and ordered")
	}
	if !state.phaseStarted["program"] {
		startPhase(state, "program")
	}
	state.activeProgram = &programBuffer{targetIndex: targetIndex, attrs: frame.Attrs}
	if len(state.programs) == 0 {
		return []GenerationEvent{{Type: "phase_start", RequestID: request.requestID, Phase: "program", Data: map[string]any{
			"target_index": targetIndex,
		}}}, nil
	}
	return []GenerationEvent{}, nil
}

// programChild ports _program_child.
func (o *GenerationOrchestrator) programChild(request *requestView, state *generationState, frame StreamFrame) ([]GenerationEvent, error) {
	program := state.activeProgram
	if program == nil {
		return nil, newStreamProtocolError("program child is outside a program frame")
	}
	switch frame.Kind {
	case "metadata":
		if program.metadata != nil || len(program.scripts) > 0 {
			return nil, newStreamProtocolError("metadata must precede scripts and appear once")
		}
		parsed, err := ParseJSON(frame.Content)
		if err != nil {
			return nil, err
		}
		value, err := decodeModel[schema.ProgramMetadata](parsed)
		if err != nil {
			return nil, err
		}
		program.metadata = value
		return []GenerationEvent{{Type: "metadata", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(
			map[string]any{"target_index": program.targetIndex}, modelDump(value),
		)}}, nil
	case "shared_state":
		if program.metadata == nil {
			return nil, newStreamProtocolError("metadata must precede shared_state")
		}
		if program.sharedState != nil || len(program.scripts) > 0 {
			return nil, newStreamProtocolError("shared_state must precede scripts and appear once")
		}
		parsed, err := ParseJSON(frame.Content)
		if err != nil {
			return nil, err
		}
		value, err := decodeModel[schema.ProgramState](parsed)
		if err != nil {
			return nil, err
		}
		program.sharedState = value
		return []GenerationEvent{{Type: "shared_state", RequestID: request.requestID, Phase: "program", Data: mergeAnyMaps(
			map[string]any{"target_index": program.targetIndex}, modelDump(value),
		)}}, nil
	}
	if program.metadata == nil || program.sharedState == nil {
		return nil, newStreamProtocolError("metadata and shared_state are required before scripts")
	}
	if len(frame.Attrs) != 3 {
		return nil, newStreamProtocolError("script requires target_index, index, and total")
	}
	if _, present := frame.Attrs["target_index"]; !present {
		return nil, newStreamProtocolError("script requires target_index, index, and total")
	}
	if _, present := frame.Attrs["index"]; !present {
		return nil, newStreamProtocolError("script requires target_index, index, and total")
	}
	if _, present := frame.Attrs["total"]; !present {
		return nil, newStreamProtocolError("script requires target_index, index, and total")
	}
	targetIndex, targetIndexErr := strconv.Atoi(strings.TrimSpace(frame.Attrs["target_index"]))
	index, indexErr := strconv.Atoi(strings.TrimSpace(frame.Attrs["index"]))
	if targetIndexErr != nil || indexErr != nil {
		return nil, newStreamProtocolError("script target_index and index must be integers")
	}
	if targetIndex != program.targetIndex {
		return nil, newStreamProtocolError("script target_index does not match program target_index")
	}
	if index != len(program.scripts) {
		return nil, newStreamProtocolError("scripts must arrive in index order")
	}
	total, totalErr := strconv.Atoi(strings.TrimSpace(frame.Attrs["total"]))
	if totalErr != nil {
		return nil, newStreamProtocolError("script total must be an integer")
	}
	if total < 1 || index >= total {
		return nil, newStreamProtocolError("script total is invalid")
	}
	if program.total != nil && *program.total != total {
		return nil, newStreamProtocolError("script total must remain constant within a program")
	}
	program.total = &total
	parsed, err := ParseJSON(frame.Content)
	if err != nil {
		return nil, err
	}
	script, err := decodeModel[schema.Script](parsed)
	if err != nil {
		return nil, err
	}
	for _, item := range program.scripts {
		if item.ID == script.ID {
			return nil, newStreamProtocolError("script ids must be unique within a program")
		}
	}
	if err := o.validatePartialProgram(request, state, program, script); err != nil {
		return nil, err
	}
	program.scripts = append(program.scripts, *script)
	logger.Debug("统一生成 程序 脚本就绪", "target_index", program.targetIndex, "index", index, "total", total)
	return []GenerationEvent{{Type: "script_ready", RequestID: request.requestID, Phase: "program", Data: map[string]any{
		"target_index": program.targetIndex,
		"index":        index,
		"total":        total,
		"script":       modelDump(script),
	}}}, nil
}

// programEnd ports _program_end.
func (o *GenerationOrchestrator) programEnd(request *requestView, state *generationState) ([]GenerationEvent, error) {
	program := state.activeProgram
	if program == nil {
		return nil, newStreamProtocolError("program end without program start")
	}
	if program.metadata == nil || program.sharedState == nil || len(program.scripts) == 0 {
		return nil, newStreamProtocolError("program requires metadata, shared_state, and at least one script")
	}
	if program.total != nil && *program.total != len(program.scripts) {
		return nil, newStreamProtocolError("script total does not match the program")
	}
	output := &schema.ProgramIR{
		SchemaVersion:   program.metadata.SchemaVersion,
		Summary:         program.metadata.Summary,
		RemoveScriptIDs: program.metadata.RemoveScriptIDs,
		Variables:       program.sharedState.Variables,
		Lists:           program.sharedState.Lists,
		Broadcasts:      program.sharedState.Broadcasts,
		Procedures:      program.metadata.Procedures,
		Scripts:         program.scripts,
	}
	targetKind, mode, currentProgram, err := o.targetContract(request, state, program.targetIndex)
	if err != nil {
		return nil, err
	}
	allowedExtensions := map[string]bool{}
	if request.scope == "project" && state.manifest != nil {
		allowedExtensions = stringSet(state.manifest.RequiredExtensions)
	}
	if _, err := schema.ValidateProgramContracts(output, schema.ProgramContractOptions{
		TargetKind:        targetKind,
		AllowedExtensions: allowedExtensions,
		AllowResourceRefs: request.scope == "project",
	}); err != nil {
		return nil, err
	}
	if _, err := schema.ValidateProgramForMode(output, mode, currentProgram); err != nil {
		return nil, err
	}
	if request.scope == "project" && state.manifest != nil {
		if _, err := schema.ValidateProjectProgramSharedState(output, &state.manifest.SharedState, targetKind); err != nil {
			return nil, err
		}
		targetContextValue, err := o.targetContextForManifestTarget(request, state.manifest, program.targetIndex, &mode)
		if err != nil {
			return nil, err
		}
		if _, err := schema.ValidateProjectProgramResourceRefs(output, state.manifest, o.existingAssetsForTarget(request, targetContextValue)); err != nil {
			return nil, err
		}
		if _, err := schema.ValidateProjectProgramStateRefs(output, state.manifest); err != nil {
			return nil, err
		}
	}
	state.programs[program.targetIndex] = output
	state.activeProgram = nil
	state.phaseCompleted["program"] = true
	logger.Info("统一生成 程序完成", "target_index", program.targetIndex, "script_count", len(program.scripts))
	return []GenerationEvent{{Type: "phase_complete", RequestID: request.requestID, Phase: "program", Data: map[string]any{
		"target_index": program.targetIndex,
		"script_count": len(program.scripts),
		"summary":      output.Summary,
	}}}, nil
}

// validatePartialProgram ports _validate_partial_program.
func (o *GenerationOrchestrator) validatePartialProgram(
	request *requestView,
	state *generationState,
	program *programBuffer,
	script *schema.Script,
) error {
	if program.metadata == nil || program.sharedState == nil {
		return errors.New("program metadata and shared_state are required")
	}
	partial := &schema.ProgramIR{
		SchemaVersion:   program.metadata.SchemaVersion,
		Summary:         program.metadata.Summary,
		RemoveScriptIDs: program.metadata.RemoveScriptIDs,
		Variables:       program.sharedState.Variables,
		Lists:           program.sharedState.Lists,
		Broadcasts:      program.sharedState.Broadcasts,
		Procedures:      program.metadata.Procedures,
		Scripts:         []schema.Script{*script},
	}
	targetKind, mode, currentProgram, err := o.targetContract(request, state, program.targetIndex)
	if err != nil {
		return err
	}
	allowedExtensions := map[string]bool{}
	if request.scope == "project" && state.manifest != nil {
		allowedExtensions = stringSet(state.manifest.RequiredExtensions)
	}
	if _, err := schema.ValidateProgramContracts(partial, schema.ProgramContractOptions{
		TargetKind:        targetKind,
		AllowedExtensions: allowedExtensions,
		AllowResourceRefs: request.scope == "project",
	}); err != nil {
		return err
	}
	_, err = schema.ValidateProgramForMode(partial, mode, currentProgram)
	return err
}

// targetContract ports _target_contract.
func (o *GenerationOrchestrator) targetContract(request *requestView, state *generationState, index int) (string, string, string, error) {
	if request.scope == "single_sprite" {
		targetKind := "sprite"
		if request.target.IsStage {
			targetKind = "stage"
		}
		return targetKind, request.target.Mode, request.target.CurrentProgram, nil
	}
	if state.manifest == nil {
		return "", "", "", newStreamProtocolError("project program requires a manifest")
	}
	if index < 0 || index >= len(state.manifest.ProgramTargets) {
		return "", "", "", newStreamProtocolError("program target_index is not present in the manifest")
	}
	programTarget := state.manifest.ProgramTargets[index]
	targetRef := programTarget.Target
	if request.outputContract == "project_program" {
		targetKind := "sprite"
		if request.target.IsStage {
			targetKind = "stage"
		}
		return targetKind, programTarget.Mode, request.target.CurrentProgram, nil
	}
	if targetRef.Kind == "current_target" {
		targetKind := "sprite"
		if request.target.IsStage {
			targetKind = "stage"
		}
		return targetKind, programTarget.Mode, request.target.CurrentProgram, nil
	}
	if targetRef.Kind == "created_sprite" {
		return "sprite", programTarget.Mode, "", nil
	}
	if request.projectContext == nil {
		return "", "", "", newStreamProtocolError("project context is required for a manifest target")
	}
	var matches []schema.ProjectTargetContext
	targetKind := "sprite"
	if targetRef.Kind == "stage" {
		targetKind = "stage"
		for _, item := range request.projectContext.Targets {
			if item.IsStage {
				matches = append(matches, item)
			}
		}
	} else {
		wanted := caseFold(stringValue(targetRef.Name))
		for _, item := range request.projectContext.Targets {
			if !item.IsStage && caseFold(item.Name) == wanted {
				matches = append(matches, item)
			}
		}
	}
	if len(matches) != 1 {
		return "", "", "", newStreamProtocolError("manifest target is not represented exactly once in project context")
	}
	return targetKind, programTarget.Mode, matches[0].CurrentProgram, nil
}

// finish ports _finish.
func (o *GenerationOrchestrator) finish(request *requestView, state *generationState) *GenerationEvent {
	if state.activeProgram != nil {
		return &GenerationEvent{Type: "validation_error", RequestID: request.requestID, Phase: "program", Data: map[string]any{
			"path": "program", "message": "program frame is incomplete", "recoverable": false,
		}}
	}
	if state.requirements == nil || state.plan == nil {
		return &GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "OUTPUT_INCOMPLETE", "message": "AI 未完成需求和计划输出",
		}}
	}
	if !request.proceedWithDefaults && !state.requirements.Ready {
		return &GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "REQUIREMENTS_INCOMPLETE", "message": "需求信息还不完整",
		}}
	}
	if request.scope == "project" {
		if state.manifest == nil {
			return &GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
				"code": "MANIFEST_MISSING", "message": "AI 未完成项目清单输出",
			}}
		}
		if len(state.programs) != len(state.manifest.ProgramTargets) {
			return &GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
				"code": "PROGRAMS_INCOMPLETE", "message": "AI 未完成项目目标程序输出",
			}}
		}
		return nil
	}
	if len(state.programs) != 1 || state.programs[0] == nil {
		return &GenerationEvent{Type: "error", RequestID: request.requestID, Data: map[string]any{
			"code": "PROGRAM_MISSING", "message": "AI 未完成当前目标程序输出",
		}}
	}
	return nil
}

// resetState ports _reset_state.
func resetState(state *generationState) {
	state.requirements = nil
	state.taskSpec = nil
	state.blueprint = nil
	state.plan = nil
	state.manifest = nil
	state.programs = map[int]*schema.ProgramIR{}
	state.activeProgram = nil
	state.phaseStarted = map[string]bool{}
	state.phaseCompleted = map[string]bool{}
}

// expectNew ports _expect_new.
func expectNew(state *generationState, name string) error {
	if state.phaseStarted[name] {
		return newStreamProtocolError("duplicate %s frame", name)
	}
	return nil
}

func startPhase(state *generationState, name string) { state.phaseStarted[name] = true }

func completePhase(state *generationState, name string) { state.phaseCompleted[name] = true }

// phaseFor ports _phase_for.
func phaseFor(frame StreamFrame) string {
	if frame.Kind == "manifest" {
		return "plan"
	}
	switch frame.Kind {
	case "program_start", "program_end", "metadata", "shared_state", "script":
		return "program"
	default:
		return frame.Kind
	}
}

// structuredIssue ports _structured_issue.
func structuredIssue(err error, frame StreamFrame) map[string]any {
	var resourceErr *schema.ResourceContractError
	if errors.As(err, &resourceErr) {
		return resourceErr.Issue.AsDict()
	}
	scope := frameScope(frame.Kind)
	var programErr *schema.ProgramContractError
	if errors.As(err, &programErr) && len(programErr.Issues) > 0 {
		issue := programErr.Issues[0]
		return map[string]any{
			"code":         "PROGRAM_CONTRACT_INVALID",
			"path":         schema.ToJSONPointer(issue.Path),
			"operation":    "program",
			"actual":       nil,
			"expected":     issue.Message,
			"repair_scope": "program",
			"repair_id":    nil,
		}
	}
	if path, message, ok := asValidationError(err); ok {
		input := frameInput(frame, path)
		unsupportedVersion := path == "/schema_version" && message != "Field required" && input != nil
		if number, isNumber := jsonx.Int(input); isNumber && number == 3 {
			unsupportedVersion = false
		}
		code := "SCHEMA_VALIDATION_ERROR"
		expected := message
		if expected == "" {
			expected = "structured output is invalid"
		}
		if unsupportedVersion {
			code = "UNSUPPORTED_SCHEMA_VERSION"
			expected = "schema_version must equal 3"
		}
		return map[string]any{
			"code":         code,
			"path":         schema.ToJSONPointer(path),
			"operation":    frame.Kind,
			"actual":       resourcecontract.BoundedActual(input, boundedActualLimit),
			"expected":     expected,
			"repair_scope": scope,
			"repair_id":    nil,
		}
	}
	var streamErr *StreamProtocolError
	if errors.As(err, &streamErr) {
		return map[string]any{
			"code":         "STREAM_PROTOCOL_ERROR",
			"path":         "/output",
			"operation":    frame.Kind,
			"actual":       resourcecontract.BoundedActual(frame.Content, boundedActualLimit),
			"expected":     streamErr.Error(),
			"repair_scope": scope,
			"repair_id":    nil,
		}
	}
	return map[string]any{
		"code":         "OUTPUT_VALIDATION_ERROR",
		"path":         "/output",
		"operation":    frame.Kind,
		"actual":       nil,
		"expected":     "structured output validation failed",
		"repair_scope": scope,
		"repair_id":    nil,
	}
}

// frameScope ports the repair_scope selection of _structured_issue.
func frameScope(kind string) string {
	switch kind {
	case "manifest":
		return "manifest"
	case "resource_op":
		return "resource_op"
	case "program_start", "program_end", "metadata", "shared_state", "script":
		return "program"
	case "requirements", "task_spec", "blueprint", "plan":
		return kind
	default:
		return "program"
	}
}

// asValidationError reports whether an error is a pydantic ValidationError
// equivalent and returns its first error location and message.
func asValidationError(err error) (string, string, bool) {
	var schemaErr *schema.SchemaValidationError
	if errors.As(err, &schemaErr) {
		return schemaErr.Path, schemaErr.Message, true
	}
	var modelErr *modelValidationError
	if errors.As(err, &modelErr) {
		return "", modelErr.Error(), true
	}
	return "", "", false
}

// frameInput resolves the pydantic error input for a JSON-pointer path inside
// the failing frame.
func frameInput(frame StreamFrame, path string) any {
	parsed, err := jsonx.Parse([]byte(frame.Content))
	if err != nil {
		return nil
	}
	if path == "" {
		return parsed
	}
	current := parsed
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		switch typed := current.(type) {
		case map[string]any:
			value, present := typed[part]
			if !present {
				return nil
			}
			current = value
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil
			}
			current = typed[index]
		default:
			return nil
		}
	}
	return current
}

// errorPath ports _error_path.
func errorPath(err error) string {
	var resourceErr *schema.ResourceContractError
	if errors.As(err, &resourceErr) {
		return resourceErr.Issue.Path
	}
	var programErr *schema.ProgramContractError
	if errors.As(err, &programErr) && len(programErr.Issues) > 0 {
		return schema.ToJSONPointer(programErr.Issues[0].Path)
	}
	if path, _, ok := asValidationError(err); ok {
		location := schema.ToJSONPointer(path)
		if location == "" {
			return "/output"
		}
		return location
	}
	return "/output"
}

// safeMessage ports _safe_message.
func safeMessage(err error) string {
	var resourceErr *schema.ResourceContractError
	if errors.As(err, &resourceErr) {
		return resourceErr.Issue.Expected
	}
	var streamErr *StreamProtocolError
	if errors.As(err, &streamErr) {
		return streamErr.Error()
	}
	if _, _, ok := asValidationError(err); ok {
		return "结构化输出不符合协议"
	}
	return "结构化输出校验失败"
}

// errorTypeName mirrors type(error).__name__ for the ported error values.
func errorTypeName(err error) string {
	var resourceErr *schema.ResourceContractError
	if errors.As(err, &resourceErr) {
		return "ResourceContractError"
	}
	var programErr *schema.ProgramContractError
	if errors.As(err, &programErr) {
		return "ProgramContractError"
	}
	if _, _, ok := asValidationError(err); ok {
		return "ValidationError"
	}
	var streamErr *StreamProtocolError
	if errors.As(err, &streamErr) {
		return "StreamProtocolError"
	}
	return fmt.Sprintf("%T", err)
}

// errorDetail ports _error_detail.
func errorDetail(err error) string {
	var resourceErr *schema.ResourceContractError
	if errors.As(err, &resourceErr) {
		return compactJSON(resourceErr.Issue.AsDict())
	}
	var programErr *schema.ProgramContractError
	if errors.As(err, &programErr) {
		parts := []string{}
		for index, issue := range programErr.Issues {
			if index >= 5 {
				break
			}
			parts = append(parts, schema.ToJSONPointer(issue.Path)+": "+issue.Message)
		}
		return strings.Join(parts, "; ")
	}
	if path, message, ok := asValidationError(err); ok {
		location := schema.ToJSONPointer(path)
		if location == "" {
			location = "/output"
		}
		// The ported models do not retain pydantic's error type slug; "missing"
		// and "value_error" are the two slugs the orchestrator distinguishes.
		errorType := "value_error"
		if message == "Field required" {
			errorType = "missing"
		}
		return location + ": " + errorType + ": " + message
	}
	return err.Error()
}

// resumeTag ports _resume_tag.
func resumeTag(frameKind string) string {
	switch frameKind {
	case "requirements", "plan", "manifest":
		return frameKind
	default:
		return "program"
	}
}

// resumeState ports _resume_state.
func resumeState(state *generationState, resumeKind string) *generationState {
	if resumeKind == "requirements" {
		return newGenerationState()
	}
	if resumeKind == "plan" {
		next := newGenerationState()
		next.requirements = state.requirements
		return next
	}
	next := newGenerationState()
	next.requirements = state.requirements
	next.plan = state.plan
	if resumeKind == "program" {
		next.manifest = state.manifest
	}
	// The Python reference passes the same set object for both phase fields,
	// so the two sets alias after a manifest/program resume. Keep that
	// behaviour: it changes whether a trailing program phase_complete event is
	// synthesised.
	keep := map[string]bool{"requirements": true, "plan": true}
	next.phaseStarted = keep
	next.phaseCompleted = keep
	return next
}

// repairMessages ports _repair_messages.
func repairMessages(messages []prompt.Message, failure *GenerationEvent, resumeKind string) []prompt.Message {
	instructions := map[string]string{
		"requirements": "重新输出完整的 <requirements>、<plan> 及后续标签流",
		"plan":         "从 <plan> 标签开始重新输出（保持已通过的 requirements 不变）",
		"manifest":     "从 <manifest> 标签开始重新输出（保持已通过的 requirements 和 plan 不变）",
		"program":      "从第一个 <program> 标签开始重新输出全部程序（保持已通过的 requirements、plan 和 manifest 不变）",
	}
	detail := orString(failure.Data["detail"])
	if detail == "" {
		detail = orString(failure.Data["message"])
	}
	if detail == "" {
		detail = "未知校验错误"
	}
	hint := prompt.RepairHint(failure.Phase, orString(failure.Data["path"]), detail)
	instruction := instructions[resumeKind]
	content := "上一次输出的以下内容未通过服务端校验：" + detail + "。" +
		"修复重点：" + hint +
		"请立即修正并" + instruction + "。" +
		"只输出标签和严格 JSON，禁止 markdown、解释、代码围栏或任何标签外文本；" +
		"先对照 FINAL OUTPUT CONTRACT 逐条检查报错所指字段，再输出修正后的内容。"
	// A repair receives the original schema/context plus a bounded protocol
	// diagnostic. Never replay model output, user prompt text, or project code.
	return append(append([]prompt.Message{}, messages...), prompt.Message{Role: "user", Content: content})
}

// failureEvent ports _failure.
func failureEvent(requestID string, code string) GenerationEvent {
	return GenerationEvent{Type: "error", RequestID: requestID, Data: map[string]any{
		"code": code, "message": "AI 输出未通过协议校验",
	}}
}

// ---------------------------------------------------------------------------
// Small shared helpers
// ---------------------------------------------------------------------------

func optionalInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func withTargetIndex(data map[string]any, targetIndex *int) map[string]any {
	if targetIndex == nil {
		return data
	}
	result := cloneAnyMap(data)
	result["target_index"] = *targetIndex
	return result
}

func usageData(usage map[string]int) map[string]any {
	result := make(map[string]any, len(usage))
	for key, value := range usage {
		result[key] = value
	}
	return result
}

func promptCharCount(messages []provider.ChatMessage) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Content)
	}
	return total
}

func sortedProgramIndices(programs map[int]*schema.ProgramIR) []int {
	indices := make([]int, 0, len(programs))
	for index := range programs {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	return indices
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func normalizationsAsDict(issues []schema.ResourceContractIssue) []map[string]any {
	result := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issue.AsDict())
	}
	return result
}

// programGenerationLayers ports run_manager._program_generation_layers: stable,
// cross-job dependency layers built from the verified Blueprint.
func programGenerationLayers(blueprint *schema.ProjectBlueprint, manifest *schema.ProjectManifest) ([][]int, error) {
	behaviorToTarget := map[string]int{}
	for index, target := range manifest.ProgramTargets {
		for _, behaviorID := range target.BehaviorIDs {
			if _, present := behaviorToTarget[behaviorID]; present {
				return nil, errors.New("behavior is assigned to multiple program targets: " + behaviorID)
			}
			behaviorToTarget[behaviorID] = index
		}
	}

	dependencies := make([]map[int]bool, len(manifest.ProgramTargets))
	for index := range dependencies {
		dependencies[index] = map[int]bool{}
	}
	if blueprint != nil {
		for _, behavior := range blueprint.BehaviorGraph {
			targetIndex, present := behaviorToTarget[behavior.BehaviorID]
			if !present {
				return nil, errors.New("Blueprint behavior has no program target: " + behavior.BehaviorID)
			}
			for _, dependencyID := range behavior.DependsOn {
				dependencyTarget, present := behaviorToTarget[dependencyID]
				if !present {
					return nil, errors.New("Blueprint dependency has no program target: " + dependencyID)
				}
				if dependencyTarget != targetIndex {
					dependencies[targetIndex][dependencyTarget] = true
				}
			}
		}
	}

	remaining := make([]map[int]bool, len(dependencies))
	for index := range dependencies {
		remaining[index] = map[int]bool{}
		for key := range dependencies[index] {
			remaining[index][key] = true
		}
	}
	completed := map[int]bool{}
	layers := [][]int{}
	for len(completed) < len(manifest.ProgramTargets) {
		layer := []int{}
		for index := range remaining {
			if len(remaining[index]) == 0 && !completed[index] {
				layer = append(layer, index)
			}
		}
		if len(layer) == 0 {
			logger.Warn("Blueprint program dependencies contain a cycle; falling back to self-healing layers")
			minDeps := int(^uint(0) >> 1)
			fallbackCandidate := -1
			for index := range remaining {
				if !completed[index] && len(remaining[index]) < minDeps {
					minDeps = len(remaining[index])
					fallbackCandidate = index
				}
			}
			if fallbackCandidate >= 0 {
				layer = []int{fallbackCandidate}
			} else {
				for index := range remaining {
					if !completed[index] {
						layer = append(layer, index)
					}
				}
			}
		}
		layers = append(layers, layer)
		for _, index := range layer {
			completed[index] = true
		}
		for index := range remaining {
			for _, done := range layer {
				delete(remaining[index], done)
			}
		}
	}
	return layers, nil
}
