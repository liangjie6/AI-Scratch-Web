import json
from pathlib import Path

from .schemas.ai_blocks import AIBlocksRequestBase, GenerateRequest, NATIVE_OPCODES, RequirementsRequest

SCRATCH_CODE_GENERATION_PROMPT = (Path(__file__).resolve().parents[2] / 'docs' / 'scratch代码生成提示词.md').read_text(encoding='utf-8').strip()
SCRATCH_CODE_STANDARD = (
    'Scratch 代码规范：只输出一个严格 ProgramIR JSON；事件帽不能有子栈；'
    '循环表达持续关系，条件积木表达前置/分支关系；受条件控制的动作必须放在对应的 SUBSTACK 中。'
    '禁止 SUBSTACK1；禁止虚构 sensing_touchingedge 等不存在的 opcode。'
    '字段与 inputs 严格按 BLOCK_CATALOG；number/angle 的 value 必须是 JSON 数字。'
    '示例输入：{\"kind\":\"angle\",\"value\":90}。'
    '每个核心断言都必须在规划和 ProgramIR 中有对应实现；条件体只放受该条件控制的动作。'
    '发现需求无法由当前原生积木表达时输出 UNSUPPORTED_SCRATCH_REQUEST；禁止根据自然语言臆造 opcode。'
    'append: 只新增脚本；rewrite: 重写全部脚本；modify: 仅允许移除未保护脚本。'
    '不支持的行为输出 UNSUPPORTED_SCRATCH_REQUEST。'
)

PROJECT_SCENARIO_PROMPTS = {
    'add_role': '添加角色只输出资源操作，不生成积木；created_sprite 只能引用前面已创建的角色。',
    'add_costume': '添加造型使用 add_costume，目标必须是已有或已创建角色。',
    'add_backdrop': '新增背景使用 add_backdrop，切换已有背景使用 select_backdrop，不重复创建。',
    'select_backdrop': 'select_backdrop 只允许 op、op_id、name。',
    'create_variable': '共享变量和列表使用 global，名称稳定且唯一。',
    'broadcast': '广播名称稳定，发送和接收完全一致，不输出 ID。',
    'stage_control': '舞台只负责初始化、背景和全局流程，不放角色移动逻辑。',
    'unsupported_resource': '不支持的删除、外部 URL、base64 或外部资源不得臆造操作。',
    'single_target': '每个 project_program 请求只生成一个目标的 ProgramIR。',
}

def _context(request: AIBlocksRequestBase) -> str:
    target=request.target
    return json.dumps({'name':target.name,'is_stage':target.is_stage,'has_scripts':target.has_scripts,'mode':target.mode,'costumes':target.costumes,'sounds':target.sounds,'sprite_names':target.sprite_names,'current_program':json.loads(target.current_program) if target.current_program else {}},ensure_ascii=False,separators=(',',':'))

def _conversation(request: AIBlocksRequestBase) -> list[dict[str,str]]:
    return [{'role':m.role,'content':m.content} for m in request.messages]

def build_chat_messages(request: AIBlocksRequestBase) -> list[dict[str,str]]:
    system=('你是 Scratch 功能需求助手。根据用户输入直接确定可实现的角色或舞台行为；'
            '未说明细节一律使用最简单且符合 Scratch 习惯的默认值，绝不提问、绝不请求确认；禁止输出代码、JSON 或积木结构。')
    return [{'role':'system','content':system},*_conversation(request),{'role':'user','content':f'目标上下文：{_context(request)}'}]

def build_requirements_messages(request: RequirementsRequest) -> list[dict[str,str]]:
    system=('根据用户输入直接整理 Scratch 功能需求，只输出严格 JSON，字段必须是 ready,title,goal,behavior_steps,triggers,controls,success_criteria,missing_questions。'
            '未说明的触发方式默认点击绿旗；未说明的数值、时长和资源使用最简单默认值。'
            '禁止提问、禁止等待确认；必须 ready=true，missing_questions 必须始终为空数组。')
    content=f'目标上下文：{_context(request)}\n立即使用默认值开始：{str(request.proceed_with_defaults).lower()}'
    return [{'role':'system','content':system},*_conversation(request),{'role':'user','content':content}]

def _append_repair_context(messages:list[dict[str,str]], name:str, failures:list[tuple[str,str]]) -> list[dict[str,str]]:
    repaired=list(messages)
    for i,(raw,error) in enumerate(failures,1):
        repaired.extend([{'role':'assistant','content':raw[:12000]},{'role':'user','content':f'第 {i} 次 {name} 未通过服务器解析或校验。请重新只输出完整严格 JSON，不要 markdown 或解释。错误：{error[:12000]}'}])
    return repaired

