# AI 写积木扩展角色、舞台背景与造型功能实施计划

**文档状态**：方案评审稿

**适用仓库**：`D:/ywj/AI-Scratch-web/`

**目标**：在现有 AI 写积木模块中支持用户选择“单角色”或“整个项目”的作用范围，通过自然语言创建角色、为角色添加造型、添加或切换舞台背景，并优先使用 Scratch 内置素材。只有内置素材没有匹配项或用户明确要求时才生成图片；生成的角色和背景写入带标签的本地素材库。角色抠图、透明化和其他画布处理由用户在无边画布中手动完成。

## 1. 结论摘要

### 1.1 当前 Scratch VM 可以支持这些操作

当前项目使用 `@scratch/scratch-vm@15.0.1`。Scratch VM 已经具备完成资源操作所需的原生入口，且项目当前的图片导入流程已经在真实使用其中一部分能力：

| 能力 | VM 入口 | 当前项目状态 | 结论 |
| --- | --- | --- | --- |
| 添加角色 | `vm.addSprite(input)` | `asset-adapter.ts` 构造 SB3 单角色对象，`scratch-gui-bridge.ts` 调用并校验新增 target | 已支持，可复用 |
| 为新角色添加多个造型 | 在单角色 SB3 对象的 `costumes` 中声明；导入后由 VM 安装 | 当前 `buildSpriteImport` 已支持多个 `CostumeExport` | 已支持，可扩展名称、位置和初始大小 |
| 为已有角色添加造型 | `vm.addCostume(md5ext, costumeObject, targetId)` | 当前 bridge 尚未提供面向 AI 的封装 | VM 支持，需要补充 bridge/adapter |
| 添加舞台背景 | `vm.addBackdrop(md5ext, backdropObject)` | `importBackdropCostume` 已创建本地 asset 并调用该入口 | 已支持，可复用 |
| 添加背景后切换到该背景 | `addBackdrop` 完成后 VM 会把舞台当前造型设为新增背景 | 当前流程已有该行为 | 已支持 |
| 切换到已有背景 | 舞台 target 的 `setCostume(index)` | 当前 bridge 未封装按名称解析和切换 | VM 支持，需要补充受控封装 |
| 删除新增角色和失败补偿 | `vm.deleteSprite(targetId)`，或恢复 SB3 | 当前角色导入失败时已有删除半成品逻辑；AI 积木也有 SB3 fallback | 可扩展为项目级事务 |
| 保存、恢复和导出 | `saveProjectSb3()`、`loadProject()` | bridge 已封装，AI 积木撤销已使用 | 可作为统一回滚机制 |

具体代码依据：

- `frontend/src/scratch/asset-adapter.ts:20-48` 已将一个或多个造型转换为 SB3 角色对象。
- `frontend/src/scratch/scratch-gui-bridge.ts:128-192` 已通过 `vm.addSprite` 创建角色，校验 target、造型数量、资源 ID、分辨率和旋转中心，并在异常时删除新增角色。
- `frontend/src/scratch/asset-adapter.ts:50-61` 已通过 storage 创建图片 asset，再调用 `vm.addBackdrop`。
- 已安装 VM 的 `virtual-machine.js:581-677`、`677-749`、`1017-1024`、`1066-1099` 分别提供了角色、造型、背景和 target 生命周期入口。

因此，问题不是 Scratch VM 是否支持，而是如何将 AI 产出的“资源意图”安全地交给浏览器端执行。

### 1.2 推荐方案

**沿用当前处理方式，扩展为“项目级声明式补丁”，由前端 bridge/adapter 执行；不采用后端直接执行 VM 的 ToolCalls。**

推荐链路：

1. AI 写积木面板先让用户选择作用范围：`单角色` 或 `整个项目`，并记录素材来源策略。
2. 后端继续负责需求整理、规划、模型输出和严格 schema 校验，模型输出受限的 `ProjectPlan`。
3. 前端先在 Scratch 内置素材目录中匹配并展示候选，优先打开原生 Scratch 角色、造型或背景库供用户选择。
4. 只有内置素材没有匹配项，或用户明确要求生成时，前端才向用户确认并调用现有生图接口。
5. 生成成功的每张图片通过现有素材库接口保存，并带上角色/背景类别、来源、用途和语义标签；之后由 `ScratchAssetAdapter` 和 bridge 执行导入。
6. 前端通过受控的 bridge 方法串行执行 `addSprite`、`addCostume`、`addBackdrop`、`setCostume`，资源操作成功后再执行对应的 `ProgramIR`。
7. 角色透明化、抠图和其他像素处理不在 AI 写积木模块内完成，用户可转到无边画布手动处理后再导入。
8. 任一步骤失败、取消或撤销时，使用一次性的 SB3 备份恢复整个项目；素材库中的已生成原图作为独立本地资产保留。

后端可以在未来使用模型原生 ToolCalls 来帮助模型组织计划，但 ToolCalls 最终必须归一化为同一个 `ProjectPlan`，不能把任意 VM 方法暴露给模型。

## 2. 为什么不采用“后端 ToolCalls 直接操作 VM”

### 2.1 进程边界不允许

当前 Scratch GUI 和 VM 运行在浏览器中，后端是独立的 FastAPI 进程。后端没有浏览器中的 `window`、Scratch runtime、storage 或 renderer，因此后端 ToolCall 无法直接调用 `vm.addSprite` 等方法。

如果所谓“后端 ToolCalls”只是把 `add_sprite`、`add_backdrop` 等命令通过 HTTP 返回给前端，那么仍然需要：

- 前端定义命令 schema；
- 前端校验参数和目标引用；
- 前端解析图片并创建本地 asset；
- 前端调用 VM；
- 前端处理回滚和用户确认。

这种做法的实际执行边界仍然是前端，只是把当前的声明式 JSON 换成另一套命令格式，收益有限且会重复建设协议。

### 2.2 当前 provider 也没有 ToolCalls 协议

当前后端 provider 的流事件类型只有 `reasoning` 和 `content`，见 `backend/app/providers/agnes_text_provider.py:27-30`。`/api/ai-blocks/generate` 当前通过 SSE 发送规划、推理和最终 JSON，见 `backend/app/routes/ai_blocks.py:291-431`。

引入原生 ToolCalls 会额外要求：

- provider 统一不同模型的 tool call 和参数增量格式；
- 新增 SSE 的 tool call 事件及中断协议；
- 处理工具调用未完成、重复调用、参数 JSON 不完整等情况；
- 为工具结果增加 repair 和验证逻辑；
- 仍然需要一个浏览器端执行器。

在当前需求中，这些工作不能解决后端无法触达 VM 的根本问题。

### 2.3 当前方案已有可复用的安全基础

