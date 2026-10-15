package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ai-scratch-web/backend/internal/blockcatalog"
	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/resourcecontract"
)

// Message is one prompt conversation message.
type Message struct {
	Role    string
	Content string
}

// ProjectContext describes the whole Scratch project for the prompt prefix.
type ProjectContext struct {
	ProjectID   string
	Version     int
	Targets     []map[string]any
	SharedState map[string]any
}

// TargetContext describes the target being edited.
type TargetContext struct {
	TargetID       string
	Revision       int
	Name           string
	IsStage        bool
	HasScripts     bool
	Mode           string
	Costumes       []string
	Backdrops      []string
	Sounds         []string
	SpriteNames    []string
	CurrentProgram string
}

// DefaultProjectContext mirrors the Python dataclass defaults.
func DefaultProjectContext() ProjectContext {
	return ProjectContext{ProjectID: "scratch-project", Version: 0, Targets: []map[string]any{}, SharedState: map[string]any{}}
}

// DefaultTargetContext mirrors the Python dataclass defaults.
func DefaultTargetContext() TargetContext {
	return TargetContext{TargetID: "current-target", Revision: 0, Mode: "append", CurrentProgram: "{}"}
}

// CacheMetadata reports which stable prompt layers were reused.
type CacheMetadata struct {
	Keys map[string]string
	Hits map[string]bool
}

// EstimatedHitRate returns the fraction of cached layers that were reused.
func (m CacheMetadata) EstimatedHitRate() float64 {
	if len(m.Hits) == 0 {
		return 0
	}
	hits := 0
	for _, value := range m.Hits {
		if value {
			hits++
		}
	}
	return float64(hits) / float64(len(m.Hits))
}

// AsDict renders the metadata with the same keys as the Python reference.
func (m CacheMetadata) AsDict() map[string]any {
	return map[string]any{
		"keys":               m.Keys,
		"hits":               m.Hits,
		"estimated_hit_rate": m.EstimatedHitRate(),
	}
}

// Request is the prompt-relevant projection of a unified generation request.
type Request struct {
	Scope               string
	AssetPolicy         string
	ProceedWithDefaults bool
	PermittedExtensions []string
	TargetMode          string
	Messages            []Message
}

// SegmentNames lists the segments accepted by the prompt builder.
var SegmentNames = []string{"requirements", "task_spec", "blueprint", "plan", "manifest", "resource_op", "program"}

// KnowledgeBaseVersion identifies the cached knowledge layer.
const KnowledgeBaseVersion = "scratch-ai-v2"

// CacheTTLSeconds bounds the stable prompt layer cache.
const CacheTTLSeconds = 300

// PromptBuilder builds one stable system prefix plus request-specific data.
type PromptBuilder struct {
	Cache                       *Cache
	KnowledgeBaseOverride       *string
	EnabledExtensions           map[string]bool
	AllowDestructiveResourceOps bool
}

// NewPromptBuilder builds a prompt builder with the deployment capabilities.
func NewPromptBuilder(cache *Cache, knowledgeBase *string, enabledExtensions map[string]bool, allowDestructiveResourceOps bool) *PromptBuilder {
	if cache == nil {
		cache = NewCache()
	}
	if enabledExtensions == nil {
		enabledExtensions = map[string]bool{}
	}
	return &PromptBuilder{
		Cache:                       cache,
		KnowledgeBaseOverride:       knowledgeBase,
		EnabledExtensions:           enabledExtensions,
		AllowDestructiveResourceOps: allowDestructiveResourceOps,
	}
}

