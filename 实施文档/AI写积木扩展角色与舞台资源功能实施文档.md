# AI 写积木扩展角色与舞台资源功能实施文档

**文档状态**：实施基线稿（本文已完成；功能代码仍须按 Phase 0 至 Phase 4 实施和验收）

**依据文档**：`开发文档/AI写积木扩展角色与舞台资源功能计划.md`

**适用仓库**：`D:/ywj/AI-Scratch-web/`

**适用版本**：`@scratch/scratch-gui@15.0.1`、`@scratch/scratch-vm@15.0.1`、Vue 3、FastAPI/Pydantic 2

**实施目标**：在不向模型暴露任意 VM API、内部 target ID、图片二进制或外部 URL 的前提下，为 AI 写积木增加单角色/整个项目作用范围、Scratch 内置素材优先、受控生图、角色/造型/背景导入、跨 target 积木写入、带标签本地素材库及项目级回滚与撤销能力。

---

## 1. 文档目的

本文不是需求复述，而是开发时直接使用的工程实施说明。它完成以下工作：

1. 将功能计划映射到当前仓库中的具体文件、类型、函数和测试。
2. 冻结第一阶段的数据契约、限制值、状态机、错误边界和兼容策略。
3. 解决原计划中尚未闭合的实现问题，例如内置素材匹配结果如何回传、`library_asset` 的可信来源、多 target 的执行模式、`select_after=false` 与 VM 自动选中行为、画布处理如何不绕过事务。
4. 给出按依赖顺序可执行的 Phase 0 至 Phase 4 任务清单、退出条件和验收矩阵。

本文中的“必须”属于第一阶段完成条件；“建议”可以在 Phase 0 根据真实 VM 验证调整，但调整后必须同步更新前后端合同和测试。

## 2. 第一阶段边界

### 2.1 纳入范围

- AI 写积木面板显式选择 `single_sprite` 或 `project`。
- 显式选择 `builtin_first`、`existing_only` 或 `allow_generation`。
- 单角色范围只允许当前 sprite 的积木和造型操作。
- 项目范围允许新增角色、为角色添加造型、添加背景、切换已有背景及向多个 target 写入积木。
- Scratch 内置角色、造型、背景使用本地 manifest 和本地资源文件，选择内置素材时绝不调用生图接口。
- 用户明确要求生成，或本地匹配证据确认没有内置候选时，才允许产生待确认的生图项。
- 每个生图项必须逐项显示原因、名称、提示词、样式、尺寸和标签；确认前不得调用 `/api/image/generate`。
- 每张成功生成的原图立即保存到 IndexedDB v2 素材库；VM 提交失败不删除原图。
- 资源与积木使用一个 SB3 backup 作为提交和撤销边界。
- 生成原图可以转入无边画布手动处理；AI 资源执行器本身不抠图、不透明化、不裁切、不修像素。

### 2.2 不纳入范围

- 后端持有浏览器 VM，或后端 ToolCalls 直接执行 `vm.addSprite` 等方法。
- 模型输出 JavaScript、VM 方法名、VM target ID、base64、data URL 或 HTTP URL。
- AI 自动删除或覆盖已有角色、造型、背景。
- 音效、扩展资源、克隆体、自定义扩展积木。
- 2x2 候选图、参考图、多图种子一致性、自动抠图。
- 将任意 ToolCall 作为绕过 `ProjectPlan`、用户确认或前端 bridge 的执行通道。

### 2.3 第一阶段完成定义

功能只有同时满足下列条件才可标记完成：

- 请求范围、模型计划和前端执行三层均执行相同的 scope 规则。
- block-only 旧流程仍可生成、取消、回滚和撤销。
- 内置素材命中时，网络记录中不存在 `/api/image/generate` 请求。
- 生成确认未通过时，VM、素材库和生图服务均不发生写入。
- 新角色、多个造型、已有角色新增造型、新背景和已有背景切换均可导出 SB3 并重新加载。
- 资源或积木任一步失败时，Scratch 项目恢复到提交前；已经保存的生成原图继续存在。
- 撤销前若 project revision 已变化，系统拒绝覆盖用户后续编辑。
- 所有后端、前端单元测试、构建和关键 E2E 场景通过。

## 3. 当前代码基线

| 模块 | 当前实现 | 本次缺口 |
| --- | --- | --- |
| `frontend/src/ai-blocks/program-ir.ts` | `ProgramIR` v1，只描述单 target 积木 | 保持语义不变，另建 ProjectPlan |
| `frontend/src/ai-blocks/target-context.ts` | 读取 `editingTarget`、脚本引用和 revision | 缺项目级快照、scope 和 topology 断言 |
| `frontend/src/ai-blocks/ai-blocks-api.ts` | 只识别 `program_ready`，返回 `ProgramIR` | 缺新请求字段、资源证据和 `project_ready` |
| `frontend/src/ai-blocks/generation-runner.ts` | 单 target 编译、注入、backup、局部回滚、撤销 | 高层函数不能被项目事务嵌套，需要拆出低层 apply |
| `frontend/src/ai-blocks/AiBlocksPanel.vue` | 对话、需求整理、计划、注入、取消、撤销 | 缺 scope/policy、资源预览、选择和确认状态 |
| `frontend/src/scratch/asset-adapter.ts` | 新角色可含多个造型；可导入 PNG 背景 | 缺指定 target 造型、内置 SVG/PNG 统一准备和位置参数 |
| `frontend/src/scratch/scratch-gui-bridge.ts` | VM 门面、角色导入、revision、block 锁、SB3 保存恢复 | 缺统一项目锁、定向造型、背景封装和按名切换 |
| `frontend/src/scratch/ScratchHost.vue` | storage 指向本地 library-assets | 已具备本地资源基础，需由 library adapter 使用 |
| `frontend/src/ai/asset-library-store.ts` | IndexedDB v1，只有类别、提示词、样式和图片 | 缺 tags、origin、groupId、派生关系、迁移和编辑 |
| `frontend/src/ai/AssetLibraryDialog.vue` | 列表、删除、清空、导入角色/背景/画布 | 缺类别/标签筛选、标签编辑和资源组视图 |
| `backend/app/schemas/ai_blocks.py` | strict ProgramIR、mode 校验和 block contract 校验 | 缺 scope、policy、ProjectPlan 和引用校验 |
| `backend/app/routes/ai_blocks.py` | `Plan -> ProgramIR`，SSE 返回 `program_ready` | 缺 project 分支、`project_ready` 和项目错误码 |
| `backend/app/prompts_ai_blocks.py` | prompt 只围绕积木 | 缺资源意图、素材策略和 ProjectPlan repair |
| `backend/app/providers/agnes_text_provider.py` | 只流式输出 reasoning/content | 协议无需改；完整 payload/delta 日志必须收敛 |

Scratch GUI 15.0.1 已导出 `@scratch/scratch-gui/sprites`、`costumes` 和 `backdrops`，仓库的 `frontend/scripts/prepare-scratch-assets.mjs` 也已经把 manifest 和实际资源准备到：

- `/assets/scratch/libraries/sprites.json`
- `/assets/scratch/libraries/costumes.json`
- `/assets/scratch/libraries/backdrops.json`
- `/assets/scratch/library-assets/<md5ext>`。

因此内置素材检索、预览、预加载和导入均可在本地完成，不需要新增远程素材服务。

### 3.1 实施前基线问题

只读核对时，`backend/tests/test_ai_blocks.py` 的实际基线为 27 个通过、1 个失败。失败用例是 `test_requirements_accumulates_chunks_and_accepts_one_code_fence`，原因是测试固定检查 prompt 中的字面句子，而当前 prompt 使用了同义文案。该失败与 ProjectPlan 无关，但 Phase 0 必须先修正基线，不能让新功能在非全绿基线上开发。

## 4. 已冻结的工程决策

### D-01：执行边界保持在浏览器前端

后端只产出严格 JSON。所有 Scratch storage、renderer、runtime 和 VM 操作集中在 `ScratchGuiBridge` 与 adapter 中。provider 将来即使支持 ToolCalls，也必须先归一化为同一个 ProjectPlan，再经过相同校验和确认。

### D-02：保留 ProgramIR，新建 ProjectPlan v1

资源字段不得加入现有 `ProgramIR`。ProjectPlan 只负责组合资源操作和带逻辑 target 引用的 ProgramIR。纯资源计划允许 `block_programs=[]`。

### D-03：返回合同由请求显式指定

后端不能根据模型输出猜测返回类型。新客户端在 GenerateRequest 中发送：

- 单角色且没有资源意图：`output_contract='program_ir'`；
- 单角色添加造型、任何项目范围请求：`output_contract='project_plan'`。

