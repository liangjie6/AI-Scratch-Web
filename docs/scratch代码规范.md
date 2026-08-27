# AI Scratch 积木生成规范（项目版）

> 适用范围：本项目的“AI 写积木”功能（Vue + Scratch GUI/VM 15.0.1 + FastAPI）。
>
> 本文规范 AI 与应用之间交换的 ProgramIR，不是最终 project.json。ProgramIR 由前端编译为 VM block graph，再注入当前 GUI 使用的 VM；SB3 导出时才由 VM 生成 project.json。
>
> 底层 Scratch 3 的 project.json、SB3 资源、opcode 语义和 VM 往返约束见 scratch语法规范.md。两份文档不可混用。

## 1. 数据流与边界

1. 前端读取当前编辑目标，生成目标快照和当前脚本上下文。
2. 后端通过 /api/ai-blocks/requirements 归纳需求。
3. 后端通过 SSE /api/ai-blocks/generate 返回计划和 ProgramIR。
4. 后端使用 Pydantic 和 block contract 校验，前端再按自身编译契约校验并拒绝超出前端限制的结果。
5. 前端将 ProgramIR 编译为 VM block graph，生成 parent、next、input、shadow 和字段 ID。
6. 前端渐进式创建积木并刷新 Scratch 工作区；失败时回滚到生成前快照。

因此：

- AI 必须输出 ProgramIR JSON，禁止直接输出 hydrated VM block、XML、SB3、Base64 或 JavaScript。
- AI 不生成 Scratch block ID、变量 ID、列表 ID、广播 ID、assetId 或 md5ext。
- AI 只描述业务意图、脚本结构、字段名称和输入值；ID 和 VM 连接关系由前端编译器生成。
- 生成目标必须是当前编辑的舞台或角色，不能跨目标写入局部变量、积木或资源。
- 目标快照发生变化后，前端会拒绝继续应用结果，必须重新生成。

## 2. ProgramIR 顶层格式

成功响应中的 program 必须是 JSON 对象，且只允许以下属性：

| 属性 | 类型 | 规则 |
|---|---|---|
| schema_version | 1 | 必须等于数字 1 |
| summary | string | 1..1000 个字符 |
| remove_script_ids | string[] | 默认为 []；必须匹配 ^script_[A-Za-z0-9_-]+$ |
| variables | Variable[] | 默认为 []，名称不可重复，最多 100 项 |
| lists | List[] | 默认为 []，名称不可重复，最多 50 项 |
| broadcasts | string[] | 默认为 []，名称不可重复，最多 100 项 |
| scripts | Script[] | 至少 1 项，最多 12 项 |

严格 JSON 要求：

- 只返回一个 JSON 对象；禁止 Markdown 围栏、解释文字、注释和尾随内容。
- 后端当前会容忍完整的 json 围栏，但生成器不得依赖该兼容行为。
- 禁止 NaN、Infinity、undefined、循环对象和重复引用对象。
- 未声明的顶层属性会被后端和前端拒绝。

最小合法示例：

~~~json
{
  "schema_version": 1,
  "summary": "绿旗后移动角色",
  "remove_script_ids": [],
  "variables": [],
  "lists": [],
  "broadcasts": [],
  "scripts": [
    {
      "id": "script_green_flag_move",
      "purpose": "绿旗点击后移动角色",
      "x": 40,
      "y": 40,
      "blocks": [
        {"opcode": "event_whenflagclicked"},
        {"opcode": "motion_movesteps", "inputs": {"STEPS": {"kind": "number", "value": 10}}}
      ]
    }
  ]
}
~~~

## 3. 目标上下文与生成模式

后端请求中的 target 由前端提供，AI 不得自行修改：

| 属性 | 说明 |
|---|---|
| name | 当前舞台或角色名称 |
| is_stage | 是否为舞台 |
| has_scripts | 当前目标是否已有脚本 |
| mode | modify、append 或 rewrite |
| costumes / sounds | 当前目标已有资源名称 |
| sprite_names | 项目中的原始角色名称 |
| current_program | 当前脚本的规范化上下文，不是可直接写回的 VM JSON |

| 模式 | 行为 | remove_script_ids |
|---|---|---|
| append | 只新增脚本，保留现有可编辑脚本 | 必须为空 |
| rewrite | 清除当前目标中可编辑脚本，再添加新脚本；资源和变量保留 | 必须为空 |
| modify | 删除并替换指定旧脚本 | 只能填写当前上下文中的 script_N |

