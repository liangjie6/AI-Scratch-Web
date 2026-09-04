# Scratch VM 研究报告：让 AI 高效、稳定、节省 Token 地生成整个 Scratch 项目

**文档状态**：研究报告（基于源码勘察）
**适用仓库**：`D:/ywj/AI-Scratch-web/`
**研究对象**：`frontend/node_modules/@scratch/scratch-vm@15.0.1` 源码（`src/` 目录），并与当前工程前端实现（`frontend/src/ai-blocks/*`、`frontend/src/scratch/*`）和后端实现（`backend/app/**`）交叉验证

---

## 0. 摘要

1. Scratch VM 已具备 AI 生成整个项目所需的全部程序化入口：`addSprite`、`addCostume`、`addBackdrop`、`deleteSprite`、`renameSprite`、`shareBlocksToTarget`、`setEditingTarget`、`setVariableValue`、`loadProject`、`saveProjectSb3`，以及最关键的底层 `target.blocks.createBlock/moveBlock`（绕过 Blockly 直接落积木）。问题不在"能不能"，而在"如何安全、可回滚、省 Token 地把 AI 意图翻译成这些调用"。
2. Token 效率的关键结论（实测，见第 8 章）：**永远不要把 SB3 项目 JSON 或 VM 块图交给大模型**。当前工程的 `ProgramIR`（紧凑结构化中间表示，配合前端 `block-catalog` 编译）在目标级比 SB3 JSON 小约 2.4 倍；对含 15 块脚本的 3 目标项目，输入侧可从约 3.5k token 降到约 1k token 量级。真正的大头是"结构样板"（id/next/parent/shadow/topLevel/资源元数据），而非积木语义本身。
3. 稳定性结论：当前工程已有的"后端严格 schema + repair（默认最多 3 次尝试）→ 前端两阶段执行（Prepare/Commit）→ SB3 备份回滚 + 补偿日志"的骨架是正确的，本次重构应在其上增加**模块级流水线状态机**与**部分成功语义**，而不是另起炉灶。
4. 用户示例中的 Pico、Star 均存在于此工程打包的 Scratch 内置角色库（`public/assets/scratch/libraries/sprites.json`，共 343 个角色；Pico 标签 fantasy/drawing，Star 标签 shapes/space），因此示例流程应走"内置库直接导入"通道，不触发图像生成。

---

## 1. 研究范围与方法

- **VM 版本**：`@scratch/scratch-vm@15.0.1`（`frontend/package.json` dependencies）。
- **研读文件**（`frontend/node_modules/@scratch/scratch-vm/src/`）：
  - `virtual-machine.js`（67 KB，VM 门面，全部程序化入口）
  - `engine/blocks.js`（55 KB，积木集合与事件处理，`createBlock/moveBlock` 所在）
  - `engine/runtime.js`（100 KB，运行核心）
  - `engine/target.js`、`sprites/sprite.js`、`sprites/rendered-target.js`（目标模型）
  - `serialization/sb3.js`（61 KB，SB3 project JSON 序列化/反序列化）
  - `serialization/deserialize-assets.js`、`import/load-costume.js`（资源加载）
  - `engine/sequencer.js`、`engine/execute.js`、`engine/thread.js`（执行模型）
- **交叉验证**：`frontend/src/ai-blocks/program-ir.ts`、`program-compiler.ts`、`generation-runner.ts`、`ai-blocks-api.ts`、`project-plan.ts`、`target-context.ts`、`scratch/scratch-gui-bridge.ts`、`scratch/asset-adapter.ts`、`ai/scratch-library.ts`；`backend/app/schemas/ai_blocks.py`、`backend/app/routes/ai_blocks.py`、`backend/app/orchestration/orchestrator.py`。
- **测量**：用真实序列化格式构造样本程序，对比 SB3 JSON、ProgramIR、行式 DSL 三者的字符数与估算 token 数（第 8 章）。

---

## 2. Scratch VM 架构总览

```
┌─────────────────────────────────────────────────────────────┐
│  scratch-gui（React / Redux，本工程为 Vue + 自有宿主）        │
│  Blockly 工作区 │ 舞台渲染 │ 素材库 │ AI 面板（本工程）        │
└──────────────┬──────────────────────────────────────────────┘
               │ 事件流（blocklyListen / targetsUpdate / …）
┌──────────────▼──────────────────────────────────────────────┐
│  VirtualMachine（VM 门面：addSprite / addCostume /           │
│  addBackdrop / loadProject / saveProjectSb3 / greenFlag …）  │
│   ├─ runtime（Runtime：targets、monitorBlocks、线程调度）      │
│   │    ├─ RenderedTarget[]（舞台 + 精灵，含 blocks/variables）│
│   │    ├─ Sprite（costumes/sounds，克隆体来源）               │
│   │    ├─ Blocks（块图：id → block，scripts 保序列表）         │
│   │    ├─ Sequencer/Thread（逐 block 执行）                   │
│   │    └─ IO Devices（键盘/鼠标/用户数据/时钟/云）             │
│   ├─ ExtensionManager（扩展积木按需加载）                      │
│   └─ storage（scratch-storage：asset 存取，本项目绑定 IndexedDB）│
└──────────────────────────────────────────────────────────────┘
```