现有 AI 写积木已经具备适合扩展资源操作的机制：

- 后端 Pydantic strict model、`extra='forbid'` 和 ProgramIR 合同校验；
- 前端和后端各自维护 block catalog；
- 前端使用 target snapshot 和 project revision 防止过期计划覆盖用户新修改；
- `generation-runner.ts:312-402` 已有保存备份、增量注入、失败回滚；
- `generation-runner.ts:404-417` 已有带 revision 检查的撤销；
- 图片通过本地 storage asset 导入，SB3 中保存资源数据，不依赖远程图片 URL。

扩展资源计划可以沿用这些约束，而不是新增一个权限更宽的通用工具执行层。

## 3. 现状调查与需要注意的差异

### 3.1 当前 AI 写积木默认是“当前 target 级”，计划增加作用范围选择

当前请求上下文由 `frontend/src/ai-blocks/target-context.ts` 生成，包含当前目标名称、是否舞台、造型、声音、其他角色名称和当前程序。后端 schema 在 `backend/app/schemas/ai_blocks.py:32-40` 对应这些字段。当前 `AiBlocksPanel` 也只围绕当前编辑 target 保存快照和执行 `ProgramIR`。

AI 写积木页面需要增加一个“作用范围”下拉选项：

- `单角色`：仅针对当前选中的角色，允许修改该角色的积木、添加该角色的造型和使用该角色已有资源；不得创建其他角色、修改舞台背景、修改其他角色或操作项目级资源。当前 target 是舞台时，该选项不可用，并提示用户切换到角色或选择“整个项目”。
- `整个项目`：读取项目内所有角色、舞台、背景和相关程序，允许按 ProjectPlan 执行跨 target 的角色、造型、背景和积木变更。

`scope` 必须成为前后端请求上下文的一部分，并在后端校验。用户选择的范围是硬边界：模型即使在“单角色”模式下理解到需要新角色或舞台背景，也必须返回需要切换为“整个项目”的澄清，而不是自行扩大权限。

当前 `ProgramIR` 仍只描述一个 target 的：

- 脚本；
- 变量、列表和广播；
- 要删除的脚本引用。

它没有项目级资源操作，也没有跨 target 的程序绑定。因此本次扩展不能只在现有 `ProgramIR` 中随意增加 `addSprite` 字段，否则会把脚本 IR、图片来源和 VM 操作混在一起。项目模式应使用独立的 `ProjectPlan`，其中包含资源操作和带目标引用的多个 `ProgramIR`。

### 3.2 当前生图接口实际是 1x1

代码中的当前契约是：

- `frontend/src/ai/image-generation-types.ts:10-18` 将 `sheet_layout` 限定为 `1x1`；
- `backend/app/schemas/image_generation.py:14-23` 也只接受 `1x1`；
- `frontend/src/scratch/asset-adapter.ts:63-70` 将单张 PNG 转换为一个角色造型或一个背景。

旧的计划文字曾描述 2x2 候选图，但实现代码以 1x1 为准。本计划第一阶段按 1x1 设计，每个资源请求产生一个图片资源。若以后需要多候选选择，应单独升级生图协议，不把候选图逻辑混入 VM 资源导入协议。

### 3.3 角色透明度和抠图由用户在画布中处理

本模块的职责边界确定为“生图和导入”，不负责自动抠图、透明化、裁切、边缘修复或其他像素处理。当前后端图片提示词对角色使用“简洁背景，不伪造透明”的约束，见 `backend/app/prompts.py:47-56`；AI 写积木模块应按生图接口返回的原图导入，不改变图片像素语义。

当用户需要透明角色或其他处理时：

1. AI 写积木模块保留原始生成结果，并明确提示该图片未自动抠图。
2. 用户选择“导入画布处理”，进入已有无边画布流程。
3. 用户手动完成抠图、裁切、清理和其他处理。
4. 用户从画布确认处理后的结果，再按现有 Scratch 导入流程导入角色造型或背景。

`frontend/src/canvas/pixels/chroma-key.ts` 等能力属于画布模块，不在本次 AI 写积木资源执行器中调用。验收重点是原图保存、用户可手动处理以及处理后能正常导入，不验收 AI 自动透明化。

## 4. 产品范围

### 4.1 第一阶段纳入范围

1. AI 写积木页面增加“作用范围”下拉选项：`单角色`、`整个项目`。
2. `单角色`只读取和修改当前角色；`整个项目`读取所有角色、舞台、背景和程序，并允许项目级资源操作。
3. 需求整理结果明确列出资源名称、目标、造型数量、用途和推荐来源。
4. 默认优先使用 Scratch 自带的角色、造型和背景库；前端使用本地打包的 Scratch library manifest 和标签进行候选匹配，并通过原生 Scratch library 完成选择和导入。
5. 支持以下资源计划：
   - 使用 Scratch 内置素材创建新角色；
   - 使用 Scratch 内置素材或生成图片向角色添加造型；
   - 使用 Scratch 内置素材或生成图片向舞台添加背景并切换到它；
   - 按名称切换到舞台已有背景；
   - 在内置素材没有匹配项或用户明确要求时生成一个或多个图片资源。
6. 调用生图前必须经过用户确认；确认内容至少包含生成原因、资源数量、名称、提示词、尺寸和素材库标签。
7. 每张 AI 生成图片成功后写入浏览器本地素材库，保留角色/背景类别、来源、用途、样式、生成时间、资源组和可筛选标签。
8. 角色透明化、抠图、裁切和像素处理由用户在无边画布中手动完成，AI 资源模块只负责保存原图和导入原图或用户处理后的结果。
9. 资源和积木作为一次项目变更执行，支持取消、失败恢复、一次撤销和标准 SB3 导出/重载。

### 4.2 第一阶段暂不纳入范围

- 后端直接持有或操作浏览器 VM。
- 模型输出任意 JavaScript、任意 VM 方法名、任意 target 内部 JSON 或任意 URL。
- 自动删除或覆盖已有角色、造型和背景。第一阶段只增加或切换，避免不可逆破坏。
- 音效、扩展资源、克隆体和自定义扩展积木。
- 把 2x2 候选图、参考图、多图一致性或 AI 自动抠图纳入本次协议。
- 通过 AI 模块自动执行画布抠图、透明化或像素修复。
- 未经用户确认调用生图；选择内置素材时不调用生图接口。

## 5. 目标交互流程

### 5.1 AI 作用范围与素材来源控件

AI 写积木面板在发送请求前提供两个明确的选择：

- **作用范围**：`单角色`、`整个项目`。默认值根据当前选中的 target 初始化，但必须显示当前值。
- **素材来源策略**：`优先使用 Scratch 内置素材`、`仅使用已有素材`、`允许 AI 生成`。默认使用 `优先使用 Scratch 内置素材`。即使选择允许生成，实际调用生图前仍需要一次生成确认。

