import json
from pathlib import Path

from .schemas.ai_blocks import AIBlocksRequestBase, GenerateRequest, NATIVE_OPCODES, RequirementsRequest


SCRATCH_CODE_GENERATION_PROMPT = (
    Path(__file__).resolve().parents[2] / 'docs' / 'scratch代码生成提示词.md'
).read_text(encoding='utf-8').strip()


SCRATCH_CODE_STANDARD = (
    'Scratch 代码规范：'
    '1. 每条脚本从事件帽积木开始，script.blocks 按实际执行顺序排列；事件积木不能包含子栈。'
    '2. 循环和条件体只放在 substacks；键仅可为 SUBSTACK 或 SUBSTACK2，禁止 SUBSTACK1。'
    '3. 只使用允许的 Scratch 3 原生 opcode，并严格使用对应的 fields、inputs 与数据类型；字段与输入不得互换。'
    'number 和 angle 的 value 必须是无引号 JSON 数字，例如 {"kind":"angle","value":90}；text 和 color 的 value 必须是字符串。'
    '例如 sound_play 使用 inputs.SOUND_MENU(kind=text)，不用 fields.SOUND；data_setvariableto 的 inputs.VALUE 使用 kind=text。'
    '4. 使用最小且直接的实现，优先使用原生复合积木；motion_ifonedgebounce 已含边缘检测，不再套条件。'
    '5. 不把事件积木放入子栈，不在 control_forever 后追加同层积木，不生成空脚本。'
    '6. 先把用户需求拆成可验证的行为断言：触发条件、持续/重复关系、动作、前置条件、停止或暂停条件、恢复条件、顺序关系、边界条件和成功标准；每个核心断言都必须在规划和 ProgramIR 中有对应实现，不能只保留关键词。'
    '7. 将行为断言映射为最小的原生积木结构：循环表达持续关系，条件积木表达前置/分支关系，条件体只放受该条件控制的动作；需要暂停或恢复时表达为动作守卫或明确状态转换，不能把动作放在守卫外导致条件失效。'
    '8. 选择积木时以 BLOCK_CATALOG 的 opcode 契约和目标类型为准；优先使用语义完整的原生复合积木，禁止根据自然语言臆造 opcode、字段、输入、事件或传感器。'
    '9. 生成前检查需求覆盖、执行顺序、循环/条件作用域、终止块位置和目标合法性；发现需求无法由当前原生积木表达时输出 UNSUPPORTED_SCRATCH_REQUEST，不要用近似行为冒充实现。'
    '10. 最终只输出一个符合 ProgramIR 的严格 JSON 对象，不得包含 Markdown、注释或解释。'
)


def _context(request: AIBlocksRequestBase) -> str:
    target = request.target
    return json.dumps({
        'name': target.name,
        'is_stage': target.is_stage,
        'has_scripts': target.has_scripts,
        'mode': target.mode,
        'costumes': target.costumes,
        'sounds': target.sounds,
        'sprite_names': target.sprite_names,
        'current_program': json.loads(target.current_program) if target.current_program else {},
    }, ensure_ascii=False, separators=(',', ':'))


def _conversation(request: AIBlocksRequestBase) -> list[dict[str, str]]:
    return [{'role': item.role, 'content': item.content} for item in request.messages]


def build_chat_messages(request: AIBlocksRequestBase) -> list[dict[str, str]]:
    system = (
        '你是 Scratch 功能需求助手。优先推进实现，不要把简单需求复杂化。'
        '触发方式、数值、时长、坐标、资源选择等未说明细节使用最简单且符合 Scratch 习惯的默认值，不要为这些细节追问。'
        '先由你判断用户是否在描述可由 Scratch 原生积木实现的角色或舞台行为；不要使用关键词匹配。'
        '如果无法理解用户想实现什么，或请求与 Scratch 积木逻辑无关，必须只回复：无法帮到你。'
        '只有确定是积木需求但缺少一个核心条件时才提问，而且最多只问一个关键问题。'
        '只要已有可实现目标，或用户说开始写、直接生成、按默认处理、不用再问，就不要再提问，简短确认可以开始生成。'
        '禁止输出代码、JSON、ProgramIR、opcode 或积木结构，也不要声称已经修改项目。'
        '本对话只处理 Scratch 积木逻辑；如果用户无法描述可实现的角色/舞台行为，或请求与积木逻辑无关，必须只回复：无法帮到你。'
    )
    content = f'目标上下文：{_context(request)}' + chr(10) + '判断是否确实需要一个关键问题；否则确认可以开始生成。'
    return [{'role': 'system', 'content': system}, *_conversation(request), {'role': 'user', 'content': content}]