protected 脚本可能来自扩展或未知 opcode，只能保留，不能删除或重写。

## 4. 变量、列表与广播

~~~json
{
  "variables": [{"name": "score", "value": 0, "scope": "global"}],
  "lists": [{"name": "items", "value": ["apple", 2], "scope": "local"}],
  "broadcasts": ["done"]
}
~~~

- Variable 的 value 可为字符串、有限数字、布尔值或省略；省略时按 0 创建。
- List 的 value 可省略；省略时按空列表创建，最多 500 项。
- scope 只能是 local 或 global；舞台目标不应生成 local 定义。
- broadcasts 只写广播名称，不写 ID。
- 同一数组内名称不得重复；引用必须先声明或引用当前目标中已有定义。
- 前端会创建或复用 Scratch 定义，并把字段解析为真实 ID；AI 不得伪造 ID。

## 5. Script 与 BlockIR

### 5.1 Script

每个脚本包含 id、purpose、x、y、blocks：

- id 匹配 ^script_[A-Za-z0-9_-]+$，脚本内唯一。
- purpose 为 1..120 个字符，用于生成注释和界面反馈。
- x、y 为 -10000..10000 的整数，表示工作区布局位置。
- blocks 非空，按同一栈的执行顺序排列。
- blocks[0] 必须是事件帽积木（shape=hat）。
- 事件帽只能出现在脚本首块，不能出现在输入或子栈。
- reporter 和 boolean 只能作为嵌套输入，不能作为脚本块。
- cap 积木必须是当前栈最后一块；control_forever、control_stop 等后禁止追加同层积木。
- 每个 ProgramIR 最多 12 个脚本、120 个可见积木，嵌套深度最多 12。

### 5.2 BlockIR

每个积木只允许 opcode、fields、inputs、substacks：

~~~json
{
  "opcode": "motion_movesteps",
  "fields": {},
  "inputs": {"STEPS": {"kind": "number", "value": 10}},
  "substacks": {}
}
~~~

- opcode 必须来自项目原生 opcode 白名单。
- fields、inputs、substacks 可省略，有值时必须使用契约规定的名称。
- 禁止写入 next、parent、shadow、topLevel、x、y 等 VM 层属性。
- 默认禁止扩展 opcode（例如 pen_）、procedure opcode 和未知 opcode。
- 后端 catalog 与前端 block-catalog.ts 必须保持一致，新增 opcode 必须同时更新两处并补测试。

### 5.3 Inputs

输入是以下对象之一：

~~~json
{
  "STEPS": {"kind": "number", "value": 10},
  "MESSAGE": {"kind": "text", "value": "你好"},
  "DIRECTION": {"kind": "angle", "value": 90},
  "COLOR": {"kind": "color", "value": "#4C97FF"},
  "CONDITION": {"kind": "block", "block": {"opcode": "operator_gt", "inputs": {"OPERAND1": {"kind": "number", "value": 4}, "OPERAND2": {"kind": "number", "value": 2}}}}
}
~~~

| kind | 值 | 规则 |
|---|---|---|
| number | 有限 JSON 数字 | 禁止把数字写成字符串 |
| angle | 有限 JSON 数字 | 禁止把数字写成字符串 |
| text | 字符串 | 文本、菜单和字符串输入 |
| color | 字符串 | 使用 #RRGGBB |
| block | 嵌套 BlockIR | 只允许 reporter 或 boolean |

boolean 输入必须是 kind=block，且嵌套块必须是 boolean shape。不要用 null、空字符串或自造布尔字面量代替输入。输入名称必须与 opcode 契约完全匹配。

### 5.4 Fields 与 Substacks

fields 的值是字符串、有限数字或布尔值。变量、列表和广播字段只写显示名称，不写 ID：

~~~json
{
  "opcode": "data_setvariableto",
  "fields": {"VARIABLE": "score"},
  "inputs": {"VALUE": {"kind": "text", "value": "1"}}
}
~~~

编译器会把 score 解析为真实的 name 和 id。普通 dropdown 使用序列化值，不使用本地化显示文本；字段名和值区分大小写。

C 形积木的子栈只能放在 substacks，键只有 SUBSTACK 和 SUBSTACK2，禁止 SUBSTACK1。是否必须提供子栈由 opcode 契约决定；空 body 不得创建悬挂引用。

## 6. Opcode 来源与目标限制

完整白名单以代码为准：

- 后端：backend/app/scratch_block_catalog.py 的 BLOCK_CONTRACTS。
- 前端：frontend/src/ai-blocks/block-catalog.ts 的 getBlockSpec。