旧请求不带该字段时默认 `program_ir`，继续返回 `program_ready`。一次请求只能返回一个 ready 事件，禁止同时发送 `program_ready` 和 `project_ready`。

### D-04：使用同源 manifest 的非写入选择器，不补丁 Scratch GUI bundle

Scratch GUI 包没有导出“选择后不导入”的原生 modal API，而原生选择回调会直接修改 VM，违背 Prepare/Commit。第一阶段新增 `ScratchLibraryPickerDialog.vue`，使用 Scratch 官方同一份 manifest、资源和标签，选择结果只返回 manifest item，不修改 VM。不得修改 minified Scratch bundle 或依赖私有 Redux action。

如果产品验收坚持必须显示 Scratch 原生 modal，Phase 0 必须先验证能否拦截选择并延迟提交；无法证明不修改 VM 时，继续使用同源非写入选择器。

### D-05：内置匹配证据在需求整理后、正式生成前产生

后端无法读取浏览器 manifest，也不能凭空判断“没有匹配项”。完整闭环为：

1. `/requirements` 返回结构化资源意图和搜索词。
2. 前端本地匹配 manifest。
3. 前端将候选数量、候选名称和标签摘要作为 `asset_match_evidence` 发送给 `/generate`，不发送图片或内部 VM ID。
4. 后端只有在证据为零或用户明确要求生成时，才能产出 `image_generation`。
5. 最终调用生图仍必须再次经过浏览器确认。

`existing_only` 且候选为零时，前端停留在需求 review，不调用 `/generate`；用户可修改关键词、改为允许生成或取消。

### D-06：`library_asset` 不是模型可自由生成的句柄

第一阶段保留 `library_asset` 作为前端 Prepare 阶段的受控来源，但它不属于模型 wire plan 的可输出联合。只有用户在本地素材库显式选中记录后，前端才可把已校验 ID 写入 `PreparedResource`；原始 ProjectPlan 保持不变。后端 v1 对模型返回的 `library_asset` 一律拒绝。未来若允许模型引用，GenerateRequest 必须先提供白名单 handle 元数据，后端只接受白名单内 ID。

### D-07：补充计划中缺失的必要字段

ProjectPlan v1 在原计划基础上补充：

- `ImageGenerationSource.reason`：生成确认必须展示原因。
- `BlockProgramPlan.mode`：多 target 不能隐式复用唯一 current target 的 mode。
- `add_builtin_sprite.name/x/y/size/tags`：保证预览、初始位置和结果报告完整。
- `protocol_version` 与 `output_contract`：保证新旧客户端确定性兼容。

### D-08：项目范围内的破坏性积木修改受限

- `created_sprite` 只能使用 `append`。
- 非当前 existing sprite 第一阶段只允许 `append`。
- 当前 target 可以沿用用户选择的 `append/modify/rewrite`，但必须有完整 current_program 和 scriptRefs。
- stage 或其他角色的 modify/rewrite 留到独立的多 target 审核交互，不由模型自行扩大。

### D-09：VM 自动选中行为由 bridge 显式补偿

`vm.addCostume` 和 `vm.addBackdrop` 都会自动选中新资源。`select_after=false` 时，bridge 必须记录旧 currentCostume，完成添加和验证后恢复旧索引并显式发送 project changed。不能假定 VM 会遵守 plan 字段。

### D-10：内置 SVG 与生成 PNG 分开校验

Scratch 内置 costume/backdrop 同时包含 SVG 和 PNG；当前 `importBackdropCostume` 只接受 PNG。统一 adapter 必须支持：

- 内置 SVG：`dataFormat='svg'`、`bitmapResolution=1`；
- 内置 PNG：`dataFormat='png'`、`bitmapResolution=2`；
- AI 生成结果：只接受 PNG，并按现有 1x1 契约处理。

内置角色第一阶段只导入视觉造型，manifest 自带 sounds 被清空，因为音效操作不在本期范围。

### D-11：画布处理采用返回 prepared asset 模式

当前 `CanvasWorkspace.vue` 的“导入 Scratch”会直接写 VM，不能被 AI 项目事务复用。新增可选 `handoffMode='return-prepared-asset'`：从 AI 打开画布时，画布只导出 `CostumeExport`/backdrop 结果并回传，不调用 VM；项目 runner 再统一提交。手动打开画布时继续保留现有直接导入模式。

### D-12：重试和日志规则明确区分

- 旧 Requirements 和 Plan：保持 3 次总尝试。
- 旧 ProgramIR：保持当前 5 次总尝试，避免无关回归。
- 新 ProjectPlan：3 次总尝试，即首次加最多 2 次 repair。
- provider transport 瞬态失败重试 1 次，与结构 repair 分开统计。
- 日志只记录 request_id、scope、计数、字节数、阶段、耗时和错误码；禁止记录 messages、current_program、完整 provider payload、reasoning/content delta 和模型失败原文。

## 5. 总体架构

```text
AiBlocksPanel
  -> requirements API
  -> local Scratch manifest matcher
  -> generate API (ProgramIR or ProjectPlan)
  -> resource review / selection / generation confirmation
  -> project-resource-runner
       Prepare: validate -> select/preload -> generate -> save asset -> optional canvas
       Commit: lock -> recheck snapshot -> backup -> resources -> programs -> result
  -> ScratchAssetAdapter
  -> ScratchGuiBridge
  -> Scratch VM / Storage / Renderer
```

### 5.1 依赖方向

- UI 可以依赖 API、ProjectPlan 类型和 runner。
- runner 可以依赖 ProjectPlan validator、library adapter、image API、asset store、asset adapter 和 bridge。
- adapter 可以依赖 storage 类型，但不依赖 Vue UI。
- bridge 是唯一允许调用 VM 语义方法和 target `setCostume` 的应用层模块。
- 后端 schema 不依赖 provider 格式；provider 只提供文本/推理流。
- `ProgramIR` 编译器不知道资源来源，也不负责 backup。

### 5.2 两阶段时序

```text
用户确认需求
  -> 本地匹配内置素材
  -> 后端返回 ProjectPlan
  -> 前端再次严格校验
  -> 用户选择内置候选 / 确认生图
  -> 生图并保存原图 / 可选画布处理
  -> 所有资源变为 PreparedResource
  -> 获取项目变更锁
  -> 检查 revision 与 target topology
  -> 保存一次 SB3 backup
  -> 串行导入资源
  -> 解析真实 target ID
  -> 串行注入 ProgramIR
  -> 记录最终 revision 与撤销数据
```

Prepare 阶段除 IndexedDB 素材保存外不得修改 VM。Commit 阶段不得等待模型、素材选择或生图网络请求。

## 6. ProjectPlan v1 数据契约

### 6.1 固定限制

前后端分别定义同值常量，并通过共享 fixture 测试防止漂移：

| 限制 | 值 |
| --- | --- |
| 资源操作数 | 0..8 |
| 新角色造型数 | 1..8 |
| block program 数 | 0..8 |
| 每项 tags 数 | 0..12 |
| 单 tag 长度 | 1..32 |
| search_terms 数 | 1..8 |
| 单 search term 长度 | 1..64 |
| 资源名称长度 | 1..80 |
| 生图 reason 长度 | 1..240 |
| 生图 prompt 长度 | 2..800 |
| custom_style 长度 | 2..240 |
| summary 长度 | 1..1000 |
| sprite x | -240..240 |
| sprite y | -180..180 |
| sprite size | 1..500 |
| 生图尺寸 | 480x360、640x360、360x360、360x480 |
| sheet_layout | 仅 `1x1` |
| op_id | `^[A-Za-z][A-Za-z0-9_-]{0,63}$` |

所有名称、标签和搜索词先 trim、折叠空白并做 Unicode NFKC。去重比较使用 case-fold 后的值，显示时保留首次出现形式。

### 6.2 请求偏好与协议版本

```ts
type AiBlocksScope = 'single_sprite' | 'project';
type AiBlocksAssetPolicy = 'builtin_first' | 'existing_only' | 'allow_generation';

interface AiBlocksRequestPreferences {
  protocol_version: 1 | 2;
  scope: AiBlocksScope;
  asset_policy: AiBlocksAssetPolicy;
}

interface GenerateAiBlocksRequest extends AiBlocksRequestPreferences {
  output_contract: 'program_ir' | 'project_plan';
  requirements: AiBlocksRequirements;
  asset_match_evidence: AssetMatchEvidence[];
}
```

兼容默认值仅服务旧客户端：`protocol_version=1`、`scope='single_sprite'`、`asset_policy='existing_only'`、`output_contract='program_ir'`。新前端必须显式发送全部字段，不依赖默认值。

### 6.3 需求整理结果扩展