def build_requirements_messages(request: RequirementsRequest) -> list[dict[str, str]]:
    proceed_rule = (
        '用户已明确要求立即开始。只要对话中存在任何可实现的功能目标，必须 ready=true、missing_questions=[]，并自行补齐默认值。'
        if request.proceed_with_defaults
        else '只要核心行为明确就设置 ready=true；不要因为次要细节未说明而拒绝开始。'
    )
    system = (
        '根据对话和目标上下文整理 Scratch 功能需求。优先开始实现，不要把简单需求复杂化。'
        '未说明的触发方式默认点击绿旗；未说明的数值、时长、坐标和操作方式采用最简单的 Scratch 常用值；'
        '未指定造型或声音时优先使用目标已有资源，必要时可省略非核心效果。不要追问这些可默认处理的细节。'
        '先判断用户是否确实在描述可由 Scratch 原生积木实现的角色或舞台行为，不要使用关键词匹配。'
        '如果无法理解用户意图，或请求与 Scratch 积木逻辑无关，设置 ready=false 且 missing_questions=[]；这表示前端应直接回复“无法帮到你”。'
        '只有确定是积木需求但缺少一个核心条件时才设置 ready=false，并在 missing_questions 中给出最多一个关键问题。'
        + proceed_rule +
        '只输出一个 JSON 对象，不得输出解释或额外文本。'
        '对象必须且只能包含 ready,title,goal,behavior_steps,triggers,controls,success_criteria,missing_questions。'
        'ready 为布尔值；title 和 goal 为字符串；其余字段均为字符串数组。'
    )
    content = chr(10).join([
        f'目标上下文：{_context(request)}',
        f'立即使用默认值开始：{str(request.proceed_with_defaults).lower()}',
        '现在输出严格 JSON。',
    ])
    return [{'role': 'system', 'content': system}, *_conversation(request), {'role': 'user', 'content': content}]


def _append_repair_context(
    messages: list[dict[str, str]],
    output_name: str,
    failed_attempts: list[tuple[str, str]],
) -> list[dict[str, str]]:
    repaired = list(messages)
    for index, (invalid_output, validation_error) in enumerate(failed_attempts, start=1):
        repaired.extend([
            {'role': 'assistant', 'content': invalid_output[:30_000]},
            {'role': 'user', 'content': (
                f'第 {index} 次 {output_name} 未通过服务器解析或校验。'
                '请保留已正确部分，并严格根据下面的错误信息修正。'
                '重新只输出完整严格 JSON，不要解释、不要省略字段、不要使用 markdown。'
                '修复时逐项对照 BLOCK_CATALOG：字段只能放在 fields，输入只能放在 inputs；缺少 required input 必须补齐，不能把字段名改成近似名称；reporter/boolean 只能作为 inputs.kind=block 的嵌套块。若上次输出被截断，重新从头输出一个更短的完整 JSON，不要续写残片。'
                '\n解析或校验错误：' + validation_error[:12_000]
            )},
        ])
    return repaired


def build_requirements_repair_messages(
    request: RequirementsRequest,
    failed_attempts: list[tuple[str, str]],
) -> list[dict[str, str]]:
    return _append_repair_context(
        build_requirements_messages(request), '需求 JSON', failed_attempts,
    )


