# AI 写积木大 JSON 拆分与渐进提交实施方案

**文档状态**：实施中（已完成方案冻结，代码与测试正在按阶段落地）

**适用仓库**：D:/ywj/AI-Scratch-web/

**目标**：将“整个项目”模式从一次生成一个包含所有角色、资源和 ProgramIR 的大 ProjectPlan，改为小型项目清单加按目标逐个生成 ProgramIR，并在每个目标生成完成后独立校验、写入 VM，降低 JSON 截断、结构错位和单个角色错误造成全局失败的概率。

---

## 1. 问题确认

当前实现已经不是把完整 project.json 一次性加载到 VM：

- AI 输出的是 ProgramIR 或 ProjectPlan，不是 project.json。
- 单个程序由前端编译为 VM block graph，并通过 createBlock 逐步注入。
- 资源操作由 applyProjectPlan 按顺序执行。
- project.json 只在 VM 保存 SB3 时由 Scratch 生成。

当前真正的稳定性问题位于“项目模式 AI 输出层”：一个 ProjectPlan 同时携带所有 resource_ops 和所有 block_programs[i].program，模型需要一次输出并一次通过完整 JSON 校验。任一角色的脚本截断或字段错误都会导致整个计划无法进入 VM。

---

## 2. 改造后的目标流程

~~~text
用户确认需求
  -> requirements
  -> planning
  -> project_manifest（小 JSON，只含资源清单和程序目标清单）
  -> 前端校验 manifest
  -> 准备资源并按资源顺序提交 VM
  -> 为每个 target 单独请求 ProgramIR
  -> 单个 ProgramIR 校验通过后立即渐进式注入该 target
  -> 继续下一个 target
  -> 所有 target 完成后形成最终项目
~~~

单角色模式保持现有流程：

~~~text
planning -> 单个 ProgramIR -> 校验 -> 编译 -> 逐块注入 -> 失败回滚
~~~

项目模式的新边界：

1. 一次 AI 响应只负责项目清单，禁止在 manifest 中嵌入程序正文。
2. 一个程序目标对应一次独立的 ProgramIR AI 响应。
3. 单个 ProgramIR 的大小仍受现有脚本、积木、嵌套深度限制。
4. VM 永远是最终项目状态的唯一写入入口，禁止拼接 project.json 字符串。

---

## 3. 新旧接口契约

### 3.1 新的 ProjectManifest

项目清单只描述资源和待生成程序，不包含任何 program 字段：

~~~json
{
  "schema_version": 1,
  "scope": "project",
  "summary": "小猫接红包",
  "resource_ops": [
    {
      "op": "add_builtin_sprite",
      "op_id": "sprite_milk",
      "name": "红包",
      "x": 0,
      "y": 0,
      "size": 60,
      "tags": ["红包"],
      "source": {
        "kind": "scratch_library",
        "asset_type": "sprite",
        "search_terms": ["gift", "红包"]
      }
    }
  ],
  "program_targets": [
    {"target": {"kind": "current_target"}, "mode": "append", "intent": "控制当前角色并更新得分"},
    {"target": {"kind": "created_sprite", "op_id": "sprite_milk"}, "mode": "append", "intent": "控制新角色移动"},
    {"target": {"kind": "stage"}, "mode": "append", "intent": "初始化项目全局状态"}
  ]
}
~~~

约束：

- 顶层只允许 schema_version、scope、summary、resource_ops、program_targets、shared_state。
- scope 必须为 project。
- shared_state 只记录跨 target 的全局变量、列表和广播名称，不包含程序正文。
- program_targets 不得含 program、blocks 或 VM 图字段。
- created_sprite 只能引用前面声明的角色资源操作。
- 非当前角色和新角色只允许 append，当前角色沿用当前模式。
- 每个逻辑 target 最多出现一次，避免重复生成。

### 3.2 场景提示词拆分