```ts
interface ResourceRequirement {
  id: string;
  kind: 'sprite' | 'costume' | 'backdrop' | 'backdrop_selection';
  name: string;
  target_description: string;
  count: number;
  search_terms: string[];
  explicit_generation: boolean;
  preferred_style?: ImageStyle;
  tags: string[];
}

interface AiBlocksRequirements {
  // 保留现有字段
  resource_requirements: ResourceRequirement[];
  requires_project_plan: boolean;
  required_scope?: 'project';
}
```

当 `single_sprite` 请求包含新增角色或背景意图时，返回 `ready=false`、`required_scope='project'` 和一个可操作问题；不得自动修改 scope。

### 6.4 内置匹配证据

```ts
interface AssetMatchEvidence {
  requirement_id: string;
  asset_type: 'sprite' | 'costume' | 'backdrop';
  manifest_version: 'scratch-gui-15.0.1';
  match_count: number;
  candidates: Array<{name: string; tags: string[]}>; // 最多 5 项
}
```

证据只用于约束模型来源决策。最终实际候选仍由前端重新匹配并由用户选择，模型不能把候选名称当成可信 asset ID。

### 6.5 逻辑 target 引用

```ts
type TargetRef =
  | {kind: 'current_target'}
  | {kind: 'stage'}
  | {kind: 'existing_sprite'; name: string}
  | {kind: 'created_sprite'; op_id: string};
```

模型不得输出 VM target ID。`existing_sprite` 必须在请求 project context 中唯一存在；`created_sprite.op_id` 必须引用之前的创建角色操作。

### 6.6 资源来源

```ts
interface ScratchLibrarySource {
  kind: 'scratch_library';
  asset_type: 'sprite' | 'costume' | 'backdrop';
  search_terms: string[];
}

interface ImageGenerationSource {
  kind: 'image_generation';
  reason: string;
  prompt: string;
  style: ImageStyle;
  custom_style?: string;
  width: number;
  height: number;
  sheet_layout: '1x1';
}

interface LibraryAssetSource {
  kind: 'library_asset';
  asset_id: string;
}

type ProjectPlanResourceSource = ScratchLibrarySource | ImageGenerationSource;
type LocalPreparedResourceSource = ProjectPlanResourceSource | LibraryAssetSource;
```

后端 ProjectPlan v1 只接受 `ProjectPlanResourceSource`。`library_asset` 只在用户本地选择后进入 `LocalPreparedResourceSource`，不回写原始计划，也不交给模型。操作目标决定生图 API 的 `target`：角色/造型为 `sprite`，背景为 `backdrop`。

### 6.7 资源操作

```ts
interface AddBuiltinSpriteOp {
  op: 'add_builtin_sprite';
  op_id: string;
  name: string;
  x: number;
  y: number;
  size: number;
  tags: string[];
  source: ScratchLibrarySource & {asset_type: 'sprite'};
}

interface AddSpriteOp {
  op: 'add_sprite';
  op_id: string;
  name: string;
  x: number;
  y: number;
  size: number;
  tags: string[];
  costumes: Array<{name: string; source: ProjectPlanResourceSource; tags: string[]}>;
}

interface AddCostumeOp {
  op: 'add_costume';
  op_id: string;
  target: TargetRef;
  name: string;
  source: ProjectPlanResourceSource;
  tags: string[];
  select_after: boolean;
}

interface AddBackdropOp {
  op: 'add_backdrop';
  op_id: string;
  name: string;
  source: ProjectPlanResourceSource;
  tags: string[];
  select_after: boolean;
}

interface SelectBackdropOp {
  op: 'select_backdrop';
  op_id: string;
  name: string;
}

type ResourceOp = AddBuiltinSpriteOp | AddSpriteOp | AddCostumeOp | AddBackdropOp | SelectBackdropOp;
```

来源配型规则：

- `add_builtin_sprite` 只能使用 sprite manifest。
- `add_sprite.costumes` 的 `scratch_library` 只能是 costume，不能是完整 sprite。
- `add_costume` 的目标不能是 stage，Scratch 来源只能是 costume。
- `add_backdrop` 的 Scratch 来源只能是 backdrop。
- `select_backdrop` 只解析项目快照中已有的舞台造型，不准备新资源。

### 6.8 BlockProgramPlan 与 ProjectPlan

```ts
interface BlockProgramPlan {
  target: TargetRef;
  mode: 'append' | 'modify' | 'rewrite';
  program: ProgramIR;
}

interface ProjectPlan {
  schema_version: 1;
  scope: AiBlocksScope;
  summary: string;
  resource_ops: ResourceOp[];
  block_programs: BlockProgramPlan[];
}
```

`ProjectPlan.scope` 必须与请求一致。纯资源计划使用空 `block_programs`；多 target block-only 项目请求仍使用 `project_ready`，不能因 `resource_ops=[]` 降级成单 ProgramIR。

### 6.9 前端 prepared 类型

后端 ProjectPlan 只描述意图，不携带 Blob、manifest 内部对象或 VM ID。Prepare 完成后使用仅存在于浏览器内存的类型：

```ts
type PreparedResource =
  | {kind: 'builtin_sprite'; op_id: string; item: ScratchSpriteItem}
  | {kind: 'costume'; op_id: string; costume: CostumeExport; saved_asset_id?: string}
  | {kind: 'backdrop'; op_id: string; costume: CostumeExport; saved_asset_id?: string}
  | {kind: 'backdrop_selection'; op_id: string; name: string};

interface PreparedProjectPlan {
  source: ProjectPlan;
  snapshot: AiProjectSnapshot;
  resources: Map<string, PreparedResource>;
}
```

Prepared 类型不得序列化回模型，也不得写入日志。取消或完成后释放 Blob URL、ImageBitmap 和不再需要的 ArrayBuffer。

### 6.10 执行结果

```ts
interface ProjectApplyResult {
  addedTargets: Array<{op_id: string; target_id: string; name: string}>;
  addedCostumes: Array<{op_id: string; target_id: string; asset_id: string; name: string}>;
  addedBackdrops: Array<{op_id: string; asset_id: string; name: string}>;
  selectedBackdrop?: {op_id: string; asset_id: string; name: string};
  savedAssets: Array<{op_id: string; asset_id: string; target: ImageTarget; tags: string[]; groupId?: string}>;
  appliedPrograms: Array<{target_id: string; added_blocks: number; added_scripts: number; removed_scripts: number}>;
  original_target_id: string;
  revision_at_complete: number;
  backup: Uint8Array;
}
```

`target_id` 和 SB3 backup 只保存在本地结果中，不回传给文本模型。

### 6.11 SSE 事件

| 事件 | 适用 | 数据 |
| --- | --- | --- |
| `phase` | 两类 | `{request_id, phase, message}` |
| `reasoning_delta` | 两类 | `{request_id, phase, text}` |
| `plan_ready` | 两类 | `{request_id, steps}` |
| `program_ready` | 旧 block-only | `{request_id, program}` |
| `project_ready` | ProjectPlan | `{request_id, project}` |
| `message_complete` | 两类 | `{request_id}` |
| `message_error` | 两类 | `{request_id, code, message}` |

前端 `streamAiBlocksGeneration` 返回判别联合：

```ts
type AiGenerationOutput =
  | {kind: 'program'; program: ProgramIR}
  | {kind: 'project'; project: ProjectPlan};
```

流结束前未收到与 `output_contract` 对应的 ready 事件时，分别抛出 `PROGRAM_MISSING` 或 `PROJECT_PLAN_MISSING`。未知事件可以忽略以保证前向兼容，但两个 ready 事件同时出现必须报 `STREAM_PROTOCOL_ERROR`。

## 7. 后端实施

### 7.1 `backend/app/schemas/ai_blocks.py`

实施步骤：

1. 增加协议版本、scope、asset policy、output contract、资源需求和匹配证据模型。
2. 使用 `Literal` + `Field(discriminator=...)` 定义 TargetRef、ResourceSource 和 ResourceOp。
3. 所有新模型继续继承 `StrictModel`，保持 `extra='forbid'` 和 `strict=True`。
4. 将生图允许尺寸、prompt 上限和 style 集合提取为可复用常量，避免 ProjectPlan 与图片接口漂移。
5. ProjectPlan 自身 validator 检查 op_id 唯一、前向 created reference、来源配型、名称/tag/search term 归一化和全局数量。
6. 新增 `validate_project_plan_for_request(plan, request)`，检查请求 scope、policy、project context、匹配证据及每个嵌套 ProgramIR。
7. 对每个 block program 调用现有 `validate_program_for_mode` 和 `validate_program_contracts`。target kind 从逻辑引用与请求上下文解析，不由模型声明。
8. 前后端共享合法与非法 JSON fixture；Pydantic 生成的 schema 可用于文档，但不能替代业务 validator。