范围规则：

- `单角色`仅针对当前选中的 sprite，包含该角色的积木和造型；不能新建其他角色、修改舞台背景或修改其他 target。
- `整个项目`读取舞台和全部角色，允许 ProjectPlan 进行跨 target 资源和积木操作。
- 当前选中舞台时，`单角色`不可选；用户需要切换到角色，或者改用 `整个项目`。
- 如果用户的自然语言需求超出当前选择范围，界面应提示切换范围，而不是自动扩大权限。

素材来源规则：

1. 前端优先在 Scratch GUI 已打包的 `sprites.json`、`costumes.json`、`backdrops.json` 和对应标签中做本地匹配。
2. 有候选时，显示候选名称和预览，并优先打开 Scratch 原生素材库让用户选择。
3. 没有候选时，显示“未找到匹配的 Scratch 内置素材，是否生成”的确认。
4. 用户明确要求生成时，也必须显示生成数量、提示词、尺寸和素材库标签，再由用户确认。
5. 选择内置素材不会调用 `/api/image/generate`；只有用户确认生成后才调用。

### 5.2 创建角色并添加造型

示例请求：“创建一个宇航员角色，添加站立和行走两个造型，并让它在绿旗时移动。”

1. AI 先进入现有需求整理流程，同时记录当前作用范围和素材来源策略。
2. 需求摘要列出“创建角色”、造型数量、用途、推荐素材来源，以及“为该角色生成绿旗脚本”的行为意图。
3. 后端返回资源计划和积木程序，角色资源操作先于角色脚本。
4. 前端先在 Scratch 内置角色目录中匹配宇航员候选，并显示候选和对应的原生库入口。
5. 用户选择内置角色后，使用 Scratch 原生角色库导入，不调用生图；用户明确要求生成，或内置库没有匹配时，才显示生成确认。
6. 生成确认通过后，前端按顺序调用生图接口，得到 PNG Blob，不把图片二进制交给语言模型；每张结果同时保存到带标签的素材库。
7. 前端把多个生成造型组装为一个 SB3 角色对象，通过现有 `commitSpriteImport` 导入；内置角色则等待原生库完成 `vm.addSprite`。
8. 前端取得 VM 返回的真实 target ID 和实际名称，将逻辑引用映射到真实 target。
9. 如果当前范围是 `整个项目`，再把该角色对应的 `ProgramIR` 注入新角色；`单角色`模式不得创建新角色，需先切换范围。
10. UI 显示新增角色、造型、来源、标签、积木数量和可撤销状态。

### 5.3 为当前角色添加造型

示例请求：“给当前角色增加一个跳跃造型，名称叫跳跃。”

1. 该操作只允许在 `单角色`或`整个项目`范围下解析为快照中的 `current_target`，并确认目标是 sprite。
2. 前端先设置正确的编辑 target，并打开 Scratch 原生造型库，优先让用户从内置造型中选择。
3. 内置库没有匹配，或用户明确要求生成时，显示生成确认；未确认前不调用生图。
4. 生成确认通过后，调用现有生图接口并保存带标签的原图；再通过 bridge 的定向封装调用 `vm.addCostume(md5ext, costumeObject, targetId)`。
5. 校验目标的造型数量、名称、asset ID、bitmapResolution 和旋转中心，并根据用户选择决定是否将新增造型设为当前造型。
6. 如果目标是舞台，应拒绝该操作并提示改用“添加背景”；透明化和抠图交由用户在画布中手动处理。

### 5.4 添加或切换舞台背景

需要区分三个自然语言语义：

- “添加 Scratch 自带的森林背景”：打开原生背景库，用户选择后导入，不调用生图。
- “生成一张森林背景并设置为舞台背景”：只有在用户明确要求，或内置背景没有匹配时，才弹出生成确认；确认后生成 PNG、保存到带标签的素材库，调用 `addBackdrop`，新增并切换到该背景。
- “切换到已有的森林背景”：只在舞台造型清单中按名称查找并调用受控的 `setCostume`，不重复生成图片。

若内置库和已有项目背景都没有匹配，前端应先提示用户是否生成；名称不存在、名称匹配多个或目标不是舞台时，前端应回到确认界面，而不是猜测。

### 5.5 失败、取消和撤销

- 内置素材选择取消：不修改 VM，不调用生图，计划保留为待处理状态。
- 用户拒绝生成：不调用生图，不修改 VM；可以改为选择内置素材或结束本次资源操作。
- 生图阶段失败：不修改 VM，保留错误资源项和重试入口；已经成功生成并保存的其他素材仍保留在素材库。
- 导入阶段失败：使用项目备份恢复，不依赖逐项删除是否成功。
- 积木阶段失败：恢复同一个项目备份，确保新角色、背景、造型和变量一起回到变更前状态。
- 用户选择转到画布处理：AI 模块暂停资源导入，用户手动处理后再从画布确认导入；AI 模块不自动改像素。
- 用户取消：立即停止后续操作，已经发生的 VM 变更恢复到备份；素材库中已生成的原图作为独立资产保留。
- 变更后用户继续编辑：撤销按钮必须检查 project revision；revision 不一致时拒绝静默覆盖。

## 6. 推荐的数据契约

### 6.1 保留现有 ProgramIR

不修改现有 `ProgramIR` 的语义来承载资源。保留 block-only 请求和现有 `program_ready` 事件，确保当前 AI 写积木功能可以独立运行。

对于包含角色、背景或造型的请求，引入一个新的项目级结果：

`ProjectPlan`：

```ts
interface ProjectPlan {
  schema_version: 1;
  scope: 'single_sprite' | 'project';
  summary: string;
  resource_ops: ResourceOp[];
  block_programs: BlockProgramPlan[];
}

interface BlockProgramPlan {
  target: TargetRef;
  program: ProgramIR;
}
```

推荐通过新的 SSE 事件 `project_ready` 返回 `ProjectPlan`。在过渡期可以保留 `program_ready`，资源为空时继续走旧路径。

### 6.2 请求范围和素材策略

前端请求需要明确传递用户选择，后端和模型不能自行推断或扩大范围：

```ts
interface AiBlocksPreferences {
  scope: 'single_sprite' | 'project';
  asset_policy: 'builtin_first' | 'existing_only' | 'allow_generation';
}
```

- `single_sprite` 只允许当前 sprite 的脚本和造型操作。
- `project` 允许舞台、全部 sprite、背景以及跨 target 的程序绑定。
- `builtin_first` 是默认值，优先使用 Scratch 原生素材；`existing_only` 找不到素材时返回未满足状态且不调用生图；`allow_generation` 也不能跳过前端生成确认。
- ProjectPlan 的 `scope` 必须和请求一致，不一致时拒绝执行。

