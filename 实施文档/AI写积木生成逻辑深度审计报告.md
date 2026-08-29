# AI 写积木生成逻辑深度审计报告

**审计对象**：AI Scratch Web 当前 AI 写积木功能  
**审计时间**：2026-08-29  
**报告性质**：当前实现的代码审计、产品语义审计和测试覆盖审计

## 当前 AI 写积木功能流程详述

本节放在审计结论之前，描述当前代码真实执行的流程。这里的“生成”不是模型直接写入 Scratch 的 project.json，而是模型生成受限的中间表示，宿主再校验、编译并调用 Scratch VM API。需要注意：本节同时记录当前实现的实际行为，因此其中部分步骤也正是后续问题分析的依据。

### 0.1 参与组件和职责

| 组件 | 主要职责 |
|---|---|
| AiBlocksPanel.vue | 面板状态机、用户消息、目标快照、请求编排、资源确认和结果展示 |
| ai-blocks-api.ts | HTTP 请求、SSE 读取、阶段事件和 ready 事件解析 |
| backend/app/routes/ai_blocks.py | provider 选择、超时、流式转发、JSON 收集、重试、Pydantic 校验和 SSE 发送 |
| backend/app/prompts_ai_blocks.py | 按阶段构造 system prompt 和 user prompt |
| backend/app/providers/agnes_text_provider.py | 根据 model tier 选择供应商，构造上游请求并解析流 |
| schemas/ai_blocks.py | 请求、Requirements、Plan、ProgramIR、ProjectManifest 和资源操作的后端结构校验 |
| project-plan.ts | 前端 ProjectManifest 类型、资源操作和项目清单校验 |
| program-compiler.ts | 前端 ProgramIR 结构检查、catalog 合同检查和 VM block graph 编译 |
| generation-runner.ts | 单目标变量解析、脚本删除、渐进式注入、局部回滚和 SB3 备份恢复 |
| project-resource-preparer.ts | 内置素材选择、生图、图片转换和生成资源持久化 |
| project-resource-runner.ts | 项目资源备份、资源操作串行提交和资源阶段恢复 |
| project-resource-executor.ts | 将 add_sprite、add_costume、add_backdrop 等资源操作映射到 Scratch VM |
| scratch-gui-bridge.ts | 目标、变量、资源、保存、加载、运行、workspace 刷新和 revision 管理 |

### 0.2 用户打开面板和建立目标快照

1. 用户在 Scratch 中选中舞台或角色，打开 AI 写积木面板。
2. AiBlocksPanel.vue:255-267 创建或重置会话，保存当前目标快照，并根据目标是否已有脚本把默认模式设为 modify 或 append。
3. target-context.ts:74-140 的 readAiTargetSnapshot 读取以下上下文：
   - 当前 targetId、名称以及是否舞台；
   - 当前 target 是否有脚本；
   - 按位置排序的顶层脚本；
   - 可被 AI 修改的脚本引用 script_1、script_2 等；
   - 被认为受保护的脚本；
   - 脚本的递归结构、注释、位置和当前程序摘要；
   - 当前 target 的局部变量、舞台全局变量、造型、声音和项目角色名称；
   - bridge.projectRevision。
4. snapshot 同时用于给模型构造上下文和在请求结束后调用 assertSnapshotIsCurrent，见 target-context.ts:143-151。
5. 当前 snapshot 使用逻辑名称和快照 revision 作为保护基础，模型本身不会得到 Scratch VM 内部 target ID。

### 0.3 用户消息进入需求整理阶段

用户点击发送需求后，AiBlocksPanel.vue:302-314 会：

1. trim 输入内容，阻止空消息和并发请求。
2. 把用户消息加入 messages。
3. 调用 summarize，进入 summarizing 状态并创建 AbortController。
4. 重新读取当前目标快照，避免面板打开后目标已经变化。
5. 通过 summarizeAiBlocksRequirements 调用 POST /api/ai-blocks/requirements。

发送给后端的请求主要包括：

    model_tier
    session_id
    request_id
    messages
    target
    thinking
    proceed_with_defaults

当前前端在 AiBlocksPanel.vue:329-337 固定发送 proceed_with_defaults=true。后端 RequirementsRequest 在 schemas/ai_blocks.py:75-77 接收这个字段。

#### 需求整理的 system prompt

build_requirements_messages 位于 backend/app/prompts_ai_blocks.py:43-48。当前 prompt 的核心要求是：

    根据用户输入直接整理 Scratch 功能需求，只输出严格 JSON。
    顶层字段只能是 ready、title、goal、behavior_steps、triggers、controls、success_criteria、missing_questions。
    未说明的触发方式默认点击绿旗。
    未说明的数值、时长和资源使用最简单默认值。
    禁止提问、禁止等待确认。
    必须 ready=true，missing_questions 必须始终为空数组。

对应的 user prompt 包含：

    目标上下文：{target context}
    立即使用默认值开始：{true 或 false}

其中 target context 由 prompts_ai_blocks.py:31-33 拼装，包含目标名称、舞台标记、脚本存在标记、模式、造型、声音、角色名称和 current_program JSON。

#### 后端需求整理和重试

1. routes/ai_blocks.py:265-274 选择供应商并创建 requirements 处理过程。
2. routes/ai_blocks.py:275-280 调用 _collect_resilient，收集 reasoning/content，合并成完整文本。
3. extract_single_json 在 schemas/ai_blocks.py:402-418 中解析一个 JSON 对象，可接受一个完整代码围栏，但拒绝尾随内容和多个对象。
4. RequirementsResponse 在 schemas/ai_blocks.py:79-87 校验 ready、目标、行为步骤、触发器、控制项、成功标准和问题列表的数量与长度。
5. 如果 proceed_with_defaults 为真且 result.ready 为假，routes/ai_blocks.py:281-292 会把 ready 改为真，使用用户原文补 goal 和行为步骤，默认 triggers 为“点击绿旗”，并清空 missing_questions。
6. 最终结果返回给前端，当前前端又在 AiBlocksPanel.vue:338 执行一次 ready=true 和 missing_questions=[]。

因此，需求整理阶段的实际终点不是“确认需求”或“返回待回答问题”，而是当前实现中的“生成一个可直接进入下一阶段的需求对象”。

### 0.4 需求整理之外的对话接口

后端还提供 build_chat_messages 和 POST /api/ai-blocks/chat：

- build_chat_messages 位于 prompts_ai_blocks.py:38-41。
- system prompt 要求根据用户输入直接确定可实现行为，未说明细节使用默认值，禁止提问，禁止输出代码、JSON 或积木结构。
- backend/app/routes/ai_blocks.py:235-262 以 assistant_delta、reasoning_delta、message_complete 和 message_error 发送普通对话流。
- 前端 ai-blocks-api.ts:128-139 提供 streamAiBlocksChat，但当前 AiBlocksPanel 的主发送流程实际调用的是 summarizeAiBlocksRequirements，而不是这个普通 chat 接口。

这两个接口的存在容易让人误以为“聊天”和“需求整理”有相同语义；当前代码中它们的输出合同和 UI 使用路径并不相同。

### 0.5 进入计划生成阶段

当需求整理成功后，AiBlocksPanel.vue:352-354 调用 generate。generate 在 AiBlocksPanel.vue:357-397 中完成以下准备：

1. 再次读取目标快照。
2. 进入 planning 状态。
3. 清空旧计划、旧结果、旧 reasoning 和旧错误。
4. 停止 Scratch 当前运行状态，激活代码标签页。
5. 设置面板锁定状态和 AbortController。
6. 调用 POST /api/ai-blocks/generate。

单目标和项目模式共用这个入口，关键请求字段为：

    model_tier
    session_id
    request_id
    messages
    target
    thinking
    requirements
    plan（如果已有计划，可复用）
    protocol_version
    output_contract
    scope
    asset_policy
    project_manifest（project_program 时使用）
    program_target（project_program 时使用）
    asset_match_evidence（当前首次项目生成路径没有发送）

#### Plan prompt

如果请求没有携带 plan，后端 routes/ai_blocks.py:317-334 先进入 planning，并使用 build_plan_messages，定义在 prompts_ai_blocks.py:59-67。

system prompt 的职责是：

    你是 Scratch 原生积木规划器。
    只输出一个严格 JSON 对象。
    顶层只能有唯一字段 steps。
    steps 必须是非空字符串数组。
    步骤覆盖触发、状态、动作、条件、时序和成功标准。
    禁止 success_criteria、summary、triggers、controls、plan、其他字段、markdown 和解释。

user prompt 会放入：

    需求：RequirementsResponse JSON
    对话：当前 messages JSON
    目标上下文：current target context JSON

后端以 Plan 模型校验结果，并发送：

    event: plan_ready
    data: {request_id, plan}

如果 Plan 解析或校验失败，build_plan_repair_messages 会把失败诊断加入后续消息，并要求只保留 steps 字段。当前 routes/ai_blocks.py:320-327 使用 retries=2，再叠加 provider 层内部重试。

如果前端已经有 planSteps，AiBlocksPanel.vue:540 会将其作为 plan 发送给后端，后端跳过重复规划。

### 0.6 单目标 ProgramIR 生成阶段

当 output_contract=program_ir 时，后端 routes/ai_blocks.py:336-358 执行 ProgramIR 生成：