校验顺序固定为：结构与限制 -> scope 相等 -> op_id -> 来源配型 -> created 引用 -> target 存在性 -> scope 权限 -> asset policy -> block mode -> ProgramIR contract。错误应包含稳定 path，供 repair 使用。

### 7.2 scope 校验

`single_sprite` 请求必须满足：

- 当前 target 不是 stage。
- resource_ops 只能是 `add_costume`，且 target 只能为 `current_target`。
- block_programs 最多一个，target 只能为 `current_target`。
- 禁止 `add_builtin_sprite`、`add_sprite`、`add_backdrop`、`select_backdrop`、stage、existing_sprite 和 created_sprite 引用。

`project` 请求仍不能任意访问：existing sprite 必须存在且名称唯一；stage 引用只用于 stage ProgramIR；created 引用只能向后续操作和 program 提供。

### 7.3 asset policy 校验

- `existing_only`：拒绝全部 `image_generation`。
- `builtin_first`：除非用户已明确要求生成，否则有候选证据时必须先产出 `scratch_library`；零候选时才可产出 `image_generation`。明确生成时可产出 `image_generation`，但仍只表示待确认项。
- `allow_generation`：明确生成时可直接产出 `image_generation`；否则仍优先使用已有候选。
- 任意 policy 下，`image_generation` 只表示“待用户确认”，不是调用授权。

### 7.4 `backend/app/prompts_ai_blocks.py`

修改四组 prompt：

- chat：可以讨论角色、造型和背景，但明确 scope 是硬边界。
- requirements：输出 `resource_requirements`、`requires_project_plan`、`required_scope`，不直接生成资源 ID。
- plan：步骤中先列资源依赖，再列 target 绑定和积木行为。
- project：输出完整 ProjectPlan v1，优先使用 match evidence 中的 Scratch 候选。

新增 `build_project_plan_messages` 和 `build_project_plan_repair_messages`。Repair 上下文只保留最近一次模型输出的截断版本和结构化错误摘要；危险的 URL/base64 字段只记录 path 和类型，不原样回灌。

Prompt 必须明确：

- 添加背景不等于切换已有背景。
- `allow_generation` 不等于用户已确认。
- 不得输出 target ID、asset ID、URL、图片数据、JavaScript 或 VM API。
- `created_sprite` 必须引用前面创建操作。
- 单角色越权请求返回澄清，不得扩大为 project。

### 7.5 `backend/app/routes/ai_blocks.py`

Generate 路由保留现有 `Plan` 模型调用，第二段按 `output_contract` 分流：

```text
program_ir   -> build_program_messages -> ProgramIR -> program_ready
project_plan -> build_project_plan_messages -> ProjectPlan -> project_ready
```

ProjectPlan 连续 3 次总尝试失败后发送 `message_error`，code 为 `INVALID_PROJECT_PLAN_RESPONSE`。流开始后不改变 HTTP 状态；请求模型错误仍由 FastAPI 返回 422，provider 未配置仍返回 503。

新增后端错误码：

| 错误码 | 含义 | 前端动作 |
| --- | --- | --- |
| `INVALID_PROJECT_PLAN_RESPONSE` | 模型多次无法生成合法计划 | 可重试模型 |
| `PROJECT_SCOPE_MISMATCH` | 返回 scope 与请求不同 | 停止并重新生成 |
| `PROJECT_SCOPE_VIOLATION` | 操作越权 | 提示切换范围 |
| `ASSET_POLICY_VIOLATION` | 来源违反用户策略 | 返回 review 修改策略 |
| `INVALID_TARGET_REFERENCE` | target 引用不存在或歧义 | 要求用户选择/重生成 |
| `INVALID_RESOURCE_OPERATION` | op/source 配型错误 | 模型 repair |
| `PROJECT_PLAN_TOO_LARGE` | 超过合同上限 | 缩小需求 |

业务越权在 requirements 阶段应尽早变成 `ready=false`，不要伪装成模型 JSON 错误。

### 7.6 provider 与日志

第一阶段不增加 ToolCalls，也不修改 reasoning/content 流协议。必须修改 `routes/ai_blocks.py` 与 `agnes_text_provider.py` 的日志：

- 删除完整 provider payload 和逐 delta 文本日志。
- 删除 `received_content`、prompt、messages、current_program 原文。
- 记录 provider、request_id、scope、output_contract、字符数、delta 数、重试次数和耗时。
- Pydantic 错误使用 `include_input=False`，响应不泄漏模型原文。

项目上下文受现有 `AI_BLOCKS_MAX_REQUEST_BYTES=262144` 约束。前端目标序列化预算建议不超过 180 KiB，为消息、requirements 和 HTTP 包装保留余量。

### 7.7 协议能力协商

扩展现有 `GET /health` 响应，增加：

```json
{
  "capabilities": {
    "ai_blocks_protocol_versions": [1, 2],
    "project_plan_versions": [1],
    "ai_project_assets": true
  }
}
```

保留现有 `status`、`provider_configured` 和 `agnes_configured` 字段。前端启动后缓存 capability；只有构建开关开启且后端声明 protocol 2/project plan 1 时显示项目资源入口。健康检查失败或字段缺失时按旧后端处理，继续允许 block-only，不发送 v2 字段。

## 8. 前端合同、快照与 API

### 8.1 新增 `frontend/src/ai-blocks/project-plan.ts`

该文件包含 ProjectPlan 类型、限制常量、错误类型和严格运行时 validator。不要只使用 TypeScript `as ProjectPlan`。validator 必须：

- 检查对象精确 key，拒绝 extra 字段。
- 检查所有判别联合和数值范围。
- 检查字符串归一化后的长度、空值和重复项。
- 检查 op_id 顺序和 TargetRef 依赖。
- 检查 scope、policy 和来源配型。
- 对每个 ProgramIR 调用现有 `validateProgramIR`/compiler 合同。

建议错误结构：

```ts
interface ProjectPlanIssue {path: string; code: string; message: string}
class ProjectPlanValidationError extends Error {issues: ProjectPlanIssue[]}
```

### 8.2 扩展 `target-context.ts`

保留 `AiTargetSnapshot` 供旧 block-only 使用，新增：

```ts
interface AiProjectSnapshot {
  scope: AiBlocksScope;
  revision: number;
  currentTargetId: string;
  targetIds: string[];
  topologySignature: string;
  targetsById: Map<string, AiTargetSnapshot>;
  wireContext: AiBlocksProjectContext;
}
```

本地快照保留真实 ID 和每个 target 的 scriptRefs；发送给后端的 wire context 只含逻辑名称、是否舞台、造型名称、当前造型、程序摘要和 `current_target` 逻辑标记，不发送真实 target ID。

`readAiProjectSnapshot(bridge, scope, mode)` 规则：

- single sprite：只读取当前 sprite 和必要的 stage 全局变量，不发送其他角色程序。
- project：读取 stage 和所有 original sprite，按 stage、current target、名称排序生成稳定上下文。
- target topology signature 使用 `{id,name,isStage}` 的稳定序列，仅保存在浏览器。
- 超出 180 KiB 时对非当前 target 降级为脚本摘要并标记 `program_truncated=true`；被截断 target 只允许 append。
- 当前 target 是 stage 且选择 single sprite 时立即拒绝。

新增 `assertProjectSnapshotIsCurrent` 同时比较 revision、current target 和 topology signature。Prepare 结束、Commit 获取锁后各检查一次。

### 8.3 扩展 `ai-blocks-api.ts`

- BaseRequest 增加协议版本、scope、asset policy 和 project context。
- Requirements 类型增加资源意图。
- GenerateRequest 增加 output contract 和 match evidence。
- SSE parser 识别 `project_ready`，并检测 ready 事件冲突。
- 返回 `AiGenerationOutput`，而不是强制 `Promise<ProgramIR>`。
- 保留所有现有错误和 program 测试。

## 9. Scratch 内置素材实现

### 9.1 新增 `frontend/src/ai/scratch-library.ts`

职责：

1. 懒加载 `/assets/scratch/libraries/*.json` 并缓存 Promise。
2. 对 manifest item 做运行时结构校验，不信任静态 JSON。
3. 提供 sprite/costume/backdrop 统一候选类型和预览 URL。
4. 提供本地搜索、候选排序和 storage 预加载。
5. 不修改 VM，不依赖 Vue。

建议 API：

```ts
loadScratchLibrary(type): Promise<ScratchLibraryItem[]>
matchScratchLibrary(type, searchTerms, limit = 12): Promise<ScratchLibraryCandidate[]>
preloadScratchLibraryItem(bridge, item, signal): Promise<void>
toPreparedBuiltinResource(item): PreparedResource
```

### 9.2 搜索规则

Manifest 名称和标签主要为英文，中文请求不能只做 substring。第一阶段使用确定性规则：