### 6.3 目标引用必须是逻辑引用

模型不得生成 VM 内部 target ID，因为它无法可靠知道运行时 ID，也不应控制内部对象。使用受限的逻辑引用：

```ts
type TargetRef =
  | {kind: 'current_target'}
  | {kind: 'stage'}
  | {kind: 'existing_sprite'; name: string}
  | {kind: 'created_sprite'; op_id: string};
```

前端执行器根据生成前的 project snapshot 解析已有角色，根据先前操作结果解析 `created_sprite`。名称匹配多个时必须报错并要求用户选择。

### 6.4 资源来源只允许受控类型

```ts
type ResourceSource =
  | {
      kind: 'scratch_library';
      asset_type: 'sprite' | 'costume' | 'backdrop';
      search_terms: string[];
    }
  | {
      kind: 'image_generation';
      prompt: string;
      style: 'cartoon' | 'scratch' | 'paper-cut' | 'pixel' | 'watercolor' | 'custom';
      custom_style?: string;
      width: number;
      height: number;
      sheet_layout: '1x1';
    }
  | {
      kind: 'library_asset';
      asset_id: string;
    };
```

第一阶段优先实现 `scratch_library`，其次支持已确认的 `library_asset`，最后才允许 `image_generation`。`scratch_library` 只携带搜索词，候选匹配和原生库选择由前端完成；模型不得直接生成内部资源对象。`library_asset` 必须由前端确认和解析，后端不能读取浏览器 IndexedDB，也不能把任意外部 URL 当作资源来源。`image_generation` 只能在用户明确要求或前端确认没有匹配的 Scratch 内置素材后进入待确认状态。

### 6.5 第一阶段资源操作

```ts
type ResourceOp =
  | {
      op: 'add_builtin_sprite';
      op_id: string;
      source: {kind: 'scratch_library'; asset_type: 'sprite'; search_terms: string[]};
    }
  | {
      op: 'add_sprite';
      op_id: string;
      name: string;
      x: number;
      y: number;
      size: number;
      tags: string[];
      costumes: Array<{
        name: string;
        source: Exclude<ResourceSource, {kind: 'scratch_library'; asset_type: 'sprite'}>;
        tags: string[];
      }>;
    }
  | {
      op: 'add_costume';
      op_id: string;
      target: TargetRef;
      name: string;
      source: ResourceSource;
      tags: string[];
      select_after: boolean;
    }
  | {
      op: 'add_backdrop';
      op_id: string;
      name: string;
      source: ResourceSource;
      tags: string[];
      select_after: boolean;
    }
  | {
      op: 'select_backdrop';
      op_id: string;
      name: string;
    };
```

约束：

- `add_builtin_sprite` 只能使用 `scratch_library` 的 `sprite` 资源；`add_sprite` 至少包含一个造型，且造型来源不能是完整 sprite。造型数、资源操作数和提示词长度均有上限。
- `add_costume` 的 `scratch_library` 来源只能是 `costume`；`add_backdrop` 的 `scratch_library` 来源只能是 `backdrop`，其他 source 也必须符合操作目标。
- `x`、`y`、`size` 使用 Scratch 坐标和百分比范围，超出范围由 schema 拒绝。
- `add_costume` 不能指向舞台；`add_backdrop` 和 `select_backdrop` 的目标固定为舞台。
- `created_sprite.op_id` 必须指向之前的 `add_builtin_sprite` 或 `add_sprite`，资源操作不能形成循环引用。
- 资源名称只作为期望名称，最终以 VM 的唯一名称规则和前端校验结果为准。
- 资源源中禁止出现 base64、data URL、任意 HTTP URL、JavaScript、VM 方法名和内部 target ID。

### 6.6 结果需要返回实际映射

前端执行结束后生成本地结果，不回传到模型：

```ts
interface ProjectApplyResult {
  addedTargets: Array<{op_id: string; target_id: string; name: string}>;
  addedCostumes: Array<{op_id: string; target_id: string; asset_id: string; name: string}>;
  addedBackdrops: Array<{op_id: string; asset_id: string; name: string}>;
  savedAssets: Array<{op_id: string; asset_id: string; target: 'sprite' | 'backdrop'; tags: string[]; groupId?: string}>;
  appliedPrograms: Array<{target_id: string; added_blocks: number; added_scripts: number}>;
  revision_at_complete: number;
  backup: Uint8Array;
}
```

### 6.7 生成素材库和标签模型

当前 `frontend/src/ai/asset-library-store.ts` 使用浏览器 IndexedDB `ai-scratch-assets/generatedAssets`，`GeneratedAsset` 只有 `target: sprite | backdrop` 两类，没有标签。此次扩展保留这两个类别作为一级筛选，同时增加标签，不把类别改造成自由标签。

建议将素材记录升级为 schema version 2：

```ts
interface GeneratedAssetV2 {
  schemaVersion: 2;
  id: string;
  createdAt: number;
  name: string;
  prompt: string;
  style: ImageGenerationRequest['style'];
  customStyle?: string;
  target: ImageGenerationRequest['target'];
  tags: string[];
  origin: 'image-panel' | 'ai-blocks';
  groupId?: string;
  result: ImageGenerationResponse;
}
```

设计规则：

- `target` 继续表达一级类别：`sprite` 显示为“角色”，`backdrop` 显示为“背景”，保证现有导入逻辑和旧数据兼容。
- `tags` 存储语义标签，例如“宇航员”“太空”“站立”“行走”“森林”“像素风”；每个标签去空格、去重、限制长度和数量。
- 自动标签包括类别、来源和样式；用户或 AI 计划提供的业务标签单独显示，用户可以添加、删除和修改。
- 同一个 AI 角色的多个造型使用相同的 `groupId`，并共享角色主题标签；单个造型可以额外带“站立”“跳跃”等动作标签。
- `origin` 区分现有图片面板生成和 AI 写积木生成。Scratch 内置素材不重复复制到本地 AI 素材库，继续由 Scratch 原生素材库管理。
- AI 写积木生成确认后，每张图片成功返回即保存到素材库；后续 VM 导入失败或用户取消时，已生成原图仍保留，供用户重试或再次使用。
- 从 schema version 1 迁移时保留原有 `target`、名称、提示词和图片结果，`tags` 默认为空或填入类别标签，`origin` 默认设为 `image-panel`。迁移必须幂等。

素材库界面需要增加：