项目清单提示词不再使用一套覆盖所有业务的超长示例，而是按结构化上下文选择场景规则；具体模板同步维护在 docs/scratch项目清单生成提示词.md 和 backend/app/prompts_ai_blocks.py：

- 添加角色：约束唯一 op_id、资源来源、created_sprite 引用顺序和角色级编程意图。
- 添加造型：约束 add_costume 的 target 和选中行为，禁止误写成新增角色。
- 添加背景/切换背景：分别使用 add_backdrop/select_backdrop，校验资源类型，避免重复添加。
- 创建变量/列表/广播：约束稳定命名、global/local 作用域和跨 target 复用。
- 舞台控制：约束 stage 目标只描述全局初始化、背景和全局流程。
- 删除角色/不支持资源：明确禁止臆造 delete_sprite、remove_role 等操作，并要求在 summary 说明不支持。
- 普通单目标：保持资源和程序目标最小化。

每个目标程序请求都会再次携带适用的场景规则，但只要求输出该 target 的 ProgramIR。

### 3.3 单 target ProgramIR 请求

每次程序请求只携带：

- 原始需求和规划步骤。
- 当前 target 的独立上下文。
- 已通过校验的 ProjectManifest 摘要。
- 本次 program_target。
- 不携带其他 target 的程序正文。

服务端只返回一个 ProgramIR，仍使用现有 Pydantic、block contract 和最多 5 次结构修复机制。

### 3.4 协议策略

- program_ir 保持不变，单角色模式继续使用。
- project_plan、ProjectPlan、project_ready 旧合同全部移除，不保留兼容分支。
- 项目模式只允许 project_manifest 和 project_program 两个显式 output contract。
- 服务端根据 output_contract 选择固定响应事件，不猜测模型输出类型；旧请求直接按 schema 拒绝。

---

## 4. VM 提交流程

### 4.1 资源阶段

1. 前端对 manifest 做严格校验。
2. 资源选择、生图和画布处理继续在 Prepare 阶段完成。
3. applyProjectPlan 接收不含程序的资源计划，保存一次 SB3 backup。
4. 资源按 resource_ops 顺序提交，每个角色、造型和背景分别调用 bridge。
5. 角色创建完成后记录真实 target ID，但不把 ID 回传给模型。

### 4.2 程序阶段

1. 前端根据 program_targets 找到真实 target。
2. 设置 VM editing target，读取该 target 的最新上下文。
3. 发起一次 project_program 请求。
4. 收到 ProgramIR 后立即进行前端校验和编译。
5. 调用现有 applyAiProgram，逐个创建 block、刷新工作区、更新进度。
6. 成功后记录该 target 的结果，再处理下一个 target。

### 4.3 事务、取消和恢复

- 整个用户操作仍保留生成前的一份项目 backup。
- 任一资源或程序阶段失败，默认恢复整个用户操作到 backup，避免留下半个项目。
- 单个 ProgramIR 注入失败时，先使用现有局部回滚；局部回滚失败再加载项目 backup。
- 取消请求在当前 target 的渐进式注入点生效，恢复整个操作。
- 所有 target 成功后记录最终 revision，撤销仍拒绝覆盖用户后续编辑。
- 后续可增加“保留已完成 target 并重试失败 target”，但不作为本次第一阶段行为，避免改变现有事务语义。

---

## 5. 分阶段实施

### Phase 0：基线和契约

- [x] 阅读当前生成、编译、资源提交和回滚链路。
- [x] 确认当前 AI 不直接生成 project.json。
- [x] 冻结 manifest、program target 和两个新 output contract。
- [ ] 增加后端 manifest / program target schema。
- [ ] 增加前端对应类型和 validator。

退出条件：新旧类型可独立解析，manifest 不允许嵌入大程序正文。

### Phase 1：后端小 JSON 生成