def build_requirements_repair_messages(request, failed_attempts):
    return _append_repair_context(build_requirements_messages(request),'需求 JSON',failed_attempts)

def build_plan_messages(request:GenerateRequest) -> list[dict[str,str]]:
    system='你是 Scratch 原生积木规划器。只输出一个严格 JSON 对象，且只能包含唯一字段 steps；格式必须是 {"steps":["步骤1","步骤2"]}。steps 必须是非空字符串数组。禁止输出 success_criteria、summary、triggers、controls、plan、任何其他字段、markdown 或解释。步骤文本中覆盖触发、状态、动作、条件、时序和成功标准即可。'
    content=f'需求：{json.dumps(request.requirements.model_dump(mode="json"),ensure_ascii=False)}\n对话：{json.dumps([m.model_dump() for m in request.messages],ensure_ascii=False)}\n目标上下文：{_context(request)}'
    return [{'role':'system','content':system},{'role':'user','content':content}]

def build_plan_repair_messages(request, failures):
    messages = _append_repair_context(build_plan_messages(request), '规划 JSON', failures)
    messages[-1]['content'] += '最终输出只能有 steps 一个字段；删除 success_criteria、summary、triggers、controls 和所有其他字段。'
    return messages

def _program_system(request:GenerateRequest) -> str:
    rules='append/rewrite 禁止 remove_script_ids；modify 只能移除未保护脚本。'
    return f'你是 Scratch ProgramIR 生成器。{SCRATCH_CODE_GENERATION_PROMPT}\n{SCRATCH_CODE_STANDARD}\n允许 opcode：{",".join(sorted(NATIVE_OPCODES))}。{rules}现在只输出符合上述协议的 ProgramIR JSON。'

def build_program_messages(request:GenerateRequest, steps:list[str]) -> list[dict[str,str]]:
    content=f'计划：{json.dumps(steps,ensure_ascii=False)}\n需求：{json.dumps(request.requirements.model_dump(mode="json"),ensure_ascii=False)}\n目标上下文：{_context(request)}'
    return [{'role':'system','content':_program_system(request)},{'role':'user','content':content}]

def build_program_repair_messages(request, steps, failures):
    return _append_repair_context(build_program_messages(request,steps),'ProgramIR',failures)