1. 一级类别筛选：`全部`、`角色`、`背景`。
2. 标签筛选：可搜索、可多选标签；多个已选标签默认使用 AND 关系。
3. 素材卡片展示标签，并提供添加、删除和重命名标签的入口。
4. 名称和提示词搜索作为辅助筛选，不代替类别和标签筛选。
5. AI 写积木生成的素材展示来源和资源组，支持按组查看同一角色的多个造型。
6. 保留“导入为角色”“导入为背景”“导入到画布”等现有动作；素材库筛选只改变展示，不绕过 Scratch 导入确认。

## 7. 实施设计

### 7.1 后端改造

#### A. schema 和校验

修改：

- `backend/app/schemas/ai_blocks.py`

工作项：

1. 增加 `TargetRef`、`ResourceSource`、五类资源操作和 `ProjectPlan` Pydantic 模型。
2. 在请求 schema 中增加 `scope` 和 `asset_policy`，并使用 strict model 和 `extra='forbid'`。
3. 增加数量、名称、提示词、标签、坐标、尺寸和嵌套深度上限。
4. 校验操作顺序、`op_id` 唯一性、created target 引用和舞台/角色目标约束。
5. 校验 `single_sprite` 不包含新建角色、舞台背景或其他 target 操作；校验 `project` 的目标引用和每个嵌套 `ProgramIR`。
6. 允许模型输出 `scratch_library` 搜索意图，但不允许输出内部 VM 对象；`image_generation` 必须携带生成原因和资源标签。
7. 不把图片二进制放入 AI 结果；资源来源只保存内置库搜索意图、生成参数或前端资产句柄。

建议初始上限：最多 8 个资源操作、每个新角色最多 8 个造型、最多 8 个项目级程序、沿用现有 ProgramIR 的积木和脚本上限。最终数值应以浏览器内存和生图服务吞吐测试为准。

#### B. prompt 和输出流程

修改：

- `backend/app/prompts_ai_blocks.py`
- `backend/app/routes/ai_blocks.py`

工作项：

1. 在系统提示中列出可用资源操作、作用范围和目标引用规则。
2. 明确“添加背景”和“切换已有背景”的差异。
3. 默认要求优先提出 `scratch_library` 搜索意图；只有用户明确要求生成，或请求上下文明确没有匹配时，才允许提出 `image_generation`。
4. 要求先输出资源依赖关系，再输出对应的积木程序。
5. 禁止臆造 VM API、asset ID、target ID、图片 URL 和二进制字段。
6. 对不明确的角色名、背景名、造型数量、素材来源和作用范围继续走需求澄清，不用模型自行猜测。
7. 生成操作必须包含用户可读的原因、提示词和建议标签；后端不得把“允许生成”解释为已获得用户确认。
8. 增加 project plan 的 JSON repair 和 Pydantic 校验；验证失败沿用当前最多三次修复机制。
9. 增加 `project_ready` SSE 事件；保留旧 `program_ready` 的兼容路径。
10. 使用请求 ID、模型层级和断开连接处理，沿用当前 SSE 头和错误码风格。

#### C. provider 策略

第一阶段不要求修改 `AgnesTextProvider` 的流协议。它继续返回文本/推理流，后端解析并校验严格 JSON。

如果未来启用模型原生 ToolCalls，新增的 provider 适配层必须做到：

1. 将 ToolCalls 转换为 `ProjectPlan`，而不是直接调用 VM。
2. 复用相同的 schema、数量限制和 repair 流程。
3. ToolCalls 失败时能回退到普通 JSON 生成。
4. 对外 SSE 仍统一为 `project_ready`，不让前端依赖某个模型供应商的工具格式。

### 7.2 前端快照和请求上下文

修改：

- `frontend/src/ai-blocks/target-context.ts`
- `frontend/src/ai-blocks/ai-blocks-api.ts`

工作项：

1. 保留现有 target snapshot，增加用户选择的 `scope`、`asset_policy` 和项目级 target 清单：target 逻辑名称、是否舞台、造型名称、当前造型和当前编辑 target。
2. 在 project scope 下增加舞台背景、全部 sprite 和程序摘要；在 single sprite scope 下只发送当前 sprite 的必要上下文。
3. 增加本地 Scratch library manifest 适配器，读取已打包的 `sprites.json`、`costumes.json`、`backdrops.json` 和标签，按自然语言做候选匹配。
4. 增加 project revision 和 target ID 集合，作为资源计划的 apply 前置条件。
5. 增加 `AiProjectPlan` 类型和 `project_ready` 事件解析。
6. 对未知事件、缺少计划、无效操作、范围越权和过大 payload 使用明确错误码。
7. 对旧 `program_ready` 结果归一化为 block-only project plan，避免 UI 分裂为两套生成状态机。

不把完整图片数据放入 target context，也不把本地 IndexedDB 中的图片内容上传给文本模型。

### 7.3 Scratch bridge 和资源 adapter

修改：

- `frontend/src/scratch/scratch-gui-bridge.ts`
- `frontend/src/scratch/asset-adapter.ts`
- 必要时扩展 `frontend/src/env.d.ts`

工作项：

1. 将 `beginBlockGeneration` 抽象为可覆盖资源和积木的项目变更锁，保留旧方法兼容调用。
2. 增加定向的 `commitCostumeImport(targetId, costume)`，内部负责 storage asset、md5ext、`vm.addCostume` 和导入后校验。
3. 增加 `selectTargetCostume(targetId, name)` 或等价的受控方法，只允许按快照中的目标和造型选择，不暴露任意对象访问。
4. 保留并复用 `commitSpriteImport`；为它增加位置、大小和创建操作 ID 的结果映射。
5. 将背景导入统一经过 adapter，继续使用 `vm.addBackdrop`，校验 PNG、分辨率、尺寸和资产 ID。
6. 增加受控的 Scratch 内置素材选择/导入入口：候选来自 Scratch GUI 已打包的 library manifest，选择和导入不调用生图。
7. 优先复用 Scratch GUI 已有的 library 数据和导入处理，不复制一套替代素材；如果打开原生 library 弹窗，必须让选择结果先返回给 AI 流程，再在统一 Commit 阶段写入 VM，不能绕过项目事务直接修改。
8. 对角色、造型和背景的 asset 规格使用同一套 PNG/尺寸校验；当前 Scratch bitmap costume/backdrop 仍按 bitmapResolution 2 处理。
9. bridge 只暴露语义方法，不让 AI runner 直接修改 `runtime.targets`、`sprite.costumes` 等内部集合。
10. 补全 VM 类型声明，尤其是 target 的 `getCostumes`、`setCostume`、`postSpriteInfo` 和资源元数据。

角色位置和初始大小优先通过 `addSprite` 的导入对象设置。对已有角色的位置、可见性和方向修改不放入第一阶段，避免增加另一组项目属性事务。

### 7.4 项目资源执行器

新增建议文件：

- `frontend/src/ai-blocks/project-resource-runner.ts`

职责：