// RenderSegmentJSONSchema renders the canonical schema for a segment so prompt
// contracts cannot drift from validation.
func RenderSegmentJSONSchema(segment string, allowDestructiveResourceOps bool) string {
	raw, ok := SegmentJSONSchemas[segment]
	if !ok {
		return ""
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return ""
	}
	schema, ok := parsed.(map[string]any)
	if !ok {
		return ""
	}
	if segment == "resource_op" && !allowDestructiveResourceOps {
		blocked := map[string]bool{"#/$defs/DeleteSpriteOp": true, "#/$defs/DeleteSoundOp": true}
		if alternatives, ok := schema["oneOf"].([]any); ok {
			kept := make([]any, 0, len(alternatives))
			for _, item := range alternatives {
				reference, _ := item.(map[string]any)
				if reference != nil && blocked[asString(reference["$ref"])] {
					continue
				}
				kept = append(kept, item)
			}
			schema["oneOf"] = kept
		}
		if discriminator, ok := schema["discriminator"].(map[string]any); ok {
			if mapping, ok := discriminator["mapping"].(map[string]any); ok {
				delete(mapping, "delete_sprite")
				delete(mapping, "delete_sound")
			}
		}
		if definitions, ok := schema["$defs"].(map[string]any); ok {
			delete(definitions, "DeleteSpriteOp")
			delete(definitions, "DeleteSoundOp")
		}
	}
	encoded, err := jsonx.Canonical(schema)
	if err != nil {
		return ""
	}
	return "CANONICAL_OUTPUT_JSON_SCHEMA (all properties, enum values, required arrays and limits are authoritative):\n" + encoded
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func (b *PromptBuilder) enabledExtensionsForRequest(request Request) map[string]bool {
	result := map[string]bool{}
	permitted := map[string]bool{}
	for _, extension := range request.PermittedExtensions {
		permitted[extension] = true
	}
	for extension := range b.EnabledExtensions {
		if extension == "text2speech" && !permitted[extension] {
			continue
		}
		result[extension] = true
	}
	return result
}

// BuildUnifiedPrompt builds the system prompt and the provider conversation.
func (b *PromptBuilder) BuildUnifiedPrompt(
	project *ProjectContext,
	target TargetContext,
	request Request,
	transport string,
	segment string,
) ([]Message, CacheMetadata) {
	if transport == "" {
		transport = "tagged"
	}
	resolvedProject := DefaultProjectContext()
	if project != nil {
		resolvedProject = *project
	}
	if resolvedProject.Targets == nil {
		resolvedProject.Targets = []map[string]any{}
	}
	if resolvedProject.SharedState == nil {
		resolvedProject.SharedState = map[string]any{}
	}
	if target.Mode == "" {
		target.Mode = "append"
	}

	extensions := b.enabledExtensionsForRequest(request)
	level1Key := "knowledge:" + KnowledgeBaseVersion + ":extensions=" + joinKeys(extensions)
	level1, level1Hit := b.Cache.GetOrBuild(level1Key, func() any {
		return b.BuildKnowledgeBase(extensions)
	}, CacheTTLSeconds)
	level2Key := contextKey("project", resolvedProject.ProjectID, resolvedProject.Version, projectFingerprintValue(resolvedProject))
	level2, level2Hit := b.Cache.GetOrBuild(level2Key, func() any {
		return BuildProjectContext(resolvedProject)
	}, CacheTTLSeconds)
	level3Key := contextKey("target", target.TargetID, target.Revision, targetFingerprintValue(target))
	level3, level3Hit := b.Cache.GetOrBuild(level3Key, func() any {
		return BuildTargetContext(target)
	}, CacheTTLSeconds)
	level4 := BuildRequestContext(request)

	system := strings.Join([]string{
		asString(level1),
		asString(level2),
		asString(level3),
		level4,
		b.buildUnifiedOutputContract(request, extensions, transport, segment),
	}, "\n")

	messages := []Message{{Role: "system", Content: system}}
	for _, message := range request.Messages {
		messages = append(messages, Message{Role: message.Role, Content: message.Content})
	}
	return messages, CacheMetadata{
		Keys: map[string]string{"knowledge_base": level1Key, "project": level2Key, "target": level3Key},
		Hits: map[string]bool{"knowledge_base": level1Hit, "project": level2Hit, "target": level3Hit},
	}
}

func joinKeys(values map[string]bool) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return strings.Join(keys, ",")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// BuildSegmentPrompt appends the segment instruction to the unified prompt.
func (b *PromptBuilder) BuildSegmentPrompt(
	project *ProjectContext,
	target TargetContext,
	request Request,
	segment string,
	context map[string]any,
	targetIndex *int,
	resourceOpIndex *int,
) ([]Message, CacheMetadata) {
	transport := "tagged"
	if context != nil {
		if value, ok := context["transport"].(string); ok && value == "json_object" {
			transport = "json_object"
		}
	}
	messages, cache := b.BuildUnifiedPrompt(project, target, request, transport, segment)
	contextJSON, err := jsonx.Canonical(orEmptyMap(context))
	if err != nil {
		contextJSON = "{}"
	}

	details := map[string]string{
		"requirements": "输出且只输出一个 <requirements>JSON</requirements> 标签。",
		"task_spec":    "输出且只输出一个 <task_spec>JSON</task_spec> 标签；不要重出 requirements、blueprint、plan 或其他标签。TaskSpec 中条件只能出现在 behavior.conditions 或 loop.until，touching_edge 不是 action，禁止把它放入 actions。",
		"blueprint":    segmentBlueprintInstruction,
		"plan":         "输出且只输出一个 <plan>JSON</plan> 标签，不要重出 requirements。steps 每项最多 120 个字符。",
		"manifest":     "输出且只输出一个 <manifest>JSON</manifest> 标签，不要输出 requirements、plan 或 program。",
		"resource_op":  "输出且只输出一个 <resource_op>JSON</resource_op> 标签。该对象替换 resource_ops 中的指定位置。",
		"program":      "输出且只输出一个 target_index=" + itoaDefault(targetIndex) + " 的完整 <program> 标签，不要输出其他目标。必须使用带换行与标准缩进的格式化 JSON 输出，严禁单行压缩；严禁在 inputs.CONDITION 中嵌套多层复合逻辑（多条件必须拆为嵌套 control_if 或分步判断）；在输出 substacks 前仔细核对 inputs 嵌套 block 的所有闭合花括号。",
	}
	if transport == "json_object" {
		details["requirements"] = "只输出一个 JSON object，内容是 requirements 对象。"
		details["task_spec"] = "只输出一个 JSON object，内容是 ScratchTaskSpec 对象；不要重出其他 segment。TaskSpec 中条件只能出现在 behavior.conditions 或 loop.until，touching_edge 不是 action，禁止把它放入 actions。"
		details["blueprint"] = segmentBlueprintJSONInstruction
		details["plan"] = "只输出一个 JSON object，内容是 plan 对象，不要重出 requirements。steps 每项最多 120 个字符。"
		details["manifest"] = "只输出一个 JSON object，内容是 manifest 对象，不要输出 requirements、plan 或 program。"
		details["resource_op"] = "只输出一个 JSON object，内容是要替换的 resource_op 对象。"
		details["program"] = "只输出一个 JSON object，内容是当前 target_index 的完整 ProgramIR 对象，不要输出其他目标。必须使用带换行与标准缩进的漂亮格式（Pretty-printed JSON）输出，禁止单行无换行压缩；保持积木结构扁平直观，严禁在 inputs.CONDITION 中进行深层复合逻辑嵌套（多条件必须拆分为嵌套 control_if）；输出 \"substacks\" 前仔细核查 inputs 嵌套积木的所有闭合花括号是否完整。"
	}

	targetNote := ""
	if targetIndex != nil {
		targetNote = " target_index=" + itoa(*targetIndex) + "."
	}
	operationNote := ""
	if resourceOpIndex != nil {
		operationNote = " resource_op_index=" + itoa(*resourceOpIndex) + "."
	}
	outputBoundary := "标签外文本"
	if transport == "json_object" {
		outputBoundary = "JSON object 之外的文本"
	}
	failureNote := ""
	if failure, ok := context["failure"].(map[string]any); ok {
		detail, _ := failure["detail"].(string)
		message, _ := failure["message"].(string)
		path, _ := failure["path"].(string)
		hint := RepairHint(segment, path, detail)
		if path == "/output" && detail == "" {
			failureNote = "\n上一轮结构化响应正文为空或不是 JSON；本轮必须实际输出非空 JSON object，不能只输出 reasoning。"
		} else if hint != "" {
			failureNote = "\n上一轮输出未通过校验：" + orString(detail, message) + "。\n修复指导：" + hint
		} else if detail != "" || message != "" {
			failureNote = "\n上一轮输出未通过校验：" + orString(detail, message) + "。请严格对照契约修正。"
		}
	}

	instruction := "SEGMENT OUTPUT CONTRACT\n" +
		details[segment] +
		targetNote +
		operationNote +
		"\n必要的已通过上下文（只用于生成本段）：" +
		contextJSON +
		"\n禁止 Markdown、解释、代码围栏和" + outputBoundary + "。" +
		failureNote
	messages = append(messages, Message{Role: "system", Content: instruction})
	return messages, cache
}

// BuildChatMessages builds the read-only project assistant conversation.
func (b *PromptBuilder) BuildChatMessages(targetJSON json.RawMessage, messages []Message) []Message {
	system := "你是 Scratch 项目需求助手。只回答用户问题，不生成 ProgramIR，不修改项目。" +
		"回答简洁,具体，并指出当前 Scratch 原生能力的限制。"
	target := "{}"
	if len(targetJSON) > 0 {
		target = string(targetJSON)
	}
	result := []Message{{Role: "system", Content: system + "\n[TARGET_CONTEXT]\n" + target}}
	result = append(result, messages...)
	return result
}

func (b *PromptBuilder) buildUnifiedOutputContract(request Request, enabledExtensions map[string]bool, transport, segment string) string {
	if request.Scope == "single_sprite" && transport == "tagged" {
		return strings.Join([]string{
			"FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)",
			"Output tags in this exact order: requirements, plan, then one <program target_index=0>. Every tag body is exactly one JSON object. Output no text outside tags.",
			JSONObjectSegmentContracts["requirements"],
			JSONObjectSegmentContracts["plan"],
			JSONObjectSegmentContracts["program"],
			resourcecontract.RenderOperationContract(b.AllowDestructiveResourceOps),
		}, "\n")
	}
	scopeNote := "This request has scope=single_sprite. Do not emit a manifest. Emit exactly one program and its target_index must be 0."
	if request.Scope == "project" {
		scopeNote = "This request has scope=project. Emit task_spec, blueprint, display plan, then manifest; afterwards emit one program for each entry in manifest.program_targets. " +
			"Use target_index values 0, 1, 2, ... in exactly that order."
	}
	if transport == "json_object" {
		segmentNote := segment
		if segmentNote == "" {
			segmentNote = "current"
		}
		segmentContract, ok := JSONObjectSegmentContracts[segment]
		if !ok {
			segmentContract = "The JSON object must match the schema for the current segment."
		}
		if segment == "task_spec" {
			segmentContract = segmentContract + "\n" + TaskSpecEnumerationContract
		}
		if segment == "blueprint" {
			segmentContract = segmentContract + "\n" + BlueprintEnumerationContract
		}
		return strings.Join([]string{
			"FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)",
			"The transport for this " + segmentNote + " segment is one JSON object, not a tagged stream. Output exactly one JSON object for this segment and nothing else.",
			"Do not output XML-like tags, Markdown, code fences, explanations, reasoning, or any text outside the JSON object.",
			segmentContract,
			RenderSegmentJSONSchema(segmentNote, b.AllowDestructiveResourceOps),
			resourcecontract.RenderOperationContract(b.AllowDestructiveResourceOps),
			scopeNote + " Generate only the current segment; do not emit any other segment.",
		}, "\n")
	}
	extensionKeys := make([]string, 0, len(enabledExtensions))
	for key := range enabledExtensions {
		extensionKeys = append(extensionKeys, key)
	}
	sortStrings(extensionKeys)
	extensionJSON, _ := jsonx.Canonical(extensionKeys)
	extensionNote := "Enabled built-in extension IDs for this request: " + extensionJSON + ". Do not use any other extension ID or extension opcode."
	schemaContract := ""
	if segment != "" {
		schemaContract = RenderSegmentJSONSchema(segment, b.AllowDestructiveResourceOps)
	}
	return strings.Join([]string{
		strings.TrimSpace(UnifiedOutputContract),
		schemaContract,
		resourcecontract.RenderOperationContract(b.AllowDestructiveResourceOps),
		extensionNote,
		scopeNote,
	}, "\n")
}

// BuildKnowledgeBase renders the cached level-1 prompt layer.
func (b *PromptBuilder) BuildKnowledgeBase(enabledExtensions map[string]bool) string {
	source := ""
	if b.KnowledgeBaseOverride != nil {
		source = strings.TrimSpace(*b.KnowledgeBaseOverride)
	} else if text, ok := loadKnowledgeBaseDocument(); ok {
		source = strings.TrimSpace(text)
	}

	opcodes := make([]string, 0, len(blockcatalog.BlockContracts))
	for opcode := range blockcatalog.BlockContracts {
		extension := blockcatalog.ExtensionForOpcode(opcode)
		if extension == "" || enabledExtensions[extension] {
			opcodes = append(opcodes, opcode)
		}
	}
	sortStrings(opcodes)

	catalog := orderedMap{}
	for _, opcode := range opcodes {
		contract := blockcatalog.BlockContracts[opcode]
		entry := orderedMap{}
		entry.set("shape", string(contract.Shape))
		entry.set("targets", sortedKeysOf(contract.Targets))
		entry.set("fields", sortedKeysOf(contract.Fields))
		entry.set("resolved_fields", sortedKeysOf(contract.ResolvedFields))
		inputs := orderedMap{}
		order := contract.InputOrder
		if len(order) == 0 {
			order = sortedInputKeys(contract.Inputs)
		}
		for _, name := range order {
			inputs.set(name, string(contract.Inputs[name]))
		}
		entry.set("inputs", inputs)
		entry.set("substacks", sortedKeysOf(contract.Substacks))
		catalog.set(opcode, entry)
	}
	catalogJSON, err := marshalOrdered(catalog)
	if err != nil {
		catalogJSON = "{}"
	}
	return strings.Join([]string{source, "BLOCK_CATALOG=" + catalogJSON, UnifiedGenerationProtocol}, "\n")
}

func sortedInputKeys(values map[string]blockcatalog.InputKind) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortedKeysOf(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func contextKey(prefix, identity string, revision int, value any) string {
	serialized, err := jsonx.Canonical(value)
	if err != nil {
		serialized = ""
	}
	sum := sha256.Sum256([]byte(serialized))
	fingerprint := hex.EncodeToString(sum[:])[:16]
	return prefix + ":" + identity + ":v" + itoa(revision) + ":" + fingerprint
}

// BuildProjectContext renders the cached level-2 prompt layer.
func BuildProjectContext(project ProjectContext) string {
	targets := project.Targets
	if targets == nil {
		targets = []map[string]any{}
	}
	sharedState := project.SharedState
	if sharedState == nil {
		sharedState = map[string]any{}
	}
	value := projectContextJSON{
		ProjectID:   project.ProjectID,
		Version:     project.Version,
		Targets:     targets,
		SharedState: sharedState,
	}
	encoded, _ := jsonx.Marshal(value)
	return "[PROJECT_CONTEXT]\n" + string(encoded)
}

// BuildTargetContext renders the cached level-3 prompt layer.
func BuildTargetContext(target TargetContext) string {
	value := targetContextJSON{
		TargetID:       target.TargetID,
		Revision:       target.Revision,
		Name:           target.Name,
		IsStage:        target.IsStage,
		HasScripts:     target.HasScripts,
		Mode:           target.Mode,
		Costumes:       emptyIfNil(target.Costumes),
		Backdrops:      emptyIfNil(target.Backdrops),
		Sounds:         emptyIfNil(target.Sounds),
		SpriteNames:    emptyIfNil(target.SpriteNames),
		CurrentProgram: parseProgram(target.CurrentProgram),
	}
	encoded, _ := jsonx.Marshal(value)
	return "[TARGET_CONTEXT]\n" + string(encoded)
}

// BuildRequestContext renders the request-specific prompt layer.
func BuildRequestContext(request Request) string {
	value := requestContextJSON{
		Scope:               request.Scope,
		AssetPolicy:         request.AssetPolicy,
		ProceedWithDefaults: request.ProceedWithDefaults,
		PermittedExtensions: emptyIfNil(request.PermittedExtensions),
		Mode:                request.TargetMode,
	}
	encoded, _ := jsonx.Marshal(value)
	return "[REQUEST_CONTEXT]\n" + string(encoded)
}

func parseProgram(value string) any {
	if value == "" {
		return map[string]any{}
	}
	parsed, err := jsonx.Parse([]byte(value))
	if err != nil {
		return map[string]any{"unavailable": true}
	}
	return parsed
}

func emptyIfNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func orEmptyMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func orString(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func itoa(value int) string {
	return stringInt(value)
}

func itoaDefault(value *int) string {
	if value == nil {
		return "0"
	}
	return stringInt(*value)
}

func loadKnowledgeBaseDocument() (string, bool) {
	for _, candidate := range knowledgeBaseCandidates() {
		data, err := os.ReadFile(candidate)
		if err == nil && len(data) > 0 {
			return string(data), true
		}
	}
	if EmbeddedKnowledgeBase != "" {
		return EmbeddedKnowledgeBase, true
	}
	return "", false
}

func knowledgeBaseCandidates() []string {
	relative := filepath.Join("docs", "参考资料", "ProgramIR-v1生成提示词.md")
	candidates := []string{}
	if override := os.Getenv("AI_SCRATCH_KNOWLEDGE_BASE"); override != "" {
		candidates = append(candidates, override)
	}
	workingDirectory, err := os.Getwd()
	if err == nil {
		candidates = append(candidates,
			filepath.Join(workingDirectory, relative),
			filepath.Join(workingDirectory, "..", relative),
		)
	}
	if executable, err := os.Executable(); err == nil {
		directory := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(directory, relative),
			filepath.Join(directory, "..", "..", "..", relative),
		)
	}
	return candidates
}