1. 发送 phase=generating，提示“正在生成积木程序”。
2. 使用 build_program_messages(request, plan.steps)。
3. 让 provider 输出完整 ProgramIR JSON。
4. 用 ProgramIR Pydantic 模型解析。
5. 调用 validate_program_contracts 和 validate_program_for_mode。
6. 成功后发送 program_ready。

#### ProgramIR system prompt

build_program_messages 位于 prompts_ai_blocks.py:69-78。它组合以下内容：

1. docs/scratch代码生成提示词.md 的完整 Scratch 生成约束。
2. SCRATCH_CODE_STANDARD，包括事件帽不能有子栈、循环和条件的语义、禁止 SUBSTACK1、number/angle 必须为 JSON 数字等规则。
3. backend/app/scratch_block_catalog.py 中允许的原生 opcode 列表。
4. 模式规则：append/rewrite 禁止 remove_script_ids；modify 只能移除未保护脚本。
5. 最终只能输出符合协议的 ProgramIR JSON。

user prompt 位于 prompts_ai_blocks.py:73-75，包含：

    计划：steps
    需求：RequirementsResponse JSON
    目标上下文：current target context JSON

docs/scratch代码生成提示词.md 进一步约束模型：

- 输出必须是 ProgramIR，不是 project.json、SB3、XML、Base64、hydrated VM block 或 JavaScript。
- 不生成 block ID、变量 ID、列表 ID、广播 ID、assetId、md5ext、next、parent、shadow 或 topLevel。
- 只使用 catalog 允许的原生 opcode。
- 无法由当前原生积木表达时输出 UNSUPPORTED_SCRATCH_REQUEST。
- append 只新增脚本；rewrite 重写约定范围；modify 只移除未保护脚本。
- 每个变量、列表、广播名称在相应数组中唯一。
- 每个脚本有 script_N 风格 ID、purpose、位置和 blocks。

后端校验通过后发送：

    event: program_ready
    data: {request_id, program}

### 0.7 项目 ProjectManifest 生成阶段

当 scope=project 时，前端首次请求使用 output_contract=project_manifest，见 AiBlocksPanel.vue:376-386。后端 routes/ai_blocks.py:359-378：

1. 发送 phase=generating，提示“正在生成项目清单”。
2. 使用 build_project_manifest_messages。
3. 解析 ProjectManifest。
4. 使用 normalize_project_manifest_input 做兼容性归一化。
5. 通过 ProjectManifest 模型校验资源操作、程序目标和共享状态。
6. 发送 manifest_ready。

#### ProjectManifest system prompt

build_project_manifest_messages 位于 prompts_ai_blocks.py:80-95。它要求顶层只能出现：

    schema_version=1
    scope=project
    summary
    resource_ops
    program_targets
    shared_state

同时禁止：

    manifest 包装层
    version、project、description
    block_programs、program、scripts、blocks
    next、parent、shadow
    VM ID、JavaScript、project.json
    sprite_name、URL、base64

各类 ResourceOp 的 prompt 约束为：

- add_builtin_sprite：只能描述角色创建、位置、尺寸、标签和 Scratch library sprite source。
- add_sprite：描述新角色及其 costumes，不允许把 target 当成目标引用。
- delete_sprite：当前 prompt 仍允许描述已有角色删除，和部分产品文档冲突。
- add_costume：必须有非舞台 target、source、tags、select_after，不能有 x/y/size。
- add_backdrop：必须有 backdrop source 和 select_after，不能有 target。
- select_backdrop：只允许 op、op_id、name。
- shared_state：共享变量、列表必须 global，广播名称稳定且唯一。
- program_targets：每个目标只有 target、mode、intent，不嵌入 ProgramIR 正文。

project_manifest 生成成功后，前端在 AiBlocksPanel.vue:400-417 校验。若存在 image_generation source，则保留清单并进入资源 review；如果没有生图资源，当前代码可能自动选择内置素材并直接提交。

### 0.8 项目级单目标 ProgramIR 生成阶段

项目清单确认后，AiBlocksPanel.vue:524-563 按 manifest.program_targets 顺序逐个处理：

1. 先应用 manifest.shared_state。
2. 用 targetIdForManifestRef 解析 current_target、stage、existing_sprite 或 created_sprite。
3. 切换 Scratch 的 editingTarget。
4. 重新读取该目标的 target snapshot。
5. 调用 POST /api/ai-blocks/generate，设置 output_contract=project_program、scope=project，并携带 project_manifest 和当前 program_target。
6. 后端 routes/ai_blocks.py:379-407 使用 build_project_program_messages。
7. 后端只允许这次请求返回一个目标的 ProgramIR，并发送 project_program_ready。
8. 前端收到 ProgramIR 后调用 applyAiProgram，将程序写入当前 target。
9. 记录 appliedPrograms，并把 expectedRevision 更新为当前 projectRevision。
10. 继续下一个 program_target。

#### project_program prompt

build_project_program_messages 位于 prompts_ai_blocks.py:102-108。它复用 ProgramIR system prompt，并追加：

    项目模式下只为一个 program_target 生成 ProgramIR。
    不得生成其他 target 的程序正文。

user prompt 依次包括：

    原始需求：RequirementsResponse
    计划：steps
    当前 target：当前目标上下文
    已校验 ProjectManifest：完整项目清单
    当前 program_target：本次目标引用、mode 和 intent
    场景：single_target

这一步的设计意图是减少项目级大 JSON，但当前前后端没有把 program_target 与 manifest 建立不可绕过的服务端关联。

### 0.9 SSE 流式传输和前端事件处理

#### 后端流处理

1. provider 根据 model_tier 选择 agnes、deepseek 或 gpt。
2. _timed_events 在 routes/ai_blocks.py:92-132 设置超时并把 provider delta 转成内部事件。
3. _collect_resilient 在 routes/ai_blocks.py:133-165 收集 content 文本；provider 错误时按内部 retries 重试，并把已收到的片段和错误摘要保存下来。
4. _generate_json 在 routes/ai_blocks.py:168-232 对完整文本执行 extract_single_json、模型校验和业务 validator；失败后构造 repair prompt。
5. 成功时发送 phase、plan_ready、program_ready、manifest_ready 或 project_program_ready，最后发送 message_complete。
6. 失败时发送 message_error；当前实现会根据 ready_sent 决定是否发送错误事件。

#### 前端流处理

ai-blocks-api.ts:111-126 使用 TextDecoder 和 createSseParser 逐块读取 SSE。streamAiBlocksGeneration 在 162-209 维护：

    completed
    readyEvent
    program
    manifest
    programTarget

事件处理规则如下：

- phase：调用 onPhase，更新 planning/generating/repairing 等阶段。
- plan_ready：读取 steps，调用 onPlan。
- reasoning_delta：调用 onReasoning，把 reasoning 按长度截断保存在面板。
- program_ready：保存 ProgramIR，调用 onProgram。
- manifest_ready：保存 ProjectManifest，调用 onManifest。
- project_program_ready：保存 ProgramIR 和 target，调用 onProjectProgram。
- project_ready：直接报旧协议错误。
- message_complete：completed=true。
- message_error：抛出 AiBlocksApiError。
- 流结束但没有 message_complete：抛出 STREAM_INCOMPLETE。

当前事件解析只保证“出现过某种 ready 事件并最终 complete”，没有严格验证 ready 事件必须和请求 output_contract 一一对应。

### 0.10 前端结果校验和进入宿主执行

单目标输出经过 SSE 解析后，AiBlocksPanel.vue:419-441 进入注入阶段：

1. 设置 state=injeting，并加锁 Scratch 容器。
2. 确认 output.kind=program。
3. 调用 applyAiProgram。
4. applyAiProgram 开始时再次检查 target ID 和 snapshot revision。
5. 返回 addedBlocks、addedScripts、removedScripts、revisionAtComplete 和 backup。
6. 面板刷新 snapshot，追加一条 assistant 结果消息。

项目 manifest 输出经过 AiBlocksPanel.vue:400-402 的 assertProjectManifest；项目目标程序输出直接进入 applyAiProgram，前端 API 层目前只做 object 检查，未在 ready 事件处完成完整 ProgramIR、target 和 manifest 关联校验。

### 0.11 资源 review 阶段

当 ProjectManifest 中存在 image_generation source 时，AiBlocksPanel.vue:403-411 将清单放入 projectPlan，并打开 ResourcePlanReview。

ResourcePlanReview.vue:23-38 把 resource_ops 展开为可展示条目：

- add_sprite 有嵌套 costumes 时按“角色 · 造型”展开。
- 其他操作按 op_id 展开。
- image_generation 条目进入生成确认集合。
- scratch_library 条目进入本地素材选择集合。
- 只有所有生图条目已确认、所有 library 条目已选择时，确认按钮可用。

用户确认后，组件通过 confirm 事件返回：

    confirmedGenerationIds
    selections

其中 selections 只包含用户选择的本地 Scratch library item，生成确认项只包含用户允许生图的条目。

### 0.12 Prepare：准备资源但原则上不修改 VM

project-resource-preparer.ts:73-167 逐项处理 resource_ops，并为每项生成 PreparedResource。

#### Scratch library 资源

1. 使用用户选择的素材记录。
2. 把素材转换成 CostumeExport 或 SpriteExport。
3. 检查资源类型和目标类型。
4. 放入 prepared Map，等待 Commit。

#### image_generation 资源