1. 校验本地 `ProjectPlan`，不信任后端已经做过的校验。
2. 检查请求 scope 和 asset policy，解析 current/stage/existing/created target 引用。
3. 先在 Scratch 内置库 manifest 中匹配候选；对 `scratch_library` 操作展示候选并等待用户选择，选择阶段不得直接修改 VM。
4. 如果复用原生 library 弹窗，必须把选中的 library item 作为待提交结果返回；不能让原生回调在项目 backup 之前直接写入 VM。
5. 对 `image_generation` 操作显示生成原因和确认框；用户未确认时不能调用生图。
6. 只有生成结果返回后才创建 PNG Blob，并立即用 `saveGeneratedAsset` 保存到带标签的素材库。
7. 资源准备阶段不修改 VM；在提交前再次检查 project revision 和 target topology。
8. 保存一个 SB3 backup，获取项目变更锁并停止运行中的项目。
9. 按拓扑顺序串行执行资源操作：已选内置角色、生成角色及造型、已有角色造型、背景、背景选择。
10. 收集实际 target ID、实际名称和 asset ID，供后续 block program 解析。
11. 调用可复用的 block runner lower-level apply 方法，将程序写入正确 target。
12. 对每一步发送统一进度：匹配内置素材、等待选择、确认生成、生成资源、保存素材、导入角色、添加造型、添加背景、写入积木、完成。
13. 失败或取消时恢复 backup；成功时保存 `ProjectApplyResult` 供撤销，但不删除素材库中已经生成的原图。

建议将 `generation-runner.ts` 中的“编译程序”和“注入程序”拆成可复用的低层函数。不要让资源执行器嵌套调用会再次保存 backup 或获取生成锁的高层 `applyAiProgram`，否则会出现双重事务和错误的撤销边界。

### 7.5 AI 写积木 UI

修改：

- `frontend/src/ai-blocks/AiBlocksPanel.vue`
- 必要时新增资源计划预览组件。

工作项：

1. 增加“作用范围”下拉框，选项为 `单角色` 和 `整个项目`；当前 target 是舞台时禁用 `单角色`。
2. 增加素材来源策略控件，默认 `优先使用 Scratch 内置素材`，并显示当前计划使用的来源。
3. 新增资源计划预览状态，显示操作类型、目标、名称、造型数量、内置候选、生图原因、图片提示词、尺寸、标签和积木摘要。
4. 对内置素材提供“打开 Scratch 素材库并选择”入口；对没有内置匹配的资源提供“是否确认生成”入口。
5. 生成确认未通过前不调用 `/api/image/generate`；生成成功后显示保存标签，并写入素材库。
6. 资源计划状态下提供逐项重试和取消，不直接把模型生成结果静默写入 VM；透明化和抠图通过“转到画布处理”交给用户。
7. 当资源操作超过限制、目标有歧义或资源名称冲突时，在确认前显示阻塞错误。
8. 复用现有生成进度、锁定 Scratch 编辑器、取消和撤销反馈。
9. block-only 请求保持当前交互和文案，不让资源 UI 影响简单积木生成。
10. 完成后报告实际名称、实际来源、素材库标签和导入结果，因为 VM 可能自动为重复名称追加后缀。

### 7.6 素材库实现

修改：

- `frontend/src/ai/asset-library-store.ts`
- `frontend/src/ai/AssetLibraryDialog.vue`
- `frontend/src/ai/ImageGenerationPanel.vue`
- `frontend/src/app/App.vue`

工作项：

1. 将 IndexedDB 数据 schema 从 version 1 升级为 version 2，迁移旧记录时保留原有 `target` 两类，并补齐 `tags`、`origin` 和可选 `groupId`。
2. 扩展 `saveGeneratedAsset`，允许 AI 写积木传入资源名称、标签、来源和角色资源组；图片面板继续使用默认来源和自动标签。
3. AI 写积木每次生成成功后都保存原图；保存失败要单独提示，不得阻断用户对 VM 的判断，且不能把未确认的生成请求写入素材库。
4. 素材库顶部增加 `全部/角色/背景` 类别筛选、标签搜索和多选标签筛选；多个标签默认使用 AND 关系。
5. 卡片显示标签、来源和资源组，并支持添加、删除和修改标签；用户编辑标签后立即持久化。
6. 保留现有“导入为角色”“导入为背景”“导入到画布”动作，筛选不改变导入目标。
7. 对同一 AI 角色的多个生成造型使用同一 `groupId`，支持按组查看；第一版不要求从组一次性恢复为多造型角色，除非单独完成组导入交互。
8. Scratch 内置角色、造型和背景不复制到 AI 素材库，仍由 Scratch 原生 library 管理；素材库只管理生成或用户明确保存的本地 AI 资产。

## 8. 事务、并发与数据安全

### 8.1 两阶段执行

采用“准备后提交”的两阶段模型：

**Prepare 阶段**：

- 解析和校验 ProjectPlan、`scope` 和 `asset_policy`；
- 在 Scratch 内置 manifest 中匹配候选，并让用户选择内置资源；选择阶段只产生待提交的资源句柄，不修改 VM；
- 对 `image_generation` 显示生成原因和确认框，只有确认后才调用现有生图 API；
- 校验响应为允许的 PNG，校验尺寸、目标类型和请求 ID；
- 将响应转换为内存 Blob，并立即用带标签的元数据保存到本地素材库；
- 用户需要抠图时，把 Blob 交给无边画布，等待用户处理后的结果；
- 除素材库写入外，不修改 VM，不增加 target，不增加 costume。

**Commit 阶段**：

- 检查 snapshot revision、目标 ID、作用范围和名称解析结果；
- 保存一次 SB3 backup；
- 获取项目变更锁，停止项目运行；
- 按用户确认的内置候选或已准备的生成/画布结果串行导入资源并验证；
- 写入 block programs；
- 刷新 workspace、舞台和 target 选择；
- 写入结果和可撤销记录。

这样可以避免生图网络等待期间已经产生半成品 VM 状态，也能在用户修改项目后拒绝过期计划。

### 8.2 回滚策略

优先使用整项目 SB3 回滚，逐项删除只作为资源导入过程中的快速补偿：

1. 新角色导入失败时，先尝试删除明确识别的新增 target。
2. 新造型、背景或积木任一步失败时，直接加载 commit 前的 backup。
3. backup 加载后重新选中原 target，恢复代码页和 workspace。
4. backup 恢复失败时，将 bridge 置为 fatal 状态，并提示重新加载最近保存的项目，沿用当前角色导入的失败语义。
5. 撤销必须检查 revision；用户已经继续编辑时，不覆盖后续编辑。

### 8.3 并发约束