- 统一 NFKC、lowercase、空白和标点。
- 后端 requirements 要求输出中文词和对应英文词。
- 新增小型 `scratch-library-aliases.ts`，维护常见中文类别到官方英文 tag 的映射。
- 名称完全匹配 100 分，名称 token/prefix 70 分，tag 完全匹配 50 分，tag prefix 30 分，substring 20 分。
- 多个 search term 取累计分，并对命中不同词加覆盖奖励。
- 分数相同保持 manifest 原始顺序；低于最小阈值不算候选。
- 最多展示 12 个，回传后端证据最多 5 个。

搜索必须有单元测试固定排序，不能使用不稳定的浏览器 localeCompare 默认行为。

### 9.3 选择器

新增 `ScratchLibraryPickerDialog.vue`：

- 展示实际素材缩略图、名称和官方标签。
- 支持搜索和单选，确认后只 emit item。
- 取消不修改 VM、不调用生图。
- 候选为零时根据 policy 显示“修改关键词”“允许生成”或“取消”。
- 键盘、焦点恢复、Escape 和 aria 行为沿用现有 AssetLibraryDialog。

### 9.4 本地资源预加载

Commit 前调用 Scratch storage `load` 或 fetch 本地 md5ext，确保资源存在并被缓存。sprite 只预加载 costumes；第一阶段清空其 sounds。预加载失败属于 Prepare 失败，VM 保持不变。

## 10. Scratch bridge 与 asset adapter

### 10.1 统一项目变更锁

当前 `generatingBlocks` 和 `importing` 会互相拒绝，项目 runner 不能在 block 锁内调用角色导入。将其重构为一个 owner token 锁：

```ts
interface ProjectMutationLease {token: symbol; release(): void}
beginProjectMutation(label: string): ProjectMutationLease
```

所有 commit 方法接收可选 lease。没有 lease 的旧入口自行获取短锁；项目 runner 持有同一 lease 串行调用。`beginBlockGeneration` 保留为兼容包装。release 必须幂等。

### 10.2 bridge 新增语义 API

```ts
commitBuiltinSpriteImport(item, options, lease): Promise<ImportedSpriteResult>
commitSpriteImport(spriteJson, expectations, options, lease): Promise<ImportedSpriteResult>
commitCostumeImport(targetId, prepared, selectAfter, lease): Promise<ImportedCostumeResult>
commitBackdropImport(prepared, selectAfter, lease): Promise<ImportedBackdropResult>
selectTargetCostume(targetId, name, lease): Promise<SelectedCostumeResult>
selectBackdropByName(name, lease): Promise<SelectedCostumeResult>
markFatalProjectMutation(error): void
```

每个方法先验证 target 类型和存在性，再调用 VM，再比较导入前后状态。runner 不得直接访问 `runtime.targets`、`sprite.costumes` 或 `setCostume`。这些内部访问只允许封装在 bridge。

### 10.3 导入后验证

新角色：

- 新增非 stage original target 恰好一个。
- 实际名称、x、y、size、造型数可读取。
- 每个造型的 name、assetId、dataFormat、bitmapResolution、rotation center 一致。
- 返回实际 target ID、实际名称和 asset IDs。

已有角色造型：

- target 存在且不是 stage。
- 造型数量只增加一个。
- 新造型元数据一致；实际名称使用 VM 去重后的名称。
- `select_after=false` 恢复旧 currentCostume。

背景：

- stage 造型数量只增加一个。
- 元数据一致。
- `select_after=false` 恢复旧背景；true 保持 VM 自动选中新背景。

按名切换：

- 名称先做 NFKC/trim 后精确匹配。
- 0 个或多个匹配都报错，不做模糊猜测。
- 选择后显式 `emitProjectChanged`，确保 revision 和撤销保护生效。

### 10.4 `asset-adapter.ts` 重构

保留现有 `CostumeExport` 和 `SpriteExport`，新增统一 prepared costume 构造。生成资源与内置资源走不同输入，但输出相同 expectation：

```ts
interface CostumeImportExpectation {
  name: string; assetId: string; dataFormat: 'png' | 'svg';
  bitmapResolution: 1 | 2; rotationCenterX: number; rotationCenterY: number;
}
```

修改 `SpriteExport` 增加 x/y；`buildSpriteImport` 不再固定 `x=0,y=0`，也不把 size 上限硬截为 100，而是使用 ProjectPlan 的 1..500 合同。

AI PNG 校验至少检查 MIME、PNG signature、Blob 大小、解码尺寸和有限 rotation center。内置 SVG 检查 assetId/md5ext/dataFormat/bitmapResolution 组合，并通过本地 storage 加载，不把 SVG 文本交给模型。

## 11. ProgramIR runner 重构

`applyAiProgram` 当前同时承担编译、backup、锁、注入和回滚。拆分为：

```ts
prepareProgramApplication(options): PreparedProgramApplication
applyPreparedProgram(options): Promise<AppliedProgramResult>
applyAiProgram(options): Promise<AiGenerationResult> // 兼容高层包装
```

规则：

- prepare 做 ProgramIR 校验、脚本删除引用解析、布局和编译，不修改 VM。
- low-level apply 假设调用方已持有 lease 和 backup，只执行变量/列表/广播、脚本删除和块注入。
- 旧 `applyAiProgram` 自己保存 backup、获取 lease、调用 low-level apply，并保留现有局部回滚 + SB3 fallback。
- 项目 runner 不能调用旧高层 `applyAiProgram`，避免双 backup、双锁和错误撤销边界。
- created sprite 的程序在角色创建后解析 target；提交前仍先做 target-kind 层面的 ProgramIR 静态校验。

## 12. 项目资源执行器

### 12.1 新增 `frontend/src/ai-blocks/project-resource-runner.ts`

公开 API 建议为：

```ts
prepareProjectPlan(options): Promise<PreparedProjectPlan>
commitPreparedProject(options): Promise<ProjectApplyResult>
undoProjectApply(bridge, result): Promise<void>
```

UI 通过回调/事件处理候选选择、生成确认和画布交接；runner 不直接创建 Vue modal。

### 12.2 状态机

```text
idle
-> validating
-> matching_builtin
-> awaiting_selection
-> awaiting_generation_confirmation
-> generating
-> saving_asset
-> awaiting_canvas (optional)
-> prepared
-> committing
-> completed | cancelled | failed | rollback_failed
```

每个状态携带 `op_id/current/total/message`。只能按定义迁移；旧 AbortController 负责网络与 Prepare 取消，Commit 在每个原子步骤之间检查 signal。

### 12.3 Prepare 算法

1. 对后端 ProjectPlan 做前端严格校验。
2. 校验请求 scope、asset policy、snapshot 和所有 target 引用。
3. 按 resource_ops 声明顺序处理。
4. scratch_library：重新匹配，展示候选，用户确认后预加载本地资源。
5. library_asset：确认 ID 在本地白名单，读取记录并验证类别和 PNG 响应。
6. image_generation：展示完整确认；用户确认后串行调用 `generateImage`。
7. 校验 1x1 PNG 响应和请求 target；立即调用 `saveGeneratedAsset`，失败时停在可重试状态，不静默提交。
8. 同一新角色的多个造型共享 groupId；每个造型保留动作 tag。
9. 用户选择画布时，以 return-prepared 模式打开；回传结果替换该 op 的内存 Blob，并保存派生记录。
10. Prepare 结束时再次检查 revision；变化则标记 `PROJECT_STALE`，要求重新确认。

生成多个资源第一版严格串行。取消后忽略迟到响应；若响应已经成功且原图已保存，保留素材记录但不继续 VM 提交。

### 12.4 Commit 算法

1. 获取统一 project mutation lease，并让 App 将 ScratchHost 设为 inert。
2. 重新检查 revision、current target 和 topology。
3. `bridge.stop()`。
4. 保存一个 SB3 backup；记录 original target ID。
5. 按声明顺序串行执行资源 op。
6. 创建角色后建立 `createdTargetByOpId`，后续 target 引用只从该 map 解析。
7. 导入造型和背景并收集实际 asset/name 映射。
8. 执行 `select_backdrop`。
9. 解析并串行执行 block programs；每个 target 执行前调用 `setEditingTarget`，但不得让 UI 解锁。
10. 恢复 original target，刷新 workspace 和舞台。
11. 记录最终 revision、结果和 backup；释放 lease。

Commit 不允许打开 modal、调用模型、生图或等待用户输入。

### 12.5 失败与回滚

- backup 前失败：VM 未变，直接返回错误。
- backup 后任一步失败或取消：调用 `loadProject(backup)`，恢复 original target 和代码页。
- 新角色导入内部可先删除半成品，但项目级最终保证仍以 SB3 恢复为准。
- rollback 失败：bridge 进入 fatal mutation 状态，锁定后续 AI 提交，提示重新加载最近保存项目。
- IndexedDB 已保存原图不参与 SB3 回滚。

