# AI 写积木功能架构与提示词体系技术全景手册

> 本文档将当前项目（**AI-Scratch-Web**）中「AI 写积木」（AI Blocks Generation）功能的系统架构、执行链路、中间表示（IR）协议定义、全量提示词模板（System Prompts、Segment Contracts、Repair Hints）及生图/对话辅助提示词完整整合汇总于一体。

---

## 目录

1. [核心设计哲学与系统原则](#一核心设计哲学与系统原则)
2. [端到端执行流程（Prepare & Commit 双阶段）](#二端到端执行流程prepare--commit-双阶段)
3. [系统分层架构剖析](#三系统分层架构剖析)
   - 3.1 [前端展现与交互层](#31-前端展现与交互层)
   - 3.2 [前端编排与生命周期管理](#32-前端编排与生命周期管理)
   - 3.3 [前端 ProgramIR 编译器与 Scratch VM 门面](#33-前端-programir-编译器与-scratch-vm-门面)
   - 3.4 [沙箱自动化验证与原子回滚机制](#34-沙箱自动化验证与原子回滚机制)
   - 3.5 [后端 API 与调度状态机](#35-后端-api-与调度状态机)
4. [核心数据中间表示（IR）规范](#四核心数据中间表示ir规范)
   - 4.1 [RequirementsResponse（需求拆解）](#41-requirementsresponse需求拆解)
   - 4.2 [ScratchTaskSpec v2（语义规格说明书）](#42-scratchtaskspec-v2语义规格说明书)
   - 4.3 [ProjectBlueprint v2（工程架构蓝图与DAG）](#43-projectblueprint-v2工程架构蓝图与dag)
   - 4.4 [ProjectManifest v3（部署与资源操作清单）](#44-projectmanifest-v3部署与资源操作清单)
   - 4.5 [ProgramIR v1 / v2（抽象语法树规范）](#45-programir-v1--v2抽象语法树规范)
5. [提示词工程分层架构（Prompt Engineering）](#五提示词工程分层架构prompt-engineering)
   - 5.1 [4 层分级上下文与指纹缓存机制](#51-4-层分级上下文与指纹缓存机制)
   - 5.2 [Level 1 知识库核心提示词（ProgramIR 生成提示词）](#52-level-1-知识库核心提示词programir-生成提示词)
   - 5.3 [统一生成内容协议（UnifiedGenerationProtocol）](#53-统一生成内容协议unifiedgenerationprotocol)
   - 5.4 [终极输出契约（UnifiedOutputContract）](#54-终极输出契约unifiedoutputcontract)
6. [各阶段分段输出契约全量规范（Segment Contracts）](#六各阶段分段输出契约全量规范segment-contracts)
   - 6.1 [requirements 阶段契约](#61-requirements-阶段契约)
   - 6.2 [task_spec 阶段契约（含枚举值强约束）](#62-task_spec-阶段契约含枚举值强约束)
   - 6.3 [blueprint 阶段契约（含条件字段与DAG防死锁约束）](#63-blueprint-阶段契约含条件字段与dag防死锁约束)
   - 6.4 [plan 阶段契约](#64-plan-阶段契约)
   - 6.5 [manifest 阶段契约](#65-manifest-阶段契约)
   - 6.6 [resource_op 单项操作精准修复契约](#66-resource_op-单项操作精准修复契约)
   - 6.7 [program 阶段契约（Pretty-printed JSON 与逻辑扁平化铁律）](#67-program-阶段契约pretty-printed-json-与逻辑扁平化铁律)
7. [闭环自愈修复与诊断提示词全量规则库（Repair Hints）](#七闭环自愈修复与诊断提示词全量规则库repair-hints)
   - 7.1 [路径诊断规则库（Path Hints）](#71-路径诊断规则库path-hints)
   - 7.2 [错误详情诊断规则库（Detail Hints）](#72-错误详情诊断规则库detail-hints)
   - 7.3 [自愈修复提示词组装机制](#73-自愈修复提示词组装机制)
8. [多模态与辅助功能提示词全量规范](#八多模态与辅助功能提示词全量规范)
   - 8.1 [需求问答对话助手提示词（ChatMessages）](#81-需求问答对话助手提示词chatmessages)
   - 8.2 [画面与风格文生图扩写提示词（TextExpansionPrompts）](#82-画面与风格文生图扩写提示词textexpansionprompts)
   - 8.3 [Scratch 专属图像生成提示词（背景与角色绿幕抠图）](#83-scratch-专属图像生成提示词背景与角色绿幕抠图)
9. [核心源文件与代码符号全景索引表](#九核心源文件与代码符号全景索引表)

---

## 一、核心设计哲学与系统原则

AI 写积木功能旨在让用户仅需输入一道 Scratch 题目或交互需求，系统即可全自动完成需求拆解、素材匹配/生成、变量广播规划、多角色积木代码生成并安全注入 Scratch 编辑器。

系统确立了以下五大核心设计原则：

1. **模型绝不直触底层 VM / SB3**：
   大语言模型（LLM）仅负责理解题意并输出强类型、带约束的声明式中间表示（`Requirements`、`TaskSpec`、`Blueprint`、`Manifest`、`ProgramIR`），严禁模型生成底层的 Scratch VM block ID、parent/next 链表指针、Base64 或 raw project.json。底层图装配与 ID 编排全部由前端宿主编译器确定性完成。
2. **两阶段执行与原子回滚（Prepare -> Commit）**：
   将 AI 代码生成与底层编辑器写入严格解耦：
   - **Prepare 阶段**：完成所有模型流式生成、素材检索/生图、DAG 拓扑分层代码请求、静态契约预检与验证沙箱预热，不持有任何编辑器锁。
   - **Commit 阶段**：获取本地项目事务锁，先对当前项目生成 **全量 SB3 快照备份**；随后在毫秒级内批量装配素材、状态并注入积木；注入完成后立即导出候选 SB3 进行无头沙箱加载校验；若出现任何错误或冲突，**100% 自动原子回滚**至操作前状态。
3. **分阶段流水线与单向哈希追溯（Staged Pipeline & Lineage Tracking）**：
   全项目任务细分为：`requirements` -> `task_spec` -> `blueprint` -> `plan` -> `manifest` -> `program`。上游阶段产物计算 SHA-256 签名（如 `task_spec_hash`、`blueprint_hash`）并作为下游的刚性外键约束，杜绝状态漂移与模型幻觉。
4. **闭环自愈修复（Self-Repair Loop）**：
   在流水线各阶段配置独立验证器（Schema 验证、DAG 无环验证、外键一致性校验）。若模型输出违规，编排器会截获精准路径与错误描述，结合定向修复提示词（`RepairHint`）就地触发重试修复（默认 3 轮预算），而无需从头重启整个任务。
5. **分级可观测与持久化检查点（Checkpoint Store）**：
   基于 SQLite / IndexedDB 构建检查点机制（[`run-checkpoint-store.ts`](../frontend/src/ai-blocks/run-checkpoint-store.ts)），记录每一次变更的操作回执与指纹，支持跨会话崩溃恢复与版本安全撤销。

---

## 二、端到端执行流程（Prepare & Commit 双阶段）

```mermaid
flowchart TD
    Start([用户输入题目/交互需求]) --> Panel[前端面板: AiBlocksPanel.vue]

    subgraph S1 [阶段一: 准备阶段 - Prepare (无锁 / 远程流式生成 / 全预检)]
        Panel --> RunnerPrepare[UnifiedGenerationRunner.prepare]
        RunnerPrepare --> ReqStream[1. requirements 阶段: 需求拆解与完整度检测]
        ReqStream -- ready=false --> Clarify[向用户发起追问，等待确认]
        ReqStream -- ready=true --> TaskSpecStream[2. task_spec 阶段: 语义规格 ScratchTaskSpec v2]
        TaskSpecStream --> BlueprintStream[3. blueprint 阶段: 资源依赖与任务DAG ProjectBlueprint v2]
        BlueprintStream --> PlanStream[4. plan 阶段: 展示计划 Plan.steps]
        PlanStream --> ManifestStream[5. manifest 阶段: 部署清单 ProjectManifest v3]
        ManifestStream --> ResPrepare[project-resource-preparer: 匹配官方库/生图/生音]
        ResPrepare --> ProgLayers[projectProgramGenerationLayers: 按DAG拓扑分层批次并发请求]
        ProgLayers --> ProgramGen[6. program 阶段: 生成各目标 ProgramIR v2]
        ProgramGen --> Preflight[program-preflight: 静态结构、字段引用与作用域检查]
        Preflight --> PrewarmSandbox[预热无头验证沙箱 VM]
    end

    subgraph S2 [阶段二: 提交阶段 - Commit (本地原子事务 / 快速写入 / 自动回滚)]
        Panel --> RunnerCommit[UnifiedGenerationRunner.commit]
        RunnerCommit --> LockTx[获取编辑器事务锁 + 导出保存当前 SB3 快照备份]
        LockTx --> CheckpointStore[持久化检查点 run-checkpoint-store.begin]
        CheckpointStore --> ApplyRes[applyProjectResources: 安装角色、造型、背景、音效]
        ApplyRes --> InstallState[installManifestState: 创建全局/局部变量、列表、广播]
        InstallState --> Compiler[program-compiler: ProgramIR 编译为 Scratch VM 积木图]
        Compiler --> ScratchBridge[ScratchProgramPort: 批量更新 VM Blocks 树]
        ScratchBridge --> RefreshUI[刷新 Scratch 积木工作区]
        RefreshUI --> VerifySandbox[verifyCandidateSb3: 导出候选 SB3 在隔离沙箱中运行验证]
    end

    VerifySandbox -- 校验通过 --> Success([应用成功，标记资产绑定，保留检查点])
    VerifySandbox -- 校验失败/报错 --> Rollback[原子回滚: 从备份 SB3 恢复现场并释放事务锁]
    Clarify --> Start
```

---

## 三、系统分层架构剖析

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            前端展现层 (Presentation)                        │
│  - AiBlocksPanel.vue: 需求输入、模型/思考参数配置、历史会话、状态进度通知       │
│  - ResourcePlanReview.vue: 素材审查弹窗 (官方库推荐 / AI生图生音 / 手动微调)    │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
┌──────────────────────────────────────▼──────────────────────────────────────┐
│                            前端编排层 (Runner & Port)                       │
│  - UnifiedGenerationRunner: Prepare 与 Commit 流程总控                      │
│  - project-resource-preparer / executor: 素材下载、解构、音频解码与注入       │
│  - scratch-gui-bridge.ts: VM 事务锁控制、快照捕获、工作区刷新               │
│  - run-checkpoint-store.ts: 幂等检查点与状态持久化                         │
│  - verification-service.ts: 无头沙箱 SB3 加载与运行验证服务                 │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
┌──────────────────────────────────────▼──────────────────────────────────────┐
│                            前端编译层 (IR Compiler)                         │
│  - program-compiler.ts: ProgramIR -> Scratch Blocks 深度编译                │
│    * 生成唯一 Block ID 与输入/阴影积木 (Shadows)                            │
│    * 自动计算 parent / next 双向链接指针                                    │
│    * 映射 resource_ref / state_ref 为 VM 真实已安装 ID                      │
│  - program-preflight.ts: 静态类型与合法性检查                                │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │ SSE 流式传输 (JSON Object / Tagged)
┌──────────────────────────────────────▼──────────────────────────────────────┐
│                        后端 API 与调度层 (Go Backend)                       │
│  - httpapi/routes_aiblocks.go:                                              │
│    * POST /api/ai-blocks/generate-unified (单目标流式生成)                   │
│    * POST /api/ai-blocks/generate (项目清单生成 / 目标程序流式生成)           │
│    * POST /api/ai-blocks/validate (静态契约校验服务)                         │
│    * POST /api/ai-blocks/chat (需求对话助手)                                 │
│  - orchestration/orchestrator.go (GenerationOrchestrator):                  │
│    * 状态机生命周期调度 (requirements -> task_spec -> blueprint ...)         │
│    * 并发与连接预算控制 (Semaphore Slot)                                    │
│    * 自动自愈修复循环 (runSegmentWithRepair + RepairHint)                   │
│    * 单项资源精准修复 (resource_op 替换)                                     │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
┌──────────────────────────────────────▼──────────────────────────────────────┐
│                        提示词与契约中心 (Prompt & Catalog)                  │
│  - prompt/builder.go: 4层分级上下文 Prompt 组装与 TTL 缓存                   │
│  - prompt/repair_hint.go: 动态诊断规则与修复提示生成                        │
│  - blockcatalog/catalog.go: 核心 Opcode 白名单与积木形态契约库               │
│  - provider/: 多模型抽象适配 (Gemini / DeepSeek / Agnes 等)                 │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 前端展现与交互层
- [`AiBlocksPanel.vue`](../frontend/src/ai-blocks/AiBlocksPanel.vue)：
  - **模型配置**：支持三种模型梯度：普通（`Agnes · agnes-2.5-flash`）、高级（`DeepSeek · deepseek-v4.1`）、超级（`GPT · gpt-5.6`）。
  - **思考预算（Thinking）**：支持 `low`、`medium`、`high`、`xhigh`、`max` 五档思维链调节。
  - **范围与模式**：支持 `single_sprite`（单角色生成）与 `project`（全项目生成）；支持 `append`（追加）、`modify`（修改特定脚本）、`rewrite`（重写目标脚本）。
  - **追问式交互**：当 AI 判定题目条件不足时，展示追问卡片，用户补充信息后自动恢复生成。
- [`ResourcePlanReview.vue`](../frontend/src/ai-blocks/ResourcePlanReview.vue)：
  - 在素材 Commit 前提供交互式审查面板；
  - 自动呈现官方 Scratch 素材库匹配项（角色/造型/背景/声音），允许用户切换为 AI 生图或本地资产。

### 3.2 前端编排与生命周期管理
- [`UnifiedGenerationRunner`](../frontend/src/ai-blocks/generation-runner.ts#L497)：
  - **`prepare()`**：负责捕获舞台和角色快照，调用服务端流式接口分段生成合同，并按照依赖拓扑分层（[`projectProgramGenerationLayers`](../frontend/src/ai-blocks/project-contract.ts)）并发拉取各目标的 ProgramIR。
  - **`commit()`**：负责获取本地项目事务锁，先对当前项目生成全量 SB3 快照备份，随后在毫秒级内批量装配素材、状态并注入积木。

### 3.3 前端 ProgramIR 编译器与 Scratch VM 门面
- [`program-compiler.ts`](../frontend/src/ai-blocks/program-compiler.ts)：
  - 解析顶层 `scripts` 数组，将每一个 `BlockIR` 递归转换为 Scratch VM `Block` 实例；
  - 自动生成符合规范的唯一 Block ID（如 `sb3_blk_xxxx`）；
  - 绑定 `parent` 和 `next` 指针，将控制积木的 C 形分支放入 `inputs.SUBSTACK` / `inputs.SUBSTACK2`；
  - 自动为所有数值、文本、角度、颜色、下拉菜单生成合法的 Shadow 隐式积木。
- [`scratch-gui-bridge.ts`](../frontend/src/scratch/scratch-gui-bridge.ts)：
  - 隔离对 Scratch 官方 VM 的直接调用，提供 `beginProjectTransaction()` 获取事务互斥锁；
  - 提供 `saveProject()` 序列化当前项目为 `Uint8Array` 格式的 SB3 二进制，作为操作前的安全基准。

### 3.4 沙箱自动化验证与原子回滚机制
- [`verification-service.ts`](../frontend/src/ai-blocks/verification-service.ts)：
  - 在内存中启动一个完全隔离的无头 Scratch VM 实例；
  - 加载刚刚生成的候选 SB3 二进制包，执行项目载入测试与单帧推进测试；
  - 若沙箱启动抛出任何未捕获异常或存在损坏的资源/脚本，立即返回验证失败错误码。
- **原子回滚（Atomic Rollback）**：
  - 只要在 Commit 期间的任何一步（素材下载失败、编译错误、沙箱加载失败、用户取消）出错，系统立即执行 `bridge.loadProject(backup)`，100% 恢复到用户开始生成前的准确状态，避免污染已有画布与代码。

### 3.5 后端 API 与调度状态机
- [`httpapi/routes_aiblocks.go`](../backend-go/internal/httpapi/routes_aiblocks.go)：
  - 使用 SSE（Server-Sent Events）实现高吞吐流式下发；
  - 通过 Slot 信号量（`semaphore`）进行全局与租户级的并发请求预算控制。
- [`orchestration/orchestrator.go`](../backend-go/internal/orchestration/orchestrator.go#L681)：
  - 实现状态机调度（`generationState`）；
  - 驱动 `runSegmentWithRepair` 闭环，对模型输出进行强类型解码（`decodeModel`）与结构性校验；
  - 针对单项资源操作提供局部修复支持（`resource_op`），大幅降低长上下文重试的时间与 Token 消耗。

---

## 四、核心数据中间表示（IR）规范

### 4.1 RequirementsResponse（需求拆解）
在开始写积木前，系统首先将用户模糊的自然语言拆解为 8 个维度的结构化需求定义：

```json
{
  "ready": true,
  "title": "接苹果小游戏",
  "goal": "控制碗左右移动接住掉落的苹果，接中得分，落地失败",
  "behavior_steps": [
    "碗在屏幕底部随键盘左右键平移",
    "苹果从屏幕顶部随机X坐标下落",
    "苹果碰到碗增加得分并重新在顶部随机产生",
    "苹果触碰下边缘游戏结束"
  ],
  "triggers": ["点击绿旗", "按下左键", "按下右键"],
  "controls": ["左右箭头键控制移动"],
  "success_criteria": [
    "绿旗启动时分数清零",
    "左右箭头能平滑移动碗且不出屏幕",
    "接住苹果得分加1并播放声音"
  ],
  "missing_questions": []
}
```

### 4.2 ScratchTaskSpec v2（语义规格说明书）
精确的形式化行为规格说明书。包含角色实体、读写状态、具体行为序列及可执行验收准则：

```json
{
  "schema_version": 2,
  "title": "接苹果小游戏",
  "goal": "控制碗左右移动接住下落苹果",
  "assumptions": [
    {
      "id": "assump_speed",
      "text": "苹果下落速度为每次5步",
      "confidence": 0.95,
      "requires_confirmation": false
    }
  ],
  "entities": [
    {"entity_id": "entity_bowl", "role": "sprite", "description": "接苹果的碗"},
    {"entity_id": "entity_apple", "role": "sprite", "description": "掉落的苹果"},
    {"entity_id": "entity_stage", "role": "stage", "description": "舞台背景"}
  ],
  "state": [
    {
      "state_id": "score",
      "kind": "variable",
      "scope": "global",
      "initial_value": 0
    },
    {
      "state_id": "game_over",
      "kind": "broadcast",
      "scope": "global"
    }
  ],
  "behaviors": [
    {
      "behavior_id": "bh_bowl_move_left",
      "owner_entity_id": "entity_bowl",
      "trigger": {"kind": "key_pressed", "key": "left arrow"},
      "actions": [
        {"kind": "change_axis", "axis": "x", "delta": {"kind": "literal", "value": -10}}
      ]
    }
  ],
  "success_criteria": [
    {
      "criterion_id": "crit_init_score",
      "description": "点击绿旗得分初始化为0",
      "related_behavior_ids": ["bh_bowl_init"],
      "priority": "must",
      "verification": "executable"
    }
  ],
  "unsupported": []
}
```

### 4.3 ProjectBlueprint v2（工程架构蓝图与DAG）
承接 TaskSpec，进行具体的角色绑定、素材需求规划和任务有向无环依赖图（DAG）编排：

```json
{
  "schema_version": 2,
  "task_spec_hash": "a1b2c3d4...",
  "asset_needs": [
    {
      "need_id": "need_bowl",
      "kind": "sprite",
      "purpose": "接苹果的角色",
      "search_terms": ["bowl"],
      "fallback_order": ["scratch_library", "image_generation"]
    }
  ],
  "entity_resolutions": [
    {"kind": "new_sprite", "need_id": "need_bowl"}
  ],
  "state_plan": [
    {"ref_id": "score", "kind": "variable", "scope": "global", "name": "score", "initial_value": 0}
  ],
  "program_jobs": [
    {
      "job_id": "job_bowl_scripts",
      "owner_entity_id": "entity_bowl",
      "mode": "append",
      "behavior_ids": ["bh_bowl_init", "bh_bowl_move_left"],
      "behavior_graph": {
        "depends_on": [],
        "consumes_broadcasts": [],
        "produces_broadcasts": []
      }
    }
  ],
  "acceptance_tests": [
    {
      "test_id": "test_score_reset",
      "source_criterion_id": "crit_init_score",
      "related_behavior_ids": ["bh_bowl_init"],
      "trigger": {"kind": "green_flag"},
      "expect": [
        {"kind": "variable", "state_id": "score", "comparator": "eq", "value": 0}
      ],
      "timeout_ms": 1000
    }
  ]
}
```

### 4.4 ProjectManifest v3（部署与资源操作清单）
前端素材执行器与变量安装器的可执行契约：

```json
{
  "schema_version": 3,
  "scope": "project",
  "summary": "创建碗和苹果角色，初始化计分系统",
  "assumptions": [],
  "required_extensions": [],
  "resource_ops": [
    {
      "op": "add_builtin_sprite",
      "op_id": "op_add_bowl",
      "source_need_id": "need_bowl",
      "owner_entity_id": "entity_bowl",
      "source": {"kind": "scratch_library", "asset_type": "sprite", "name": "Bowl"}
    }
  ],
  "shared_state": {
    "variables": [{"ref_id": "score", "name": "得分", "initial_value": 0}],
    "lists": [],
    "broadcasts": [{"ref_id": "game_over", "name": "游戏结束"}]
  },
  "local_state": {},
  "program_targets": [
    {
      "job_id": "job_bowl_scripts",
      "behavior_ids": ["bh_bowl_init", "bh_bowl_move_left"],
      "owner_entity_id": "entity_bowl",
      "target": {"kind": "created_sprite", "op_id": "op_add_bowl"},
      "mode": "append",
      "intent": "实现碗的初始化与键盘移动控制"
    }
  ]
}
```

### 4.5 ProgramIR v1 / v2（抽象语法树规范）
针对单个角色的独立 AST 结构：

```json
{
  "schema_version": 2,
  "summary": "碗的初始化与键盘移动脚本",
  "remove_script_ids": [],
  "variables": [],
  "lists": [],
  "broadcasts": [],
  "procedures": [],
  "scripts": [
    {
      "id": "script_init",
      "purpose": "绿旗点击时初始化位置与分数",
      "x": 50,
      "y": 50,
      "blocks": [
        {
          "opcode": "event_whenflagclicked",
          "fields": {},
          "inputs": {},
          "substacks": {}
        },
        {
          "opcode": "motion_gotoxy",
          "fields": {},
          "inputs": {
            "X": {"kind": "number", "value": 0},
            "Y": {"kind": "number", "value": -140}
          },
          "substacks": {}
        },
        {
          "opcode": "data_setvariableto",
          "fields": {
            "VARIABLE": {
              "kind": "state_ref",
              "state_kind": "variable",
              "ref_id": "score"
            }
          },
          "inputs": {
            "VALUE": {"kind": "number", "value": 0}
          },
          "substacks": {}
        }
      ]
    }
  ]
}
```

---

## 五、提示词工程分层架构（Prompt Engineering）

### 5.1 4 层分级上下文与指纹缓存机制

```
[Unified System Prompt]
┌─────────────────────────────────────────────────────────────────┐
│ Level 1: 知识库与基础协议 (Knowledge Base Layer)                  │
│ -> 包含: ProgramIR生成规范 + BLOCK_CATALOG + UnifiedProtocol    │
│ -> 特点: 全局通用，静态不变，TTL = 300秒 内存共享缓存            │
├─────────────────────────────────────────────────────────────────┤
│ Level 2: 项目全局上下文 (Project Context Layer)                  │
│ -> 包含: 现有 Targets 列表、尺寸、现有全局变量/列表/广播          │
│ -> 特点: 整个项目粒度，根据项目内容计算 SHA-256 指纹缓存         │
├─────────────────────────────────────────────────────────────────┤
│ Level 3: 目标角色上下文 (Target Context Layer)                   │
│ -> 包含: 当前角色/舞台名称、已有造型/声音/背景、当前脚本代码    │
│ -> 特点: 单角色粒度，根据角色内容计算 SHA-256 指纹缓存           │
├─────────────────────────────────────────────────────────────────┤
│ Level 4: 请求动态配置 (Request Context Layer)                    │
│ -> 包含: scope (single/project)、mode (append/modify/rewrite)    │
│ -> 特点: 随单次请求动态构建                                     │
├─────────────────────────────────────────────────────────────────┤
│ FINAL OUTPUT CONTRACT (最高优先级终极契约)                        │
│ -> 规定最终输出的 JSON 契约、Pretty-print 格式与严禁单行规则     │
└─────────────────────────────────────────────────────────────────┘
```

---

### 5.2 Level 1 知识库核心提示词（ProgramIR 生成提示词）

以下为 Level 1 知识库（[`ProgramIR-v1生成提示词.md`](../docs/参考资料/ProgramIR-v1生成提示词.md)）的核心正文规范：

```markdown
# Scratch AI 积木生成提示词

你是本项目的 Scratch AI 积木生成器。你的输出会由后端和前端连续校验，并由前端编译后注入当前 Scratch GUI/VM。

## 任务边界
- 你生成的是 ProgramIR，不是 Scratch project.json、SB3、XML、Base64、hydrated VM block 或 JavaScript。
- 只根据受信的目标上下文、当前程序、资源目录和用户需求生成程序。
- 不生成 block ID、变量 ID、列表 ID、广播 ID、assetId、md5ext、next、parent、shadow 或 topLevel；这些由宿主编译器生成。
- 不跨舞台或角色写入局部变量、列表、积木或资源。
- 不使用扩展、过程、自定义积木或未知 opcode，除非 BLOCK_CATALOG/ALLOWED_EXTENSIONS 明确允许且契约支持。

## 语义结构
统一生成传输由 system prompt 的 FINAL OUTPUT CONTRACT 选择 tagged stream 或 JSON object；tagged stream 的每个标签内部是一个严格 JSON 对象。本知识库不定义第二套输出协议。
ProgramIR 是宿主内部的语义程序。禁止 NaN、Infinity、undefined、null 作为非法结构替代值、循环对象、重复引用对象和未声明属性。
如果需求超出当前原生积木能力，在 requirements 中将 ready 设为 false 并填写 missing_questions；不要输出未声明的错误对象。

## 输入与字段
输入只能使用以下形式之一：
- {"kind":"number","value":10}
- {"kind":"angle","value":90}
- {"kind":"text","value":"hello"}
- {"kind":"color","value":"#4C97FF"}
- {"kind":"block","block":{...}}

规则：
- number 和 angle 的字面量 value 必须是有限 JSON 数字。
- text 和 color 的 value 必须是字符串；颜色使用 #RRGGBB。
- 布尔值和 reporter 都使用 kind=block 表达，嵌套 block 的 opcode、shape 和输入必须与 BLOCK_CATALOG 契约兼容；不存在独立的 boolean 或 reporter wire kind。
- 禁止用 null、空字符串或自造布尔字面量替代必填输入。
- 变量、列表和广播字段只写名称（或引用），不写底层 ID。
- 普通 dropdown 使用 Scratch 序列化值：鼠标指针必须使用 "_mouse_"，舞台边缘使用 "_edge_"，不要写 "mouse-pointer" 或中文显示文本。

## 目标与 opcode
- 每条脚本必须从合法事件帽（event_hat）开始。
- reporter/boolean 不得放在脚本顶层或 substacks 的主执行序列中。
- control_stop、control_delete_this_clone 等 terminal 后不得追加同层 block。
- control_forever 是 C-block，不能在其 SUBSTACK 之外追加依赖其“结束”的同层语义。
- sound_play 使用 SOUND_MENU input，不要写 SOUND field。
- data_setvariableto 使用 VARIABLE field 和 VALUE input。
- 资源菜单必须引用 TARGET_CONTEXT 中当前 target 可用的 costumes、sounds、backdrops 或 sprite_names。
```

---

### 5.3 统一生成内容协议（UnifiedGenerationProtocol）

源自 [`protocol_gen.go`](../backend-go/internal/prompt/protocol_gen.go)：

```text
统一生成内容协议（实际传输由 system prompt 末尾的 FINAL OUTPUT CONTRACT 决定）：
- tagged stream：项目模式按 requirements、task_spec、blueprint、plan、manifest 和 program 标签顺序输出；每个标签内部是一个严格 JSON 对象。
- JSON object：当前 segment 只输出一个严格 JSON 对象，不输出 XML-like 标签；requirements、task_spec、blueprint、plan、manifest、resource_op 和 program 共用同一套对象字段约束。
项目模式先输出 ScratchTaskSpec v2，再输出含 task_spec_hash 的 ProjectBlueprint v2，最后输出 ProjectManifest v3。TaskSpec 的 entity_id/state_id/behavior_id/criterion_id、Blueprint 的 need_id/job_id、Manifest 的 op_id/ref_id 必须各自稳定且遵守上游外键关系（特别注意：Blueprint 的 state_plan[].ref_id 必须严格等于对应 TaskSpec 的 state_id，严禁自创新 ID 如 state_score 或 ref_score，name 与 ref_id 一致；Blueprint 的 program_jobs 与 Manifest 的 program_targets 的 mode 必须全部严格为 "append"；behavior_graph 的 depends_on 严禁跨角色循环依赖，不同角色的行为原则上保持独立，跨角色状态联动一律通过共享变量或广播解耦，确保 program_jobs 之间拓扑无环）。target 必须是对象：{"kind":"stage"}、{"kind":"existing_sprite","name":"角色名"}、{"kind":"created_sprite","op_id":"op_id"} 或 {"kind":"current_target"}，禁止写成字符串。program_targets 的 mode 只能取 append/modify/rewrite；只有 current_target 可用 modify 或 rewrite，stage、existing_sprite、created_sprite 必须严格为 "append"。resource_ops 必须遵循上方生成的 RESOURCE_OPERATION_CONTRACT。
每个程序的对象字段对应 ProgramIR：metadata 的 schema_version=1 或 2、summary、remove_script_ids，与 variables、lists、broadcasts、scripts 合并；tagged stream 将它们分别放在 metadata、shared_state 和 script 标签中，JSON object 的 program segment 直接输出完整对象。项目 ProgramIR v2 的 shared_state 三个数组必须为空，变量、列表、广播通过 fields 中的 state_ref 引用 Manifest ref_id。积木输出必须使用带缩进与换行的标准漂亮格式（Pretty-printed JSON），禁止输出无换行的超长单行。积木逻辑保持扁平直观，严禁深层复合逻辑嵌套（禁止在 inputs.CONDITION 内部嵌套多层 operator_and/operator_or），多条件请使用嵌套 control_if 拆分，并在输出 substacks 前核对 inputs 嵌套 block 的所有花括号配对完整性。
单目标 target_index 固定为 0；项目 target_index 必须按 manifest.program_targets 顺序递增。
禁止解释、代码围栏、协议外文本和旧接口字段。
```

---

### 5.4 终极输出契约（UnifiedOutputContract）

源自 [`contracts_gen.go`](../backend-go/internal/prompt/contracts_gen.go#L37)：

```text
FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)
The transport protocol for this request is a tagged stream, not one JSON document. Ignore any earlier sentence that says to output a single JSON object or a bare ProgramIR; that describes an internal payload, not the transport. Output no markdown, code fence, explanation, reasoning, or text outside the tags.

Output tags in this order: requirements, then task_spec, blueprint, plan, manifest only for project scope, then program tags. Every tag body is exactly one JSON object. The backend creates phase events; do not output phase_start or phase_complete tags.

The requirements object has exactly these keys: ready boolean, title string, goal string, behavior_steps string array, triggers string array, controls string array, success_criteria string array, and missing_questions string array. Always include all eight keys, using [] for an empty array. Do not wrap it in another property.
The task_spec object is ScratchTaskSpec v2. It is the execution meaning of the request. Required top-level keys are exactly schema_version, title, goal, assumptions, entities, state, behaviors, success_criteria, and unsupported (schema_version must be 2; use [] for optional arrays). Use stable entity_id, state_id, behavior_id and criterion_id values. Do not substitute UI-shaped fields such as name, entity_type, target, states, type, allowed_values, or entity_type_hint for the schema fields. Each assumptions item MUST be exactly {"id": string, "text": string, "confidence": number from 0 to 1, "requires_confirmation": boolean}; use numbers such as 0.95, never labels such as high/medium/low, and never omit requires_confirmation. Every success criterion must set verification to exactly one of executable, structural, manual or unsupported. Only executable criteria may have a behavior acceptance test; every executable criterion must have one. Its actions, triggers, conditions and operands must use only the schema's controlled vocabulary listed in TASKSPEC ENUMERATION CONTRACT. Put unsupported requests in unsupported, never in free-form action arguments.
```

---

## 六、各阶段分段输出契约全量规范（Segment Contracts）

### 6.1 `requirements` 阶段契约
- **系统提示**：`"只输出一个 JSON object，内容是 requirements 对象。"`
- **字段规范**：
  必须包含 `ready`（bool）、`title`（string）、`goal`（string）、`behavior_steps`（string[]）、`triggers`（string[]）、`controls`（string[]）、`success_criteria`（string[]）、`missing_questions`（string[]）。数组字段缺失时必须传 `[]`，严禁传 `null` 或省略属性。

---

### 6.2 `task_spec` 阶段契约（含枚举值强约束）

完整契约常量 `TaskSpecEnumerationContract`：

```text
TASKSPEC ENUMERATION CONTRACT (use exactly these values; do not invent synonyms)
- TaskEntity.role: "stage" | "sprite". Each item requires entity_id, role, description; existing_target_ref is optional.
- TaskTargetRef exact forms: {"kind":"current_target"}; {"kind":"stage"}; {"kind":"existing_sprite","name":string}. Never use a string in existing_target_ref.
- TaskState requires state_id, kind, scope. kind: "variable" | "list" | "broadcast"; scope: "global" | "local". A local item also requires owner_entity_id. A global item must omit owner_entity_id. A broadcast must be global and omit owner_entity_id and initial_value. A list initial_value is an array; a variable initial_value is a scalar.
- OperandSpec exact forms: literal={"kind":"literal","value":number|string|boolean}; state={"kind":"state","state_id":string}; attribute={"kind":"attribute","entity_id":string,"attribute":"x"|"y"|"direction"|"size"|"visible"}; timer={"kind":"timer"}; random={"kind":"random","min":number,"max":number}. Never replace an OperandSpec with a bare number/string/boolean.
- TriggerSpec.kind: "green_flag" | "key_pressed" | "sprite_clicked" | "broadcast_received" | "backdrop_switched" | "clone_started" | "timer_reached". Exact forms: {"kind":"green_flag"}; {"kind":"key_pressed","key":string}; {"kind":"sprite_clicked"}; {"kind":"broadcast_received","state_id":string}; {"kind":"backdrop_switched","asset_purpose":string}; {"kind":"clone_started"}; {"kind":"timer_reached","seconds":number}.
- ConditionSpec exact forms: compare requires kind,left,operator,right where operator is "eq"|"gt"|"gte"|"lt"|"lte"; touching requires kind,entity_id; touching_edge is {"kind":"touching_edge"}; key_down requires kind,key; logical requires kind,operator ("and"|"or"),operands (2..12 ConditionSpec items); not requires kind,operand (one ConditionSpec). Conditions are only entries in a behavior's conditions array or a loop's until field. Never put a condition object in actions, never use a condition kind as ActionSpec.kind, and never invent condition/body fields; loop children go only in loop.actions.
- ActionSpec.kind: "move_steps" | "set_axis" | "change_axis" | "go_to_entity" | "turn" | "point_towards" | "set_state" | "change_state" | "broadcast" | "use_asset" | "set_visibility" | "say" | "ask" | "create_clone" | "delete_this_clone" | "wait" | "loop" | "stop".
- ActionSpec exact required fields by kind: move_steps(steps); set_axis(axis,value); change_axis(axis,delta); go_to_entity(entity_id); turn(degrees); point_towards(entity_id); set_state(state_id,value); change_state(state_id,delta); broadcast(state_id); use_asset(asset_kind,purpose, optional wait); set_visibility(visible); say(text, optional seconds); ask(question); create_clone(entity_id); delete_this_clone(no other fields); wait(seconds); loop(mode,actions, plus count for repeat or until for repeat_until); stop(scope). axis is "x"|"y"; asset_kind is "costume"|"backdrop"|"sound"; loop mode is "forever"|"repeat"|"repeat_until"; stop scope is "this_script"|"all". All steps/value/delta/degrees/seconds/count fields are OperandSpec objects, but say.text, ask.question, use_asset.purpose and other text fields are plain strings; never wrap those strings in an OperandSpec.
- TaskBehavior requires behavior_id, owner_entity_id, trigger, actions (non-empty array); conditions, reads and writes are optional arrays. Nested actions belong only in a loop's actions field.
- SuccessCriterionSpec requires criterion_id, description, related_behavior_ids (non-empty), priority and verification. UnsupportedRequirement requires requirement and reason_code.
- SuccessCriterionSpec.priority: "must" | "should"; verification: "executable" | "structural" | "manual" | "unsupported".
- ProgramIR.schema_version: 1 | 2. Program input kind: "number" | "text" | "angle" | "color" | "boolean" | "any". Block shape: "hat" | "stack" | "cap" | "reporter" | "boolean".
- Program input reference kind: "block" | "resource_ref" | "existing_asset_ref" | "state_ref". Resource asset type: "sprite" | "costume" | "backdrop" | "sound". State reference kind: "variable" | "list" | "broadcast".
- Manifest.required_extensions: "pen" | "music" | "text2speech". Manifest scope: "project". Manifest program target mode: "append" | "modify" | "rewrite" (only current_target may use modify/rewrite). Resource source kind: "scratch_library" | "image_generation"; image sheet_layout: "1x1".
- Resource operation op: "add_builtin_sprite" | "add_sprite" | "delete_sprite" | "add_costume" | "add_backdrop" | "add_sound" | "select_backdrop" | "delete_sound". Scratch library source asset_type: "sprite" | "costume" | "backdrop" | "sound". Audio source kind: "existing_sound"; audio generation kind: "audio_generation", audio_kind: "sound_effect" | "speech" | "music".
- Acceptance trigger kind: "green_flag" | "key" | "sprite_click" | "broadcast"; arrange kind: "set_position" | "set_variable" | "set_list" | "set_timer"; expect kind: "variable" | "position_range" | "costume" | "backdrop" | "visibility" | "list" | "clone_count" | "say_text" | "question_asked" | "broadcast_emitted" | "sound_invoked" | "scripts_stopped". Expect comparator: "eq" | "gt" | "gte" | "lt" | "lte" | "equals" | "contains" | "length".
Do not emit description-only behaviors or fields not declared by the selected object kind.
```

---

### 6.3 `blueprint` 阶段契约（含条件字段与DAG防死锁约束）

完整契约常量 `BlueprintEnumerationContract`：

```text
BLUEPRINT CONDITIONAL FIELD CONTRACT (copy exact fields; omit unrelated fields)
- BlueprintAssetNeed requires need_id, kind ("sprite"|"costume"|"backdrop"|"sound"), purpose, non-empty search_terms, non-empty unique fallback_order ("project_asset"|"scratch_library"|"user_asset"|"image_generation"|"audio_generation"). A sprite need for a new_sprite binding MUST have owner_entity_id equal to that entity_id. constraints keys are only transparent, max_bytes, max_duration_ms, animation_frames.
- BlueprintEntityResolution exact forms: existing_target={"kind":"existing_target","target_ref":TaskTargetRef}; new_sprite={"kind":"new_sprite","need_id":string}. New-sprite need_id must reference a sprite need whose owner_entity_id is the bound entity_id.
- BlueprintStatePlan requires ref_id, kind ("variable"|"list"|"broadcast"), scope ("global"|"local"), name; local requires owner_entity_id, global omits owner_entity_id; broadcast is global and omits owner_entity_id and initial_value; list initial_value is an array, variable initial_value is scalar. Every TaskSpec state must appear in state_plan exactly once with ref_id equal to the exact TaskSpec state_id (copy state_id directly; never invent new IDs such as state_score or ref_1; name matches ref_id).
- BlueprintProgramJob requires job_id, owner_entity_id, mode ("append"|"modify"|"rewrite"; in project generation, all jobs for stage, existing_sprite and created_sprite must strictly use "append"), non-empty behavior_ids. Every TaskSpec behavior must occur in exactly one job. behavior_graph consumes_broadcasts and produces_broadcasts, when present, must contain the exact TaskSpec state_id values of broadcast actions/triggers (not broadcast display names, labels, or invented IDs), and must match the TaskSpec behavior's broadcast edges exactly. CRITICAL DEPENDENCY RULE: behavior_graph depends_on MUST form a strictly acyclic DAG across program_jobs! NEVER create mutual cross-job dependencies (e.g. entity_cat behaviors depending on entity_milk behaviors while entity_milk behaviors depend on entity_cat behaviors); cross-sprite interactions MUST decouple via state_plan or broadcasts, and depends_on should primarily express causal ordering within the same entity.
- AcceptanceTrigger exact required forms: green_flag={"kind":"green_flag"}; key={"kind":"key","key":string}; sprite_click={"kind":"sprite_click","entity_id":string}; broadcast={"kind":"broadcast","state_id":string}. AcceptanceArrange: set_position requires entity_id,x,y; set_variable requires state_id,value (scalar); set_list requires state_id,value (array); set_timer requires seconds.
- AcceptanceExpectation required fields: variable=(state_id,comparator,value), comparator "eq"|"gt"|"gte"|"lt"|"lte"; position_range=(entity_id,axis,min,max), axis "x"|"y"; costume=(entity_id,asset_need_id); backdrop=(asset_need_id); visibility=(entity_id,visible); list=(state_id,comparator,value), comparator "equals"|"contains"|"length"; clone_count=(entity_id,comparator,value), comparator "eq"|"gte"|"lte"; say_text=(entity_id,text); question_asked=(text); broadcast_emitted=(state_id,min_count); sound_invoked=(entity_id,asset_need_id,min_count); scripts_stopped has no extra required field. Never use min_count alone for clone_count: it must include comparator and numeric value.
- BlueprintAcceptanceTest requires test_id, source_criterion_id, non-empty related_behavior_ids, trigger, expect (non-empty), timeout_ms; total timeout across tests is at most 4000. Build a coverage checklist from TaskSpec.success_criteria before writing JSON: every criterion with verification exactly "executable" must appear exactly once as source_criterion_id, so the set and count of executable criterion IDs and acceptance_tests.source_criterion_id values must be identical. Never omit an executable criterion (including reset, collision, scoring, boundary, or game-over criteria). Never create an acceptance test for a manual, structural, or unsupported criterion; if there are no executable criteria, output acceptance_tests as [].
```

---

### 6.4 `plan` 阶段契约
- **系统提示**：`"只输出一个 JSON object，内容是 plan 对象，不要重出 requirements。steps 每项最多 120 个字符。"`
- **约束要求**：概括实现步骤与用户可见里程碑，供前端 UI 面板渲染任务列表。

---

### 6.5 `manifest` 阶段契约
- **系统提示**：`"只输出一个 JSON object，内容是 manifest 对象，不要输出 requirements、plan 或 program。"`
- **约束要求**：`ProjectManifest v3` 格式，必须严格继承 Blueprint 的所有 `need_id`、`job_id`、`behavior_ids` 和 `ref_id`。所有 `program_targets` 的 `mode` 必须为 `"append"`。

---

### 6.6 `resource_op` 单项操作精准修复契约
- **系统提示**：`"只输出一个 JSON object，内容是要替换的 resource_op 对象。"`
- **约束要求**：仅输出单个要修正的资源操作对象，替换原 Manifest 中下标为 `resource_op_index` 的错误项，避免重新生成庞大的清单。

---

### 6.7 `program` 阶段契约（Pretty-printed JSON 与逻辑扁平化铁律）

系统提示词中对于积木生成的严格要求：

```text
只输出一个 JSON object，内容是当前 target_index 的完整 ProgramIR 对象，不要输出其他目标。

【AST 语法与排版四大铁律】
1. 必须使用带换行与标准缩进的漂亮格式（Pretty-printed JSON）输出，禁止单行无换行压缩！
   （单行输出会导致深层嵌套括号漏打或被截断，引发解析崩溃）
2. 保持积木结构扁平直观，严禁在 inputs.CONDITION 中进行深层复合逻辑嵌套！
   （严禁连续嵌套多个 operator_and / operator_or；多条件必须拆分为嵌套 control_if 或分步前置判定）
3. 在输出 "substacks" 前，必须仔细核查 inputs 嵌套积木的所有闭合花括号 "}" 是否完整闭合！
4. 针对 project v2 协议，variables、lists、broadcasts 必须全部为 []；
   所有共享与局部状态读写必须使用 fields 中的 state_ref 引用 Manifest ref_id；
   素材下拉必须使用 resource_ref 引用 op_id。
```

---

## 七、闭环自愈修复与诊断提示词全量规则库（Repair Hints）

自愈修复机制由 [`backend-go/internal/prompt/repair_hint.go`](../backend-go/internal/prompt/repair_hint.go) 实现。当模型输出触发校验异常时，编排器截获错误并注入针对性的修复提示词。

### 7.1 路径诊断规则库（Path Hints）

针对出错字段路径（`path`）的匹配规则：

| 匹配路径标记 (`marker`) | 修复指导提示词 (`hint`) |
| :--- | :--- |
| `resource_ops` | 资源操作必须严格匹配资源契约；检查 op、op_id、target、source 以及互斥字段。 |
| `mode` | 项目模式下所有非当前目标（舞台、已有角色、新建角色）的 mode 只能为 "append"，严禁使用 rewrite 或 modify。 |
| `program_targets` | 项目目标必须引用舞台、已有角色、已创建角色或 current_target，并按清单顺序生成。 |
| `shared_state` | 项目全局变量、列表和广播必须与 manifest 完整一致；目标私有变量和列表要标记为 local。 |
| `scripts` | 脚本 id、数量、积木层级和输入类型必须符合 ProgramIR 与原生积木目录。 |
| `blocks` | 检查 opcode、inputs、substacks 和 reporter 类型，不要输出目录之外的积木。 |
| `current_target` | 当前目标才允许使用 modify 或 rewrite；其他项目目标只能使用 append。 |

---

### 7.2 错误详情诊断规则库（Detail Hints）

针对校验器返回的错误原因（`detail`）的正则与子串匹配规则：

| 错误特征标记 (`marker`) | 全量修复指导提示词 (`hint`) |
| :--- | :--- |
| `/title` / `/goal` | 补齐 ScratchTaskSpec 必填顶层字段 title 和 goal，并确认输出的是完整 TaskSpec 对象。不要用 name、entity_type、target 或 states 等旧字段替代 schema 字段。 |
| `/entities` | TaskEntity.role 只能是 stage 或 sprite；每个实体必须包含 entity_id、role、description。existing_target_ref 必须是对象，kind 只能是 current_target、stage 或 existing_sprite，不能写字符串。 |
| `/state` | TaskState 必须包含 state_id、kind、scope；kind 只能是 variable、list、broadcast，scope 只能是 global、local。global 不得有 owner_entity_id，local 必须有 owner_entity_id。 |
| `/behaviors` | TaskBehavior 必须包含 behavior_id、owner_entity_id、trigger、actions；actions 必须是非空数组，不能只输出 description。trigger.kind 只能使用 TASKSPEC ENUMERATION CONTRACT 列出的值。 |
| `/assumptions` | assumptions 的每一项必须同时包含 id、text、confidence、requires_confirmation；confidence 必须是 0 到 1 的数字（例如 0.95），不能写 high/medium/low。 |
| `blueprint state_plan must project every TaskSpec state` | Blueprint 的 state_plan 必须且只能投影 TaskSpec.state 中的每一个状态；每个 state_plan 项的 ref_id 必须严格等于对应 TaskSpec 状态的 state_id（例如 state_id 是“得分”，ref_id 必须严格使用“得分”，严禁自创英文 ID 如 state_score 或 ref_score），name 与 ref_id 保持一致，且保持相同 kind、scope 和 owner。 |
| `blueprint state_plan must preserve TaskSpec state semantics` | Blueprint 的 state_plan 中的 kind、scope 和 owner_entity_id 必须与 TaskSpec 对应 state 完全一致。 |
| `blueprint must bind every TaskSpec entity exactly once` | Blueprint 的 entity_bindings 必须且只能绑定 TaskSpec 中的每个实体一次。 |
| `blueprint behavior graph must project every TaskSpec behavior` | Blueprint 的 behavior_graph 必须且只能覆盖 TaskSpec 中的每个行为。 |
| `blueprint program jobs dependencies cannot contain a cycle` | Blueprint 的程序目标（program_jobs）之间存在循环依赖。不同角色（如小猫和红包）的行为之间严禁相互设置 depends_on（例如角色 A 依赖角色 B，同时角色 B 又依赖角色 A）。修复方法：删除跨角色的 depends_on，不同角色的玩法联动一律通过共享变量（如得分）或广播解耦，depends_on 仅用于同一角色内部不同行为的前后关系。 |
| `blueprint behavior dependencies cannot contain a cycle` | Blueprint 的行为图（behavior_graph）存在循环依赖。请检查每个 behavior 的 depends_on 列表，确保所有依赖构成严格的有向无环图（DAG），不要相互依赖。 |
| `行为依赖存在循环` | Blueprint 的程序目标（program_jobs）之间存在循环依赖。请删除不同角色行为之间相互声明的 depends_on，跨角色联动一律通过全局变量或广播通信。 |
| `blueprint task_spec_hash does not match` | Blueprint 的 task_spec_hash 必须与上下文中的 task_spec_hash 完全一致。 |
| `manifest program target must preserve blueprint job fields` | Manifest 的 program_targets 必须继承 Blueprint 的 job_id、owner_entity_id、behavior_ids 和 mode。注意：项目模式下所有角色与舞台的 mode 必须全部为 "append"，严禁使用 rewrite 或 modify。 |
| `stage, existing_sprite and created_sprite programs must use append` | 项目模式下所有非当前目标（舞台、已有角色、新建角色）的 mode 只能为 "append"，严禁使用 rewrite 或 modify。 |
| `trailing content` | 删除 JSON 对象后的所有额外文本。 |
| `invalid JSON` | 只输出一个合法 JSON 对象，不要使用 Markdown 代码围栏。 |
| `expecting ',' delimiter` | 输出的 JSON 存在语法错误：花括号 '}' 闭合不足或缺失逗号。通常是因为积木 inputs（如 CONDITION 嵌套的复合判断）或 substacks 层级过深，在无换行单行输出时漏打了闭合花括号 '}'。修复方法：1. 必须使用带换行和标准缩进的漂亮格式（Pretty-printed JSON）输出，禁止输出单行压缩 JSON；2. 严禁在 inputs.CONDITION 中嵌套多层 operator_and/operator_or，多条件必须拆分为嵌套 control_if 或分步判断；3. 仔细核对深层 block 嵌套处每一层 inputs、block 和 substacks 的花括号配对。 |
| `expecting ':' delimiter` | 输出的 JSON 存在语法错误：缺少冒号 ':'。请检查对象键值对格式，确保所有属性名用双引号包裹，并使用带换行缩进的标准 JSON 格式输出。 |
| `expecting value` | 输出的 JSON 存在语法错误：缺少有效值或包含多余的尾随逗号。请检查数组或对象末尾是否有多余逗号，确保每个字段值完整。 |
| `unterminated string` | 输出的 JSON 存在语法错误：字符串未正常闭合（双引号缺失或内容中包含未转义的换行符/双引号）。 |
| `extra data` | 输出的 JSON 存在多余数据：只能输出一个合法的顶层 JSON 对象，不要在对象之后附带额外文本或重复生成第二个对象。 |
| `jsondecodeerror` | 输出的 JSON 语法解析失败。请使用带标准换行与缩进的格式化 JSON 输出，不要输出单行压缩文本，并仔细核对每层积木的花括号闭合完整性。 |
| `required input is missing` | 补齐该 opcode 契约声明的所有必需输入。 |
| `expected boolean` | 将布尔输入改为嵌套 boolean reporter，而不是普通字符串。 |
| `unknown opcode` | 改用 BLOCK_CATALOG 中存在的原生 opcode。 |

---

### 7.3 自愈修复提示词组装机制

在 [`builder.go:321-345`](../backend-go/internal/prompt/builder.go#L321-L345) 中，上一轮失败信息会被动态拼装到当前轮次 Prompt 的末尾：

```text
SEGMENT OUTPUT CONTRACT
[阶段特定指令详情...]

必要的已通过上下文（只用于生成本段）：
{ ...上游已校验通过的数据... }

上一轮输出未通过校验：[错误信息 detail]。
修复指导：[根据上述规则表匹配到的 RepairHint]
请严格对照契约修正，输出且只输出修正后的单一 JSON object，禁止 Markdown、解释说明与代码围栏。
```

---

## 八、多模态与辅助功能提示词全量规范

源自 [`backend-go/internal/prompt/static.go`](../backend-go/internal/prompt/static.go)：

### 8.1 需求问答对话助手提示词（ChatMessages）
用于侧边栏与学生交互答疑：

```text
你是 Scratch 项目需求助手。只回答用户问题，不生成 ProgramIR，不修改项目。
回答简洁、具体，并指出当前 Scratch 原生能力的限制。

[TARGET_CONTEXT]
{当前角色/舞台的已有资源与程序上下文}
```

---

### 8.2 画面与风格文生图扩写提示词（TextExpansionPrompts）

#### 场景画面扩写（`scene`）：
```text
你是专业的图像生成提示词编辑器。将用户的简短描述扩写为一段可直接用于文生图模型的中文画面描述。
补充主体、环境、构图、镜头、光线、色彩、氛围、材质和关键细节；保持原意，不添加相互矛盾的内容。
输出规则具有最高优先级：只输出扩写后的画面描述正文，控制在 700 个中文字符以内；完成正文后立即停止。
禁止自我介绍、问候、解释、追问、建议、免责声明、分隔线或任何正文之外的内容；
禁止提及 Agnes、Sapiens AI、模型、助手或用户。不要加入标题、引号、Markdown、参数或负面提示词。
```

#### 视觉风格扩写（`style`）：
```text
你是专业的视觉风格设计师。将用户给出的风格词扩写为一段可直接用于文生图模型的中文风格说明。
补充媒介、造型语言、线条、色彩、光影、质感、渲染以及时代或流派特征；保持原意，不描述具体画面主体。
输出规则具有最高优先级：只输出扩写后的风格说明正文，控制在 200 个中文字符以内；完成正文后立即停止。
禁止自我介绍、问候、解释、追问、建议、免责声明、分隔线或任何正文之外的内容；
禁止提及 Agnes、Sapiens AI、模型、助手或用户。不要加入标题、引号、Markdown或参数。
```

---

### 8.3 Scratch 专属图像生成提示词（背景与角色绿幕抠图）

由 `BuildImagePrompt` 动态组装，直接发送给生图模型：

#### (1) 舞台背景图生成 Prompt 结构：
```text
Create one complete image for a Scratch project.
User scene: {userPrompt}. Style: {styleDescription}.
Canvas intent is {width} by {height}.
For a backdrop, fill the scene edge to edge with a clear foreground and background.
Use one coherent composition with no borders, grid lines, labels, text, watermark, or collage.
```

#### (2) 角色精灵图生成 Prompt 结构（绿幕与透明通道支持）：
```text
Create one complete image for a Scratch project.
User scene: {userPrompt}. Style: {styleDescription}.
Canvas intent is {width} by {height}.
For a sprite, isolate every requested character, pose, or object with whole visible silhouettes, generous gaps and outer padding for manual extraction, non-overlapping and non-touching subjects, and consistent scale.
Show each subject completely from head to feet, including all accessories, without cropping.
Omit any scene or background described in the user prompt; only render the requested subjects.
When multiple subjects are requested, arrange them in regular rows and columns while retaining the exact count requested; render one subject if singular.
Do not force a fixed sheet grid or count.

[背景要求 - 绿幕或透明通道]：
- 若指定透明背景：
  Use true alpha transparency with no green, white, or checkerboard background. Leave all space around and between subjects fully transparent, with no scenery, floor, cast shadows, or halos.
- 若指定绿幕背景：
  Use a uniform saturated #00ff00 green background across the entire canvas including holes. Every background pixel must be exactly RGB(0, 255, 0), like a flat digital color fill: no texture, noise, grain, pattern, lighting, gradients, shadows, floor, or scenery. Apply the requested art style only to the subjects, never to the background, and avoid green subject colors.

No borders, grid lines, labels, text, or watermark.
```

---

## 九、核心源文件与代码符号全景索引表

| 层次划分 | 文件路径 | 关键符号 / 函数 / 契约常量 | 职责描述 |
| :--- | :--- | :--- | :--- |
| **前端交互** | [`frontend/src/ai-blocks/AiBlocksPanel.vue`](../frontend/src/ai-blocks/AiBlocksPanel.vue) | `AiBlocksPanel` | 用户界面、思考预算与模型档位调节、追问交互、状态卡片展示 |
| **素材审查** | [`frontend/src/ai-blocks/ResourcePlanReview.vue`](../frontend/src/ai-blocks/ResourcePlanReview.vue) | `ResourcePlanReview` | 资源计划确认弹窗、官方素材推荐、AI 生图/生音方案预览 |
| **编排调度** | [`frontend/src/ai-blocks/generation-runner.ts`](../frontend/src/ai-blocks/generation-runner.ts) | [`UnifiedGenerationRunner`](../frontend/src/ai-blocks/generation-runner.ts#L497) | Prepare 与 Commit 流程总调度、快照捕获、事务锁定与回滚 |
| **拓扑分层** | [`frontend/src/ai-blocks/project-contract.ts`](../frontend/src/ai-blocks/project-contract.ts) | `projectProgramGenerationLayers` | 根据 Blueprint 的 DAG 依赖拓扑分层，计算并行生成批次 |
| **素材准备** | [`frontend/src/ai-blocks/project-resource-preparer.ts`](../frontend/src/ai-blocks/project-resource-preparer.ts) | `prepareProjectResources` | 官方素材检索、AI 生图生音任务调度、本地素材库缓存管理 |
| **素材执行** | [`frontend/src/ai-blocks/project-resource-executor.ts`](../frontend/src/ai-blocks/project-resource-executor.ts) | `createProjectResourceExecutor` | 角色/造型/背景/声音等二进制资源在 Scratch VM 中的实际注入 |
| **积木编译** | [`frontend/src/ai-blocks/program-compiler.ts`](../frontend/src/ai-blocks/program-compiler.ts) | `compileProgramIR` | 将抽象 ProgramIR 树深度编译为 Scratch VM Blocks 积木图 |
| **静态预检** | [`frontend/src/ai-blocks/program-preflight.ts`](../frontend/src/ai-blocks/program-preflight.ts) | `preflightProgram` | 检查 Opcode 适用角色、变量作用域和必填输入 |
| **VM 门面** | [`frontend/src/scratch/scratch-gui-bridge.ts`](../frontend/src/scratch/scratch-gui-bridge.ts) | `ScratchGuiBridge` | 提供事务互斥锁、SB3 二进制导出备份、画布刷新门面 |
| **沙箱验证** | [`frontend/src/ai-blocks/verification-service.ts`](../frontend/src/ai-blocks/verification-service.ts) | `verifyCandidateSb3` | 在隔离无头 Scratch VM 中执行导出 SB3 的加载与运行验收测试 |
| **检查点库** | [`frontend/src/ai-blocks/run-checkpoint-store.ts`](../frontend/src/ai-blocks/run-checkpoint-store.ts) | `createCheckpointStore` | 基于 SQLite/IndexedDB 的生成记录持久化与崩溃恢复管理 |
| **后端 API** | [`backend-go/internal/httpapi/routes_aiblocks.go`](../backend-go/internal/httpapi/routes_aiblocks.go) | `handleAIBlocksGenerateUnified` 等 | SSE 流式路由定义、并发信号量配额管理、超时控制 |
| **后端编排** | [`backend-go/internal/orchestration/orchestrator.go`](../backend-go/internal/orchestration/orchestrator.go) | [`GenerationOrchestrator`](../backend-go/internal/orchestration/orchestrator.go#L681) | 服务端流水线状态机、自愈修复重试循环、单项资源精准替换 |
| **Prompt构建**| [`backend-go/internal/prompt/builder.go`](../backend-go/internal/prompt/builder.go) | `PromptBuilder` | 4层分级上下文拼接、TTL 缓存、各阶段 Prompt 装配 |
| **修复规则库**| [`backend-go/internal/prompt/repair_hint.go`](../backend-go/internal/prompt/repair_hint.go) | `RepairHint` | 正则匹配校验错误，生成高精准度的定向自愈指导提示词 |
| **契约常量** | [`backend-go/internal/prompt/contracts_gen.go`](../backend-go/internal/prompt/contracts_gen.go) | `TaskSpecEnumerationContract` 等 | 全量 TaskSpec 枚举规范、Blueprint 约束、UnifiedOutputContract |
| **传输协议** | [`backend-go/internal/prompt/protocol_gen.go`](../backend-go/internal/prompt/protocol_gen.go) | `UnifiedGenerationProtocol` | Tagged 流与单 JSON Object 统一生成规范 |
| **Opcode库**  | [`backend-go/internal/blockcatalog/catalog.go`](../backend-go/internal/blockcatalog/catalog.go) | `BlockContracts` | Scratch 官方原生 Opcode 全量白名单与形状规格定义 |
| **多模态Prompt**| [`backend-go/internal/prompt/static.go`](../backend-go/internal/prompt/static.go) | `BuildImagePrompt`, `TextExpansionPrompts` | 文生图场景/风格扩写提示词、绿幕抠图精灵图 Prompt 模版 |