- 同一时间只允许一个项目级 AI 变更。
- 生图请求可以在 Prepare 阶段按资源顺序串行，第一版不追求并行，以便控制成本和顺序；后续可在不修改 VM 的前提下并行准备。
- Commit 阶段禁止用户操作 Scratch 编辑器，沿用现有 `inert` 和 busy 状态。
- 生图请求取消后，不能继续消费已经过期的资源计划。

## 9. 分阶段实施任务

### Phase 0：能力验证和契约冻结

交付：

- 用固定 PNG 和 Scratch 内置素材在真实 VM 中验证新增角色、多个造型、已有角色加造型、新增背景、切换已有背景、SB3 导出和重新加载。
- 确认当前浏览器和 Scratch VM 对 asset metadata、bitmapResolution、rotationCenter 的实际值。
- 验证 AI 写积木页面的 `单角色/整个项目` 范围规则和舞台 target 下的禁用行为。
- 验证 Scratch 内置 library manifest、角色/造型/背景标签和原生导入回调可以在本地使用。
- 确认透明化、抠图、裁切和像素处理全部由用户在画布中完成，AI 模块只保存和导入原图或处理后的结果。
- 冻结 ProjectPlan v1、资源上限、1x1 生图策略和素材库 schema version 2。

通过条件：固定图片可以在不使用后端 ToolCalls 的情况下完成全部资源操作，且导出再加载后资产仍在。

### Phase 1：补齐前端资源执行能力

交付：

- bridge 的项目锁、已有角色加造型、已有背景切换和内置 library 选择封装；
- adapter 的统一资源校验；
- `single_sprite/project` 快照和范围校验；
- project-resource-runner 的本地固定资源执行；
- 素材库 schema 2 迁移、标签读写和筛选基础能力；
- 资源导入和失败回滚单元测试。

通过条件：不依赖 AI，前端可以用固定 ProjectPlan 完成资源操作和整项目恢复。

### Phase 2：接入后端 ProjectPlan

交付：

- 后端资源 schema、限制和引用校验；
- prompt、JSON repair、`project_ready` SSE 事件；
- 前端 ProjectPlan 类型和 SSE 解析；
- block-only 兼容测试和后端非法计划测试。

通过条件：模型返回的计划只能落在白名单操作内；非法字段、未知操作、错误引用和超限 payload 全部被拒绝。

### Phase 3：接入内置素材、生图确认和标签素材库

交付：

- AI 写积木作用范围和素材来源控件；
- Scratch 内置角色、造型、背景候选展示和选择结果回传；
- 生图前确认、拒绝、重试和多资源进度；
- 复用 `generateImage` 和当前图片响应校验；
- AI 写积木生成结果写入素材库，保存角色/背景类别、来源、资源组和标签；
- 素材库类别筛选、标签筛选和标签编辑；
- 生成结果转入画布处理的手动交接入口。

通过条件：一次自然语言请求可以生成资源计划，用户确认后导入资源并执行脚本；生图失败不污染 VM。

### Phase 4：事务、撤销和 SB3 往返

交付：

- 统一项目 backup；
- 资源和 block program 的原子式提交与恢复；
- revision 保护和撤销；
- 导出、刷新、重新加载后的资产回归测试。

通过条件：在角色、造型、背景或积木任一步故意失败时，项目都能恢复到生成前状态；成功后撤销只撤销本次变更。

### Phase 5：体验和能力扩展

候选项：

- 素材库资源组一次性恢复为带多个造型的角色；
- 新角色多 target 脚本和跨角色消息；
- 生图请求并行准备；
- 更细的标签体系、标签推荐和批量标签编辑；
- 2x2 候选图、参考图或角色造型一致性。

自动抠图、透明化和像素修复不属于 AI 写积木模块的后续目标，仍由无边画布负责。

这些能力必须建立在 ProjectPlan v1 的资源执行边界上，不能通过放宽为任意 ToolCall 来实现。

## 10. 测试与验收

### 10.1 后端单元测试

新增或扩展 `backend/tests/test_ai_blocks.py`：

- 合法 `add_builtin_sprite`、`add_sprite` 多造型、`add_costume`、`add_backdrop`、`select_backdrop` 计划可以通过。
- `single_sprite` 和 `project` 范围规则可以通过，范围不一致或越权操作被拒绝。
- 默认 `builtin_first` 计划优先输出 `scratch_library`，生成计划带有原因、提示词和标签。
- 未知操作、额外字段、任意 URL、base64、JavaScript 和 VM 方法名被拒绝。
- 舞台/角色目标错配被拒绝。
- created target 引用不存在、重复或形成循环时被拒绝。
- 超过资源、造型、标签、名称、提示词和程序数量上限时被拒绝。
- block-only 旧请求仍返回原有 ProgramIR 语义。
- ProjectPlan JSON 解析失败时会进入 repair，连续失败返回明确错误码。

### 10.2 前端单元测试

新增或扩展：

- `frontend/tests/asset-adapter.test.ts`
- `frontend/tests/generation-runner.test.ts`
- 新增 `project-resource-runner.test.ts`
- 新增或扩展 `target-context.test.ts`
- `frontend/tests/ai-blocks-api.test.ts`
- `frontend/tests/asset-library-store.test.ts`

重点覆盖：

- 一个角色导入多个造型，名称、asset ID、bitmapResolution 和旋转中心校验。
- 已有角色定向添加造型，舞台目标被正确拒绝。
- 新增背景后自动选中，已有背景按名称选择，歧义名称被拒绝。
- 资源操作依赖顺序和 created target 引用解析。
- `single_sprite/project` 下文案、请求字段和执行权限一致。
- 有内置候选时不调用生图；无候选或用户明确要求时，生成确认前不调用生图。
- 生成成功的角色和背景写入素材库，旧记录迁移后标签、类别和来源保持正确。
- 多标签筛选使用 AND 关系，标签编辑立即持久化。
- 生图准备失败时 VM 完全不变。
- 角色已创建、背景已添加、造型已添加或积木注入失败时均能恢复 backup。
- 取消和 revision 变化不会覆盖用户修改。
- 画布处理不由 AI 资源执行器自动调用或修改像素。
- 旧的 block-only 生成、取消、撤销行为不回归。

### 10.3 E2E 验收场景

