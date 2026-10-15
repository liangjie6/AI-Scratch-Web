// Code generated from backend/app/prompt_builder.py (PromptBuilder._build_knowledge_base).
// Do not edit by hand; regenerate from the Python reference instead.

package prompt

// UnifiedGenerationProtocol is the transport protocol block appended to the knowledge base.
const UnifiedGenerationProtocol = `统一生成内容协议（实际传输由 system prompt 末尾的 FINAL OUTPUT CONTRACT 决定）：
- tagged stream：项目模式按 requirements、task_spec、blueprint、plan、manifest 和 program 标签顺序输出；每个标签内部是一个严格 JSON 对象。
- JSON object：当前 segment 只输出一个严格 JSON 对象，不输出 XML-like 标签；requirements、task_spec、blueprint、plan、manifest、resource_op 和 program 共用同一套对象字段约束。
项目模式先输出 ScratchTaskSpec v2，再输出含 task_spec_hash 的 ProjectBlueprint v2，最后输出 ProjectManifest v3。TaskSpec 的 entity_id/state_id/behavior_id/criterion_id、Blueprint 的 need_id/job_id、Manifest 的 op_id/ref_id 必须各自稳定且遵守上游外键关系（特别注意：Blueprint 的 state_plan[].ref_id 必须严格等于对应 TaskSpec 的 state_id，严禁自创新 ID 如 state_score 或 ref_score，name 与 ref_id 一致；Blueprint 的 program_jobs 与 Manifest 的 program_targets 的 mode 必须全部严格为 "append"；behavior_graph 的 depends_on 严禁跨角色循环依赖，不同角色的行为原则上保持独立，跨角色状态联动一律通过共享变量或广播解耦，确保 program_jobs 之间拓扑无环）。target 必须是对象：{"kind":"stage"}、{"kind":"existing_sprite","name":"角色名"}、{"kind":"created_sprite","op_id":"op_id"} 或 {"kind":"current_target"}，禁止写成字符串。program_targets 的 mode 只能取 append/modify/rewrite；只有 current_target 可用 modify 或 rewrite，stage、existing_sprite、created_sprite 必须严格为 "append"。resource_ops 必须遵循上方生成的 RESOURCE_OPERATION_CONTRACT。
每个程序的对象字段对应 ProgramIR：metadata 的 schema_version=1 或 2、summary、remove_script_ids，与 variables、lists、broadcasts、scripts 合并；tagged stream 将它们分别放在 metadata、shared_state 和 script 标签中，JSON object 的 program segment 直接输出完整对象。项目 ProgramIR v2 的 shared_state 三个数组必须为空，变量、列表、广播通过 fields 中的 state_ref 引用 Manifest ref_id。积木输出必须使用带缩进与换行的标准漂亮格式（Pretty-printed JSON），禁止输出无换行的超长单行。积木逻辑保持扁平直观，严禁深层复合逻辑嵌套（禁止在 inputs.CONDITION 内部嵌套多层 operator_and/operator_or），多条件请使用嵌套 control_if 拆分，并在输出 substacks 前核对 inputs 嵌套 block 的所有花括号配对完整性。
单目标 target_index 固定为 0；项目 target_index 必须按 manifest.program_targets 顺序递增。
禁止解释、代码围栏、协议外文本和旧接口字段。`