要点：

- **VM 是唯一事实来源**。GUI 的 Blockly 工作区只是"视图"，它对块的任何创建/移动/删除都以事件形式（`blocklyListen`）交给 VM 的 `Blocks` 集合处理，然后反向广播 `emitWorkspaceUpdate` 刷新工作区。因此 AI 直接调用 `target.blocks.createBlock` 写入块图，再让 GUI 刷新，即可"积木出现在工作区"。
- **本工程已绕过 GUI 的 Blockly 层**：`generation-runner.ts` 内部直接用 `target.blocks?.createBlock(...)` 与 `target.blocks.moveBlock(...)` 注入块图（`generation-runner.ts:274-289`），这是"动态注入进 VM 并实时展现"的现成机制。
- VM 不依赖渲染器也能构建项目（可 headless 验证），渲染由 scratch-render 完成；本工程通过 scratch-gui 的宿主加载全部子系统。

---

## 3. 数据模型

### 3.1 Runtime（`engine/runtime.js`）

- `runtime.targets`：`RenderedTarget[]`，index 0 恒为舞台（Stage isStage=true），其余为精灵；`runtime.getTargetForStage()` / `getTargetById(id)` 为常用查找（本工程 bridge 已在用）。
- `runtime.monitorBlocks`：监控块（变量/列表监视器）的独立 `Blocks` 集合。
- `runtime.executableTargets`：按 `layerOrder` 排序的可执行目标，供舞台绘制分层。
- 事件：`PROJECT_CHANGED`、`TARGETS_UPDATE`、`MONITORS_UPDATE`、`BLOCKSINFO_UPDATE`、`PROJECT_RUN_START/STOP` 等，VM 构造器里逐一向外转发（`virtual-machine.js:46-176`）。

### 3.2 Target / RenderedTarget / Sprite

每个目标（`engine/target.js`、`sprites/rendered-target.js`）：

| 字段 | 说明 |
| --- | --- |
| `id` | 运行期唯一 ID，由 VM 生成；**AI 不得臆造或持久化** |
| `isStage` | 舞台标记；舞台无位置/大小/方向，有 tempo/video 等属性 |
| `name` | 显示名；VM 有唯一性规则（同名自动追加后缀），见 `installTargets` 中的 `renameSprite`（`virtual-machine.js:555`） |
| `variables / lists / broadcasts` | 三种 `Variable` 类型（SCALAR/LIST/BROADCAST_MESSAGE_TYPE）；变量有全局/局部（sprite-local）语义 |
| `blocks` | `Blocks` 集合（见 3.3） |
| `comments` | 注释集合（可选） |
| `costumes / sounds / currentCostume` | 资源数组与当前造型索引；`currentCostume` 越界会被 clamp（`sb3.js:550-557`） |
| `visible / x / y / size / direction / draggable / rotationStyle` | 精灵属性（精灵专属） |
| `volume` | 音量（两态都有） |
| `layerOrder` | 层序（序列化时存在，运行时排序后删除） |

`Sprite`（`sprites/sprite.js`）持有造型/声音资源列表与克隆体表；`RenderedTarget` 绑定绘制信息（用户可见）。

### 3.3 Blocks 集合（`engine/blocks.js`）

核心结构：`_blocks: Record<blockId, block>`（自底向上的块图），`_scripts: blockId[]`（顶层堆栈头，**保序**），`blockOrder` 用于 GUI 渲染顺序。

单个 block（本工程 `program-compiler.ts:284-294` 的 `VMBlock` 与 VM 内部完全一致）：

```
{
  id: string;            // 全局唯一；本工程默认 "ai_<scriptId>_b<ordinal>"
  opcode: string;        // 如 event_whenflagclicked
  inputs: Record<string, {name, block: string, shadow: string|null}>;
  fields: Record<string, {name, value, id?, variableType?}>;
  next: string|null;     // 顺序后继
  parent: string|null;   // 父块（topLevel 时为 null）
  topLevel: boolean;     // 是否为堆栈头（且 !shadow）
  shadow: boolean;       // 是否为影子块（下拉/数值占位）
  x, y?: number;         // topLevel 才有意义
  mutation?, comment?, isMonitored?;  // 可选
}
```