1. 选择 `单角色`，为当前角色添加内置造型，确认只影响当前角色。
2. 选择 `单角色` 后请求新增角色或修改背景，确认 UI 提示切换为 `整个项目`，不执行越权操作。
3. 选择 `整个项目`，从 Scratch 内置角色库选择角色，确认不产生 `/api/image/generate` 请求。
4. 创建一个带两个内置或生成造型的新角色，确认角色列表、造型列表和舞台渲染结果。
5. 内置素材没有匹配时，确认出现“是否生成”提示；未确认前不产生生图请求。
6. 用户确认生成后，确认角色或背景写入素材库，类别、来源、资源组和标签正确。
7. 素材库按 `角色/背景` 和多个标签筛选，确认筛选结果正确且标签修改可保留。
8. 为当前角色添加一个生成造型，确认当前 target 和造型选中状态。
9. 生成并添加舞台背景，确认背景数量、当前背景和 SB3 往返。
10. 切换已有背景，确认不会产生新的图片请求和额外背景。
11. 混合执行“新增角色 + 角色脚本 + 新背景”，确认脚本写入正确 target。
12. 在每个资源导入阶段故意注入失败，确认项目恢复到变更前，但素材库中的生成原图仍在。
13. 生成完成后手动修改项目，再点击撤销，确认系统拒绝覆盖后续修改；确认透明化需由用户转到画布处理。
14. 确认编辑器锁定、取消、错误提示和重新加载后的可用性。

建议验证命令：

```powershell
npm --prefix frontend run test:unit -- --run
npm --prefix frontend run build
npm --prefix frontend run test:e2e
backend\.venv\Scripts\python.exe -m pytest backend\tests
```

## 11. 风险与决策点

| 风险或决策 | 影响 | 建议 |
| --- | --- | --- |
| 作用范围选择不清 | 可能把单角色请求误应用到整个项目 | 面板强制选择 `单角色/整个项目`，请求和 ProjectPlan 双重校验，范围越权直接停止 |
| Scratch 内置候选匹配不准 | 可能误进入生图流程或让用户找不到素材 | 前端使用本地 manifest 和标签做候选，保留原生 library 手动选择，并把生成作为明确 fallback |
| 用户误触发生成 | 增加成本，也可能产生不需要的资产 | 生图必须显示原因、数量、提示词、尺寸和标签并等待确认；内置素材选择不调用生图 |
| 透明度和抠图需求 | 角色原图可能带背景 | AI 写积木只负责生图和导入；转到无边画布由用户手动处理，不把自动抠图塞入本模块 |
| 生成素材库数据迁移 | 旧记录没有标签和来源 | IndexedDB schema 升级到 version 2，迁移幂等，保留原有 `target` 类别 |
| 标签过多或不一致 | 筛选结果不稳定 | 自动标签与用户标签分开，去重、限长、限量；类别保留 `角色/背景`，语义标签用于细分 |
| 多造型视觉一致性不足 | 同一角色不同造型可能差异较大 | 第一阶段使用共享角色描述和风格提示；参考图/种子放到后续 |
| VM 私有字段变化 | 直接访问 runtime 内部集合可能失效 | 所有操作集中在 bridge，升级 VM 时运行能力验证和 SB3 往返 |
| 模型生成错误引用 | 可能写入错误 target | 只允许逻辑引用，前后端双重校验，名称歧义即停止 |
| 生图成本和耗时 | 多造型请求时间变长 | 第一版串行、明确进度和取消；后续只优化 Prepare 阶段并行 |
| 资源和积木部分成功 | 项目状态不一致 | 一个 SB3 backup 覆盖整个项目提交边界 |
| ToolCalls 供应商差异 | 升级 provider 成本高 | ToolCalls 只作为内部可选输入，统一转换为 ProjectPlan |
| 旧文档中的 2x2 描述 | 需求和实现不一致 | 以当前代码的 1x1 契约为准，若要 2x2 先单独改生图协议 |

本轮已确定的产品规则：

1. AI 写积木页面增加 `单角色/整个项目` 作用范围选择；用户选择的范围是硬边界。
2. AI 写积木模块只负责生图和导入；透明化、抠图、裁切和像素处理交由用户在无边画布中手动完成。
3. 素材来源默认优先 Scratch 内置角色、造型和背景；内置库没有匹配，或用户明确要求生成时，才允许进入生图确认。
4. 生成的角色、造型和背景保存到本地素材库，并支持角色/背景类别和多标签筛选。

仍需在 Phase 0 细化的实现决策：

1. 原生 Scratch library 是采用“选择结果先返回、Commit 阶段导入”，还是在 backup 保护下监听原生导入回调；第一方案优先。
2. 多个生成造型的素材库是否需要支持按 `groupId` 一次性恢复为一个多造型角色；第一版只要求按组查看。
3. 用户在画布处理后的结果是否回写为同一素材记录的派生版本，还是创建新的素材记录；建议保留原图并创建派生记录。

## 12. 完成定义

本功能完成时必须同时满足：

- Scratch VM 资源能力通过 bridge 的语义 API 使用，前端没有散落的内部 target 修改。
- AI 写积木页面可以明确选择 `单角色/整个项目`，并且请求、计划和执行结果遵守同一范围边界。
- 默认优先使用 Scratch 内置角色、造型和背景；选择内置素材不会调用生图。
- 后端输出只能是经过严格校验的 ProjectPlan/ProgramIR，不包含任意函数调用、图片二进制或外部 URL。
- 只有用户明确要求或内置素材没有匹配时，生成确认通过后才调用生图。
- 新角色、角色造型和舞台背景可以被添加、验证、渲染、导出和重新加载。
- 每张 AI 生成角色、造型和背景图片都保存到带来源、类别、资源组和标签的本地素材库，素材库支持类别和多标签筛选。
- 透明化、抠图、裁切和像素处理由用户在无边画布中完成，AI 资源执行器不自动改图。
- 资源和积木的混合操作有统一的确认、锁定、取消、失败恢复和撤销行为。
- 旧的 AI block-only 生成流程和现有图片导入流程保持兼容。
- 关键单元测试、后端测试、构建和 E2E 验收全部通过，并记录实际 revision、target ID、资源 ID、标签、导出往返和回滚结果。

## 13. 维护规则

- 修改 ProjectPlan、ResourceOp、TargetRef、ResourceSource、scope 或 asset policy 时，必须同步更新后端 schema、prompt、前端类型、双端测试和文档示例。
- 修改素材库记录时，必须同步更新 IndexedDB 迁移、标签筛选、标签编辑和旧记录兼容测试。
- 修改 Scratch VM、Scratch GUI、storage 或 npm 版本时，必须重新运行 Phase 0 的固定资源验证、前端构建、E2E 和 SB3 往返测试。
- 不允许为了让单个模型输出通过而放宽任意操作、任意 target ID 或任意 URL 的白名单。
- “VM 自动修复后可以运行”不能替代源计划校验；所有修复都必须落在显式的 schema 或 adapter 逻辑中。
- ToolCalls 如果被引入，只能作为模型到 ProjectPlan 的受控转换层，不能成为绕过前端 bridge、素材来源策略和用户确认的执行通道。
- AI 写积木不负责透明化、抠图和像素处理；相关能力只在无边画布模块维护。