1. 检查 confirmGeneration 是否明确同意本次生图。
2. 读取 reason、prompt、style、width、height、sheet_layout。
3. 调用 POST /api/image/generate。
4. 将 Base64 图片转换成 Blob 和 CostumeExport。
5. 按 requested_canvas 计算 Scratch 的资源宽高、旋转中心和 bitmapResolution。
6. 调用 saveGeneratedAsset 保存到 IndexedDB，并记录 generatedAssets。
7. 将结果放入 prepared Map。

当前 Prepare 阶段会产生图片网络请求和 IndexedDB 持久化，但不应直接调用 VM 的 addSprite、addCostume 或 addBackdrop。用户取消生图会以 AbortError 中断准备流程。

### 0.13 Commit：资源操作提交到 Scratch VM

project-resource-runner.ts:58-86 负责资源提交：

1. 使用 existingSpriteNames 校验当前角色名。
2. 调用 assertProjectManifest。
3. 调用 assertSnapshotIsCurrent。
4. 调用 bridge.saveProject 创建 backup。
5. 按 resource_ops 顺序读取 PreparedResource。
6. 调用 ProjectResourceExecutor.apply。
7. 每项成功后更新进度。
8. 完成后把 editingTarget 切回原始 target，刷新 workspace，记录 revision_at_complete。
9. 任一操作失败或取消时，直接 loadProject(backup) 恢复资源阶段。

ProjectResourceExecutor 会把资源操作映射为：

- add_builtin_sprite/add_sprite -> bridge.commitSpriteImport 或 vm.addSprite。
- add_costume -> bridge.addCostumeToTarget。
- add_backdrop -> bridge.addBackdropToStage。
- select_backdrop -> bridge.selectBackdropByName。
- delete_sprite -> vm.deleteSprite，尽管该能力与当前产品文档存在冲突。

### 0.14 项目共享状态和多目标程序注入

资源提交成功后，AiBlocksPanel.vue:524-563 进入程序阶段：

1. applyProjectSharedState 取舞台变量表。
2. 对 shared.variables、shared.lists、shared.broadcasts 执行 ensure。
3. 新变量通过 stage.createVariable 创建，并尝试直接读取 stage.variables[id]。
4. 调用 runtime.emitProjectChanged。
5. 记录当前 revision 为 expectedRevision。
6. 对每个 program_target：
   - 在循环开始检查 projectRevision 是否仍为 expectedRevision；
   - 通过 current_target、stage、existing_sprite 或 created_sprite 解析真实目标；
   - 读取 targetSnapshot；
   - 发送 project_program 请求；
   - 收到 ProgramIR 后调用 applyAiProgram；
   - 把本次程序结果放入 appliedPrograms；
   - 更新 expectedRevision。

当前 program_target 请求会重新发送 messages、requirements、plan、manifest 和该目标上下文，但不会把前一个目标实际写入结果的结构化摘要作为下一目标的受信输入。因此，跨角色广播协作主要依赖模型从原始 manifest 和上下文自行推断。

### 0.15 ProgramIR 编译和渐进式 VM 注入

generation-runner.ts:314-426 负责单个目标的执行：

1. assertSnapshotIsCurrent，确认编辑目标和 revision 未变化。
2. 根据 mode 计算 removeTopIds：
   - append 不删除已有脚本；
   - modify 根据 remove_script_ids 查找未保护脚本；
   - rewrite 删除 isAiEditableScript 判定为可编辑的脚本。
3. 创建 ProgramResolver，解析本地变量、全局变量、列表和广播。
4. positionProgram 计算新脚本位置。
5. compileProgramIR 将 ProgramIR 转为 Scratch VM block graph，并由宿主生成 block ID、shadow ID、next、parent 和 topLevel。
6. collectRemovedState 保存待删除脚本的 block graph 和部分评论。
7. bridge.saveProject 创建单目标 backup。
8. 再调用 bridge.beginBlockGeneration 获取生成锁。
9. 安装待创建变量，删除旧脚本，调用 injectProgressively 逐块创建 VM block。
10. append 模式额外创建脚本注释。
11. 成功时返回新增块、脚本、删除脚本、最终 revision 和 backup。
12. 失败时先执行 localRollback；局部回滚失败再 loadProject(backup)。

编译器会生成宿主数据，而不是让模型生成这些 VM 细节。这是当前设计中最重要的安全边界之一，但 compiler 的 catalog 语义检查和 VM 目标上下文仍需要更完整的绑定。

### 0.16 完成、取消、失败和撤销

#### 单目标成功

AiBlocksPanel.vue:432-441 保存生成结果、刷新快照、追加成功消息并回到 chat 状态。Scratch VM 保留本次新增程序，用户可以运行验证。

#### 单目标取消或失败

AiBlocksPanel.vue:442-454 清理 result 和 projectApplyResult，追加失败/取消消息，恢复 chat 状态并解除锁定。注入阶段的取消由 applyAiProgram 的局部回滚或备份恢复负责。

#### 项目成功

AiBlocksPanel.vue:640-647 在所有 target 程序成功后记录最终 revision、恢复原始 editingTarget、刷新 workspace、保存 projectApplyResult 并清理 preparedPlan。用户可以随后点击撤销。

#### 项目取消或失败

AiBlocksPanel.vue:648-658 若资源阶段已经返回 applied 结果，则外层再执行 bridge.loadProject(applied.backup)，尝试将整个项目恢复到资源提交前。该恢复与资源 runner 内部恢复、单目标局部回滚并非同一个事务控制器。

#### 撤销

AiBlocksPanel.vue:461-479 根据是否存在 projectApplyResult 选择 undoProjectManifest，或选择单目标 undoAiGeneration。撤销前检查 bridge.projectRevision 是否等于结果中的 revision_at_complete；如果用户已经继续编辑，则拒绝覆盖。

### 0.17 当前提示词清单

| 阶段 | 构造函数 | 模型任务 | 输出/终点 |
|---|---|---|---|
| 普通对话 | build_chat_messages | 根据输入确定可实现行为，当前 prompt 强制默认化 | assistant 文本流 |
| 需求整理 | build_requirements_messages | 把对话整理为结构化需求，当前 prompt 强制 ready=true | RequirementsResponse |
| 需求修复 | build_requirements_repair_messages | 根据解析/校验错误重写需求 JSON | RequirementsResponse |
| 计划 | build_plan_messages | 把需求拆成覆盖触发、状态、动作、条件、时序和成功标准的 steps | Plan |
| 计划修复 | build_plan_repair_messages | 删除多余字段，只保留 steps | Plan |
| 单目标程序 | build_program_messages | 按 Scratch 生成规范输出 ProgramIR | ProgramIR |
| 单目标程序修复 | build_program_repair_messages | 根据校验错误重写 ProgramIR | ProgramIR |
| 项目清单 | build_project_manifest_messages | 只输出资源操作、程序目标和共享状态 | ProjectManifest |
| 项目清单修复 | build_project_manifest_repair_messages | 修复资源/目标/共享状态结构 | ProjectManifest |
| 项目目标程序 | build_project_program_messages | 只为一个 program_target 输出 ProgramIR | ProgramIR + target |
| 项目目标程序修复 | build_project_program_repair_messages | 根据当前 target 的 ProgramIR 校验错误重写 | ProgramIR |

所有结构化生成阶段的共同原则是：system prompt 指定输出合同，user prompt 提供需求、计划、上下文和项目清单，后端再做 JSON/Pydantic/业务校验。当前问题在于不同阶段对默认值、目标身份、策略证据、语义合同和修复历史的处理并不统一。

### 0.18 当前模型可见数据与宿主保留数据

模型可见：

- 用户对话 messages；
- RequirementsResponse；
- Plan.steps；
- 当前目标名称、舞台标记、模式、脚本摘要、造型、声音和角色名称；
- 项目模式下的 ProjectManifest；
- 当前 program_target 的 kind、name/op_id、mode 和 intent；
- asset_policy 和客户端提供的 asset_match_evidence（当前主项目路径未完整提供）。

宿主保留、不交给模型生成：

- Scratch block ID、next、parent、shadow、topLevel；
- VM target ID、变量 ID、列表 ID、广播 ID；
- assetId、md5ext、图片二进制和 SB3；
- 真实资源选择结果和 IndexedDB 记录；
- 最终 revision、backup 和回滚责任。

这种边界是正确方向，但只有在服务端验证聚合关系、前端验证用户选择、宿主验证 target identity 后才真正成立。

## 1. 执行摘要

当前 AI 写积木已经具备“自然语言需求 -> 需求整理 -> 计划 -> ProgramIR -> 前端编译 -> Scratch VM 注入”的可运行链路，也具备一定的结构防护：后端严格模型、opcode 白名单、数量/深度限制、前端 catalog、目标快照、单目标局部回滚和项目资源阶段备份都已经存在。

但当前最危险的问题不是模型偶尔输出错误 JSON，而是系统会把不完整需求、未经证明的目标引用和半完成的项目变化当成成功结果。需求确认、项目级 target 关联和全项目事务这三个核心不变量没有贯穿前后端。

本次审计的最高优先级判断如下：

1. 未确认的需求被前后端强制标记为 ready，模糊需求可能直接生成默认行为。
2. ProjectManifest、program_target、当前 target 快照和 ProgramIR 没有作为一个聚合进行校验，项目程序请求可以在结构合法但对象关系错误的情况下通过。
3. 项目事务只覆盖资源提交，程序生成和共享状态由面板另行处理；失败时存在双层恢复、无条件 loadProject 和并发修改覆盖风险。
4. 重试预算由多个层级分别控制，默认 ProgramIR 请求最多可产生 15 次上游调用，同时修复上下文还会不断增长。
5. 内置素材匹配证据没有从需求整理贯通到生成策略，existing_only 和 builtin_first 不能形成可靠约束。