**Blocks 集合方法面**（本报告关注的部分）：

| 方法 | 位置 | 用途 |
| --- | --- | --- |
| `getBlock(id)` / `getScripts()` | `blocks.js:113/121` | 读块图；`getScripts` 返回顶层脚本头列表（保序） |
| `getTopLevelScript(id)` | `blocks.js:213` | 由任意块定位所在脚本头（校验/删除用） |
| `createBlock(block)` | `blocks.js:590` | **直接落块**；topLevel 且非 shadow 时自动 `_addScript`；触发 `emitProjectChanged` |
| `moveBlock(args)` | `blocks.js:748` | 重新连接（`newParent/newInput`），接续/嵌套 |
| `changeBlock(args)` | `blocks.js:616` | 改字段（变量/列表/广播字段需在 target 已建变量且 id 合法） |
| `deleteBlock(id)` / `deleteAllBlocks()` | `blocks.js:871/912` | 删除块；删除时连带修正引用（本工程 `removeAiScripts` 在用，`generation-runner.ts:258`） |
| `blocklyListen(e)` | `blocks.js:301` | GUI 事件入口（本工程未用，仅作对照） |
| `resetCache()` | `blocks.js:567` | 清运行时缓存（`createBlock/moveBlock` 内部自动调用） |

**Shadow（影子块）机制（关键难点）**：

- 每个 `inputs[name]` 由 `{block, shadow}` 组成。普通数值/文本/下拉输入需要**一个影子块**（`shadow`，如 `math_number`、`text`、`math_positive_number`、`math_angle`、`colour_picker`、`data_variable`、`data_listcontents`、`event_broadcast_menu`）。
- 当输入被真实 reporter 函数块覆盖时：`block != shadow`（被遮挡）；当直接填数时：`block === shadow`（未被遮挡，两者同一）。
- 序列化时压缩为三种形态（`sb3.js:39-41`）：`[1, blockId]`（同块）、`[2, blockId]`（无 shadow）、`[3, blockId, shadowId]`；纯原始块（如 `[4, 10]` 数字、`[10,"你好"]` 文本）会内联进 `inputs`（`compressInputTree`，`sb3.js:346-376`）。
- **缺失 shadow 的块在 GUI 区会渲染为"破损块"或输入框丢失**，所以 AI 编译出的块图必须带全 shadow（本工程 `compileProgramIR` 已自动补齐：`program-compiler.ts:399-451`）。

### 3.4 变量 / 列表 / 广播

- 三者均为 `engine/variable.js`，类型由 `Variable.SCALAR_TYPE/LIST_TYPE/BROADCAST_MESSAGE_TYPE` 区分。
- 变量 id 是**运行期稳定引用**，`fields.VARIABLE.id` 必须指向已存在的变量；`changeBlock` 会按 id 反查名称（`blocks.js:616-648`）。
- 序列化时压缩为密集格式（`serializeVariables`，`sb3.js:480-531`）：`variables` 为 `{id: [name, value, isCloud?]}`，`lists` 为 `{id: [name, value]}`，`broadcasts` 为 `{id: name}`。这在 `ProjectManifest.shared_state` 中已是同样的结构（先声明、后引用）。
- 监视器（monitor）由 scratch-gui 控制与存储，AI 阶段不需要生成（除非明确要变量面板显示）。

---

## 4. 序列化：SB3 的"全量"语义

### 4.1 project.json 结构（`serialization/sb3.js`）

顶层：`{targets[], monitors[], extensions, meta}`。每个 target（`serializeTarget`，`sb3.js:533-589`）含：`isStage,name,variables,lists,broadcasts,blocks,comments,currentCostume,costumes,sounds` 及目标类型专属属性（舞台：`tempo,videoTransparency,videoState,textToSpeechLanguage`；精灵：`visible,x,y,size,direction,draggable,rotationStyle`；两者：`volume,layerOrder`）。

### 4.2 资源字段格式（`serializeCostume`，`sb3.js:424-441`）

```
costume = { name, bitmapResolution, dataFormat, assetId, md5ext,
            rotationCenterX, rotationCenterY }
```

- `md5ext` = `md5(资源字节) + "." + dataFormat`（如 `0123….png`），同时是文件在 zip 中的文件名；`assetId` 通常与 md5 一致。
- PNG 位图造型：Scratch 3 中 `bitmapResolution = 2`（2 倍图）；`rotationCenterX/Y` 为旋转中心像素坐标（造型宽高的 1/2 或语义中心）。
- SVG 矢量造型：`dataFormat = "svg"`，可带 `rotationCenter`。
- 声音：`{name, assetId, dataFormat, format, rate, sampleCount, md5ext}`。