### 12.6 撤销

撤销只允许 `bridge.projectRevision === result.revision_at_complete`。满足时获取 lease、停止项目、加载 result.backup、恢复 original target。revision 不一致时返回 `UNDO_CONFLICT`，不得弹出会覆盖用户修改的二次确认。

## 13. AI 写积木 UI

### 13.1 `AiBlocksPanel.vue` 状态扩展

ViewState 扩展为：

```text
chat -> summarizing -> review -> planning -> resource_review
-> preparing_resources -> ready_to_commit -> committing -> chat
```

block-only 仍从 planning 进入 injecting，文案和操作保持现状。

### 13.2 顶部控制

- 作用范围下拉：单角色、整个项目。始终显示当前值。
- 素材策略下拉：优先 Scratch 内置、仅已有素材、允许 AI 生成。
- 当前 target 是 stage 时禁用单角色，并显示切换说明。
- 改变 scope/policy 后清除旧 requirements、证据和计划，重新读取 snapshot。
- 会话历史保存 scope/policy，但恢复时必须重新检查 target 和 revision，不能恢复旧执行句柄。

### 13.3 资源计划 review

每项显示：操作、逻辑 target、期望名称、造型数量、候选来源、生成原因、prompt、尺寸、tags、依赖和 block 摘要。

状态动作：

- 内置候选：选择/重新选择。
- 待生成：查看详情并确认/拒绝。
- 生成失败：重试/改用内置/取消。
- 已生成：查看保存结果/转到画布处理。
- 引用歧义或越权：阻塞提交并显示明确修复动作。

只有所有项为 prepared 且 snapshot 仍有效时启用“应用到项目”。不能在计划到达后自动提交。

### 13.4 App 集成

`AiBlocksPanel` 新增事件：

- `assetSaved`：刷新素材计数。
- `openCanvasForResource`：携带 op_id、原始资源和 return-prepared 模式。
- `lockChange`：只在 Commit 锁 Scratch。
- `busyChange`：阻止面板被关闭。

`App.vue` 保存 pending canvas handoff，并将 CanvasWorkspace 的 prepared 输出回传对应 panel/run。手动素材库和图片面板流程不改变。

## 14. 素材库 v2

### 14.1 数据模型

```ts
interface GeneratedAssetV2 {
  schemaVersion: 2;
  id: string;
  createdAt: number;
  name: string;
  prompt: string;
  style: ImageStyle;
  customStyle?: string;
  target: ImageTarget;
  tags: string[];
  origin: 'image-panel' | 'ai-blocks' | 'canvas-derived';
  groupId?: string;
  derivedFromId?: string;
  result: ImageGenerationResponse;
}
```

`target` 仍是角色/背景一级类别。`tags` 只存可编辑语义标签；类别、origin 和 style 作为结构化系统标签在 UI 中派生显示，不重复写入 tags。

### 14.2 IndexedDB 迁移

`openDB(DB_NAME, 2, {upgrade})`：

- oldVersion 0：创建 store 和 indexes。
- oldVersion 1：游标遍历旧记录，补 `schemaVersion=2`、`tags=[]`、`origin='image-panel'`。
- 保留 id、createdAt、name、prompt、style、customStyle、target 和 result。
- 迁移操作可重复执行且结果相同。
- 建议 indexes：createdAt、target、origin、groupId、tags(multiEntry)。

旧前端回滚后会看不到 v2 记录但不会删除数据，因此前端发布回滚说明必须注明这一点。

### 14.3 store API

```ts
saveGeneratedAsset(result, request, options?): Promise<GeneratedAssetV2>
listGeneratedAssets(filter?): Promise<GeneratedAssetV2[]>
updateGeneratedAssetTags(id, tags): Promise<GeneratedAssetV2>
renameGeneratedAsset(id, name): Promise<GeneratedAssetV2>
getGeneratedAsset(id): Promise<GeneratedAssetV2 | null>
```

`saveGeneratedAsset` options 包含 name、tags、origin、groupId 和 derivedFromId。所有 tag 使用同一 normalize 函数。

### 14.4 素材库 UI

`AssetLibraryDialog.vue` 增加：

- 全部/角色/背景一级筛选。
- tag 搜索和多选，多个 tag 使用 AND。
- 名称和 prompt 文本搜索。
- 卡片显示语义 tag、来源、风格和 groupId。
- 添加、删除、重命名 tag 后立即持久化。
- 按 group 查看同一角色多个造型；第一阶段不做整组一键恢复。

Scratch 内置素材不复制到该库，继续由 Scratch manifest 管理。

## 15. 逐操作执行细节

### 15.1 新增内置角色

1. Match 阶段得到 sprite manifest candidates。
2. 用户选择 item；Prepare 预加载其 costumes。
3. Commit 将 manifest 复制为新对象，清空 sounds/blocks/variables，写入计划 name/x/y/size。
4. 调用 `vm.addSprite`，验证唯一新增 target。
5. 用实际 target ID 解析后续 `created_sprite` 程序。

### 15.2 新增生成角色及多个造型

1. 每个 costume 单独 1x1 生图、单独保存素材。
2. 所有造型共享 groupId 和角色主题 tags。
3. 转换为 CostumeExport，使用 `buildSpriteImport` 生成一个 SB3 sprite。
4. 一次 `addSprite` 安装全部造型，验证顺序和元数据。
5. 任一造型准备失败时不进入 Commit；任一导入验证失败时恢复整个 backup。

### 15.3 给已有角色添加造型

1. 解析 TargetRef；stage 立即拒绝。
2. 内置 costume 或生成 PNG 在 Prepare 变为 CostumeExport。
3. Commit 调用 `vm.addCostume(md5ext, object, targetId)`。
4. 验证数量和元数据；按 select_after 保持新造型或恢复旧索引。

### 15.4 添加背景

1. 内置 backdrop 可以是 SVG 或 PNG；生成背景始终 PNG。
2. Commit 调用 `vm.addBackdrop`，VM 会自动选中新背景。
3. 校验 stage 造型数量和元数据。
4. select_after=false 时恢复旧背景。

### 15.5 切换已有背景

1. 只在 snapshot stage costumes 中按规范化名称匹配。
2. 不读取 manifest、不调用生图、不新增背景。
3. Bridge 按索引调用 stage.setCostume，并发出 project changed。

### 15.6 写入积木

资源全部成功后才执行。created target 从实际映射解析；existing target 从 snapshot 解析。每个 ProgramIR 保持现有 compiler、block catalog、变量和脚本保护规则。

## 16. 文件级实施清单

### 16.1 新增文件

| 文件 | 职责 |
| --- | --- |
| `frontend/src/ai-blocks/project-plan.ts` | 类型、限制、前端严格校验 |
| `frontend/src/ai-blocks/project-resource-runner.ts` | Prepare/Commit/rollback/undo |
| `frontend/src/ai/scratch-library.ts` | manifest 加载、匹配、预加载 |
| `frontend/src/ai/scratch-library-aliases.ts` | 中英文确定性别名 |
| `frontend/src/ai-blocks/ResourcePlanReview.vue` | 资源计划 review |
| `frontend/src/ai/ScratchLibraryPickerDialog.vue` | 非写入内置素材选择 |
| `frontend/tests/project-plan.test.ts` | 前端合同 |
| `frontend/tests/project-resource-runner.test.ts` | 项目事务 |
| `frontend/tests/scratch-library.test.ts` | manifest 匹配 |
| `frontend/tests/scratch-gui-bridge.test.ts` | bridge 语义方法 |
| `contracts/ai-blocks/project-plan-v1/` | 双端共享合法/非法 fixture |

### 16.2 修改文件