def build_project_manifest_messages(request:GenerateRequest, steps:list[str]) -> list[dict[str,str]]:
    system=('你是 Scratch ProjectManifest 生成器，只输出 ProjectManifest v1 严格 JSON。顶层且只能有 schema_version=1、scope=project、summary、resource_ops、program_targets、shared_state；禁止输出 manifest 包装层、version、project、description、block_programs、program、scripts、blocks、next、parent、shadow、VM ID、JavaScript、project.json。'
            'ResourceOp 必须严格按操作填写：add_builtin_sprite 只能有 op、op_id、name、x、y、size、tags、source，禁止 target 和 select_after；add_sprite 只能有 op、op_id、name、x、y、size、tags、source、costumes，禁止 target 和 select_after；delete_sprite 只能有 op、op_id、name、target，target 必须是 existing_sprite，禁止 source、x、y、size、tags、select_after、costumes；delete_sprite 只能有 op、op_id、name、target，target 必须是 existing_sprite，禁止 source、x、y、size、tags、select_after、costumes；add_costume 必须有 target、name、tags、source、select_after，禁止 x、y、size；add_backdrop 只能有 op、op_id、name、tags、source、select_after，禁止 target、x、y、size；select_backdrop 只允许 op、op_id、name。禁止 sprite_name、URL、base64。对于 scratch_library source，add_builtin_sprite 的 asset_type 必须是 sprite；add_costume 的 source.asset_type 必须是 costume，绝对不能是 sprite；add_backdrop 的 source.asset_type 必须是 backdrop；add_sprite 的顶层 source 可以是 image_generation 或 costume 资源，nested costumes 也只能使用 costume 资源。'
            'source.kind=image_generation 时必须完整提供 reason、prompt、style、width、height、sheet_layout；width 和 height 只能是 512、768 或 1024，sheet_layout 必须是字符串 "1x1"，不能写对象 {cols,rows}。空白背景优先使用 scratch_library 的 backdrop source。'
            'TargetRef.kind 只能是 current_target、stage、existing_sprite、created_sprite，绝对不能写 sprite、ref_type 或其他值。'
            'TargetRef 形状必须严格遵守：current_target 只能是 {\"kind\":\"current_target\"}；stage 只能是 {\"kind\":\"stage\"}，绝对不能附带 name 或 op_id；existing_sprite 必须是 {\"kind\":\"existing_sprite\",\"name\":\"已有角色名\"}；created_sprite 必须是 {\"kind\":\"created_sprite\",\"op_id\":\"前面创建操作的op_id\"}。'
            'add_costume 的 target 必须使用 existing_sprite 或 created_sprite；如果刚创建角色，写 target={\"kind\":\"created_sprite\",\"op_id\":\"该角色的op_id\"}，不能写 name 或 sprite_name。'
            'program_targets 的每项且只能有 target、mode、intent，绝对不能有 programs；程序正文由后续 project_program 请求单独生成。target 也只能使用上述 TargetRef；新建角色引用 created_sprite，舞台使用 stage，已有角色使用 existing_sprite。所有 stage、existing_sprite、created_sprite 的 mode 都必须为 append，不能写 rewrite 或 modify；created_sprite 必须引用前面已出现的 add_sprite/add_builtin_sprite。'
            'shared_state 的 variables 每项且只能有 name、value、scope，scope 必须为 global；lists 每项且只能有 name、value、scope，广播是字符串数组。禁止 type、variable、list、id 等字段。')
    if request.asset_policy == 'existing_only':
        system += ' 本次素材策略是 existing_only（仅已有素材）：resource_ops 的所有 source.kind 必须是 scratch_library，严禁 image_generation；只能引用目标上下文和素材匹配证据中的已有素材。找不到匹配素材时省略该非核心资源或使用已有素材，不得建议生图。'
    elif request.asset_policy == 'builtin_first':
        system += ' 本次素材策略是 builtin_first：优先使用 scratch_library；只有没有可用内置素材且确有必要时才使用 image_generation。'
    else:
        system += ' 本次素材策略是 allow_generation：允许在必要时使用 image_generation。'
    content=f'素材策略：{request.asset_policy}\n素材匹配证据：{json.dumps([item.model_dump(mode="json") for item in request.asset_match_evidence],ensure_ascii=False)}\n计划：{json.dumps(steps,ensure_ascii=False)}\n需求：{json.dumps(request.requirements.model_dump(mode="json"),ensure_ascii=False)}\n目标上下文：{_context(request)}\n场景规则：{json.dumps(PROJECT_SCENARIO_PROMPTS,ensure_ascii=False)}'
    return [{'role':'system','content':system},{'role':'user','content':content}]

def build_project_manifest_repair_messages(request, steps, failures):
    messages=_append_repair_context(build_project_manifest_messages(request,steps),'ProjectManifest',failures)
    messages[-1]['content'] += '逐项修复 resource_ops 和 program_targets：add_builtin_sprite/add_sprite/add_backdrop 禁止 target；stage target 只能是 {\"kind\":\"stage\"}，不能有 name 或 op_id；add_costume 的 scratch_library source.asset_type 必须改为 costume，不能写 sprite；add_backdrop 必须使用 backdrop；created_sprite 只能引用前面 add_sprite 或 add_builtin_sprite 的 op_id。'
    return messages

def build_project_program_messages(request:GenerateRequest, steps:list[str]) -> list[dict[str,str]]:
    target=request.program_target.model_dump(mode='json') if request.program_target else None
    manifest=request.project_manifest.model_dump(mode='json') if request.project_manifest else None
    system=_program_system(request)+' 项目模式下只为一个 program_target 生成 ProgramIR，不得生成其他 target 的程序正文。'
    content=f'原始需求：{json.dumps(request.requirements.model_dump(mode="json"),ensure_ascii=False)}\n计划：{json.dumps(steps,ensure_ascii=False)}\n当前 target：{_context(request)}\n已校验 ProjectManifest：{json.dumps(manifest,ensure_ascii=False)}\n当前 program_target：{json.dumps(target,ensure_ascii=False)}\n场景：{PROJECT_SCENARIO_PROMPTS["single_target"]}'
    return [{'role':'system','content':system},{'role':'user','content':content}]

def build_project_program_repair_messages(request, steps, failures):
    return _append_repair_context(build_project_program_messages(request,steps),'ProgramIR',failures)
