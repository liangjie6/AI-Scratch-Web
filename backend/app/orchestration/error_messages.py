from __future__ import annotations

import re


_PATH_HINTS = (
    ('resource_ops', '资源操作必须严格匹配资源契约；检查 op、op_id、target、source 以及互斥字段。'),
    ('mode', '项目模式下所有非当前目标（舞台、已有角色、新建角色）的 mode 只能为 "append"，严禁使用 rewrite 或 modify。'),
    ('program_targets', '项目目标必须引用舞台、已有角色、已创建角色或 current_target，并按清单顺序生成。'),
    ('shared_state', '项目全局变量、列表和广播必须与 manifest 完整一致；目标私有变量和列表要标记为 local。'),
    ('scripts', '脚本 id、数量、积木层级和输入类型必须符合 ProgramIR 与原生积木目录。'),
    ('blocks', '检查 opcode、inputs、substacks 和 reporter 类型，不要输出目录之外的积木。'),
    ('current_target', '当前目标才允许使用 modify 或 rewrite；其他项目目标只能使用 append。'),
)

_DETAIL_HINTS = (
    ('/title', '补齐 ScratchTaskSpec 必填顶层字段 title 和 goal，并确认输出的是完整 TaskSpec 对象。不要用 name、entity_type、target 或 states 等旧字段替代 schema 字段。'),
    ('/goal', '补齐 ScratchTaskSpec 必填顶层字段 title 和 goal，并确认输出的是完整 TaskSpec 对象。不要用 name、entity_type、target 或 states 等旧字段替代 schema 字段。'),
    ('/entities', 'TaskEntity.role 只能是 stage 或 sprite；每个实体必须包含 entity_id、role、description。existing_target_ref 必须是对象，kind 只能是 current_target、stage 或 existing_sprite，不能写字符串。'),
    ('/state', 'TaskState 必须包含 state_id、kind、scope；kind 只能是 variable、list、broadcast，scope 只能是 global、local。global 不得有 owner_entity_id，local 必须有 owner_entity_id。'),
    ('/behaviors', 'TaskBehavior 必须包含 behavior_id、owner_entity_id、trigger、actions；actions 必须是非空数组，不能只输出 description。trigger.kind 只能使用 TASKSPEC ENUMERATION CONTRACT 列出的值。'),
    ('/assumptions', 'assumptions 的每一项必须同时包含 id、text、confidence、requires_confirmation；confidence 必须是 0 到 1 的数字（例如 0.95），不能写 high/medium/low。'),
    ('blueprint state_plan must project every TaskSpec state', 'Blueprint 的 state_plan 必须且只能投影 TaskSpec.state 中的每一个状态；每个 state_plan 项的 ref_id 必须严格等于对应 TaskSpec 状态的 state_id（例如 state_id 是“得分”，ref_id 必须严格使用“得分”，严禁自创英文 ID 如 state_score 或 ref_score），name 与 ref_id 保持一致，且保持相同 kind、scope 和 owner。'),
    ('blueprint state_plan must preserve TaskSpec state semantics', 'Blueprint 的 state_plan 中的 kind、scope 和 owner_entity_id 必须与 TaskSpec 对应 state 完全一致。'),
    ('blueprint must bind every TaskSpec entity exactly once', 'Blueprint 的 entity_bindings 必须且只能绑定 TaskSpec 中的每个实体一次。'),
    ('blueprint behavior graph must project every TaskSpec behavior', 'Blueprint 的 behavior_graph 必须且只能覆盖 TaskSpec 中的每个行为。'),
    ('blueprint program jobs dependencies cannot contain a cycle', 'Blueprint 的程序目标（program_jobs）之间存在循环依赖。不同角色（如小猫和红包）的行为之间严禁相互设置 depends_on（例如角色 A 依赖角色 B，同时角色 B 又依赖角色 A）。修复方法：删除跨角色的 depends_on，不同角色的玩法联动一律通过共享变量（如得分）或广播解耦，depends_on 仅用于同一角色内部不同行为的前后关系。'),
    ('blueprint behavior dependencies cannot contain a cycle', 'Blueprint 的行为图（behavior_graph）存在循环依赖。请检查每个 behavior 的 depends_on 列表，确保所有依赖构成严格的有向无环图（DAG），不要相互依赖。'),
    ('行为依赖存在循环', 'Blueprint 的程序目标（program_jobs）之间存在循环依赖。请删除不同角色行为之间相互声明的 depends_on，跨角色联动一律通过全局变量或广播通信。'),
    ('blueprint task_spec_hash does not match', 'Blueprint 的 task_spec_hash 必须与上下文中的 task_spec_hash 完全一致。'),
    ('manifest program target must preserve blueprint job fields', 'Manifest 的 program_targets 必须继承 Blueprint 的 job_id、owner_entity_id、behavior_ids 和 mode。注意：项目模式下所有角色与舞台的 mode 必须全部为 "append"，严禁使用 rewrite 或 modify。'),
    ('stage, existing_sprite and created_sprite programs must use append', '项目模式下所有非当前目标（舞台、已有角色、新建角色）的 mode 只能为 "append"，严禁使用 rewrite 或 modify。'),
    ('trailing content', '删除 JSON 对象后的所有额外文本。'),
    ('invalid JSON', '只输出一个合法 JSON 对象，不要使用 Markdown 代码围栏。'),
    ("expecting ',' delimiter", "输出的 JSON 存在语法错误：花括号 '}' 闭合不足或缺失逗号。通常是因为积木 inputs（如 CONDITION 嵌套的复合判断）或 substacks 层级过深，在无换行单行输出时漏打了闭合花括号 '}'。修复方法：1. 必须使用带换行和标准缩进的漂亮格式（Pretty-printed JSON）输出，禁止输出单行压缩 JSON；2. 严禁在 inputs.CONDITION 中嵌套多层 operator_and/operator_or，多条件必须拆分为嵌套 control_if 或分步判断；3. 仔细核对深层 block 嵌套处每一层 inputs、block 和 substacks 的花括号配对。"),
    ("expecting ':' delimiter", "输出的 JSON 存在语法错误：缺少冒号 ':'。请检查对象键值对格式，确保所有属性名用双引号包裹，并使用带换行缩进的标准 JSON 格式输出。"),
    ("expecting value", "输出的 JSON 存在语法错误：缺少有效值或包含多余的尾随逗号。请检查数组或对象末尾是否有多余逗号，确保每个字段值完整。"),
    ('unterminated string', '输出的 JSON 存在语法错误：字符串未正常闭合（双引号缺失或内容中包含未转义的换行符/双引号）。'),
    ('extra data', '输出的 JSON 存在多余数据：只能输出一个合法的顶层 JSON 对象，不要在对象之后附带额外文本或重复生成第二个对象。'),
    ('jsondecodeerror', '输出的 JSON 语法解析失败。请使用带标准换行与缩进的格式化 JSON 输出，不要输出单行压缩文本，并仔细核对每层积木的花括号闭合完整性。'),
    ('required input is missing', '补齐该 opcode 契约声明的所有必需输入。'),
    ('expected boolean', '将布尔输入改为嵌套 boolean reporter，而不是普通字符串。'),
    ('unknown opcode', '改用 BLOCK_CATALOG 中存在的原生 opcode。'),
)


def _array_index(path: str) -> str:
    match = re.search(r'(?:resource_ops|program_targets)(?:\.|\[)(\d+)', path)
    return f'这是数组第 {int(match.group(1)) + 1} 项。' if match else ''


def repair_hint(phase: str | None, path: str | None, detail: str | None) -> str:
    normalized_path = str(path or '')
    normalized_detail = str(detail or '')
    for marker, hint in _PATH_HINTS:
        if marker in normalized_path or marker in normalized_detail:
            return f'{_array_index(normalized_path)}{hint}'
    for marker, hint in _DETAIL_HINTS:
        if marker.lower() in normalized_detail.lower():
            return hint
    if phase == 'program':
        return '对照 ProgramIR schema 与 BLOCK_CATALOG 逐项检查 metadata、shared_state 和 scripts。'
    if phase == 'manifest':
        return '对照 ProjectManifest v3 检查资源操作、Blueprint 追溯、目标引用和状态 ref_id。'
    return '对照当前 segment 的 FINAL OUTPUT CONTRACT 检查字段、类型和输出顺序。'