- [ ] 新增 build_project_manifest_messages 及 repair prompt。
- [ ] 新增 build_project_program_messages 及 repair prompt。
- [ ] /api/ai-blocks/generate 支持 project_manifest。
- [ ] /api/ai-blocks/generate 支持 project_program。
- [ ] 每次响应只发送对应 ready 事件。
- [x] 移除旧 project_plan 分支、ProjectPlan 模型和 project_ready 事件。

退出条件：单次 manifest 响应不含 block_programs 或程序正文；每个 program 请求可独立修复和返回。

### Phase 2：前端逐 target 执行

- [ ] 项目模式先请求并保存 manifest。
- [ ] manifest 资源确认、准备、提交沿用现有资源 UI 和 runner。
- [ ] 提交资源后按 program target 顺序切换真实 target。
- [ ] 每个 target 单独请求 ProgramIR。
- [ ] 每个 ProgramIR 成功后立即调用渐进注入器。
- [ ] UI 显示“第 N/M 个目标”和当前目标名称。
- [ ] 任一步失败时整体恢复 backup。

退出条件：多个角色场景中每次网络响应只包含一个目标的程序，前一个目标完成后才写入下一个目标。

### Phase 3：验证与文档收口

- [ ] 后端 schema、prompt、路由单元测试。
- [ ] 前端 manifest validator、API 事件和多 target runner 测试。
- [ ] E2E 覆盖多角色、多变量、取消、单 target 失败回滚。
- [ ] 前端构建和后端测试通过。
- [ ] 更新 docs/scratch代码规范.md 的项目模式说明。
- [ ] 在本文记录实际变更文件、测试命令和结果。

退出条件：所有自动化测试通过，且能确认不存在一次性大 ProjectPlan 程序响应。

---

## 6. 风险与处理

| 风险 | 处理 |
| --- | --- |
| 多次模型请求增加总耗时 | 程序请求按 target 串行，避免并发污染 VM；保留 transport retry，不重复已成功 target |
| 资源提交后模型请求看到的 target 上下文变化 | 每次请求前重新读取真实 target snapshot；manifest 只传逻辑引用 |
| 全局变量被多个 target 重复声明 | 现有 resolver 按名称和作用域复用；项目程序请求共享 manifest 的变量约定 |
| 某个 target 程序失败留下部分项目 | 项目级 backup 统一恢复，局部回滚作为第一层补偿 |
| 用户在阶段间修改项目 | 每个 target 请求和提交前检查 revision；变化则停止并恢复 |
| 直接拼接 JSON 引用冲突 | 禁止拼接 project.json；所有写入通过 VM API 和现有编译器 |
| 旧客户端仍请求 project_plan | schema 直接拒绝，提示重新发起项目生成；不执行兼容分支 |

---

## 7. 验收用例

1. 单角色简单需求仍返回 program_ready，逐块注入、取消和撤销不回归。
2. 两个新增角色的项目请求先返回小 manifest，再分别返回两个 project_program 响应。
3. 第一个角色完成后，VM 中已有第一个角色和程序；第二个角色响应尚未返回时不提前写入第二个程序。
4. 第二个角色返回非法 JSON 时，只重试第二个 target；最终失败时恢复整个本次项目操作。
5. 多个全局变量和列表只在目标对应的 ProgramIR 中声明一次，并能被其他 target 正确解析引用。
6. 取消发生在任意 target 的逐块注入中，项目恢复到生成前状态。
7. 生成结束后导出 SB3，重新加载后角色、变量、列表、广播和脚本均存在。
8. 通过日志只能看到 request_id、阶段、target 序号、响应大小和错误码，不记录完整 prompt 或模型输出。

---

## 8. 实施记录

### 2026-08-15

- 已确认当前 VM 层已经支持资源逐项提交和积木逐块注入。
- 已创建本方案并完成 Phase 0：项目清单和单目标程序合同已冻结。
- 已完成 Phase 1 的后端和前端主链路改造，正在补充多场景测试与最终验收。

实施完成后在此追加：

- 变更文件：
- 测试命令：
- 测试结果：
- 未完成项和后续建议：