### 4.3 loadProject 的"全清空"语义（`virtual-machine.js:310-367`）

`loadProject` 先经 `scratch-parser` 全量校验，失败时尝试 SB1 转换；成功后 `deserializeProject`（`:490`）先 `this.clear()` **清空整个运行时**，再重建全部 target。意义：

- **整项目加载是原子的**（失败即拒绝，运行时保持旧状态），适合作为"新建项目基线 + 失败回滚"的载体。
- **代价**：全量重建意味着丢失未保存的编辑态（undo 栈、编辑器 UI 选择状态），所以增量打补丁时不应频繁 `loadProject`；仅在"会话级回退"时使用。

### 4.4 目标级导入（`addSprite`，`virtual-machine.js:587-641`）

- 接受 sprite3 JSON（含 `isStage:false` 的 target 对象），内部再经 `scratch-parser`（单 sprite 模式校验），安装后执行重命名唯一化。
- 本工程 `asset-adapter.ts` 已实现"多个造型组装为一个 sprite3 对象 + storage asset 注册 → `vm.addSprite`"的完整链路（`scratch-gui-bridge.ts` 的 `commitSpriteImport`）。

---

## 5. 程序化构建入口总表（VM 15.0.1 实测签名）

| VM 方法 | 位置 | 语义 | 本工程现状 |
| --- | --- | --- | --- |
| `addSprite(input)` | `vm.js:587` | 导入 sprite3 JSON（可带多造型），安装后自动唯一命名 | 已封装（commitSpriteImport） |
| `addCostume(md5ext, costumeObject, optTargetId)` | `vm.js:677` | 给目标加造型；`optTargetId` 缺省为当前编辑目标 | bridge 未暴露（需补） |
| `addCostumeFromLibrary(md5ext, costumeObject)` | `vm.js:705` | 库造型快捷入口 | 未用（自有库通道可替代） |
| `addBackdrop(md5ext, backdropObject)` | `vm.js:1017` | 给舞台加背景并自动切换 | 已封装（importBackdropCostume） |
| `deleteSprite(targetId)` | `vm.js:1066` | 删除角色 | 已封装（补偿删除） |
| `renameSprite(targetId, newName)` | `vm.js:1031` | 改名（自动唯一化） | 已用 |
| `duplicateSprite(targetId)` | `vm.js:1112` | 复制角色 | 未用 |
| `shareBlocksToTarget(blocks, targetId)` | `vm.js:1275` | 跨目标搬运块 | 未用（AI 直接写块图） |
| `setEditingTarget(targetId)` | `vm.js:1251` | 切换编辑目标（GUI 选区） | bridge 已用 |
| `refreshWorkspace()` / `emitWorkspaceUpdate()` | `vm.js:1359/1398` | **通知 GUI 重建工作区**（注入后必须调用） | bridge.refreshWorkspace 已用 |
| `emitTargetsUpdate()` | `vm.js:1375` | 通知角色列表刷新 | bridge 已用 |
| `postSpriteInfo(data)` | `vm.js:1555` | 设置精灵位置/朝向/大小（运行期） | 未用（初始值走导入） |
| `setVariableValue(targetId, variableId, value)` | `vm.js:1575` | 写变量初值 | 未用（初值走 blocks/创建） |
| `loadProject(input)` | `vm.js:310` | 全量加载（含校验+清空） | bridge 已用（备份/回滚） |
| `saveProjectSb3()` | `vm.js:390` | 导出 SB3 Blob | bridge 已用（备份） |
| `greenFlag()` / `stopAll()` | `vm.js:191/221` | 运行/停止 | bridge 已用（"运行查看"） |
| `attachStorage(storage)` | `vm.js:1168` | 绑定存储（资源工作区） | 宿主已绑定 |

**结论**：除"已有角色加造型""按名称切背景""跨目标搬块"外，其余入口本工程均已打通；AI 重构不需要新增 VM 能力，只需把以上入口收敛到 bridge 的语义方法层。

---

## 6. 直接向 VM 注入积木：正确姿势与坑