在上述问题解决之前，继续扩展删除角色、复杂跨角色协作或更多模型档位，会扩大不可控行为面。

## 2. 审计范围和方法

### 2.1 代码范围

- 前端：frontend/src/ai-blocks 下的 AiBlocksPanel.vue、ai-blocks-api.ts、generation-runner.ts、program-compiler.ts、program-ir.ts、target-context.ts、project-plan.ts、project-resource-preparer.ts、project-resource-runner.ts、project-resource-executor.ts、ResourcePlanReview.vue。
- Scratch 桥接：frontend/src/scratch/scratch-gui-bridge.ts、ScratchHost.vue、frontend/src/env.d.ts。
- 后端：backend/app/routes/ai_blocks.py、schemas/ai_blocks.py、prompts_ai_blocks.py、providers/agnes_text_provider.py、scratch_block_catalog.py、图片生成 schema/service/route。
- 验证：backend/tests、frontend/tests、frontend/tests/e2e，以及 README、docs、开发文档和实施文档。

### 2.2 审计方法

1. 以当前实际代码为准还原单目标和项目级调用链。
2. 对照提示词、Pydantic schema、前端 validator、执行器和 VM bridge，检查同一语义在不同层是否一致。
3. 以异常、取消、重复名称、跨 target、无效协议、模型断流和供应商失败为边界进行控制流审计。
4. 对照产品文档和实施方案，区分已确定的实现缺陷、产品政策矛盾和需要真实浏览器验证的风险。
5. 执行后端全量测试、前端全量单元测试和生产构建；不把单元测试通过等同于项目级闭环验收。

## 3. 当前实际生成流程

### 3.1 单目标模式

1. AiBlocksPanel.vue:302-314 接收用户消息。
2. AiBlocksPanel.vue:316-354 调用 /api/ai-blocks/requirements 并决定是否生成。
3. 当前实现发送 proceed_with_defaults=true，并在前端把结果改成 ready=true。
4. AiBlocksPanel.vue:357-397 调用 /api/ai-blocks/generate。
5. backend/app/routes/ai_blocks.py:317-334 先生成或复用 Plan。
6. backend/app/routes/ai_blocks.py:336-358 生成 ProgramIR，并在后端执行合同验证和模式验证。
7. ai-blocks-api.ts:162-208 解析 SSE，前端再交给 generation-runner 和 program-compiler。
8. generation-runner.ts:338-370 创建变量、编译图、删除待替换脚本并逐步注入 VM。
9. generation-runner.ts:398-405 先尝试局部回滚，失败后加载 SB3 备份。

### 3.2 项目模式

1. AiBlocksPanel.vue:376-386 请求 output_contract=project_manifest。
2. 前端在 AiBlocksPanel.vue:400-417 校验清单；有生图资源时打开 ResourcePlanReview，无生图资源时可能自动选择并直接提交。
3. ResourcePlanReview.vue:23-38 展示资源条目，但自动内置素材路径可能绕过该界面。
4. project-resource-preparer.ts:73-167 执行素材选择、生图和 IndexedDB 保存。
5. project-resource-runner.ts:58-86 保存备份并提交 resource_ops。
6. AiBlocksPanel.vue:524-563 安装共享状态，再按 program_targets 循环请求 project_program 并逐目标注入。
7. 任一目标失败时，AiBlocksPanel.vue:648-656 另行加载资源阶段拿到的备份。

这条项目链路减少了一次输出超大 JSON 的截断概率，但把一致性问题从一次响应转移到了多个请求、多个 target 和多个 VM mutation 之间。

## 4. 已有的合理基础

1. backend/app/schemas/ai_blocks.py:24-25 使用 extra=forbid 和 strict=True；消息、字符串、数组、块数量和递归深度有上限。
2. backend/app/scratch_block_catalog.py:33-160 和 frontend/src/ai-blocks/program-compiler.ts:18-36、101-190 对 opcode、shape、input、field 和对象结构有基础检查。
3. frontend/src/ai-blocks/target-context.ts:143-151 在生成前后检查 target ID 和 projectRevision。
4. 单目标生成有 backup、局部回滚和 fallback loadProject；资源阶段也有独立 backup。
5. Prepare 与 Commit 在设计上分开，素材选择和生图结果可以先停留在前端。
6. 测试已经覆盖若干单目标生成、取消、编译、资源准备、资源执行、SSE 基本成功路径和 revision 撤销保护。

这些措施应保留，但需要从模块级防护升级为跨请求、跨 target 的统一不变量。

## 5. 问题分级

- S1：会直接造成错误项目行为、未经授权的行为变化、数据覆盖或高额/不可控调用，应优先修复。
- S2：会造成较高的可运行性、协议、策略、并发或维护风险，应在主链路稳定后修复。
- S3：结构或体验层面的缺口，通常不会单独造成数据损坏，但会降低生成成功率和可诊断性。

## 6. 问题总览

| 编号 | 等级 | 类别 | 结论 |
|---|---|---|---|
| F-01 | S1 | 需求语义 | 默认值被当成用户确认，ready 失去门槛作用 |
| F-02 | S1 | 项目完整性 | manifest、program_target 和 ProgramIR 缺少聚合一致性 |
| F-03 | S1 | 项目事务 | 资源、共享状态和多 target 程序不在一个原子事务中 |
| F-04 | S1 | 调用预算 | transport retry 与 semantic repair 相乘，修复上下文累积 |
| F-05 | S1 | 素材策略 | asset_match_evidence 没有形成需求到生成的闭环 |
| F-06 | S1 | 破坏性能力 | delete_sprite 与产品文档及前端 validator 相互矛盾 |
| F-07 | S2 | 图片契约 | 项目生图尺寸与图片 API 允许尺寸不一致 |
| F-08 | S2 | 目标身份 | 按名称和首个匹配项定位角色，重复名可错写 |
| F-09 | S2 | 共享状态 | 变量、列表、广播不使用统一 resolver/事务语义 |
| F-10 | S2 | 模式语义 | rewrite 实际只删除可识别的 AI 脚本 |
| F-11 | S2 | target 校验 | 舞台 ProgramIR 后端验证使用 sprite 默认类型 |
| F-12 | S2 | 协议组合 | scope、output_contract、protocol_version 只做字段枚举 |
| F-13 | S2 | SSE 状态 | ready 事件未绑定合同，供应商 EOF 不总是 incomplete |
| F-14 | S2 | 模型档位 | model tier 和 thinking strength 的 provider 映射不一致 |
| F-15 | S2 | 严格性 | normalizer 静默改变模型输出的语义 |
| F-16 | S2 | 用户确认 | 内置资源自动取第一个候选或静默删除无候选操作 |
| F-17 | S2 | VM 事件 | setVm 重复绑定事件，PROJECT_LOADED 可能重复计 revision |
| F-18 | S2 | 数据保护 | 日志仍可能记录模型输出和供应商错误细节 |
| F-19 | S3 | 语义校验 | catalog 主要校验结构，不完整校验菜单和符号引用 |
| F-20 | S2 | 工程治理 | 文档、协议命名、实现状态和测试覆盖已经分叉 |

---

## 7. 详细问题分析

### F-01 需求整理把默认值当成用户确认

**等级**：S1，用户可感知的核心语义问题。

**代码证据**：

- backend/app/prompts_ai_blocks.py:43-48 明确要求未说明内容使用默认值、禁止提问、必须 ready=true、missing_questions 必须为空。
- frontend/src/ai-blocks/AiBlocksPanel.vue:329-338 每次需求整理都发送 proceed_with_defaults: true，并在收到结果后再次执行 ready: true、missing_questions: []。
- AiBlocksPanel.vue:352-358 只有 ready 为真才允许进入生成。
- backend/app/routes/ai_blocks.py:281-292 会用用户原文补 goal、behavior_steps、success_criteria，并把未说明触发方式补成“点击绿旗”。
- README.md:190-194 却对用户承诺信息不足时先执行需求整理，实施文档也要求返回可操作的 missing_questions。

**复现路径**：

1. 输入“做一个好玩的角色”或“让它能攻击”，不说明触发方式、攻击目标、数值和成功标准。
2. 前端自动向 /requirements 发送 proceed_with_defaults=true。
3. 模型被系统提示禁止追问并强制 ready=true。
4. 前端无条件清空问题列表，直接进入 /generate。
5. 服务端和模型共同猜出的触发器、数值或资源被写入 ProgramIR/ProjectManifest。

**不合理之处**：

需求整理本来应当把自然语言的不确定性显式化；当前实现却把不确定性转换成不可见的默认事实。对于 Scratch，点击绿旗、按键、碰撞、广播、变量初值和目标范围都会改变程序结构，不是可以无提示替换的展示格式。

**影响**：

- 用户以为 AI 已理解需求，实际得到的是模型和服务端共同猜出的需求。
- 同一句模糊自然语言可能因模型或默认策略变化而生成完全不同的程序。
- review 页面失去“用户确认”的门槛作用。
- 生成错误后无法区分是用户没说清楚、默认值不合适，还是模型实现错误。

**修复建议**：

