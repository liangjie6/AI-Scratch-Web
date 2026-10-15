package prompt

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// segmentBlueprintInstruction is the tagged-transport blueprint segment instruction.
const segmentBlueprintInstruction = "输出且只输出一个 <blueprint>JSON</blueprint> 标签；先建立成功标准覆盖清单：每个 verification=executable 的 criterion_id 必须在 acceptance_tests.source_criterion_id 中恰好出现一次，数量和集合必须完全相等，不能遗漏结束、碰撞、计分等标准；manual/structural/unsupported 标准禁止生成验收测试。behavior_graph 的 consumes_broadcasts/produces_broadcasts 只能填写 TaskSpec 广播的 state_id，并且必须与对应 broadcast action/trigger 完全匹配，不能填写广播显示名称。state_plan 必须且只能投影 TaskSpec.state 中的每一个状态，集合与数量必须严格完全一致；每个 state_plan 项的 ref_id 必须严格等于对应 TaskSpec 的 state_id（例如 state_id 为“得分”，则 ref_id 必须直接填“得分”，禁止自创新英文 ID 如 state_score 或 ref_score；name 与 ref_id 保持一致），kind、scope、owner_entity_id 必须与 TaskSpec 保持完全一致。严禁在不同角色行为之间形成互相循环依赖（例如角色1行为依赖角色2，角色2行为又依赖角色1；跨角色玩法联动一律通过共享变量或广播解耦，depends_on 仅用于同一角色内部前后关系，确保 program_jobs 之间拓扑无环）。program_jobs 的 mode 必须严格为 \"append\"（项目模式下舞台、已有角色和新建角色一律使用 \"append\"，严禁使用 rewrite 或 modify）。"

// segmentBlueprintJSONInstruction is the JSON-object-transport blueprint instruction.
const segmentBlueprintJSONInstruction = "只输出一个 JSON object，内容是 ProjectBlueprint 对象；先建立成功标准覆盖清单：每个 verification=executable 的 criterion_id 必须在 acceptance_tests.source_criterion_id 中恰好出现一次，数量和集合必须完全相等，不能遗漏结束、碰撞、计分等标准；manual/structural/unsupported 标准禁止生成验收测试。behavior_graph 的 consumes_broadcasts/produces_broadcasts 只能填写 TaskSpec 广播的 state_id，并且必须与对应 broadcast action/trigger 完全匹配，不能填写广播显示名称。state_plan 必须且只能投影 TaskSpec.state 中的每一个状态，集合与数量必须严格完全一致；每个 state_plan 项的 ref_id 必须严格等于对应 TaskSpec 的 state_id（例如 state_id 为“得分”，则 ref_id 必须直接填“得分”，禁止自创新英文 ID 如 state_score 或 ref_score；name 与 ref_id 保持一致），kind、scope、owner_entity_id 必须与 TaskSpec 保持完全一致。严禁在不同角色行为之间形成互相循环依赖（例如角色1行为依赖角色2，角色2行为又依赖角色1；跨角色玩法联动一律通过共享变量或广播解耦，depends_on 仅用于同一角色内部前后关系，确保 program_jobs 之间拓扑无环）。program_jobs 的 mode 必须严格为 \"append\"（项目模式下舞台、已有角色和新建角色一律使用 \"append\"，严禁使用 rewrite 或 modify）。"

type projectContextJSON struct {
	ProjectID   string           `json:"project_id"`
	Version     int              `json:"version"`
	Targets     []map[string]any `json:"targets"`
	SharedState map[string]any   `json:"shared_state"`
}

type targetContextJSON struct {
	TargetID       string   `json:"target_id"`
	Revision       int      `json:"revision"`
	Name           string   `json:"name"`
	IsStage        bool     `json:"is_stage"`
	HasScripts     bool     `json:"has_scripts"`
	Mode           string   `json:"mode"`
	Costumes       []string `json:"costumes"`
	Backdrops      []string `json:"backdrops"`
	Sounds         []string `json:"sounds"`
	SpriteNames    []string `json:"sprite_names"`
	CurrentProgram any      `json:"current_program"`
}

type requestContextJSON struct {
	Scope               string   `json:"scope"`
	AssetPolicy         string   `json:"asset_policy"`
	ProceedWithDefaults bool     `json:"proceed_with_defaults"`
	PermittedExtensions []string `json:"permitted_extensions"`
	Mode                string   `json:"mode"`
}

func projectFingerprintValue(project ProjectContext) map[string]any {
	targets := project.Targets
	if targets == nil {
		targets = []map[string]any{}
	}
	sharedState := project.SharedState
	if sharedState == nil {
		sharedState = map[string]any{}
	}
	return map[string]any{
		"project_id":   project.ProjectID,
		"version":      project.Version,
		"targets":      targets,
		"shared_state": sharedState,
	}
}

func targetFingerprintValue(target TargetContext) map[string]any {
	return map[string]any{
		"target_id":       target.TargetID,
		"revision":        target.Revision,
		"name":            target.Name,
		"is_stage":        target.IsStage,
		"has_scripts":     target.HasScripts,
		"mode":            target.Mode,
		"costumes":        emptyIfNil(target.Costumes),
		"backdrops":       emptyIfNil(target.Backdrops),
		"sounds":          emptyIfNil(target.Sounds),
		"sprite_names":    emptyIfNil(target.SpriteNames),
		"current_program": target.CurrentProgram,
	}
}

func stringInt(value int) string { return strconv.Itoa(value) }

// orderedMap preserves insertion order for the BLOCK_CATALOG prompt payload,
// matching Python's json.dumps without sort_keys.
type orderedMap struct {
	keys   []string
	values []any
}

func (m *orderedMap) set(key string, value any) {
	m.keys = append(m.keys, key)
	m.values = append(m.values, value)
}

// MarshalJSON emits the object with its keys in insertion order.
func (m orderedMap) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range m.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		encodedValue, err := json.Marshal(m.values[index])
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedValue)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func marshalOrdered(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(buffer.Bytes(), "\n")), nil
}