1. **用 `target.blocks.createBlock` + `moveBlock` 代替 Blockly 事件**（已验证 `generation-runner.ts:274-289`）。每个 block 必须自包含 `id/opcode/inputs/fields/next/parent/topLevel/shadow`；`topLevel && !shadow` 的块会进 `_addScript` 保序列表。
2. **图完整性三重校验**：引用一致（next 指向的块 parent 必须回指）、无环、全部可达（topLevel 起 BFS）。本工程 `validateCompiledGraph`（`program-compiler.ts:324-365`）已实现，AI 注入前必须过一遍。
3. **Shadow 自动补齐**：按 catalog 的 `shadow/shadowOpcode/shadowField/shadowResolver` 生成影子块（`program-compiler.ts:399-451`）；下拉类字段（变量/列表/广播/造型/背景名）必须用 `{name, value, id, variableType}` 且 `variableType` 注明 `''`(变量)/`'list'`(列表)/`'broadcast_msg'`(广播)，否则 GUI 下拉无法回显。
4. **变量先于块创建**：块图里引用 `data_variable` 前，要用 `runtime`/target 的变量创建接口建立变量并取得稳定 id，再写入 `fields.VARIABLE.id`（本工程 `PendingVariable` 机制，`generation-runner.ts:64-72/257-289`）。
5. **注入后必须刷新**：`emitTargetsUpdate()` + `emitWorkspaceUpdate()`（角色列表、工作区重建），否则 GUI 不出现新积木。本工程 `bridge.refreshWorkspace()` 已封装。
6. **舞台/精灵目标差异**：舞台不能有 `motion`/`looks` 的部分积木（catalog 的 `targets` 约束已处理），`ask/wait` 等需要 IO 通道。
7. **topLevel 坐标**：`x/y` 只对 topLevel 有意义，缺省会渲染在 0,0 附近重叠；AI 可给脚本排布 `x/y`（本工程 `positionProgram` 已做）。
8. **不做的事**：不得直接改 `runtime.targets`、`sprite.costumes`、`target.blocks._blocks` 私有字段（升级 VM 即失效）；本工程 bridge 已坚持"只暴露语义方法"。
9. **资源导入前校验**：md5ext 格式（`/^[a-f0-9]{32}\.(png|svg)$/i`，见 `scratch-library.ts:33`）、PNG/SVG 头、尺寸上限、`bitmapResolution`、`rotationCenterX/Y`；SVG 走 scratch-svg-renderer 解析（本工程打包 `@scratch/scratch-svg-renderer`）。
10. **唯一命名**：创建角色后 VM 会把重名自动加后缀，所以 AI 只给"期望名"，前端要回读实际名称并展示、回填后续引用（本工程已有该语义）。

---

## 7. 运行与验证：AI 时代的新问题

- `greenFlag()` 启动线程，`stopAll()` 停止；`control_wait`、`looks_*` 等通过 sequencer 的事件循环执行（`engine/sequencer.js`、`engine/execute.js`）。
- **`sensing_askandwait`（询问并等待）会阻塞线程等待用户回答**：回答通道是 GUI 的 ask 弹窗 → runtime IO userData。因此：
  - 自动化"运行验证"遇到 ask 会挂起，需要注入回答或超时终止；
  - 用户示例（Pico 询问"天上有几颗星星"，回答后说正确/错误）天然是**交互式**的，AI 只能保证脚本正确，运行效果必须由用户点绿旗体验。
- **结论（与用户决策一致）**：AI 负责"编辑器内分批注入可见"（角色/背景/积木陆续出现），**不自动运行演示**；完成态提供"运行查看"按钮由用户触发。若未来做"自动验证"，可按"免交互试运行 + 超时熔断 + 回答注入"单独设计，且需在 Phase 0 验证 ask 的自动化通道是否有公开 API。

---

## 8. Token 效率分析（实测）

### 8.1 样本与测量方法

样本程序：*当绿旗被点击 → 等待 1 秒 → 说"你好"2 秒*（3 块，1 角色）。三种表示：

- **A. SB3 目标级 JSON**：完整 target（含 blocks 原始块 + primitive 内联压缩 + 1 个造型资源元数据）。
- **B. ProgramIR**：本工程现有紧凑 IR（`program-ir.ts`）。
- **C. 行式 DSL**：未来可选的更紧凑文本（每行一条指令）。

### 8.2 实测结果

| 表示 | 字符数 | 估算 token* | 说明 |
| --- | --- | --- | --- |
| SB3 仅积木块 JSON | 464 | ~118 | 含 id/next/parent/shadow/topLevel 样板 + 输入引用 |
| SB3 目标级（连造型元数据） | 932 | ~235 | 资源元数据占大头之一 |
| **ProgramIR** | **392** | **~103** | 比 SB3 目标级小 **2.38 倍** |
| 行式 DSL（示意） | 38 | ~12 | 极端紧凑，但需要新增解析层 |