1. 首次需求整理传 proceed_with_defaults=false。
2. 保留服务端返回的 ready=false 和 missing_questions，前端不得覆盖。
3. 用户点击“按默认值继续”后，才对明确列出的字段应用默认值。
4. 默认值按字段展示，例如触发器、数值、资源策略和目标范围，不能由一个总开关覆盖所有不确定性。
5. RequirementsResponse 增加资源意图、搜索词和不确定字段，为后续素材证据提供输入。

**验收标准**：模糊输入不会自动调用程序/项目清单生成；用户确认默认值之前，必须看到至少一个可操作问题。

### F-02 ProjectManifest、program_target 和 ProgramIR 缺少聚合一致性校验

**等级**：S1，项目模式完整性缺口。

**代码证据**：

- backend/app/schemas/ai_blocks.py:90-99 只把 project_manifest 和 program_target 声明为两个可选字段，没有校验二者关系。
- backend/app/routes/ai_blocks.py:379-381 只检查 project_program 请求中的两个对象非空。
- backend/app/routes/ai_blocks.py:402-405 返回的 target 直接来自 body.program_target，不是服务端从 manifest 解析并证明存在的目标。
- ProjectManifest 在 schemas/ai_blocks.py:130-159 中单独校验，ProjectProgramTarget 也只校验自身字段。
- 前端 AiBlocksPanel.vue:527-545 虽然按 manifest 循环发送目标，但这是当前 UI 的调用约定，不是服务端不变量。

**可构造的无效请求**：

- manifest.program_targets 为空，但 body.program_target 仍存在。
- manifest 只有 target=stage，body.program_target 却是 existing_sprite。
- program_target 使用 created_sprite，但 op_id 不对应 manifest.resource_ops 中的创建角色操作。
- manifest.scope=project，但 body.target.current_program 仍属于另一个当前编辑角色。

**影响**：

- 后端可能发出 project_program_ready，前端后续却无法解析目标。
- 资源操作和程序目标之间的先后关系只依赖模型提示词与前端循环。
- 非浏览器客户端、旧客户端或篡改请求可以绕过 UI 的正常关联。

**修复建议**：

建立服务端不可绕过的 ProjectProgramRequest 聚合校验：

1. 在 manifest.program_targets 中精确找到当前 target。
2. created_sprite 必须引用前面已出现且类型正确的角色创建操作。
3. existing_sprite 必须与受信目标快照的唯一身份匹配。
4. stage/current_target 的 kind、name 和 op_id 形状严格一致。
5. program_target.mode 与目标上下文、manifest 政策一致。
6. 从聚合上下文解析 target kind、current_program 和 protected script，再执行 ProgramIR 校验。

### F-03 项目提交不是一个完整的原子事务

**等级**：S1，可能造成半成品项目或并发覆盖。

**代码证据**：

- frontend/src/ai-blocks/project-resource-runner.ts:58-86 的备份、revision 检查和异常恢复只包住 resource_ops。
- AiBlocksPanel.vue:631-640 先完成资源提交，再调用 applyProjectPrograms。
- AiBlocksPanel.vue:648-656 在外层再次 loadProject(applied.backup)，形成资源 runner 内层恢复和面板外层恢复的双层路径。
- AiBlocksPanel.vue:524-531 先修改共享状态，再把当前 revision 记为 expectedRevision；共享状态没有被统一事务 lease 管理。
- generation-runner.ts:347-350 先 saveProject、再 assertSnapshotIsCurrent、最后 beginBlockGeneration，备份和编译窗口不在生成锁保护下。
- project-resource-runner.ts:65-70 在资源操作之间没有再次检查 revision；失败时 78-84 无条件 loadProject(backup)。

**需要区分的事实和待验证风险**：

当前面板会把 Scratch 容器设为 inert，普通鼠标编辑被阻止；但 inert 不等于 VM 级事务锁，也不能阻止异步 VM 事件、其他入口、测试注入或未来代码调用。第二 target 失败、取消和并发修改的最终 VM 状态仍需要真实浏览器 VM E2E 确认，但从控制流看恢复责任已经分散。

**影响**：

- 第一个 target 已注入、第二个 target 失败时，恢复路径可能跨越多个 revision 和 VM 事件。
- 恢复期间发生用户或其他 VM 修改时，无条件 loadProject 可能覆盖这些修改。
- 内层 runner、外层面板和 projectApplyResult 对“本次操作已经提交到哪一步”的认识可能不同。

**修复建议**：

将项目生成建模成一个 ProjectTransaction：

1. 在所有编译、资源提交和程序生成前获取统一 lease。
2. 获取 lease 后立即建立唯一 backup 和 baselineRevision。
3. Prepare 阶段只能生成内存/IndexedDB 结果，不得修改 VM。
4. 需要网络的 target ProgramIR 生成放在 Commit 前完成；Commit 阶段不等待模型、素材选择或生图。
5. 每次 VM mutation 前后检查 lease、target identity 和 revision。
6. 只有事务 owner 可以恢复；恢复前确认 revision 仍是事务自身最后已知值。
7. 内层失败只返回结构化错误，由事务管理器统一决定局部回滚或全项目恢复。

**验收标准**：资源成功、第二个 target 失败、取消、共享状态写入后失败、并发修改五类场景都能证明最终 VM 状态，且并发修改不被覆盖。

### F-04 重试预算内外层相乘，修复上下文持续增长

**等级**：S1，成本和稳定性风险。

**代码证据**：

- backend/app/config.py:27 默认 ai_blocks_retry_attempts=3。
- backend/app/routes/ai_blocks.py:84-85 将其转换为 _collect_resilient 的内部重试次数。
- routes/ai_blocks.py:180-197 的 ProgramIR 逻辑生成最多进行 5 次外层尝试。
- routes/ai_blocks.py:213-231 的流式 JSON 路径同样受内层重试影响。
- ProgramIR 使用 routes/ai_blocks.py:339-345 的 retries=4，默认最坏可达到 5 x 3 = 15 次供应商请求；计划路径 retries=2，最坏可达到 3 x 3 = 9 次。
- prompts_ai_blocks.py:50-54 会把每次失败的原文和错误追加到消息列表。

**影响**：

- “允许 4 次修复”实际可能消耗 15 次上游请求。
- 单条用户操作长时间占用 ai_blocks_max_concurrency。
- 费用、延迟、供应商限流和超时风险隐藏在两个配置层里。
- 网络错误、JSON 错误、schema 错误和语义错误没有不同预算。

**修复建议**：

分离并统一预算：

- transport retry 只对连接失败、超时和明确的 5xx 重试，次数小且带退避。
- semantic repair 只在原始结果解析或校验失败时进行固定次数修复。
- 每条请求携带 total_attempt_budget，并在路由层强制上限。
- 不允许各层独立叠加 retries。
- 记录结构化的 attempt_count、transport_retry_count 和 repair_count。

**验收标准**：测试能断言各错误类型的总供应商调用次数、最大 prompt 大小和最终用户可见的重试次数。

### F-05 素材匹配证据没有形成策略闭环

**等级**：S1，项目资源功能的核心承诺未兑现。

**代码证据**：

- 实施文档要求 requirements 返回资源意图，前端匹配本地 manifest，再以 asset_match_evidence 发送给 generate，见实施文档/AI写积木扩展角色与舞台资源功能实施文档.md:120-130。
- AiBlocksPanel.vue:376-386 的首次项目生成请求没有发送 asset_match_evidence。
- backend/app/schemas/ai_blocks.py:79-87 的 RequirementsResponse 没有资源意图字段。
- prompts_ai_blocks.py:89-95 只是把 evidence 放入提示词，不执行服务端策略验证。
- project-resource-preparer.ts:81-112 直到 Prepare 阶段才阻止 existing_only 下的生图。

**影响**：

- 模型无法看到可靠的本地候选证据，只能猜测资源是否存在。
- existing_only 不是生成前策略，而是较晚阶段的异常退出。
- builtin_first 无法证明已优先使用内置素材，也无法证明生图是必要的。
- requirement、资源候选和资源操作没有可审计的关联 ID。

**修复建议**：

1. RequirementsResponse 增加资源意图、资源类型、搜索词和是否核心资源。
2. 前端基于可信本地 manifest 产生 evidence，只发送数量、候选名和标签摘要。
3. 后端验证 asset_policy 与 evidence；existing_only 且候选为零时拒绝含 image_generation 的 manifest。
4. builtin_first 没有候选时要求模型解释或直接停在 review。
5. 用户确认候选资源后再生成 ProjectManifest，保存用户确认后的 immutable evidence。

### F-06 删除角色政策与实现相互矛盾

**等级**：S1，破坏性能力边界不清。

**代码证据**：

- README.md:320-323、docs/scratch项目清单生成提示词.md:75-77、实施文档/修复问题恢复计划.md:31-33 和 87-93 都声明不支持删除已有角色。
- backend/app/schemas/ai_blocks.py:485-507 接受 delete_sprite。
- frontend/src/ai-blocks/project-plan.ts:42-48 声明 delete_sprite，project-resource-executor.ts:55-60 调用 deleteSprite。
- project-plan.ts:121-123 的前端操作白名单又没有 delete_sprite，使 126-130 的分支不可达。
- backend/app/prompts_ai_blocks.py:82、100 同时包含 delete_sprite 规则，显示代码处于中间态。

**影响**：

- 后端 schema、前端类型、执行器、validator、提示词和产品文档对同一操作给出不同答案。
- 当前虽然可能被前端 validator 拦截，但 wire 层仍暴露了部分破坏性能力。
- 未来补齐 validator 时可能无意打开无二次确认的角色删除。