def build_plan_messages(request: GenerateRequest) -> list[dict[str, str]]:
    system = (
        '你是 Scratch 原生积木规划器。根据需求和目标上下文规划实现步骤。'
        '先将需求拆成触发、状态、动作、条件、时序和成功标准，再把每个行为映射到原生积木。'
        '规划必须明确每个条件控制哪些动作、每个循环覆盖哪些动作、停止或暂停后如何恢复，以及各行为的执行顺序；不得只列出积木名称或复述用户原句。'
        '规划完成后逐条核对用户需求是否都有对应步骤；无法由当前原生积木准确表达的行为必须标记为不支持，不能用相近行为替代。'
        '只输出一个严格 JSON 对象 {"steps":["..."]}，不得输出代码、ProgramIR 或额外文本。'
    )
    content = chr(10).join([
        f'需求：{json.dumps(request.requirements.model_dump(mode="json"), ensure_ascii=False)}',
        f'对话：{json.dumps([m.model_dump() for m in request.messages], ensure_ascii=False)}',
        f'目标上下文：{_context(request)}',
    ])
    return [{'role': 'system', 'content': system}, {'role': 'user', 'content': content}]


def build_plan_repair_messages(
    request: GenerateRequest,
    failed_attempts: list[tuple[str, str]],
) -> list[dict[str, str]]:
    return _append_repair_context(
        build_plan_messages(request), '规划 JSON', failed_attempts,
    )


def build_program_messages(request: GenerateRequest, steps: list[str]) -> list[dict[str, str]]:
    mode_rules = {
        'append': 'append: 只新增脚本，remove_script_ids 必须为空，不得移除或改写旧脚本。',
        'rewrite': 'rewrite: 前端会仅清空当前目标的脚本；保留角色/舞台、造型、声音、变量、列表等资源，remove_script_ids 必须为空。',
        'modify': 'modify: 可通过 current_program 中的 script_N 标识在 remove_script_ids 引用要移除或替换的旧脚本。',
    }
    schema = (
        '{"schema_version":1,"summary":"...","remove_script_ids":[],"variables":[],"lists":[],'
        '"broadcasts":[],"scripts":[{"id":"script_new_1","purpose":"...","x":0,"y":0,'
        '"blocks":[{"opcode":"motion_movesteps","fields":{},'
        '"inputs":{"STEPS":{"kind":"number","value":10}},"substacks":{}}]}]}'
    )
    system = (
        '你是 Scratch ProgramIR 生成器。'
        '以下是本项目的 Scratch AI 积木生成规范，必须完整遵循：\n'
        f'{SCRATCH_CODE_GENERATION_PROMPT}\n'
        f'{SCRATCH_CODE_STANDARD}'
        '禁止 extensions、procedures、自定义积木和未知 opcode。'
        'ProgramIR schema_version 必须为 1。字面量 Input 使用 {"kind":"number|text|angle|color","value":...}；'
        'number/angle 的 value 必须是 JSON 数字，禁止使用带引号数字或方向文字；text/color 的 value 必须是 JSON 字符串；sensing_touchingobject 的 TOUCHINGOBJECTMENU 使用 _mouse_ 表示鼠标指针、_edge_ 表示边缘，不要使用 mouse-pointer 等界面显示文本。'
        '嵌套积木 Input 使用 {"kind":"block","block":{...}}，禁止 type 字段。'
        '变量只允许 name,value,scope；列表只允许 name,value,scope；scope 只能是 local 或 global。'
        'Block 仅有 opcode,fields,inputs,substacks；fields 值允许字符串、数字或布尔值；substacks 的值是 Block 数组。'
        '只有 control_repeat、control_forever、control_if、control_if_else、control_repeat_until 可以包含子栈。'
        '禁止虚构 sensing_touchingedge，也不要为反弹额外包 control_if。'
        'current_program 中 protected=true 的脚本包含自定义积木或扩展，必须原样保留，remove_script_ids 禁止引用。'
        f'允许 opcode：{",".join(sorted(NATIVE_OPCODES))}。{mode_rules[request.target.mode]}'
    )
    content = chr(10).join([
        f'计划：{json.dumps(steps, ensure_ascii=False)}',
        f'需求：{json.dumps(request.requirements.model_dump(mode="json"), ensure_ascii=False)}',
        f'目标上下文：{_context(request)}',
        f'结构示例：{schema}',
    ])
    return [{'role': 'system', 'content': system}, {'role': 'user', 'content': content}]


def build_program_repair_messages(
    request: GenerateRequest,
    steps: list[str],
    failed_attempts: list[tuple[str, str]],
) -> list[dict[str, str]]:
    return _append_repair_context(
        build_program_messages(request, steps), 'ProgramIR', failed_attempts,
    )