两者必须保持 opcode、shape、目标、fields、inputs、substacks 一致。stage 只能使用标记为 stage 的积木；sprite 才能使用移动、角色外观和角色专属传感器积木。

常见正确约束：

| 场景 | 写法 |
|---|---|
| 绿旗触发 | 脚本首块使用 event_whenflagclicked |
| 角色移动 | motion_movesteps + STEPS:number |
| 条件 | control_if + CONDITION:block + operator_* boolean |
| 重复 | control_repeat + TIMES:number + substacks.SUBSTACK |
| 播放声音 | sound_play 的 SOUND_MENU:text，不是 field |
| 变量赋值 | data_setvariableto 的 VARIABLE field + VALUE input |

## 7. 编译结果与运行时保证

ProgramIR 通过后，前端 compileProgramIR 负责生成 VM 图。编译结果必须满足：

- 所有 block ID 全局唯一，由应用生成。
- next、parent、input block 和 shadow 引用都存在。
- next 与 parent 互相对应；input 子块和 shadow 的 parent 都指向拥有者。
- reporter/boolean 的 next 为 null；shadow 为非顶层、无 next，且只被一个 input 使用。
- 每个脚本恰有一个顶层事件帽；无孤立 block、孤立 shadow 或跨 target 引用。
- 变量、列表和广播字段带有 VM 所需的真实定义 ID。
- 注释 ID 由顶层 block ID 派生，格式为 <topLevelId>_comment。
- 注入失败时前端删除本次新增内容并恢复生成前状态。

ProgramIR 校验通过不等于 SB3 已完成验收。最终导出仍应按 scratch语法规范完成 VM load、保存、ZIP 资源和新 VM reload 检查。

## 8. 后端 SSE 合同

### 8.1 需求接口

POST /api/ai-blocks/requirements 请求包含 session_id、request_id、messages、target、thinking、proceed_with_defaults；响应包含 ready、title、goal、behavior_steps、triggers、controls、success_criteria、missing_questions。

### 8.2 生成接口

POST /api/ai-blocks/generate 通常依次发送 phase、plan_ready、program_ready、message_complete。program_ready 的 data 必须是 {"program": ProgramIR}。message_complete 之前流结束属于失败，前端不得应用不完整结果。模型修复不能绕过 schema 和 contract 校验。

## 9. 验证清单

### 9.1 自动化

在项目根目录执行：

~~~powershell
npm --prefix frontend run test:unit -- --run
npm --prefix frontend run build
~~~

重点检查：

- frontend/tests/program-compiler.test.ts：IR、shadow、父子引用、变量解析、循环和限制。
- frontend/tests/opcode-catalog-contract.test.ts：前后端 opcode 契约同步。
- frontend/tests/generation-runner.test.ts：注入、回滚、撤销。
- frontend/tests/e2e/ai-blocks.spec.ts：Scratch GUI、SSE、锁定、渐进注入和撤销。
- backend/tests/test_ai_blocks.py：后端 schema、模式和 contract 校验。

### 9.2 5173 浏览器验收

前端地址为 http://127.0.0.1:5173/，后端由 Vite 的 /api 代理提供：

1. 打开页面，等待 Scratch 工作区、舞台和角色列表出现。
2. 选择角色，打开“AI 写积木”。
3. 输入“点击绿旗后移动 10 步”，发送需求并使用默认确认。
4. 生成期间确认输入框和 Scratch 工作区锁定，页面无横向溢出。
5. 完成后确认新增 1 个脚本、2 个可见积木，并出现“运行查看”和“撤销生成”。
6. 点击“运行查看”确认脚本可执行；点击“撤销生成”确认工作区恢复。
7. 刷新页面确认 Scratch GUI 正常加载；导出 SB3 时按底层规范做 load -> save -> reload 验证。

验收记录至少包含页面 URL、控制台错误、请求状态、生成模式、目标名称、ProgramIR 校验结果、注入结果、撤销结果和导出往返结果。

## 10. 维护规则

- 修改 ProgramIR 字段、限制或 block contract 时，必须同时更新后端 schema、prompt、前端类型/校验器、前后端 catalog 和测试。
- 修改 Scratch VM、GUI、storage 或 npm 版本时，必须重新运行编译测试、E2E 和 SB3 往返验收。
- “VM 自动修复后能运行”不能替代源 ProgramIR 修复；修复行为必须被测试捕获。
- 扩展、自定义积木和资源导入应增加版本化契约，不得直接放宽默认白名单。