**修复建议**：

先确定唯一产品政策：

- 若不支持：从 wire schema、prompt、前端类型、执行器和测试中一并删除 delete_sprite，统一输出 UNSUPPORTED_SCRATCH_REQUEST。
- 若支持：定义为高风险操作，要求二次确认、唯一 target handle、事务备份、撤销和真实 E2E，不能由普通资源确认静默执行。

在当前事务和身份保护尚未统一前，建议第一阶段继续保持不支持删除。

### F-07 项目生图尺寸与图片 API 尺寸契约不一致

**等级**：S2，当前路径可直接触发。

**代码证据**：

- backend/app/schemas/ai_blocks.py:465-473 接受 image_generation source 的 width、height 为 512、768、1024。
- frontend/src/ai-blocks/project-plan.ts:84-88 也只接受 512、768、1024。
- backend/app/schemas/image_generation.py:6-10 的 ALLOWED_SIZES 却是 480x360、640x360、360x360、360x480。
- backend/app/services/image_generation.py:21-23 在调用供应商前执行 request.validate_canvas_size()。
- project-resource-preparer.ts:86-95 将 manifest 尺寸原样发送到 /api/image/generate。
- backend/app/routes/image_generation.py:44-49 将该异常转为 422 INVALID_REQUEST。

**复现路径**：项目清单返回 image_generation source，width=512、height=512；前端 Prepare 调用图片接口，服务端在供应商调用前拒绝。

**影响**：

- 项目级生图与直接生成素材使用两套不兼容尺寸集合。
- 用户在资源确认后才看到协议错误。
- 模型输出的项目合法尺寸并不保证能进入图片服务。

**修复建议**：

只保留一个共享尺寸常量和明确的转换边界。要么 ProjectManifest 直接采用图片 API 尺寸，要么 Prepare 明确映射项目尺寸并分别记录 requested_canvas 和实际生成尺寸。增加 manifest 到图片接口的端到端测试。

### F-08 目标身份依赖名称，重复角色和大小写规则不一致

**等级**：S2，可能导致错写对象。

**代码证据**：

- frontend/src/ai-blocks/project-plan.ts:91-105 对 existing_sprite 使用名称引用，校验时通过 existingSpriteNames.includes(value.name)，大小写敏感。
- AiBlocksPanel.vue:495-497 解析已有角色时转成小写，并返回第一个匹配项。
- project-resource-executor.ts:28-30 也按小写名称 find 第一个角色。
- target-context.ts:134-137 给模型的项目上下文只有角色名称，没有稳定 target 身份。

**影响**：

- validator 可能拒绝执行阶段会接受的大小写变体，或反过来让执行阶段接受未被 validator 确认的目标。
- 两个角色同名时资源和程序会写入第一个匹配角色。
- 生成中途目标重命名后，名字引用不能证明仍是原对象。

**修复建议**：

- 在计划确认阶段由宿主把名称解析成 transaction-local target handle。
- existing_sprite 必须唯一匹配，重复名称直接阻止提交。
- 校验与执行统一 Unicode/大小写归一化。
- 每次 mutation 前验证 target handle 仍存在且名称未改变；模型不接触 VM 内部 ID。

### F-09 共享变量、列表和广播不使用统一 resolver 与事务语义

**等级**：S2，跨角色行为风险。

**代码证据**：

- AiBlocksPanel.vue:501-521 直接读取 stage.variables，直接调用 stage.createVariable，并立即读取 stage.variables[id]。
- 该路径没有使用 generation-runner.ts:157-186 的 ProgramResolver/统一变量解析 API。
- applyProjectPrograms 在资源提交后先应用 shared_state，再逐个生成 target，见 AiBlocksPanel.vue:524-532。
- generation-runner.ts:357-362 的广播安装只扫描已有 blocks 中 fields.BROADCAST_OPTION.id，未覆盖所有可能的广播输入引用或刚生成的结构。

**影响**：

- 如果 createVariable 的事件落表不是同步操作，立即读取可能拿不到新变量。
- 同名变量、列表和广播的作用域规则与单目标生成器不同。
- 共享状态已经写入但第二个 target 失败时，恢复依赖外层 loadProject，而不是统一状态记录。
- 广播接收、发送和 shadow 菜单之间的引用可能不完整，导致程序生成成功但运行时广播不起作用。

**修复建议**：

1. 统一变量、列表、广播的 ensure、resolve 和 rollback API。
2. 优先使用 VM 的 lookupVariableByNameAndType 或宿主封装，不直接依赖内部变量字典的即时可见性。
3. 编译前建立完整符号表，验证所有 variable/list/broadcast 引用。
4. 从编译结果的所有 fields 和 shadow 结构收集广播引用，不只扫描 BROADCAST_OPTION。
5. 共享状态 mutation 必须登记到 ProjectTransaction，并与资源和程序一起恢复。

### F-10 rewrite 模式不一定真的重写全部脚本

**等级**：S2，产品模式语义不完整。

**代码证据**：

- backend/app/prompts_ai_blocks.py:14-16 把 rewrite 描述为重写全部脚本。
- frontend/src/ai-blocks/generation-runner.ts:325-327 只删除 isAiEditableScript 判断为可编辑的顶层脚本。
- frontend/src/ai-blocks/target-context.ts:89-101 将未知、扩展或人工脚本标记为 protected，不进入 scriptRefs。

**影响**：

用户选择 rewrite 时，实际结果可能是“保留未知/扩展/人工脚本，重写一部分 AI 脚本”。如果 UI 没有明确说明，这与清空并重建的理解不同；保留脚本还可能与重写脚本共享变量、广播或状态。

**修复建议**：

将模式拆为 rewrite_all_ai、replace_target_scripts、append、modify。当前第一阶段至少把 rewrite 的 UI 文案改为“重写 AI 生成脚本”，并在真正清空目标全部脚本时增加独立风险确认和完整备份。

### F-11 舞台 target 在后端按 sprite 默认规则校验

**等级**：S2，目标能力边界错误。

**代码证据**：

- backend/app/routes/ai_blocks.py:346-349 对 program_ir 调用 validate_program_contracts(item)，没有传 target_kind。
- backend/app/schemas/ai_blocks.py:315-317 的校验默认 target_kind 为 sprite。
- routes/ai_blocks.py:391-394 对 project_program 也没有从 program_target 解析舞台/角色。
- 前端 generation-runner.ts:342-345 虽然按 target.isStage 传入 stage/sprite，但后端已经先做过不一致判断。
- frontend/src/ai-blocks/block-catalog.ts:28-30、34-40、44-61 存在 both、stage-only 和 sprite-only 分类。

**影响**：

舞台合法行为可能被错误拒绝；某些角色专属或舞台专属结构也可能被后端放行，直到前端切换目标后编译才失败。后端校验结果因此不能作为“已按实际 target 验证”的可信信号。

**修复建议**：

把 target kind 作为 validator context 必填字段。单目标从受信 target 快照读取，项目目标从 manifest 关联记录读取，禁止路由级 validator 使用 sprite 默认值。

### F-12 scope、output_contract 和 protocol_version 没有组合校验

**等级**：S2，协议边界缺口。

**代码证据**：

- backend/app/schemas/ai_blocks.py:90-99 只声明字段枚举，没有组合 model_validator。
- backend/app/routes/ai_blocks.py:336-407 基本按 output_contract 分支。
- 可以构造 scope=single_sprite + output_contract=project_manifest、scope=project + output_contract=program_ir、project_manifest 缺失但 output_contract=project_program 等组合。
- protocol_version: Literal[1, 2] 被接受，但没有选择不同 schema、prompt、SSE 事件或响应版本；前端也没有显式传该字段。

**影响**：

无效请求进入模型调用后才失败，浪费供应商请求。调用方以为 protocol_version=2 会协商新协议，但服务端仍按当前实现处理。

**修复建议**：

在 GenerateRequest 上增加组合 validator：single_sprite 只允许 program_ir；project 只允许 project_manifest 或 project_program；project_manifest 禁止 program_target；project_program 必须有 manifest 和精确关联的 target；不支持的 protocol_version 立即 422。响应事件必须携带实际协议版本。

### F-13 SSE ready 事件没有绑定请求合同，供应商 EOF 不总是被识别为不完整

**等级**：S2，客户端状态机风险。

**代码证据**：

- frontend/src/ai-blocks/ai-blocks-api.ts:162-193 会记录三种 ready 事件，但没有先根据 output_contract 建立允许事件集合。
- project_program 请求收到 program_ready 时，客户端会先保存普通 program，最后按 207-208 返回普通 program 结果，没有立即报告合同错误。
- ai-blocks-api.ts:196-202 只要求 message_complete，不要求 ready 类型与请求合同匹配。
- backend/app/routes/ai_blocks.py:408 发送 message_complete；异常时 414-419 只有在 ready_sent 为假时发送 message_error。
- backend/app/providers/agnes_text_provider.py:108-121 的 OpenAI parser 看到 [DONE] 会 return，但没有向上层报告 finished；自然 EOF 也可能正常结束。
- Responses 分支在 agnes_text_provider.py:124-147 处理 failed/incomplete，但没有统一的 EOF 完成状态。

**影响**：

- 上游输出半截内容后断流时，chat 可能仍发送成功 terminal。
- JSON 截断可能被误当成 schema 错误并进入 repair。
- 已发送部分 delta 后失败时，客户端没有 partial 的一致语义，重试可能重复拼接。

