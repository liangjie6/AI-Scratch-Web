# AI 写积木重构文档实施大纲

## 0. 文档目的与阅读方式

本文针对「AI 积木生成」功能的项目模式（`scope=project`）不可用问题，给出根因分析与整体架构重构方案，作为后续分阶段实施的依据。

- 结论分两类，请区别对待：**已验证事实**（附代码位置，可直接复核）与**设计建议**（需实施时决策）。
- 本文不含可直接提交的代码，只定义契约、边界、任务与验收标准。
- 相关既有文档：[AI积木重构-Phase1实施文档.md](docs/AI积木重构-Phase1实施文档.md)、[scratch代码生成提示词.md](docs/scratch代码生成提示词.md)、[scratch项目清单生成提示词.md](docs/scratch项目清单生成提示词.md)。本文与它们冲突处，以本文为准，并在 Phase 0 完成文档收敛。

## 1. 现状链路（事实基线）

一次项目模式生成的完整链路：

```
前端构造 project_context + target_context
  → POST /api/ai-blocks/generate-unified (SSE)
  → PromptBuilder 拼装单条 system prompt（知识库 + 上下文 + 输出契约）
  → Provider 文本流（json_output=False，模型自行产出标签流）
  → UnifiedFrameParser 切分标签帧
  → 每帧 Pydantic 校验；失败则整段重出（修复轮）
  → 前端收集帧 → 需求/计划/清单二次校验 → 资源确认弹窗
  → 资源准备（生图/素材检索）
  → 建立项目事务锁 + 保存单份 SB3 备份
  → 顺序应用资源 → 顺序编译并写入各目标程序
  → 失败则 loadProject(备份) 回滚 → 释放锁
```

关键实现位置：