| 文件 | 主要修改 |
| --- | --- |
| `backend/app/schemas/ai_blocks.py` | 新请求、ProjectPlan、业务校验 |
| `backend/app/prompts_ai_blocks.py` | 资源 requirements/project/repair prompt |
| `backend/app/routes/ai_blocks.py` | 显式分流、project_ready、错误码、日志 |
| `backend/app/providers/agnes_text_provider.py` | 日志脱敏，不改流协议 |
| `backend/app/main.py` | `/health` 增加 AI Blocks 协议 capability |
| `backend/tests/test_ai_blocks.py` | schema、repair、SSE、兼容与安全测试 |
| `frontend/src/ai-blocks/target-context.ts` | 项目快照和 topology |
| `frontend/src/ai-blocks/ai-blocks-api.ts` | 新请求字段和 SSE 联合输出 |
| `frontend/src/ai-blocks/generation-runner.ts` | 拆出低层 program apply |
| `frontend/src/ai-blocks/AiBlocksPanel.vue` | scope/policy、review、准备和提交 UI |
| `frontend/src/scratch/asset-adapter.ts` | SVG/PNG 统一准备、位置和元数据 |
| `frontend/src/scratch/scratch-gui-bridge.ts` | 统一锁、造型/背景/选择语义 API |
| `frontend/src/env.d.ts` | target.setCostume、postSpriteInfo、runtime 类型 |
| `frontend/src/ai/asset-library-store.ts` | IndexedDB v2、tags、origin、group |
| `frontend/src/ai/AssetLibraryDialog.vue` | 类别/tag/filter/edit/group |
| `frontend/src/ai/ImageGenerationPanel.vue` | 使用 v2 save options，旧体验不变 |
| `frontend/src/canvas/CanvasWorkspace.vue` | return-prepared handoff 模式 |
| `frontend/src/app/App.vue` | 画布 handoff、素材刷新和统一锁 |
| `frontend/tests/*.test.ts` | 扩展旧回归测试 |
| `frontend/tests/e2e/ai-blocks.spec.ts` | 新交互和权限场景 |
| `frontend/tests/e2e/import-flows.spec.ts` | SB3 往返与资源导入 |

## 17. 分阶段实施顺序

### Phase 0：基线修复、能力验证和合同冻结

任务：

1. 修复现有后端 prompt 测试基线并跑全套测试。
2. 用固定 PNG、SVG 和真实 manifest item 验证 addSprite/addCostume/addBackdrop/setCostume。
3. 记录 VM 对名称去重、x/y/size、bitmapResolution、rotation center 和 currentCostume 的实际行为。
4. 验证内置资源完全离线可加载，且 sprite 去除 sounds 后仍可导入/往返。
5. 冻结本文件第 6 节合同、上限、请求分流和 repair 次数。
6. 建立双端共享 fixtures。

退出条件：固定资源可以完成五类操作并成功 SB3 导出/重载；所有现有测试全绿；合同评审无未决项。

### Phase 1：前端固定 ProjectPlan 执行

任务：

1. 实现 project-plan validator。
2. 实现项目 snapshot/topology。
3. 重构 bridge 锁与 adapter。
4. 拆分 generation-runner 低层 apply。
5. 实现 project-resource-runner，先只接受测试固定资源和固定 plan。
6. 实现 IndexedDB v2 迁移和 store API。

退出条件：不依赖后端 AI，用固定 plan 完成资源+积木一次提交；每个故障点均恢复 backup。

### Phase 2：后端 ProjectPlan 和 SSE 接入

任务：

1. 扩展 Pydantic schema 和 request validator。
2. 扩展 requirements，接收 match evidence。
3. 增加 project prompt/repair 和 generate 分流。
4. 增加 project_ready；前端 API 返回联合类型。
5. 完成日志脱敏。
6. 保持旧 program_ready 全部回归。

退出条件：后端只可能输出白名单 ProjectPlan；非法字段、越权、错误引用、URL/base64 和超限 payload 全部被拒绝。

### Phase 3：内置选择、生图确认、素材库 UI 和画布交接

任务：

1. 实现 manifest matcher、候选 evidence 和 picker。
2. 扩展 AiBlocksPanel scope/policy 和资源 review。
3. 串行生图、逐项确认、保存、重试和取消。
4. 完成素材库类别/tag/group UI。
5. 完成 CanvasWorkspace return-prepared 模式和派生记录。

退出条件：一次自然语言请求可优先选内置资源；无候选时确认后生成、保存并导入；生图失败时 VM 完全不变。

### Phase 4：事务、撤销、E2E 和发布

任务：

1. 完成所有资源阶段的故障注入。
2. 完成 revision 冲突、取消和 rollback fatal 场景。
3. 完成 SB3 导出、刷新、重载和撤销 E2E。
4. 完成协议兼容、请求体预算、日志检查和浏览器内存检查。
5. 按第 20 节顺序灰度发布。

退出条件：第 18 节全部测试通过，完成定义逐项签字。

### Phase 5：后续能力

资源组一键恢复、多 target modify/rewrite 审核、并行 Prepare、2x2、参考图和多造型一致性均在 v1 稳定后单独设计。自动抠图仍不进入 AI 写积木。

## 18. 测试与验收矩阵

### 18.1 后端单元测试

必须覆盖：

- 所有 TargetRef、ResourceSource 和五类 ResourceOp 合法分支。
- unknown discriminator、extra 字段和 strict 类型拒绝。
- op_id 重复、不存在、前向 created 引用和错误创建类型。
- single sprite 允许当前角色 add_costume；拒绝新增角色、背景和其他 target。
- project target 名称不存在、stage/sprite 类型错配。
- existing_only 拒绝生图；builtin_first 有候选时拒绝直接生图。
- reason、prompt、tag、search term、坐标、size、资源数和程序数边界。
- 允许的四组生图尺寸和 1x1；非法组合拒绝。
- 每个嵌套 ProgramIR 的 mode、protected scripts 和 target block contract。
- ProjectPlan 首次失败后 repair 成功、连续三次失败、错误 path。
- SSE 事件顺序、断连、provider 失败、ready 冲突不存在。
- 旧请求结构和 program_ready 完全兼容。
- 262144 字节请求限制和日志不含项目程序原文。

### 18.2 前端单元测试

| 测试文件 | 重点 |
| --- | --- |
| `project-plan.test.ts` | 严格 key、上限、配型、引用、scope/policy |
| `target-context.test.ts` | 单/项目快照、stage 禁用、预算、topology |
| `scratch-library.test.ts` | manifest 校验、中英搜索、稳定排序、零候选 |
| `scratch-gui-bridge.test.ts` | 指定 target addCostume、selectAfter、背景切换、歧义 |
| `asset-adapter.test.ts` | SVG/PNG、多个造型、x/y/size、元数据 |
| `generation-runner.test.ts` | 旧 block-only 无回归、低层 apply |
| `project-resource-runner.test.ts` | Prepare 无 VM 写入、依赖顺序、rollback、undo |
| `ai-blocks-api.test.ts` | 新请求、project_ready、ready 冲突、旧事件 |
| `asset-library-store.test.ts` | v1->v2 幂等迁移、AND filter、tag 编辑、group |

每个 runner 故障注入点至少包括：创建角色后、添加造型后、添加背景后、切换背景后、第一 target program 后和最后刷新前。

### 18.3 E2E 场景

1. 单角色添加内置造型，只影响当前角色。
2. 单角色请求新增角色或背景，UI 要求切换 project，VM 不变。
3. stage 下 single sprite 禁用。
4. 项目范围选择内置角色，确认没有生图请求。
5. 新角色含两个造型，并将脚本写入真实 created target。
6. 零候选 + existing_only 阻塞；改为允许生成后出现确认。
7. 未确认生成时无生图请求；拒绝后 VM/素材库不变。
8. 确认后生成角色/背景，素材库 target/origin/group/tags 正确。
9. 素材库类别与多 tag AND 筛选、编辑后刷新仍保留。
10. 已有角色新增生成造型，select_after true/false 均正确。
11. 添加内置 SVG 背景和生成 PNG 背景，均可渲染。
12. 切换已有背景不新增资产、不调用生图。
13. 混合执行新增角色、背景和多个 target 脚本。
14. 每个 Commit 故障点恢复 SB3；生成原图仍在素材库。
15. 生成后用户编辑，再撤销时返回冲突且不覆盖。
16. 转到画布处理时不自动改像素；return-prepared 后由统一 Commit 导入。
17. 导出 SB3、刷新页面并重新加载，角色/造型/背景/脚本仍存在。
18. 旧 block-only 对话、取消、注入、撤销和历史记录无回归。

### 18.4 验证命令

```powershell
backend\.venv\Scripts\python.exe -m pytest backend\tests
npm --prefix frontend run test:unit
npm --prefix frontend run build
npm --prefix frontend run test:e2e
```

Phase 0 和最终验收均需执行全部命令，不能只运行新增测试。

## 18.5 当前实现增量：统一输出管线

本轮实现按当前“需求整理 -> 计划 -> ready 事件 -> 前端执行”的流程管线重新收敛协议，不再保留默认返回旧版 ProgramIR 的兼容分支：