**修复建议**：

1. 建立 contract 到 allowed_ready_event 的映射，收到其他 ready 事件立即报协议错误。
2. provider parser 返回 finished 状态，并要求各供应商的明确结束事件。
3. terminal event 统一带 request_id、sequence、status=completed|failed|partial。
4. 客户端失败后丢弃或显式标记未完成 buffer，不把部分消息当完整结果。

### F-14 model tier 和 thinking strength 没有统一映射

**等级**：S2，能力和成本不可预测。

**代码证据**：

- backend/app/providers/agnes_text_provider.py:29-30 将 normal/advanced/super 映射到 agnes/deepseek/gpt。
- deepseek 分支在 62-64 只在启用 thinking 时固定发送 reasoning_effort=high，忽略 low、medium、xhigh、max。
- GPT Responses 分支在 68-73 使用请求 strength，但 backend/app/config.py:39 的 gpt_reasoning_effort 没有参与该映射。
- Agnes 分支使用 THINKING_BUDGET_TOKENS，和其他 provider 使用的 effort 名称不同，见 agnes_text_provider.py:11-12、75-80。

**影响**：

- UI 选择的思考档位在不同 tier 下含义不同，甚至完全不生效。
- 费用、延迟和质量不能从用户选项推断。
- disabled 时各 provider 的请求协议也不一致。

**修复建议**：

定义 provider capability matrix，明确每个 tier、strength、JSON mode 和 stream completion 能力。对不支持的档位明确降级并发送 warning，或直接返回可操作错误。增加请求 payload 单测，逐档验证实际 provider 字段。

### F-15 normalizer 静默修改模型输出

**等级**：S2，严格协议和可审计性冲突。

**代码证据**：

- backend/app/routes/ai_blocks.py:362-369 使用 normalize_project_manifest_input。
- backend/app/schemas/ai_blocks.py:531-558 删除部分 operation.target，把字符串 true/false 转成布尔值，把 add_costume 的 scratch_library asset_type 从 sprite 改成 costume，并删除 stage/current_target 的 name/op_id。
- prompts_ai_blocks.py:81-88 已要求模型严格输出正确形状。

**影响**：

模型输出违反协议时不一定进入 repair，服务端会在用户不可见的情况下替换其语义。原始输出、校验结果和最终执行结果不一致，复盘难以判断是模型错误、旧客户端兼容还是服务端修正。

**修复建议**：

只保留版本化、无歧义的兼容别名；每次兼容转换都记录结构化 warning。涉及资源类型、目标引用和操作语义的变化直接进入 repair 或拒绝。

### F-16 资源自动选择会静默改变模型计划

**等级**：S2，用户确认语义不完整。

**代码证据**：

- frontend/src/ai-blocks/AiBlocksPanel.vue:565-596 的 autoSelectBuiltInResources 对搜索结果只取第一个候选。
- 没有候选时把 op_id 加入 skipped，并在 595 直接从 plan.resource_ops 过滤掉。
- AiBlocksPanel.vue:413-417 对不需要生图的项目自动选择内置资源、自动 confirmProjectPlan 并提交。
- ResourcePlanReview.vue:23-38 虽提供资源条目和候选确认，但纯内置路径可能绕过。

**影响**：

模型提出的资源可能被宿主替换为任意第一个候选；没有候选的资源操作被静默删除；用户最后只看到项目提交完成，无法知道哪些资源被替换或跳过。

**修复建议**：

候选超过一个时要求用户选择；没有候选时停在 review，要求修改搜索词、切换策略或取消；纯内置资源也显示清单、目标和候选摘要。提交前保存用户确认后的 immutable manifest，不要原地过滤模型输出。

### F-17 ScratchGuiBridge 事件绑定和 revision 计数不稳定

**等级**：S2，并发保护基础风险。

**代码证据**：

- frontend/src/scratch/scratch-gui-bridge.ts:30-44 每次 setVm 都给 VM 和 runtime 注册新的监听器，但没有解绑旧 VM。
- 同一 PROJECT_LOADED 同时监听 vm.on 和 vm.runtime.on，markProjectLoaded 在非首次加载时递增 revision，见 41-44、62-73。
- bridge.beginBlockGeneration 在 113-121 只保护 generatingBlocks 标志，不覆盖 project resource runner 或模型请求阶段。

**影响**：

旧 VM 事件可能继续修改当前 bridge 的 revision 或通知监听者；同一次 PROJECT_LOADED 可能重复计数，导致合法流程被误判为外部修改，或在漏计数时允许覆盖修改。

**修复建议**：

保存并执行旧 VM/runtime 的 unsubscribe；为 VM 实例增加 generation token；明确一个 PROJECT_LOADED 权威来源，其他来源只用于 ready 探测；将单目标生成锁扩展为项目事务 lease。

### F-18 日志仍可能记录模型输出和供应商错误细节

**等级**：S2，数据保护问题。

**代码证据**：

- backend/app/routes/ai_blocks.py:116-119 直接 print(delta.text)。
- routes/ai_blocks.py:410-412 记录 _error_summary(exc)，其内容可能源自模型原文解析和校验。
- backend/app/routes/image_generation.py:44-49 记录图片请求错误文本；62-65 记录供应商错误消息。
- 实施文档要求只记录 request_id、阶段、错误码、字符数、delta 数、重试次数和耗时，见实施文档/AI写积木扩展角色与舞台资源功能实施文档.md:575-583。

**影响**：

用户输入、模型输出、提示注入内容、资源描述或供应商内部错误可能进入日志，并被本地收集或长期保留。

**修复建议**：

删除逐 delta stdout 和错误原文；只保留 request_id、provider、scope、output_contract、阶段、错误类型、结构化错误码、字符数、delta 数、重试次数和耗时。Pydantic 错误序列化不包含 input。

### F-19 ProgramIR catalog 仍未完成语义级验证

**等级**：S3，生成质量和可运行性问题。

**代码证据**：

- backend/app/scratch_block_catalog.py 中 FieldSpec 主要描述 required/resolver，字段值没有完整枚举集合。
- frontend/src/ai-blocks/program-compiler.ts:132-152 主要检查 field 是否存在、基本类型和 resolver 字符串。
- block-catalog.ts 中的 KEY_OPTION、STYLE、EFFECT、STOP_OPTION、OPERATOR 等菜单字段没有统一列出真实可接受选项。
- resolver 主要验证名称和声明关系，资源名称、菜单值和当前 VM 是否存在的跨对象检查不足。

**影响**：

程序可能结构合法却使用无效菜单值；变量、列表、广播、造型、声音和背景引用可能直到编译或运行时才暴露。

**修复建议**：

让 catalog 与 Scratch toolbox/真实 VM 合同定期对照；为菜单字段添加 enum；为 resolver 绑定当前 target/context 符号表；将 ProgramIR、资源清单和目标上下文做一次完整语义验证。

### F-20 文档、协议和测试状态已经分叉

**等级**：S2，长期维护风险。

**代码证据**：

- 当前前端处理 manifest_ready 和 project_program_ready，并拒绝 project_ready，见 frontend/src/ai-blocks/ai-blocks-api.ts:162-208。
- 实施基线仍描述 ProjectPlan、block_programs 和 project_ready，见实施文档/AI写积木扩展角色与舞台资源功能实施文档.md:67-79、101-112、424-435、475-495。
- 拆分方案存在已勾选和未勾选阶段，见实施文档/AI写积木大JSON拆分与渐进提交实施方案.md:166-208；恢复计划却把相近阶段标为完成。
- README.md:188-194 描述需求先整理、检查后应用，但没有完整说明新 manifest/per-target 协议，API 部分仍主要说明旧 program_ready。
- frontend/tests/e2e/ai-blocks.spec.ts:40-323 主要覆盖单角色、舞台单程序、默认值和取消，没有双角色、多 target、资源选择、画布回填、第二 target 失败恢复和 SB3 reload。
- backend/tests/test_ai_blocks.py 项目协议覆盖主要集中在一个 manifest 流测试，见 758-782。

**影响**：

开发者可能依据旧文档继续实现已移除的 project_ready 或 block_programs；测试全绿容易被误解为项目模式已完成；delete_sprite、尺寸和素材策略等中间态可能再次混入。

**修复建议**：

1. 指定唯一 canonical 文档和唯一协议图。
2. 统一 README、提示词、OpenAPI、前后端类型、实施状态和 E2E fixture 的命名。
3. 每个“已完成”状态都必须对应可执行测试或明确的未完成项。
4. 旧协议测试要标记为 legacy compatibility 或删除，不与当前主路径混用。

## 8. 跨问题根因

### 8.1 AI 输出被当成语义事实，而不是不可信提案

当前有较多 JSON 结构校验，但结构合法不等于语义合法。模型仍可能提出未在 manifest 中登记的目标、未匹配本地证据的资源、不存在的菜单值、需要用户决定的默认触发方式和破坏性删除操作。服务端主要验证“对象长什么样”，还没有统一验证“对象是否基于当前项目状态、用户确认和本次事务产生”。

### 8.2 两阶段拆分减少单次 JSON 风险，却增加跨请求关联风险

ProjectManifest 和 project_program 分开是合理方向，但分开后必须有 transaction id、manifest digest、target handle、baseline revision、program_target 精确关联、每个 target 的执行结果和统一取消/恢复状态。当前这些信息主要存在于前端变量和约定中，没有形成前后端共同强制的聚合合同。