\* 估算口径：ASCII 字符约 4 字符/token，中文字符约 0.85 token/字（OpenAI 类分词近似，仅为量级比较）。
外推（线性）：15 块脚本的 3 目标项目，SB3 全量约 8.4k 字符（约 3.5k token），ProgramIR 约 6.3k 字符（约 2.6k token）。**纯脚本部分两者接近**，差距主要来自：
1. **结构样板**（每个块的 next/parent/inputs 引用数组）约占总开销一半以上；
2. **资源元数据**（造型/声音数组）不进 IR（资源以 `ResourceSource` 意图描述，二进制绝不出模型）；
3. **全量 project.json 要带 stage、monitors、extensions、meta**，IR 天然只带增量。

### 8.3 三层 Token 成本模型

| 层 | 内容 | 优化手段 | 现状 |
| --- | --- | --- | --- |
| ① 模型输出 | ProgramIR / ProjectPlan | 紧凑结构 + catalog 限制 opcode 白名单 + 确定性 ID + 数量上限 | 已实现 |
| ② 模型输入（上下文） | 项目快照 + 对话历史 | **摘要化**：只发"目标名/造型名/脚本用途/变量名/当前程序摘要"，不发块图（target-context 的 `current_program` 是文本摘要，上限 40k） | 已实现（可再压） |
| ③ 工程往返 | SSE 传输、JSON 包装 | 分段生成（per-target script_ready）+ cache_hit（后端 prompt 级缓存） | 已实现 |

### 8.4 进一步压缩的建议（按收益排序）

1. **Tight-IR（低成本、推荐）**：输入值利用 catalog 推断类型，把 `{kind:'number',value:1}` 压成 `1`、`{kind:'text',value:'你好'}` 压成 `"你好"`。仅输入包装，预计再省 15% 左右输出；编译层 `compileProgramIR` 改动集中、风险低。给 model 的 schema 里用 `oneOf` 表达即可。
2. **共享状态前置**：项目级变量/列表/广播只声明一次（现 `ProjectManifest.shared_state` 已做），各角色脚本引用 id 或短名称。
3. **模块化分段输出**：按模块（背景/角色/脚本）分批生成与校验，失败只重试该段（后端已有 per-phase repair，`orchestrator.py:113-260`），避免整单重来。
4. **上下文自动压缩摘要**（用户已确认）：每轮结束后固化「当前项目结构 + 已执行模块结果」摘要，裁剪早期消息；格式见设计文档第 10 章。
5. **行式 DSL（可选、后续）**：如 `EVENT_FLAG / WAIT 1 / SAY "你好" 2`。收益大（一个订单级请求可省 70%+），但需要：DSL 语法稳定 + 与 ProgramIR 同构编译 + 前端校验错误定位（行号）。**建议 Phase 2 后作为实验性能力，与 IR 并存（模型二选一），不做唯一通道。**
6. **模型分层**：规划/资源清单用高能力模型一次小输出；每个角色脚本用普通模型并行/串行生成（本工程已有 `model_tier`）。

---

## 9. AI 生成整个项目的推荐流程（模块化流水线）

结合用户确认的决策（默认新建项目、仅生图需确认、注入不自动运行、失败停止可重试/对话、模块级撤销+会话级回退、素材缺失跳过继续、自动压缩摘要），推荐流程：

```
用户输入（自然语言）＋ 基线选择（新建项目 / 当前项目）
        │
        ▼
S0 需求理解与规划（模型输出 ModulePlan：模块列表 + 依赖 + 目的）
   · 模块示例：M1 舞台背景 Stars（内置库）
               M2 删除默认角色（精灵列表裁剪）
               M3 导入 Pico（内置库）/ M4 导入 Star（内置库）
               M5 Pico 脚本（位置/绿旗/等待/生成星星/询问判对错）
               M6 Star 脚本（克隆体/排列逻辑）
   · 无"计划确认"步骤（用户决策：仅生图需要确认）
        │
        ▼
S1 模块图执行引擎（前端 + 后端协同）
   · 依赖拓扑序；每模块为独立原子单元（Prepare → Commit）
   · Prepare：内置库匹配（sprites.json/costumes.json/backdrops.json + library-assets）
              → 有候选：直接走内置素材通道（预取资源句柄，不调生图）
              → 无候选：弹"是否生成图像"确认（唯一需确认点）
                  · 确认：调生图 → 存素材库 → 转入 Commit
                  · 拒绝：标记 skipped，继续后续模块（用户决策）
   · Commit：SB3 备份（会话级一次）→ 根锁 → 逐模块写入
              · 资源模块：storage asset → addSprite/addCostume/addBackdrop
              · 积木模块：ProgramIR → compileProgramIR（含 shadow/图校验）→
                target.blocks.createBlock/moveBlock → refreshWorkspace
              · 变量模块：先建变量/列表/广播 → 再写引用块
              · 每模块生成"逆操作日志"（模块级撤销依据）
              · 每模块完成后更新「项目摘要」并广播 SSE 进度
        │
        ▼
S2 失败与重试
   · 任一模块失败：带错误原因 regenerate 该模块（≤3 次，复用后端 repair 语义）
   · 3 次后：停止流水线（之前成功模块全部保留），模块标记 failed
   · UI：失败模块卡可"重试"；对话可继续（"把 Pico 移到右上角" → 追加新模块）
        │
        ▼
S3 完成态
   · 报告实际名称（VM 唯一化后）、来源、模块列表、素材库条目
   · "运行查看"按钮（用户手动）、"撤销此模块" 逐个可撤、"回到会话前"一键回退
```