1. streamAiBlocksGeneration 始终返回判别联合 AiGenerationOutput，结果必须是 {kind:'program', program} 或 {kind:'project', project}。
2. GenerateRequest.output_contract 必须由调用方显式设置为 program_ir 或 project_plan；不能根据模型输出猜测合同。
3. 前端调用点必须先按 kind 收窄，再进入对应执行器。当前积木执行器发送 program_ir 并只允许 program 分支；项目资源执行器接入后发送 project_plan，不得把项目计划强转为单 target 程序。
4. project_ready 和 program_ready 互斥；流中收到两种 ready 事件时抛出 STREAM_PROTOCOL_ERROR。缺少目标 ready 事件分别抛出 PROJECT_PLAN_MISSING 或 PROGRAM_MISSING。
5. 旧单测和调用方不通过兼容返回维持，而是迁移到联合结果断言和显式 output contract。这样后续资源 Prepare/Commit 可以直接复用同一条管线。

本轮已完成：前后端 ProjectPlan 基础模型、严格资源来源和 scope 校验、created target 向后引用校验、library_asset 模型输入拒绝、前端 SSE 联合输出、默认积木调用点迁移，以及后端 project prompt/route 分流和 project_ready 回归测试。本轮另外完成本地 Scratch manifest matcher、中文别名扩展、候选评分、资源标识校验和离线资源预加载，并完成素材库 v2 的 IndexedDB 升级、旧记录迁移、tags/origin/groupId/derivedFrom 元数据保存和编辑 API；本轮新增不写入 VM 的 ScratchLibraryPickerDialog 和 ResourcePlanReview 资源审查组件及响应式样式，并实现 project-resource-runner：统一执行前快照校验、SB3 backup、资源/积木顺序提交、失败恢复和 revision-safe undo；资源具体 VM 操作通过受控 executor 回调注入。本轮另外完成 bridge 的定向角色造型添加、舞台背景添加、按名称切换已有背景，以及 select_after=false 的旧索引恢复和 SVG/PNG 分辨率校验；主面板已接入 scope/policy 显式选择、project_plan 请求和 ResourcePlanReview 展示，新增 prepareProjectResources 完成内置素材预加载、生图逐项确认、原图先入素材库和 PreparedResource 生成，并将逐项确认事件接入 Prepare；本轮新增 createProjectResourceExecutor，将已有角色造型、舞台背景和已有背景切换接入 bridge，并记录资源提交结果；本轮进一步接入新增角色的多造型 Prepare 与 commitSpriteImport，内置角色候选可通过同一事务导入；本轮进一步接入跨 target 项目积木 executor：按逻辑 target 解析 current_target、stage、existing_sprite 和前序 created_sprite，并复用现有 ProgramIR 执行器写入统一事务审计；本轮进一步完成内置候选选择回填、项目积木 executor 和主面板 Prepare -> Commit -> undo 接线；尚未完成的 E2E 场景、历史记录展示和画布 handoff 继续按 Phase 3 至 Phase 4 实施。 当前 E2E 验收发现顶部 AI 面板按钮在展开后从“打开 AI 写积木”改名为“收起 AI 写积木”，旧测试在异步锁定期间继续使用旧定位器；该可访问名称契约需在 Phase 4 单独修复，不通过隐藏按钮或放宽断言规避。 本轮已单独验证 AI 锁定生成、素材库 v2 导入和 Scratch Abby 角色预览三个场景均通过；完整 23 项 E2E 已在提高冷启动预算后以单 worker 全部通过（23/23）。 本轮新增 CanvasWorkspace 的 return-prepared-asset 模式：角色多造型和背景导出通过 prepared 事件返回，不直接写 VM；默认手动导入仍走 direct-import。 本轮补齐 AI 对话历史中的 scope 与 asset_policy 持久化及恢复，旧记录只按显式默认值恢复，不恢复隐式旧输出合同。 本轮将 VITE_AI_PROJECT_ASSETS_ENABLED 接入 AI 面板：显式设为 false 时隐藏 project scope，并将历史记录恢复限制为 single_sprite；默认值保持当前项目资源能力开启。 本轮补充 App 对 CanvasWorkspace prepared 事件的接收和 pending 保存，关闭画布后保留一次性结果；ResourcePlanReview -> Prepare -> 画布处理 -> ResourceOp 回填 -> Commit 用户流程已接入。 本轮进一步完成 Panel preparedResources 状态、acceptCanvasPrepared(opId, payload) 回填 API 及 App ref 路由；画布结果替换指定 PreparedResource 后仍由原 ProjectPlan Commit 提交。 本轮增加生成资源 Prepare 后的提交前暂停、转到画布按钮和提交项目按钮；回填按 costume 与多造型 payload 正确收敛。 本轮修复 Prepare 暂停点的 busy cleanup，资源准备完成后立即释放锁，允许用户继续画布处理或提交项目。

## 19. 安全、性能与可观测性

### 19.1 安全

- 后端 strict schema 和前端 validator 双重校验。
- 资源 source 没有 URL/base64 字段；extra 字段直接拒绝。
- ProjectPlan 中出现 `http://`、`https://`、`data:`、明显 base64、JavaScript 或 VM 方法名时拒绝。
- `library_asset` 的 local asset ID 必须来自本次用户在素材库中显式选择的白名单；本次确认后成功生成的资产 ID 由前端在同一 run 内登记并仅供该 run 使用，二者都不得发送给文本模型。
- 不把 IndexedDB 图片、Blob、target ID 或 SB3 上传给文本模型。
- 不在日志中保存 prompt 对话、项目程序或模型原文。

### 19.2 性能

- manifest 懒加载并缓存；不把 2 万行 sprites JSON 打入 AI panel 首屏 bundle。
- 搜索索引首次加载构建一次，后续复用。
- Prepare 生图第一版串行，Commit 始终串行。
- 项目上下文前端预算 180 KiB；超预算使用摘要并限制为 append。
- 每次 run 完成/取消后 revoke ObjectURL、close ImageBitmap、清空大数组引用。
- 最大 8 个资源操作与 8 个程序，防止单次提交长时间锁定编辑器。

### 19.3 可观测性

建议记录匿名结构指标：request_id、scope、policy、资源操作数、候选命中数、生图确认/拒绝数、Prepare/Commit 耗时、回滚次数、错误码和最终 revision。不得记录用户 prompt、图像、项目代码、素材名称或任何可还原资源的 ID。

## 20. 发布与回滚

发布顺序：

1. 后端先上线显式合同 schema：protocol v2 请求必须声明 output_contract，禁止依赖默认返回类型。
2. 上线前端基础 validator/runner，功能入口先受 feature flag 控制。
3. 开启内部固定 plan 测试。
4. 开启项目范围与内置素材；生图继续单独开关。
5. 全量开启后观察 scope violation、rollback 和 invalid plan 指标。

增加前端 `VITE_AI_PROJECT_ASSETS_ENABLED`，并读取第 7.7 节 `/health.capabilities`。前端发现后端不支持 protocol v2/project plan 1 时隐藏项目资源入口，继续保留 block-only，不能发送后端会 422 的字段。

代码回滚不回退 IndexedDB v2。旧前端会暂时看不到 v2 记录但数据保留；重新升级后恢复可见。SB3 项目数据不依赖远程图片 URL，因此前端回滚不会破坏已经导入的资源。

## 21. 维护规则

- 修改 ProjectPlan、ResourceOp、TargetRef、scope、policy 或限制值时，同步更新 Pydantic、TypeScript validator、prompt、共享 fixtures、双端测试和本文。
- 修改素材库模型时，同步更新 IndexedDB upgrade、旧数据 fixture、筛选和标签编辑测试。
- 升级 Scratch GUI/VM/storage 时，重新执行 Phase 0 固定资源、SVG/PNG、select_after 和 SB3 往返验证。
- 不允许为通过某个模型输出而放宽 extra 字段、任意 URL、任意 ID 或任意操作。
- VM 自动修复后能运行不能替代源计划校验；所有修复必须在显式 schema 或 adapter 中可测试。
- ToolCalls 只能作为 ProjectPlan 的可选输入适配层，不能改变浏览器执行、素材优先和用户确认边界。
- AI 写积木永远不负责自动抠图和像素处理；相关算法只在无边画布维护。

## 22. 最终验收清单

- [ ] Phase 0 合同和所有上限已评审冻结。
- [ ] 现有测试基线全绿。
- [ ] 前后端 ProjectPlan fixture 一致。
- [ ] single sprite/project 权限在请求、计划、执行三层一致。
- [ ] 内置素材命中不触发生图。
- [ ] 每个生图调用都有可追溯的用户确认。
- [ ] 每张生成原图进入素材库 v2。
- [ ] 内置 SVG、内置 PNG、生成 PNG 均能导入和重载。
- [ ] 角色、造型、背景和程序共用一个 backup。
- [ ] 所有故障注入均恢复项目，素材原图保留。
- [ ] revision 冲突下撤销不覆盖用户修改。
- [ ] 画布 handoff 不绕过项目事务。
- [ ] 日志不包含消息、程序、图片、模型 delta 和内部 ID。
- [ ] 后端测试、前端单测、构建和 E2E 全部通过。