### 8.3 名称是展示字段，不是可靠身份

模型需要看到角色名、造型名和背景名，但执行器不能只凭名称定位。展示名称可以重复、改变大小写、出现 Unicode 归一化差异，也可能在一次事务中被用户修改。身份解析必须在宿主侧完成并持续验证。

### 8.4 严格校验与自动修复边界没有定义

严格拒绝、兼容旧别名、语义纠错、策略降级和用户确认是五种不同动作。当前 normalizer、autoSelect、默认化和 delete_sprite 混合了这些概念，用户无法知道最终行为由谁决定。

### 8.5 保护机制是局部的，缺少统一 lease

单目标有 beginBlockGeneration，资源 runner 有自己的 backup，面板又有一层恢复；这些局部保护彼此不知道对方的 revision、取消状态和恢复责任。项目级操作需要一个统一 owner、一个事务状态机和一个恢复责任方。

## 9. 推荐的目标协议与状态机

### 9.1 建议的请求上下文

每次用户操作由前端创建 transaction context：

    transaction_id
    protocol_version
    scope
    output_contract
    baseline_revision
    baseline_target_handle
    requirements_digest
    manifest_digest（项目模式）
    target_handle（project_program）
    total_attempt_budget

模型永远不直接生成 VM 内部 ID。target_handle 是宿主创建的逻辑引用和受信快照的关联值，不是模型可以自由编造的 VM 句柄。

### 9.2 建议的状态机

    idle
      -> clarifying
      -> requirements_confirmed
      -> planning
      -> plan_confirmed
      -> preparing
      -> prepared
      -> generating_programs
      -> validating
      -> committing
      -> completed

任一阶段都可以进入：

    cancelled
    failed
    restoring
    restored
    restore_failed

必须满足以下规则：

- clarifying 阶段不得因服务端默认值自动变成 requirements_confirmed。
- preparing 阶段不得改变 VM。
- generating_programs 阶段不得改变 VM，或必须由统一 transaction lease 包住所有状态变更。
- committing 阶段不等待模型、素材选择或网络。
- completed 只在所有 target、共享状态和资源操作成功且最终 revision 已记录后发送。
- failed/cancelled 只有在恢复完成后才释放 lease。

### 9.3 建议的 canonical output contract

以当前实际代码使用的事件名称为基础，建议固定为：

| scope | output_contract | ready 事件 | 说明 |
|---|---|---|---|
| single_sprite | program_ir | program_ready | 一个目标的 ProgramIR |
| project | project_manifest | manifest_ready | 仅资源和目标意图，不含程序正文 |
| project | project_program | project_program_ready | 一个已在 manifest 中登记的 target 的 ProgramIR |

旧的 project_plan、block_programs、project_ready 要么删除，要么单独标记为 legacy；不能继续混入当前主路径。

## 10. 分阶段整改路线

### P0：先修正错误行为和事务边界

1. 修复需求确认：首次整理不传默认授权，保留 ready=false 和 missing_questions。
2. 为 GenerateRequest 增加 scope、contract、protocol、manifest、program_target 组合校验。
3. 建立 manifest + program_target 聚合校验，从聚合上下文解析 target kind 和 current_program。
4. 引入项目级 transaction lease，统一资源、共享状态和所有 target 程序的 backup、revision、取消和恢复。
5. 统一 target identity，重复名称直接阻止提交。
6. 决定 delete_sprite 唯一产品政策；第一阶段建议完全关闭 wire 和 executor。
7. 修正项目生图尺寸契约，增加 manifest 到图片 API 的实际调用测试。

### P1：修复生成质量和供应商协议

1. 统一 transport retry 与 semantic repair 总预算。
2. 限制 repair context，只保留结构化错误和最近一次必要片段。
3. 完成 asset_match_evidence 闭环，existing_only 零候选时在 generate 前停止。
4. 加入 contract-to-ready-event 映射和明确的 SSE terminal 状态。
5. provider parser 必须识别明确完成事件。
6. 路由按 stage/sprite 传入 validator context。
7. 统一 model tier/thinking capability matrix。
8. normalizer 只处理版本化、无语义变化的兼容别名。
9. 完成变量、列表、广播、菜单和资源名称的符号级验证。

### P2：收口代码质量、可观测性和文档

1. ScratchGuiBridge 解绑旧 VM 监听器，加入 VM generation token。
2. 删除模型 delta stdout 和供应商错误原文日志。
3. 统一前后端 schema fixture，严格拒绝未知项目字段。
4. 更新 README、提示词、实施状态和 OpenAPI。
5. 将项目模式测试提升到 Playwright + 真实 VM + SB3 往返级别。
6. 把 autoSelect 改成可见的候选确认，不再静默选择和过滤。

## 11. 必须补齐的测试矩阵

### 11.1 需求和协议

| 场景 | 预期 |
|---|---|
| 模糊需求且未授权默认 | requirements.ready=false，返回问题，不调用 generate |
| 明确授权默认 | 记录明确默认字段，才允许继续 |
| single_sprite + project_manifest | 422，供应商调用次数为 0 |
| project + program_ir | 422 |
| project_program 缺 manifest | 422 |
| manifest 中没有 program_target | 422 |
| created_sprite 引用错误 op_id | 422 |
| protocol_version 不支持 | 422 或明确降级事件 |
| program 请求收到 manifest_ready | 客户端协议错误 |

### 11.2 目标和语义

| 场景 | 预期 |
|---|---|
| 舞台生成 stage 合法程序 | 后端和前端均按 stage 校验 |
| 舞台生成 sprite-only 程序 | 后端拒绝 |
| 两个同名角色 | review 阶段阻止提交 |
| 角色名大小写差异 | validator 和 executor 规则一致 |
| 目标在生成期间重命名/删除 | 事务失败且不覆盖用户修改 |
| rewrite | 只删除用户确认的模式范围 |
| 未声明变量、列表、广播 | 在语义校验阶段拒绝 |
| 无效菜单值 | 在 catalog 校验阶段拒绝 |

### 11.3 项目事务

| 场景 | 预期 |
|---|---|
| 资源准备取消 | VM 不变，资产持久化按策略保留 |
| 资源提交第 2 项失败 | 恢复到 baseline |
| 第 1 target 成功、第 2 target 失败 | 资源、共享状态和第 1 target 全部恢复 |
| 第 2 target 生成期间用户修改 | 拒绝恢复覆盖，明确提示用户 |
| 共享变量已创建后程序失败 | 共享变量随事务恢复 |
| 广播帽和广播输入同时出现 | 所有引用安装且运行有效 |
| 用户点击撤销前继续编辑 | 拒绝覆盖用户后续修改 |
| 取消后重新生成 | 旧 controller、lease 和状态全部释放 |

### 11.4 供应商和流式协议

| 场景 | 预期 |
|---|---|
| 连接失败 | 只消耗 transport retry 预算 |
| schema 错误 | 消耗 semantic repair 预算，不无限叠加 |
| 连续失败 | prompt 和调用次数有硬上限 |
| OpenAI EOF 无 DONE | incomplete，不发送成功 terminal |
| Responses response.incomplete | failed/incomplete，不发送 ready |
| 已发送 delta 后失败 | partial + error，不报告 complete |
| low/medium/high/xhigh/max | 每个 provider payload 可断言实际映射 |

### 11.5 资源和文档

| 场景 | 预期 |
|---|---|
| builtin_first 有唯一候选 | 不调用图片服务，显示候选和选择结果 |
| builtin_first 多候选 | 用户显式选择 |
| existing_only 无候选 | 停在 review，不调用生图 |
| allow_generation | 用户确认后才调用生图 |
| manifest 512x512 | 与图片 API 共享契约，不出现 422 |
| delete_sprite | 与唯一产品政策一致 |
| 完成后保存/重新加载 SB3 | 角色、背景、变量、列表、广播和积木都保留 |

## 12. 当前验证结果与限制

本次审计实际执行：

- 后端：backend/.venv/Scripts/python.exe -m pytest -q，56 passed。
- 前端单元测试：npm run test:unit -- --run，18 个测试文件、104 passed。
- 前端生产构建：npm run build，vue-tsc 和 Vite build 均通过。
- Playwright E2E：本次没有执行，因此不能据单测和构建通过宣称项目级 AI 写积木闭环已经验收。

现有测试通过说明基础模块没有立即回归，但没有覆盖本报告最重要的跨边界场景：双角色、多 target、资源策略、第二 target 失败整体恢复、并发修改、SSE 错误 terminal、图片尺寸契约和 SB3 往返。

本次审计没有回退工作区中原有的用户或生成改动。报告本身是新增文档；其他工作区变化应按原归属单独处理。

## 13. 最终判断

当前 AI 写积木已经从“让模型直接输出 Scratch project.json”进化为“生成受限 IR、由宿主编译和注入”，方向是合理的；但当前仍把模型生成、用户确认、项目资源计划和 VM 事务分散在多个局部状态机里。

在这三个不变量成立前，不建议继续扩大能力范围：

1. **未确认的需求不能被标记为 ready。**
2. **没有被 manifest 和受信 target 快照证明的程序不能被执行。**
3. **一次用户操作的所有 VM 变化必须由一个事务、一个备份、一个 lease 和一个恢复责任方管理。**

这三个不变量成立后，再扩充资源操作、跨角色协作和模型档位，才有可验证的基础。