### 9.1 为什么是"模块原子单元"而不是"整项目原子"

- 用户要求"前面成功的全部保留"，整项目快照无法表达"保留到某一步"的前进态。
- 模块级原子 = 会话级备份（一次 `saveProjectSb3`，内存态，仅回退时用）+ **逆操作日志**（删除新增角色用 `deleteSprite`、删除脚本用 `deleteBlock`、恢复背景选择用 `restoreCostumeSelection`，本工程 `compensateJournal` 已有雏形）。
- 模块级撤销成本：逆操作（O(模块规模)），远低于整包恢复；会话级回退才用备份。

### 9.2 素材双通道决策表

| 场景 | 动作 | 是否需确认 |
| --- | --- | --- |
| 内置库有候选（Pico/Star/Stars 等已实测存在） | 预取 `md5ext` 资源 → storage asset → 导入 | 否 |
| 内置库无候选 | 弹确认（原因/数量/提示词/尺寸/标签） | **是（唯一确认点）** |
| 用户拒绝生成 | 跳过该素材项，继续后续模块 | 否 |
| 用户要求"用已有素材"（素材库/项目内） | 按 `library_asset` 句柄复用 | 否 |
| 名称歧义（如两个同名目标） | 停止该模块，请用户澄清 | 是（澄清式） |

---

## 10. 稳定性与安全边界（延续并强化现状）

1. **白名单**：opcode 只允许 `block-catalog.ts`（前端）与 `scratch_block_catalog.py`（后端）同一份清单；`procedures_*` 等扩展 opcode 一律拒绝（`program-compiler.ts:113-119`）。
2. **严格 schema**：后端 Pydantic strict + `extra='forbid'`（`schemas/ai_blocks.py:25-33`），前端 `CompiledProgram` 校验兜底，双端同一契约。
3. **引用规则**：模型只能输出 `TargetRef`（`current_target/stage/existing_sprite/created_sprite`），禁止 target id/URL/JS/base64（`project-plan.ts`、`schemas/ai_blocks.py:104-128`）。
4. **数量上限**：当前上限（`schemas/ai_blocks.py:128-134`）：resource_ops ≤ 24、program_targets ≤ 12、shared_state variables ≤ 100 等；`PROGRAM_IR_LIMITS`（`program-ir.ts`）：scripts ≤ 12、可见块 ≤ 120、深度 ≤ 12、字符串 ≤ 120。整项目模式需评估 project 级别的同套上限是否够用。
5. **revision 防过期**：提交前校验快照 revision（`target-context.ts` 的 `assertSnapshotIsCurrent`），用户已手动修改则拒绝静默覆盖。
6. **事务**：Prepare 阶段不动 VM；Commit 阶段锁编辑器（inert）+ 停止运行；失败恢复用备份；`loadProject` 恢复后重新选回 target。
7. **重试语义**：生成期失败（schema/repair）与执行期失败（图校验/资源导入/VM 异常）分开计数；均"携带上一个错误原因"进入下一轮（后端 `_repair_messages` 已按此实现）。

---

## 11. 与现有实现的差距（重构要点清单）

| 能力 | 现状 | 重构方向 |
| --- | --- | --- |
| 生成模式 | 一体化 generate-unified（requirements→plan→program 全流程） | 拆为"规划（ModulePlan）+ 按模块执行与修复"的流水线，模块可为空（纯对话不生成） |
| 部分成功 | 全有或全无（回滚到备份） | 模块级原子 + 部分成功 + 逆操作日志 + 会话级回退兜底 |
| 连续对话 | 已有 chat 接口（`/chat` SSE，assistant_delta 等）与历史 | 把"对话"与"流水线执行"统一为会话状态机；对话消息可追加/引用已执行模块 |
| 上下文 | 全量消息 + 项目快照 | 会话摘要自动压缩（用户已确认），摘要格式见设计文档 |
| 素材通道 | 内置库搜索已有；生图确认在资源审核 UI | 内联到流水线模块节点（每模块卡片内确认/跳过） |
| 基线选择 | 无 | 新增"新建项目 / 当前项目"；新建项目走 `loadProject(默认项目)` 或 `vm.clear()+默认 target` |
| 实时展现 | 注入 + refreshWorkspace 已工作 | 明确模块级"注入完成"事件与高亮（新角色/新背景/新脚本滚动定位） |
| 模块级撤销 | 会话级 undo（`undoUnifiedGeneration`，`generation-runner.ts:625`） | 逐模块撤销 + 会话回退双通道 |
| 失败停止 | 全程 repair（≤3）后整体失败 | 模块粒度停止 + 可重试 + 对话续推 |