- 编排与修复轮：[orchestrator.py:75](backend/app/orchestration/orchestrator.py#L75)、[orchestrator.py:93](backend/app/orchestration/orchestrator.py#L93)
- 标签流解析：[streaming_parser.py](backend/app/orchestration/streaming_parser.py)
- 输出契约文本：[prompt_builder.py:14](backend/app/prompt_builder.py#L14)
- 资源操作校验：[ai_blocks.py:559](backend/app/schemas/ai_blocks.py#L559)
- 前端二次校验：[project-plan.ts](frontend/src/ai-blocks/project-plan.ts)
- 资源准备/执行：[project-resource-preparer.ts](frontend/src/ai-blocks/project-resource-preparer.ts)、[project-resource-runner.ts](frontend/src/ai-blocks/project-resource-runner.ts)
- 事务与回滚：[generation-runner.ts](frontend/src/ai-blocks/generation-runner.ts)、[scratch-gui-bridge.ts](frontend/src/scratch/scratch-gui-bridge.ts)

## 2. 故障根因分析

### 2.1 直接根因：`add_backdrop` 的三合一校验

报错来自 [ai_blocks.py:591-593](backend/app/schemas/ai_blocks.py#L591-L593)：

```python
elif self.op == 'add_backdrop':
    if self.target is not None or any(v is not None for v in positional) or self.select_after is None:
        raise ValueError('add_backdrop requires select_after and no target/placement fields')
```

**已验证事实：** 这一条分支同时拦截三种完全不同的错误，却只回抛一句话：

1. 模型输出了 `target`（最可能是 `{"kind":"stage"}`）；
2. 模型输出了 `x`/`y`/`size` 任意一个；
3. 模型**没有**输出 `select_after`。

由此产生三个叠加问题：

- **语义反直觉。** 背景天然属于舞台，模型写 `"target":{"kind":"stage"}` 是最自然的选择，而契约要求必须省略。同一份契约里 `add_costume` 又**必须**带 `target`，两个相邻操作的规则相反。
- **`select_after` 必填但无默认。** 它是布尔可选字段，模型漏写即失败，而这个字段对生成结果几乎没有语义价值，属于纯协议负担。
- **修复提示无法定位。** 修复轮把这句话原文塞回给模型（[orchestrator.py:576](backend/app/orchestration/orchestrator.py#L576)），模型不知道自己错在「多了 target」还是「少了 select_after」，只能猜，于是二次失败概率很高。`max_repair_rounds` 默认仅 1 轮，猜错即整轮失败。

### 2.2 结构性根因

单点修补 `add_backdrop` 不能解决稳定性问题，以下五项是同一类缺陷的不同表现。

**根因 A：一个宽松模型承载六种操作。** `ResourceOp` 把 `target`/`source`/`x`/`y`/`size`/`select_after`/`costumes` 全部定义为可选，再用一个 `model_validator` 事后判别（[ai_blocks.py:559-602](backend/app/schemas/ai_blocks.py#L559-L602)）。后果是：类型层面几乎不约束任何东西，模型看到的是「一堆可选字段」，真正的规则只存在于 prompt 文字和运行时抛错里；Pydantic 也无法给出字段级定位。**判别式联合（discriminated union）才能把「哪个操作允许哪些字段」表达在类型里**——项目里已有成熟先例：`ProjectManifestResourceSource` 就是按 `kind` 判别的（[ai_blocks.py:550](backend/app/schemas/ai_blocks.py#L550)）。

**根因 B：契约存在三份，且已漂移。** 同一套规则被写了三遍——后端 Pydantic、前端手写 validator、prompt 自然语言段落。已发现的实际漂移：

| 项 | 后端 | 前端 | 结论 |
| --- | --- | --- | --- |
| `add_sprite.source` | 可选（`costumes` 非空时可省） | 必填，无条件校验 | 后端合法的清单会被前端拒绝 |
| `delete_sprite` | schema 支持 | 有准备路径 | 文档写「暂不支持」 |
| `manifest.shared_state` | 有默认值 | 可选 | prompt 要求「必须输出」 |

**根因 C：知识库自相矛盾。** `PromptBuilder` 把 [scratch代码生成提示词.md](docs/scratch代码生成提示词.md) 全文注入为知识库（[prompt_builder.py:136-162](backend/app/prompt_builder.py#L136-L162)），而该文档开头要求「输出一个裸 ProgramIR JSON 并定义错误 JSON」，末尾又要求「遵循标签流」。`UNIFIED_OUTPUT_CONTRACT` 只能靠一句 "OVERRIDES EARLIER TEXT" 去覆盖（[prompt_builder.py:16](backend/app/prompt_builder.py#L16)）。**让模型在同一份 prompt 里先读错的、再读对的，本身就是不稳定来源。**

**根因 D：全量重出式修复。** 修复轮把已通过的阶段保留在服务端状态里（[orchestrator.py:548](backend/app/orchestration/orchestrator.py#L548)），但要求模型「从某个标签开始重新输出后续全部内容」（[orchestrator.py:570-575](backend/app/orchestration/orchestrator.py#L570-L575)）。一个 `select_after` 缺失，代价是整份 manifest 加全部 program 重出。既慢又给出新的出错机会。

**根因 E：状态被声明两次。** `manifest.shared_state` 与每个 `program.shared_state` 各自校验，但没有跨阶段一致性校验（[ai_blocks.py:108-155](backend/app/schemas/ai_blocks.py#L108-L155)）。同步责任完全落在模型身上。

### 2.3 应用阶段的稳定性缺陷（与本次报错无关，但同属重构范围）

即便模型输出通过校验，落地阶段仍有确认过的缺陷：

- **异步窗口无版本再校验。** 资源逐个 `await` 期间只检查事务锁 `assertActive()`，不重新校验项目快照（[project-resource-runner.ts:65](frontend/src/ai-blocks/project-resource-runner.ts#L65)）。用户在生图等待期间改动项目，后续资源与程序仍会写入，且失败回滚会用旧 SB3 覆盖用户改动。
- **回滚不覆盖 Scratch storage。** 备份/恢复只针对项目（[generation-runner.ts:476](frontend/src/ai-blocks/generation-runner.ts#L476)、[generation-runner.ts:528](frontend/src/ai-blocks/generation-runner.ts#L528)），而 `createAsset()` 产生的资源对象没有对应清理路径。
- **VM 重绑定掩盖原始错误。** 回滚前先 `assertActive()`，VM 重绑定会让它先抛错，备份恢复根本不会尝试，用户看到的是「无法恢复项目备份」而非真实失败原因。
- **VM 无原生事务。** 已确认 `scratch-vm` 提供的是增量 API（`addSprite`/`addCostume`/`addBackdrop`/`deleteSprite`），每个操作独立异步并各自发事件，**不存在跨资源与脚本的原子事务或 undo journal**。`deleteSprite` 会先导出 sprite3 生成恢复函数，恢复本身也是异步的。因此事务语义必须由应用层自己实现。

## 3. 设计目标与非目标

### 3.1 目标

| 编号 | 目标 | 可度量标准 |
| --- | --- | --- |
| G1 | 项目模式可用 | 固定 20 条真实指令样本集，首轮通过率 ≥ 95% |
| G2 | 校验错误可定位 | 每个失败都带字段路径 + 期望值 + 实际值 |
| G3 | 修复代价局部化 | 单字段错误只重出该操作，不重出 manifest 与 program |
| G4 | 契约单一来源 | 后端 schema、前端 validator、prompt 文本由同一份定义生成 |
| G5 | 落地可回滚 | 任一步失败后，项目与素材存储都回到操作前状态 |
| G6 | 延迟可控 | 生图与素材检索并行；程序编译前移出提交锁 |

### 3.2 非目标

- 不改动积木语义与 `BLOCK_CATALOG` 生成机制（[prompt_builder.py:142](backend/app/prompt_builder.py#L142) 已是机器生成，保持）。
- 不实现自定义扩展积木、不改 Scratch 渲染层。
- 不引入服务端 Scratch 运行时做预执行验证（成本高，留待后续评估）。
- 不改动单目标模式（`single_sprite`）已稳定的行为，只要求它复用新契约。

## 4. 目标架构总览

```
                    ┌──────────────────────────────┐
                    │  resource_contract.py        │  ← 单一契约源（SSOT）
                    │  操作 → 必需/允许/禁止字段    │
                    └──────┬───────────┬───────────┘
          代码生成          │           │          代码生成
        ┌──────────────────┘           └──────────────────┐
        ▼                                                 ▼
  Pydantic 判别式联合                            prompt 操作矩阵（表格）
  + 前端 validator (生成为 .ts)                  注入 UNIFIED_OUTPUT_CONTRACT
        │
        ▼
  归一层 normalize_resource_op()  ← 吸收可安全推断的偏差（补默认值、剥离冗余 target）
        │
        ▼
  分段生成：requirements → plan → manifest → program[i]
        │        每段独立校验，失败只重出该段（甚至只重出该 op）
        ▼
  前端：预校验 → 资源确认 → 并行准备 → 版本化事务提交
        │
        ▼
  ApplyJournal（应用层事务日志）→ 失败逆序补偿 + SB3 兜底
```

四条主线：

1. **契约收敛**（解决根因 A/B/C）；
2. **分段生成 + 精准修复**（解决根因 D，缓解 2.1）；
3. **应用层事务化**（解决 2.3）；
4. **性能与可观测性**。

## 5. 契约重构：从宽模型改为判别式操作联合

### 5.1 新类型模型

将当前的单一 `ResourceOp` 拆为六个禁止额外字段的子模型，以 `op` 为 discriminator：

```python
class AddBuiltinSpriteOp(StrictModel):
    op: Literal['add_builtin_sprite']
    op_id: ShortString
    name: ShortString
    x: int = Field(ge=-10000, le=10000)
    y: int = Field(ge=-10000, le=10000)
    size: int = Field(ge=1, le=1000)
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    source: ScratchSpriteLibrarySource

class AddBackdropOp(StrictModel):
    op: Literal['add_backdrop']
    op_id: ShortString
    name: ShortString
    source: BackdropSource
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    select_after: bool = False

ResourceOp = Annotated[
    AddBuiltinSpriteOp | AddSpriteOp | DeleteSpriteOp |
    AddCostumeOp | AddBackdropOp | SelectBackdropOp,
    Field(discriminator='op'),
]
```

完整字段矩阵（也应成为生成 prompt 的唯一资源规则）：

| 操作 | 必需字段 | 可选字段 / 默认值 | 不存在的字段 |
| --- | --- | --- | --- |
| `add_builtin_sprite` | `op_id,name,x,y,size,source` | `tags=[]` | `target,select_after,costumes` |
| `add_sprite` | `op_id,name,x,y,size,costumes` | `tags=[]` | `target,select_after` |
| `delete_sprite` | `op_id,name,target(existing_sprite)` | 无 | `source,x,y,size,tags,select_after,costumes` |
| `add_costume` | `op_id,target(non-stage),name,source` | `tags=[]`,`select_after=false` | `x,y,size,costumes` |
| `add_backdrop` | `op_id,name,source` | `tags=[]`,`select_after=false` | `target,x,y,size,costumes` |
| `select_backdrop` | `op_id,name` | 无 | 其余全部 |

### 5.2 重新定义 `add_sprite` 的素材语义

当前「顶层 `source` 与 `costumes[]` 并存」是语义重叠来源。建议选择以下单一模型：

```json
{
  "op": "add_sprite",
  "op_id": "sprite_cat",
  "name": "小猫",
  "x": 0,
  "y": -110,
  "size": 80,
  "costumes": [
    {"name": "站立", "source": {"kind": "scratch_library", "asset_type": "costume", "search_terms": ["cat"]}, "tags": ["idle"]}
  ]
}
```

规则：

- `add_sprite.costumes` 最少 1 个，首个即初始造型；不再有顶层 `source`。
- 每个造型自带 `source`，而且仅允许 `costume` 或 `image_generation`。
- `add_builtin_sprite` 保留独立语义，只接受 Scratch sprite library source。
- 前端 `sourceForCostume()` 的顶层回退分支移除，消除当前后端放行、前端拒绝的分歧。

这是一次协议破坏性变更。兼容策略见 5.5。

### 5.3 `add_backdrop` 的安全归一化

目标是让模型表达「为舞台添加背景」时不因无害冗余而失败，同时严格阻止真正不合法的语义。

输入进入 `AddBackdropOp` 前经过 `normalize_resource_op(raw, context)`：

| 原始情况 | 处理 | 诊断 |
| --- | --- | --- |
| 缺少 `select_after` | 补 `false` | `DEFAULT_APPLIED` |
| `target={"kind":"stage"}` | 剥离 `target` | `REDUNDANT_STAGE_TARGET_REMOVED` |
| `target` 为其他值 | 拒绝 | `INVALID_BACKDROP_TARGET` |
| `x/y/size` 都为 `null` | 剥离 `null` 字段 | `NULL_FIELD_REMOVED` |
| `x/y/size` 有实际值 | 拒绝 | `INVALID_BACKDROP_PLACEMENT` |
| `source.asset_type != backdrop` | 拒绝 | `INVALID_BACKDROP_SOURCE` |

归一化必须满足：**仅做无损、单义的转化；不得猜测目标、资源或位置。** 归一化结果、原始值与规则编号写入审计事件，便于量化模型偏差。

### 5.4 校验错误格式

停止通过 Pydantic 文案让模型猜错误。定义可机读的统一错误：

```json
{
  "code": "FIELD_FORBIDDEN",
  "path": "resource_ops[2].target",
  "operation": "add_backdrop",
  "actual": {"kind": "stage"},
  "expected": "field must be omitted; backdrop always targets the stage",
  "repair_scope": "resource_op",
  "repair_id": "op_background_day"
}
```

规则：

- `code` 为稳定枚举，不直接暴露框架错误类型。
- `path` 始终是 JSON Pointer 或统一点路径，二选一并全系统固定。建议 JSON Pointer（例如 `/resource_ops/2/target`），方便前端精确定位。
- `repair_scope` 只能是 `requirements`、`plan`、`manifest`、`resource_op`、`program` 或 `script`。
- `actual` 长度上限 2 KB，避免回灌大型 prompt。
- 使用同一份契约定义生成错误代码清单和用户可读中文文案。

### 5.5 版本切换（无兼容层）

本次重构直接切换到新流程，不实现 v1 双读、双写、影子转换、迁移器或 feature flag。项目清单明确要求 `schema_version=2`；显式提供其他版本时，服务端和前端都返回稳定的 `UNSUPPORTED_SCHEMA_VERSION` 诊断。ProgramIR 的内部 metadata 继续使用 `schema_version=1`，它与项目 manifest 版本不是同一层协议。

| 项 | 当前行为 | 验收标准 |
| --- | --- | --- |
| 项目 manifest | 只接受 v2 严格字段和资源操作联合 | v1 或未知版本不进入资源/程序应用 |
| 前端校验 | 使用生成的 v2 resource contract | 后端与前端拒绝/允许结果一致 |
| 旧接口 | 不保留旧生成入口或 v1 兼容类型 | 旧接口返回 404，代码不包含双读逻辑 |
| 文档与 prompt | 统一描述新分段流程和 v2 字段 | 不把 v1 manifest 示例注入模型 |

版本必须显式存在，不能用「字段存在与否」隐式推断；解析器只按当前 v2 契约校验项目 manifest。

## 6. 生成编排重构：分段生成与精准修复

### 6.1 为什么不继续「一条流出全部」

当前是单次请求内让模型连续产出 `requirements → plan → manifest → program*`（[orchestrator.py:113-118](backend/app/orchestration/orchestrator.py#L113-L118)）。它的优点是只付一次 prompt 成本，缺点是**失败半径等于整条流**：manifest 里一个字段错，后面已经生成的 program 全部作废。

分段的代价是多次请求，但知识库前缀稳定、可缓存（`PromptCache` 已按知识库/项目/目标三级缓存，[prompt_builder.py:91-96](backend/app/prompt_builder.py#L91-L96)），重复前缀的边际成本远低于重出全部内容。

### 6.2 分段边界

| 段 | 输入 | 输出 | 失败重试范围 |
| --- | --- | --- | --- |
| S1 需求 | 用户消息 + 目标上下文 | `requirements` | 仅 S1 |
| S2 计划 | S1 结果 | `plan` | 仅 S2 |
| S3 清单 | S1+S2 + 项目上下文 | `manifest` | 仅 S3，或单个 `resource_op` |
| S4 程序 | S1..S3 + 目标契约 | `program[i]` | 仅第 i 个目标 |

S4 各目标之间**没有生成期依赖**（依赖已在 manifest 固化为 `program_targets` 顺序与 `op_id` 引用），因此可并发发起，按 `target_index` 归位。这是延迟优化的主要来源：N 个目标从串行 N 次变为并发。

**并发上限建议 3～4**，同时受 provider 速率限制约束；超出时排队。

### 6.3 精准修复

修复不再要求「从某标签重新输出后续全部」。按 `repair_scope` 分级：

| scope | 重出内容 | 提示词形态 |
| --- | --- | --- |
| `resource_op` | 只重出该 op 的 JSON 对象 | 给出该 op 原文 + 结构化错误 + 该操作的字段矩阵行 |
| `program`/`script` | 只重出该目标/该脚本 | 给出该脚本原文 + 错误 + 相关 opcode 契约 |
| `manifest` | 整个 manifest | 仅当结构性错误（如 `program_targets` 引用不存在的 `op_id`） |
| `requirements`/`plan` | 该段 | 保持现状 |

修复请求只携带**必要上下文**：失败对象原文 + 结构化错误 + 相关契约片段，不再回灌 `raw_output[:12000]`（[orchestrator.py:583](backend/app/orchestration/orchestrator.py#L583)）。这同时降低 token 成本与「模型被旧错误输出带偏」的概率。

`max_repair_rounds` 由 1 提升至 2，并按 scope 分别计数——局部修复便宜，可以多给一次机会；整段重出仍限 1 次。

### 6.4 结构化输出与模型配置

**实现决策：S1～S4 的结构化 provider 使用 JSON object 分段传输；不具备该能力的 provider 保留 tagged stream + 归一层 + 精准修复。**

当前 provider 能力矩阵：

| provider | S1/S2/S3/S4 传输 | schema 参数 |
| --- | --- | --- |
| GPT | JSON object | 传递由 Pydantic/TypeAdapter 生成的严格 JSON Schema |
| DeepSeek | JSON object | 不传 JSON Schema，仅使用 JSON object response format |
| 其他 provider | tagged stream | 不传 JSON Schema |

能力由 provider 明确声明并按 segment 路由；JSON object 的最终 prompt 不包含 tagged-stream 输出指令。provider wire payload 与编排层 schema 转发均有测试覆盖，新增 provider 必须先补能力和 payload 测试。

## 7. Scratch VM 应用层事务

### 7.1 VM 提供了什么、不提供什么（已验证）

基于本地 `@scratch/scratch-vm` 15.0.1 源码确认：

**可用的增量 API：**

| API | 行为 | 事务相关性 |
| --- | --- | --- |
| `vm.addSprite(json)` | 校验 + 反序列化 + `installTargets`，完成后发 `PROJECT_CHANGED` | 可用 `deleteSprite` 补偿 |
| `vm.addCostume(md5ext, obj, targetId)` | `loadCostume` → `target.addCostume` → `setCostume` | 需自行记录以便补偿 |
| `vm.addBackdrop(md5ext, obj)` | 同上，作用于 stage | 同上 |
| `vm.deleteSprite(targetId)` | 先导出 sprite3 生成**异步** restore 函数，再 stop/dispose clones | restore 可用，但异步 |
| `vm.loadProject(input)` | 先 `runtime.dispose/clear`，再反序列化并加载资源 | 全量兜底 |

**明确不提供：**

- 跨资源/脚本的原子事务；
- undo journal；
- 已发出事件的撤销；
- asset cache 的回收。

**关键约束：**

- `saveProjectSb3()` 依赖内存中 `asset.data` 存在；只有 assetId 不足以保存。
- `RenderedTarget.addCostume` 只入数组并自动处理重名，skin 必须先经 `loadCostume`。
- 直接写 `blocks._blocks` 或 `currentCostume` 会绕过缓存失效与事件派发，**禁止**。
- `runtime.targets` 是面板顺序，`executableTargets` 是执行顺序，二者不同。

结论：**事务语义必须由应用层实现，VM 只提供可补偿的增量操作。**

### 7.2 ApplyJournal 设计

引入应用日志，把「已做了什么」显式记录下来，作为补偿依据：

```ts
type ApplyEntry =
  | { kind: 'sprite'; targetId: string; opId: string }
  | { kind: 'costume'; targetId: string; costumeIndex: number; assetId: string; opId: string }
  | { kind: 'backdrop'; costumeIndex: number; assetId: string; opId: string }
  | { kind: 'deleted_sprite'; restore: () => Promise<void>; opId: string }
  | { kind: 'program'; targetId: string; scriptIds: string[] }
  | { kind: 'asset'; assetId: string };
```

提交流程：

1. `beginTransaction()` → 记录项目 revision + VM lifecycle token；
2. 保存一次 SB3 兜底备份（保留现有行为）；
3. 逐步应用，每步**成功后立即追加 journal 条目**；
4. 每个 `await` 边界后同时校验 `assertActive()` 与 `assertProjectSnapshotIsCurrent()`；
5. 失败 → 逆序补偿 → 校验一致性 → 仍不一致才 `loadProject(备份)`。

### 7.3 补偿优先、SB3 兜底

现在只有 SB3 全量恢复一种回滚，粒度过粗且会覆盖窗口期内的用户改动。分级：

| 层级 | 手段 | 适用 |
| --- | --- | --- |
| L1 补偿 | 逆序执行 journal 的逆操作 | 常规失败 |
| L2 兜底 | `loadProject(备份)` | 补偿失败或状态不可信 |
| L3 上报 | 标记 `fatalImport`，提示用户手动恢复 | L1、L2 都失败 |

**必须修的顺序缺陷：** 当前回滚前先 `assertActive()`，VM 重绑定时它先抛错，导致备份恢复根本不执行（[generation-runner.ts:528](frontend/src/ai-blocks/generation-runner.ts#L528)）。新逻辑必须把「VM 已失效」与「备份恢复失败」建模为两种独立结果，且 VM 失效时不应阻止对新 VM 的恢复尝试。

### 7.4 素材清理

`createAsset()` 产生的对象需登记入 journal 的 `asset` 条目。回滚时：

- 先按 journal 清理本次事务新增的 asset；
- 再确认 `loadProject()` 后是否仍有无引用 asset 残留（需实测确认 VM 行为，不要假设）；
- 生成失败连续发生时，无引用素材不应累积。

### 7.5 并发闸门

现有 `beginProjectTransaction()` 只是并发闸门 + token。需增强为：

- 事务期间 UI 层禁用会改动项目的入口（角色增删、造型编辑、积木拖拽）；
- revision 与事务 token 绑定，任一变化即判定冲突；
- 冲突时**停止后续写入**并进入 L1 补偿，而不是继续写完再回滚。

## 8. 性能与延迟

### 8.1 当前串行链路

项目模式把以下环节全部串起来，延迟随资源数与目标数线性增长：

- 资源准备逐个等待生图或素材检索（[project-resource-preparer.ts:162](frontend/src/ai-blocks/project-resource-preparer.ts#L162)）；
- 资源应用逐个等待（[project-resource-runner.ts:71](frontend/src/ai-blocks/project-resource-runner.ts#L71)）；
- 程序逐目标编译并写入（[generation-runner.ts:496](frontend/src/ai-blocks/generation-runner.ts#L496)）。

### 8.2 优化项

| 项 | 现状 | 目标 | 依据 |
| --- | --- | --- | --- |
| P1 生图/检索 | 串行 | 并发（上限 3～4） | 相互独立 |
| P2 程序生成 | 串行 | 并发 | 目标间无生成期依赖（6.2） |
| P3 程序编译 | 在提交锁内 | 前移到锁外预编译 | 编译是纯计算，不碰 VM |
| P4 资源应用 | 全串行 | 按依赖拓扑分层，层内并发 | 仅 `created_sprite` 引用需顺序 |
| P5 项目上下文 | 每目标全量遍历 | 单次遍历建索引，按 scope 裁剪 | 见下 |
| P6 `content_delta` | 每 chunk 发送但前端不消费 | 删除，或赋予明确 UI 职责 | [orchestrator.py:146](backend/app/orchestration/orchestrator.py#L146) |

**P4 需谨慎：** 资源并发化会改变 VM 中角色/造型的插入顺序，进而影响面板顺序与 `costumeIndex`。实施时必须保证 journal 记录的是**实际结果索引**而非预期索引，并优先保证正确性——若测试显示顺序不稳定，P4 应降级为「仅生图准备并发，VM 写入保持串行」。

**P5：** `buildProjectContext()` 对每个目标调用 `readAiTargetSnapshot()`（[AiBlocksPanel.vue:166](frontend/src/ai-blocks/AiBlocksPanel.vue#L166)），而后者会遍历脚本、输入、影子块并计算完整 `current_program`；`targetStateSignature()` 又独立遍历一遍全部 block、变量、注释与素材（[target-context.ts:107](frontend/src/ai-blocks/target-context.ts#L107)）。大型项目会在发请求前造成明显主线程阻塞，且把完整程序 JSON 重复放进每个目标上下文。改为单次遍历建索引，非当前目标只带摘要与哈希。

### 8.3 缓存

三级缓存机制保留（[prompt_builder.py:91-96](backend/app/prompt_builder.py#L91-L96)），但要保证分段生成不破坏前缀稳定性：

- 知识库段必须**逐字节稳定**，不得包含时间戳、请求 ID 或随机排序的 JSON；
- 易变内容（`request_id`、用户消息）一律放在稳定前缀之后；
- 分段请求共享同一前缀，段间差异只体现在末尾指令。

上线后需验证缓存命中率，若重复请求命中率为 0，说明存在静默失效源。

## 9. 可观测性

当前日志只有中文文本 + `detail` 拼接（[orchestrator.py:300-303](backend/app/orchestration/orchestrator.py#L300-L303)），无法聚合统计。改为结构化事件：

```json
{
  "event": "resource_op_rejected",
  "request_id": "...",
  "schema_version": 2,
  "provider": "normal",
  "segment": "manifest",
  "operation": "add_backdrop",
  "code": "FIELD_FORBIDDEN",
  "path": "/resource_ops/2/target",
  "repair_attempt": 0,
  "normalized": false
}
```

需要能够回答的问题：

- 哪个操作、哪个字段最常出错？（决定下一轮 prompt 优化方向）
- 归一化救回了多少次？（衡量归一层价值，也提示契约仍不自然之处）
- 修复轮成功率是多少？分 scope 看有无差异？
- 各 provider 的首轮通过率差异？
- 端到端耗时在 provider、生图、编译、VM 写入之间如何分布？

建议同时落一份**失败样本留存**：脱敏后保存失败的原始输出与错误，作为回归样本集来源。

## 10. 分阶段实施计划

各阶段独立可交付、可回滚。除 Phase 0 外，每阶段结束都应能正常出包。

### Phase 0：止血与统一（1～2 天）

目标：让项目模式先能跑通，同时消除已知契约漂移。

| 任务 | 说明 | 验收 |
| --- | --- | --- |
| 0.1 | `add_backdrop` 拆分为三条独立校验，各带独立错误码与文案 | 三种错误分别可复现且信息不同 |
| 0.2 | `select_after` 给默认值 `false` | 缺失不再失败 |
| 0.3 | 容忍并剥离 `target={"kind":"stage"}` | 记录归一化事件 |
| 0.4 | 修正 `add_sprite.source` 前后端分歧 | 后端合法样例前端必通过 |
| 0.5 | 从知识库删除裸 ProgramIR / 错误 JSON / project.json 段落 | prompt 内无自相矛盾表述 |
| 0.6 | 修复回滚前 `assertActive()` 顺序缺陷 | VM 重绑定时仍尝试恢复且错误信息准确 |

Phase 0 完成即应观察到报错消失。**这是最小可上线单元**，后续阶段是结构性改进。

### Phase 1：契约单一来源（3～5 天）

| 任务 | 说明 |
| --- | --- |
| 1.1 | 建立 `resource_contract.py` 作为 SSOT |
| 1.2 | 由 SSOT 生成 Pydantic 判别式联合 |
| 1.3 | 由 SSOT 生成前端 TS 类型与 validator（生成物入库并 CI 校验最新） |
| 1.4 | 由 SSOT 生成 prompt 操作矩阵，替换手写段落 |
| 1.5 | 结构化错误对象贯通后端→SSE→前端 |
| 1.6 | 文档收敛：过时描述统一指向本文与 SSOT |

**验收：** 任一契约变更只需改 SSOT；CI 能检出手改生成物。

### Phase 2：分段生成与精准修复（3～5 天）

| 任务 | 说明 |
| --- | --- |
| 2.1 | 编排拆为 S1～S4 |
| 2.2 | 按 `repair_scope` 实现局部修复 |
| 2.3 | 修复提示只带失败对象 + 错误 + 契约片段 |
| 2.4 | S4 目标级并发 |
| 2.5 | provider 结构化输出能力探测与分流 |

**验收：** 单字段错误的修复请求 token 量较当前下降一个数量级；样本集首轮通过率达标。

### Phase 3：应用层事务（3～5 天）

| 任务 | 说明 |
| --- | --- |
| 3.1 | ApplyJournal |
| 3.2 | L1 补偿 / L2 兜底 / L3 上报分级回滚 |
| 3.3 | 每个 `await` 边界校验 revision + token |
| 3.4 | asset 登记与清理 |
| 3.5 | 事务期间 UI 编辑入口禁用 |

**验收：** 冲突与失败注入测试下，项目与素材均回到操作前状态。

### Phase 4：性能与可观测性（2～3 天）

P1～P6 与第 9 节。**验收：** 典型项目（3 目标 + 5 资源）端到端延迟较基线下降 ≥ 40%，且有分段耗时数据支撑。

### Phase 5：清理（1 天）

删除 v1 契约、feature flag、兼容分支与过时文档段落。

## 11. 测试策略与验收矩阵

### 11.1 后端契约测试

| 类别 | 必测场景 |
| --- | --- |
| 判别联合 | 每种 `op` 的最小合法对象；所有多余字段均拒绝 |
| 背景 | 无 `select_after` 自动补默认；`stage` target 自动剥离；非 stage target 拒绝；位置字段拒绝 |
| 资源 | source 类型与 asset_type 组合；嵌套 costume；created sprite 前向引用与循环引用 |
| 版本 | v1/v2 双读；规范化结果等价；不支持版本报稳定错误码 |
| 跨阶段 | `program_targets` 的 `op_id`、模式、目标唯一性；shared state 一致性 |
| 修复 | `resource_op`、`manifest`、`script` 三种 scope 只重出正确范围 |
| 模糊测试 | 对有效 manifest 随机插入一个禁止字段，断言路径与错误码稳定 |

### 11.2 前端事务测试

| 场景 | 断言 |
| --- | --- |
| 资源准备期间 revision 改变 | 停止写入，逆序补偿，不覆盖用户改动 |
| 资源应用中途失败 | journal 逆序执行，目标/造型/asset 无残留 |
| 补偿失败 | 调用 SB3 兜底恢复 |
| VM 重绑定 | 区分生命周期失效与恢复失败；不掩盖原始错误 |
| `add_sprite` 多造型 | 新契约正确创建并选择第一个造型 |
| 背景归一化 | stage target 被剥离、默认 `select_after=false` |
| 资产 | 连续失败后 storage 中无新增无引用 asset |
| repair UI | requirements/plan/manifest/program 各种修复时旧草稿不会被错误展示 |

### 11.3 Scratch VM 集成探针

现有 `d:/ywj/_scratch_vm_probe/probe.js` 已验证 load、green flag、广播与停止后重启。新增探针：

1. 新建多造型 sprite，保存 SB3，再 loadProject，检查造型、skin、资源数据是否完整；
2. 添加背景、设置背景、回滚，检查 stage `currentCostume` 与 renderer 更新；
3. `deleteSprite` + restore，检查 clone、变量与脚本恢复；
4. 连续 `createAsset`、回滚与 loadProject，检查 storage 资源生命周期；
5. 并发资源应用的顺序与 `costumeIndex` 稳定性。

探针必须以当前 `@scratch/scratch-vm` 15.0.1 运行，不能只依赖旧探针使用的 5.0.300。

### 11.4 端到端回归样本集

建立 version-controlled fixtures，至少包含：

- 单背景 + 单角色；
- 角色库资源；
- 生图背景；
- 多角色、created sprite 引用；
- 追加、修改、重写三种当前目标模式；
- 删除角色 + 新建角色；
- 广播、变量、列表跨目标；
- 每种已知错误的「原始模型输出 → 归一化/修复 → 最终清单」。

每次 prompt 或 schema 改动必须运行样本集，并记录：首轮合法率、修复成功率、输出 token、总耗时。

## 12. 风险与决策点

| 风险/决策 | 建议 |
| --- | --- |
| 多 provider 不支持完整 JSON Schema | 按 provider 能力选择 JSON object 或标签流分段传输，先完成真实能力探测，不以假设推进 |
| 判别式联合会使 schema 大 | 将每段 schema 分开下发，S3 只给 manifest schema，S4 只给 program schema |
| `add_backdrop` 容忍 stage target 会掩盖模型质量 | 记录并设置告警阈值；它是过渡兼容，不是永久默许 |
| 并发 VM 写入可能改变顺序 | 先只并发准备与生成，VM 写入保持串行；有实测后再逐层放开 |
| SB3 备份很大 | 保留单份备份，不做每步快照；L1 journal 成功时无需 loadProject |
| shared_state 两处声明 | 采用「每 program 完整重复」边界：manifest 是全局 SSOT，后端和前端均要求每个 program 完整重复并做一致性校验 |
| 用户在事务期间编辑 | UI 禁用是第一道防线，revision 校验是第二道；两者缺一不可 |

## 13. 推荐实施顺序

先实施 **Phase 0**。它以最小改动解决当前 `add_backdrop` 报错、清理明显 prompt 冲突，并修复回滚顺序问题；完成后应立即重新跑 20 条项目样本验证首轮成功率。

之后按 **Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5** 推进。不要反过来先做并发优化：在契约和事务不稳定时缩短耗时，会更快地产生更难复现的错误。

## 14. 首轮实施文件清单

Phase 0 / Phase 1 预期涉及：

- [backend/app/schemas/ai_blocks.py](backend/app/schemas/ai_blocks.py)
- [backend/app/prompt_builder.py](backend/app/prompt_builder.py)
- [backend/app/orchestration/orchestrator.py](backend/app/orchestration/orchestrator.py)
- [backend/tests/test_ai_blocks.py](backend/tests/test_ai_blocks.py)
- [frontend/src/ai-blocks/project-plan.ts](frontend/src/ai-blocks/project-plan.ts)
- [frontend/src/ai-blocks/project-resource-preparer.ts](frontend/src/ai-blocks/project-resource-preparer.ts)
- [frontend/src/ai-blocks/generation-runner.ts](frontend/src/ai-blocks/generation-runner.ts)
- [frontend/src/scratch/scratch-gui-bridge.ts](frontend/src/scratch/scratch-gui-bridge.ts)
- [frontend/tests/project-resource-preparer.test.ts](frontend/tests/project-resource-preparer.test.ts)
- [frontend/tests/generation-runner.test.ts](frontend/tests/generation-runner.test.ts)
- [docs/scratch代码生成提示词.md](docs/scratch代码生成提示词.md)
- [docs/scratch项目清单生成提示词.md](docs/scratch项目清单生成提示词.md)

Phase 1 建议新增：

- `backend/app/resource_contract.py`
- `frontend/src/ai-blocks/generated/resource-contract.ts`
- `scripts/generate-resource-contract.mjs` 或相同职责的现有构建脚本扩展
- `frontend/tests/resource-contract-parity.test.ts`

## 15. 完成定义

重构完成不以「某次模型没有报错」为标准，而以以下全部满足为准：

1. `add_backdrop` 的 stage target、漏 `select_after` 等高频偏差能被归一化或精准修复；
2. 后端、前端、prompt 三处不再维护独立字段矩阵；
3. 资源、清单、脚本的校验失败都有稳定错误码、字段路径和局部重试范围；
4. 资源/程序任一步失败或项目发生并发修改时，不产生部分提交、用户改动覆盖或无引用素材泄漏；
5. 样本集首轮通过率、修复成功率、延迟与缓存命中率持续可观测；
6. 所有新增与存量测试通过，并有 Scratch VM 15.0.1 的真实集成探针覆盖资源生命周期。