---

## 12. 风险与待验证项（建议 Phase 0 验证清单）

1. `vm.addCostume(md5ext, costumeObject, targetId)` 对"已有角色加造型 + 造型名重复"的实际行为（同名是否报错/追加后缀）。
2. 按名称切换背景：`stage.setCostume(index)` 与 `currentCostume` 刷新链路。
3. 内置库资源的 `md5ext` 与 `library-assets` 文件是否严格一致（本工程已按 `scratchAssetUrl` 规则自校验，需抽样验证 Pico/Star/Stars 三文件）。
4. `ask/wait` 自动化通道：是否有公开 API 注入回答（用于未来"试运行+代答"，本期不做）。
5. 项目级共享逻辑（如"生成 N 个星星克隆"）在 clone/私有变量下的正确性：Star 角色"当克隆体启动时"脚本 + 私有变量（例如 `for this sprite only`），需要确认 local variable 的 id 归属（`ProjectSharedState` 目前只有全局语义，需补充 `scope:'local'` 与目标绑定）。
6. 删除默认角色时 GUI 刷新顺序：`deleteSprite` 后 `editingTarget` 悬空的边界。
7. 中文/emoji 文本字段在 `text` shadow 中的序列化转义（`StringUtil.stringify` 是否安全）。
8. 大项目（12 目标、1200 块）下 `emitWorkspaceUpdate` 的渲染耗时，评估阈值与分批策略。
9. `monitors` 自动化：若需求要求"显示变量面板"，需要 GUI 侧 monitor 注入接口，当前计划不含。

---

## 13. 附录：关键源码索引（后续实现可直接引用）

| 主题 | 文件:行 |
| --- | --- |
| VM 程序化入口 | `frontend/node_modules/@scratch/scratch-vm/src/virtual-machine.js`（loadProject:310, saveProjectSb3:390, addSprite:587, addCostume:677, addBackdrop:1017, deleteSprite:1066, renameSprite:1031, setEditingTarget:1251, shareBlocksToTarget:1275, refreshWorkspace:1359, emitWorkspaceUpdate:1398, postSpriteInfo:1555, setVariableValue:1575） |
| 块集合（createBlock/moveBlock/deleteBlock） | `…/src/engine/blocks.js`（createBlock:590, moveBlock:748, changeBlock:616, deleteBlock:871, getScripts:121, resetCache:567） |
| SB3 序列化格式 | `…/src/serialization/sb3.js`（serializeTarget:533, serializeBlocks:378, serializeCostume:424, serializeVariables:480, primitive 常量:60-82, compressInputTree:346, deserializeBlocks:905） |
| 资源装配 | `…/src/import/load-costume.js`、`…/src/serialization/deserialize-assets.js` |
| 前端 IR/编译/注入 | `frontend/src/ai-blocks/program-ir.ts`、`program-compiler.ts`（VMBlock:284, compileProgramIR:367, validateCompiledGraph:324）、`generation-runner.ts`（commit:499, createBlock:285, 备份回滚:593, 撤销:625） |
| 项目计划与素材 | `frontend/src/ai-blocks/project-plan.ts`、`project-resource-runner.ts`、`ai/scratch-library.ts`、`scratch/scratch-gui-bridge.ts`、`scratch/asset-adapter.ts` |
| 后端相位与修复 | `backend/app/orchestration/orchestrator.py`（repair 轮数:116-118, 相位机:290-472）、`backend/app/schemas/ai_blocks.py`、`backend/app/routes/ai_blocks.py`（/chat:76, /generate-unified:120） |
| 内置素材清单 | `frontend/public/assets/scratch/libraries/{sprites,costumes,backdrops}.json` + `library-assets/`（Pico/Star 实测存在） |
| 面板现状 | `frontend/src/ai-blocks/AiBlocksPanel.vue`、`src/styles/ai-blocks.css` |

---

*研究报告完。下一步：见同目录 `02-AI写积木页面设计文档.md`（含 UI 规格与交互设计），以及 `README.md` 目录索引。*
