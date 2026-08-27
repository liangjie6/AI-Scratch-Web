# Scratch 3 AI 代码生成与 scratch-vm 注入语法规范

## 1. 规范定位

### 1.1 适用范围

本文规定以下内容：

1. SB3 ZIP 容器和 <code>project.json</code> 的结构。
2. 舞台、角色、变量、列表、广播、积木图、注释、监视器和资源的编码。
3. Scratch 核心 opcode、扩展 opcode、输入名、字段名和菜单值。
4. 当前版本自定义积木的规范编码。
5. 通过 <code>scratch-vm</code> 加载并同步到 <code>scratch-gui</code> 的实现边界。
6. AI 输出前、VM 加载后、保存重载后的验收规则。

本文不把 Scratch Blocks XML 当作 AI 的主输出格式。XML 是 VM 向 GUI 发出的工作区表示；AI 的主输出应是受约束的 <code>project.json</code>，由宿主与真实资源一起打包为 SB3。

### 1.2 三层合法性

必须区分三种“合法”：

| 层级 | 含义 | 本规范的态度 |
|---|---|---|
| parser 可接受 | <code>scratch-parser</code> 的 JSON Schema 没有拒绝 | 必要但远远不充分；schema 对字段、图引用、opcode 和 monitor 检查较弱 |
| VM 可执行 | <code>vm.loadProject()</code> resolve，运行时能找到 opcode | 仍不充分；VM 可能修复 shadow、替换损坏资源、忽略未知结构或只记录 warning |
| GUI 稳定往返 | GUI 能重建，保存后再加载语义不变，资源完整，无修复和悬挂引用 | 本规范要求的最终标准 |

AI 输出必须满足第三层。不得把“Promise 成功”当作唯一验收条件。

### 1.3 规范关键词

- **必须**：违反即拒绝项目。
- **应当**：默认执行；只有宿主明确支持例外时才可偏离。
- **可以**：在满足前置条件时使用。
- **禁止**：AI 生成器不得输出，适配器不得静默接受。

### 1.4 版本结论

分析时的 develop HEAD 为 <code>b027676cbf5510edbd386081082c107efa75e4d9</code>。npm 发布包 <code>@scratch/scratch-vm@15.0.1</code> 的 <code>gitHead</code> 为 <code>5c49774f5b39a738e0d5506757de56fd7a9feea4</code>。两者之间在 <code>packages/scratch-vm</code> 下只有包元数据变化，本文涉及的 VM 源码行为一致。

升级 VM、Blocks、Parser 或 GUI 任一组件时，必须重新运行第 18 节的契约测试；不得只修改本文版本号。

## 2. 推荐生成与注入架构

### 2.1 默认主链路

~~~text
用户需求
  -> AI 生成受约束的 project.json 语义对象
  -> 宿主执行 schema、opcode、字段、图、作用域和资源预检
  -> 宿主从可信资源目录取得真实二进制资源
  -> 计算资源哈希并打包 SB3
  -> 隔离 VM 预加载
  -> saveProjectSb3 后在第二个 VM 重载并比较语义
  -> 备份 GUI 当前项目
  -> 在 scratch-gui 使用的同一个 VM 上 await vm.loadProject(sb3)
  -> 校验 targetsUpdate、workspaceUpdate、资源和扩展状态
  -> 成功后提交，失败则加载备份回滚
~~~

完整项目加载是默认路径。仅在给现有角色追加少量脚本时，才可以使用第 16.5 节的 <code>shareBlocksToTarget</code> 辅助路径。

### 2.2 AI 输出边界

AI 应只输出一个 JSON 对象，不输出 Markdown、解释文字、JavaScript 表达式、注释或尾随逗号。该对象必须是完整 <code>project.json</code>，或是由宿主定义并可确定性编译为本文格式的业务 AST。

如果 AI 直接输出 <code>project.json</code>：

- 资源 descriptor 必须从宿主提供的资源目录选择；禁止编造 <code>assetId</code>、<code>md5ext</code>、采样率或文件名。
- 宿主必须负责 ZIP、MD5、magic bytes、SVG 清洗和大小限制。
- AI 可以生成稳定 ID，但宿主仍必须重新检查唯一性和引用闭合。
- 未在请求中启用的扩展必须为空，不能由 AI 自行开启。

### 2.3 禁止的主链路

禁止生产代码直接执行以下操作：

~~~js
vm.runtime.targets.push(rawTarget);
vm.editingTarget.blocks._blocks[id] = rawBlock;
vm.runtime.monitorBlocks.createBlock(rawMonitor);
require('@scratch/scratch-vm/src/serialization/sb3');
~~~

这些均为内部实现边界，会绕过 ID 重分配、变量修复、扩展加载、脚本索引、执行缓存、GUI 事件或包级 exports。

## 3. SB3 容器

### 3.1 ZIP 根目录

SB3 是 ZIP 文件。根目录必须包含：

~~~text
project.json
<32位小写MD5>.svg
<32位小写MD5>.png
<32位小写MD5>.wav
<32位小写MD5>.mp3
~~~

具体项目只包含实际引用的文件。规则如下：

1. 文件名必须与 descriptor 的 <code>md5ext</code> 完全一致，包含大小写。
2. 应把所有资源放在 ZIP 根目录；VM 兼容子目录不代表生成器可以使用子目录。
3. 禁止绝对路径、<code>../</code>、反斜杠路径、重复 entry、加密 entry 和符号链接。
4. <code>project.json</code> 必须是 UTF-8 JSON，ZIP 中只能有一个同名 entry。
5. ZIP MIME 建议使用 <code>application/x.scratch.sb3</code>。
6. AI 不得直接生成 Base64 ZIP；宿主应从结构化 JSON 与二进制资源构建 ZIP。

### 3.2 project.json 根对象

规范根对象：

~~~json
{
  "targets": [],
  "monitors": [],
  "extensions": [],
  "meta": {
    "semver": "3.0.0",
    "vm": "15.0.1",
    "agent": "AICM-Scratch-Generator"
  }
}
~~~

| 属性 | 类型 | 规则 |
|---|---|---|
| <code>targets</code> | array | 必须非空；第 0 项必须是唯一舞台，其后全部是原始角色 |
| <code>monitors</code> | array | 必须存在；没有监视器时为 <code>[]</code> |
| <code>extensions</code> | string[] | 必须存在、去重；只能包含宿主白名单中的扩展 ID |
| <code>meta.semver</code> | string | 必须为 <code>3.0.0</code> |
| <code>meta.vm</code> | string | 写生成目标版本；本规范为 <code>15.0.1</code> |
| <code>meta.agent</code> | string | 写宿主生成器标识，不写用户秘密或 token |
| <code>meta.origin</code> | string | 可选；只写可信来源标识 |

严格规则：

- 必须恰好一个 <code>isStage:true</code> 的 target，并位于索引 0。
- 舞台名必须是精确字符串 <code>Stage</code>。
- 所有角色名必须非空且互不相同，也不能叫 <code>_stage_</code> 或 <code>Stage</code>。
- 不保存 clone；<code>targets</code> 只保存原始角色。
- JSON 中禁止 <code>NaN</code>、<code>Infinity</code>、<code>-Infinity</code>、<code>undefined</code> 和循环对象。

## 4. Target 结构

### 4.1 舞台

~~~json
{
  "isStage": true,
  "name": "Stage",
  "variables": {},
  "lists": {},
  "broadcasts": {},
  "blocks": {},
  "comments": {},
  "currentCostume": 0,
  "costumes": [
    {
      "assetId": "8d1e4b38ed4f03643ffc324edc9e572f",
      "name": "backdrop1",
      "bitmapResolution": 1,
      "md5ext": "8d1e4b38ed4f03643ffc324edc9e572f.svg",
      "dataFormat": "svg",
      "rotationCenterX": 48,
      "rotationCenterY": 48
    }
  ],
  "sounds": [],
  "volume": 100,
  "layerOrder": 0,
  "tempo": 60,
  "videoTransparency": 50,
  "videoState": "on",
  "textToSpeechLanguage": null
}
~~~

舞台专有规则：

- <code>layerOrder</code> 必须为 <code>0</code>。
- <code>tempo</code> 必须为有限数；建议范围 <code>20..500</code>。
- <code>videoTransparency</code> 必须为有限数；规范化到 <code>0..100</code>。
- <code>videoState</code> 只能是 <code>on</code>、<code>off</code>、<code>on-flipped</code>。
- <code>textToSpeechLanguage</code> 为 <code>null</code> 或 text-to-speech 支持的语言代码。
- 舞台不得包含角色专有的 <code>x</code>、<code>y</code>、<code>size</code>、<code>direction</code>、<code>draggable</code>、<code>rotationStyle</code>。

### 4.2 角色

~~~json
{
  "isStage": false,
  "name": "Sprite1",
  "variables": {},
  "lists": {},
  "broadcasts": {},
  "blocks": {},
  "comments": {},
  "currentCostume": 0,
  "costumes": [
    {
      "assetId": "8d1e4b38ed4f03643ffc324edc9e572f",
      "name": "costume1",
      "bitmapResolution": 1,
      "md5ext": "8d1e4b38ed4f03643ffc324edc9e572f.svg",
      "dataFormat": "svg",
      "rotationCenterX": 48,
      "rotationCenterY": 48
    }
  ],
  "sounds": [],
  "volume": 100,
  "layerOrder": 1,
  "visible": true,
  "x": 0,
  "y": 0,
  "size": 100,
  "direction": 90,
  "draggable": false,
  "rotationStyle": "all around"
}
~~~

角色专有规则：

- <code>x</code>、<code>y</code>、<code>size</code>、<code>direction</code> 必须是有限数。
- <code>rotationStyle</code> 只能是 <code>all around</code>、<code>left-right</code>、<code>don't rotate</code>。
- 原始角色的 <code>layerOrder</code> 必须是正整数；项目内应使用无重复的 <code>1..N</code>。
- <code>currentCostume</code> 必须满足 <code>0 &lt;= currentCostume &lt; costumes.length</code>。
- 每个 target 的 <code>costumes</code> 必须至少有一项。当前 parser 会拒绝空数组。
- 同一 target 内造型名和声音名应当分别唯一，避免动态菜单产生歧义。

### 4.3 所有 target 必须存在的属性

即使 parser 没有全部要求，AI 严格档必须为每个 target 写出：

~~~text
isStage, name, variables, lists, broadcasts, blocks, comments,
currentCostume, costumes, sounds, volume, layerOrder
~~~

舞台和角色再分别写出其专有属性。空 map 写 <code>{}</code>，空数组写 <code>[]</code>，禁止省略后依赖 VM 补默认值。

## 5. ID、变量、列表与广播

### 5.1 ID 规则

AI 生成 ID 必须符合：

~~~text
^[A-Za-z][A-Za-z0-9_-]{7,63}$
~~~

建议格式：<code>b_</code> 积木、<code>v_</code> 变量、<code>l_</code> 列表、<code>bc_</code> 广播、<code>c_</code> 工作区注释、<code>p_</code> 自定义积木、<code>a_</code> 参数。前缀后追加随机或确定性哈希片段，使长度达到 8 个字符以上。附着注释 ID 不是独立生成，而是 <code>blockId + "_comment"</code>；带附着注释的 block ID 最长 56 个字符，确保派生 comment ID 仍不超过 64 个字符。

必须满足：

1. block ID 在整个项目所有 target 间唯一。
2. comment ID 在整个项目所有 target 间唯一。
3. 变量、列表和广播定义 ID 在项目定义命名空间内唯一。
4. monitor 可以故意复用其变量或列表定义 ID。
5. asset ID 是内容哈希，可以被多个 target 合法复用。
6. ID 区分大小写；引用必须逐字匹配。

### 5.2 定义格式

~~~json
{
  "variables": {
    "v_score01": ["score", 0],
    "v_cloud01": ["☁ cloudScore", 0, true]
  },
  "lists": {
    "l_items001": ["items", ["apple", 2, true]]
  },
  "broadcasts": {
    "bc_done01": "done"
  }
}
~~~

| 定义 | 值格式 | 作用域 |
|---|---|---|
| 普通变量 | <code>[name, scalar]</code> | 舞台定义为全局；角色定义为该角色局部 |
| 云变量 | <code>[name, scalar, true]</code> | 只能在舞台；默认 AI 档禁止，除非宿主明确启用云服务 |
| 列表 | <code>[name, scalar[]]</code> | 舞台为全局；角色为局部 |
| 广播 | <code>id: name</code> | 必须只定义在舞台，所有 target 引用该定义 |

<code>scalar</code> 只能是字符串、有限数或布尔值。列表运行时上限为 200000 项；生成器应使用远低于该值的业务限制。

### 5.3 作用域引用

- 角色积木可以引用该角色局部定义或舞台全局定义。
- 舞台积木只能引用舞台定义。
- 变量/列表 field 必须同时携带名称和 ID；名称必须等于定义名称。
- 广播 field 或 primitive 必须同时携带广播名称和舞台广播 ID。
- 为减少歧义，严格档禁止同一角色的局部变量与可见全局变量同名，列表同理。
- 不能引用另一个角色的局部变量或列表。
- 不能通过只写同名字符串来替代 ID。

## 6. Block 图模型

### 6.1 block map

<code>target.blocks</code> 是 <code>{blockId: block}</code>，map key 就是 block ID。普通 block 的严格形状：

~~~json
{
  "opcode": "motion_movesteps",
  "next": null,
  "parent": "b_hat0001",
  "inputs": {
    "STEPS": [1, [4, 10]]
  },
  "fields": {},
  "shadow": false,
  "topLevel": false
}
~~~

| 属性 | 类型 | 严格规则 |
|---|---|---|
| <code>opcode</code> | string | 必须出现在本规范白名单，大小写精确 |
| <code>next</code> | string 或 null | 命令栈下一块；reporter、shadow、cap 必须为 null |
| <code>parent</code> | string 或 null | 顶层为 null，其余必须是唯一直接拥有者 |
| <code>inputs</code> | object | key 必须是 opcode 契约中的 input 名 |
| <code>fields</code> | object | key 必须是 opcode 契约中的 field 名 |
| <code>shadow</code> | boolean | 普通块 false，菜单/默认输入 shadow true |
| <code>topLevel</code> | boolean | 只有工作区根块为 true |
| <code>x</code>/<code>y</code> | number | 每个非 shadow 顶层块必须有有限坐标；非顶层不得依赖坐标 |
| <code>mutation</code> | object | 只用于 procedure 或已知扩展 mutation |
| <code>comment</code> | string | 可选；必须引用同一 target 的 comment ID |

### 6.2 图不变量

生成器和预检器必须逐条验证：

1. 每个 <code>next</code>、<code>parent</code>、input 的 block 引用、shadow 引用都存在于同一 target 的 block map。
2. 图中不存在由 <code>next</code> 或 input 引用形成的环。
3. 每个非顶层 block 只有一个结构拥有者；禁止把同一 reporter 共享给两个 input。
4. 若 A 的 <code>next=B</code>，则 B 的 <code>parent</code> 必须为 A。
5. 若 A 的 input 引用 B，则 B 的 <code>parent</code> 必须为 A；被遮挡的 shadow S 的 <code>parent</code> 也必须为 A。
6. <code>topLevel:true</code> 当且仅当 <code>parent:null</code>。
7. <code>shadow:true</code> 的 block 必须 <code>topLevel:false</code>、<code>next:null</code>，并且只被一个 input 当作 shadow。
8. reporter 和 Boolean block 作为 input 子树时必须 <code>next:null</code>。
9. hat block 必须顶层。严格可执行档的普通脚本必须以 hat 开始。
10. <code>procedures_definition</code> 必须顶层；procedure body 从 definition 的 <code>next</code> 开始。
11. C 形积木的 body 第一块由 <code>SUBSTACK</code> 或 <code>SUBSTACK2</code> 引用，第一块 parent 指向 C 形积木；body 后续块按 next/parent 链接。
12. cap/terminal block 必须 <code>next:null</code>，包括 <code>control_delete_this_clone</code> 和会终止当前栈的 stop 形式。
13. 一个 block 不能同时是两个栈的成员，也不能跨 target 引用。
14. 未使用的孤立非顶层 block、孤立 shadow、孤立 procedure prototype 一律拒绝。
15. loader 对 parent、shadow 或 ID 的修复应视为生成失败，不应接受修复后的结果代替源修正。

### 6.3 顶层 reporter primitive

SB3 schema 允许在 <code>blocks</code> map 顶层放置数组：

~~~json
{
  "b_loosevar": [12, "score", "v_score01", 100, 200],
  "b_looselist": [13, "items", "l_items001", 100, 240]
}
~~~

它表示工作区里松散放置的变量或列表 reporter，最后两个值是 x/y。AI 严格可执行档禁止生成松散 reporter；只有“复刻用户工作区布局”模式可以启用，并必须使用 5 项数组、有效定义 ID 和有限坐标。

## 7. Input、primitive 与 field

### 7.1 input tuple

每个 <code>inputs.NAME</code> 必须是以下三种之一：

| 格式 | 含义 | 严格约束 |
|---|---|---|
| <code>[1, S]</code> | 未被覆盖的 shadow；运行块和 shadow 是同一个 S | S 为 code 4-11 shadow primitive，或引用 <code>shadow:true</code> 的 block |
| <code>[2, B]</code> | 无 shadow 的真实 block | B 为 <code>shadow:false</code> block ID，或压缩的变量/列表 reporter code 12/13 |
| <code>[3, B, S]</code> | 真实 reporter B 覆盖默认 shadow S | B 为非 shadow block ID 或 code 12/13；S 为 code 4-11 或 shadow block ID |

禁止：

- <code>[1]</code>、<code>[2]</code>、<code>[3,B]</code>、第四个元素。
- 用 <code>null</code> 作为 payload。
- 把非 shadow reporter 填入状态 1。
- 状态 3 的 active 与 shadow payload 完全相同。
- 把 code 4-11 的 shadow primitive 填入状态 2；状态 2 只额外允许 code 12/13 reporter。
- 空 SUBSTACK 写成悬挂引用；空 body 应省略对应 key。

### 7.2 compact primitive

| code | GUI shadow | payload | 约束/示例 |
|---:|---|---|---|
| 4 | <code>math_number</code> | string 或 number | 任意有限数，例 <code>[4,10]</code> |
| 5 | <code>math_positive_number</code> | string 或 number | 应大于或等于 0，例 <code>[5,1]</code> |
| 6 | <code>math_whole_number</code> | string 或 number | 应为非负整数，例 <code>[6,10]</code> |
| 7 | <code>math_integer</code> | string 或 number | 应为整数，例 <code>[7,-2]</code> |
| 8 | <code>math_angle</code> | string 或 number | 角度，例 <code>[8,90]</code> |
| 9 | <code>colour_picker</code> | <code>#RRGGBB</code> | 必须 6 位十六进制颜色 |
| 10 | <code>text</code> | string 或 number | 文本，例 <code>[10,"hello"]</code> |
| 11 | <code>event_broadcast_menu</code> | name, id | 三项：<code>[11,"done","bc_done01"]</code> |
| 12 | <code>data_variable</code> | name, id | 三项 input reporter；顶层时再加 x/y |
| 13 | <code>data_listcontents</code> | name, id | 三项 input reporter；顶层时再加 x/y |

JSON 不存在通用 Boolean literal primitive。Boolean 插槽使用状态 2 连接 Boolean reporter；未连接时省略 input，运行时按 false 处理。

### 7.3 常见输入示例

~~~json
{
  "STEPS": [1, [4, 10]],
  "MESSAGE": [1, [10, "hello"]],
  "COLOR": [1, [9, "#4C97FF"]],
  "CONDITION": [2, "b_boolean01"],
  "SUBSTACK": [2, "b_body0001"],
  "VALUE": [3, "b_operator1", [4, 0]],
  "BROADCAST_INPUT": [1, [11, "done", "bc_done01"]]
}
~~~

### 7.4 field tuple

field 值只能是以下两种互斥形式：

~~~text
["plain value"]
["display name", "definition id"]
~~~

规则：

- 普通 dropdown 使用一项数组，如 <code>"STYLE":["left-right"]</code>。
- <code>VARIABLE</code>、<code>LIST</code>、<code>BROADCAST_OPTION</code> 必须使用两项数组并携带有效 ID。
- 自定义积木参数 reporter 的 <code>VALUE</code> 使用一项数组，值为参数显示名。
- 不要给普通 field 添加 <code>null</code> ID。
- field 名和值均区分大小写；表中的值是序列化值，不是本地化显示文本。

### 7.5 菜单 shadow block

动态菜单通常不能压缩成 code 4-13，必须保留 shadow block。例如：

~~~json
{
  "b_hat0001": {
    "opcode": "event_whenflagclicked",
    "next": "b_goto0001",
    "parent": null,
    "inputs": {},
    "fields": {},
    "shadow": false,
    "topLevel": true,
    "x": 48,
    "y": 48
  },
  "b_goto0001": {
    "opcode": "motion_goto",
    "next": null,
    "parent": "b_hat0001",
    "inputs": {"TO": [1, "b_gotomenu"]},
    "fields": {},
    "shadow": false,
    "topLevel": false
  },
  "b_gotomenu": {
    "opcode": "motion_goto_menu",
    "next": null,
    "parent": "b_goto0001",
    "inputs": {},
    "fields": {"TO": ["_random_"]},
    "shadow": true,
    "topLevel": false
  }
}
~~~

核心菜单 shadow 对照：

| shadow opcode | field | 合法值来源 |
|---|---|---|
| <code>motion_pointtowards_menu</code> | <code>TOWARDS</code> | <code>_mouse_</code> 或其他原始角色名 |
| <code>motion_goto_menu</code> | <code>TO</code> | <code>_random_</code>、<code>_mouse_</code>、其他角色名 |
| <code>motion_glideto_menu</code> | <code>TO</code> | <code>_random_</code>、<code>_mouse_</code>、其他原始角色名 |
| <code>looks_costume</code> | <code>COSTUME</code> | 当前 target 造型名 |
| <code>looks_backdrops</code> | <code>BACKDROP</code> | 舞台背景名 |
| <code>sound_sounds_menu</code> | <code>SOUND_MENU</code> | 当前 target 声音名 |
| <code>sensing_touchingobjectmenu</code> | <code>TOUCHINGOBJECTMENU</code> | <code>_mouse_</code>、<code>_edge_</code>、其他角色名 |
| <code>sensing_distancetomenu</code> | <code>DISTANCETOMENU</code> | <code>_mouse_</code> 或其他角色名 |
| <code>sensing_keyoptions</code> | <code>KEY_OPTION</code> | <code>space</code>、箭头、<code>any</code>、a-z、0-9 |
| <code>sensing_of_object_menu</code> | <code>OBJECT</code> | <code>_stage_</code> 或其他角色名 |
| <code>control_create_clone_of_menu</code> | <code>CLONE_OPTION</code> | 角色上为 <code>_myself_</code> 或角色名；舞台上只允许角色名 |

广播菜单优先使用 compact code 11，不需要显式创建 <code>event_broadcast_menu</code> block。

## 8. 自定义积木

### 8.1 当前 canonical 格式

当前 develop/15.0.1 的规范格式是“非 shadow prototype”：

- definition 的 <code>inputs.custom_block</code> 为 <code>[2,"prototypeId"]</code>。
- <code>procedures_prototype.shadow</code> 必须是 <code>false</code>。
- prototype 的 <code>inputs</code> 必须是空对象。
- prototype 参数只存在于 mutation 数组，不在 prototype 下创建 argument reporter shadow。
- 旧格式 <code>[1,prototype]</code>、shadow prototype 和 prototype 参数子块仅用于读取兼容；AI 禁止生成。

完整结构示例：

~~~json
{
  "p_definition": {
    "opcode": "procedures_definition",
    "next": "p_body_say",
    "parent": null,
    "inputs": {"custom_block": [2, "p_prototype"]},
    "fields": {},
    "shadow": false,
    "topLevel": true,
    "x": 48,
    "y": 320
  },
  "p_prototype": {
    "opcode": "procedures_prototype",
    "next": null,
    "parent": "p_definition",
    "inputs": {},
    "fields": {},
    "shadow": false,
    "topLevel": false,
    "mutation": {
      "tagName": "mutation",
      "children": [],
      "proccode": "say input %s",
      "argumentids": "[\"a_words001\"]",
      "argumentnames": "[\"words\"]",
      "argumentdefaults": "[\"\"]",
      "warp": "false"
    }
  },
  "p_body_say": {
    "opcode": "looks_say",
    "next": null,
    "parent": "p_definition",
    "inputs": {"MESSAGE": [3, "p_argument", [10, ""]]},
    "fields": {},
    "shadow": false,
    "topLevel": false
  },
  "p_argument": {
    "opcode": "argument_reporter_string_number",
    "next": null,
    "parent": "p_body_say",
    "inputs": {},
    "fields": {"VALUE": ["words"]},
    "shadow": false,
    "topLevel": false
  },
  "p_call_hat": {
    "opcode": "event_whenflagclicked",
    "next": "p_call001",
    "parent": null,
    "inputs": {},
    "fields": {},
    "shadow": false,
    "topLevel": true,
    "x": 320,
    "y": 320
  },
  "p_call001": {
    "opcode": "procedures_call",
    "next": null,
    "parent": "p_call_hat",
    "inputs": {"a_words001": [1, [10, "hello"]]},
    "fields": {},
    "shadow": false,
    "topLevel": false,
    "mutation": {
      "tagName": "mutation",
      "children": [],
      "proccode": "say input %s",
      "argumentids": "[\"a_words001\"]",
      "warp": "false"
    }
  }
}
~~~

### 8.2 mutation 不变量

1. prototype 和 call 的 <code>mutation</code> 都必须包含 <code>"tagName":"mutation"</code> 与 <code>"children":[]</code>；禁止省略、设为 null 或生成 mutation 子节点。GUI 的 mutation XML 转换会直接读取 <code>children.length</code>。
2. prototype 的 <code>argumentids</code>、<code>argumentnames</code>、<code>argumentdefaults</code> 值本身是 JSON 字符串，不是外层数组；call 只保留 <code>argumentids</code>。
3. prototype 的三个参数数组长度必须一致，并等于 <code>proccode</code> 参数占位符数量。
4. <code>%s</code> 表示 string/number 参数；<code>%b</code> 表示 Boolean 参数。严格档不生成旧式 <code>%n</code>。
5. Boolean 默认值使用字符串 <code>"false"</code>；string/number 默认值通常使用空字符串或有限数。
6. <code>argumentids</code> 中每个 ID 唯一，并且也是 call 的 input key。
7. definition 所连接的 prototype 与所有 call 的 <code>proccode</code> 必须逐字一致；definition 自身不写 mutation。
8. prototype 与所有 call 的 <code>warp</code> 必须一致，只能是字符串 <code>"true"</code> 或 <code>"false"</code>。
9. call mutation 不写 <code>argumentnames</code> 和 <code>argumentdefaults</code>。
10. string/number 参数 reporter opcode 为 <code>argument_reporter_string_number</code>；Boolean 参数 reporter 为 <code>argument_reporter_boolean</code>。
11. 未连接的 Boolean call 参数应省略该 input，不能使用文本 <code>"true"</code> 或 <code>"false"</code> 冒充 Boolean。
12. 同一 target 内 <code>proccode</code> 必须唯一，所有 call 必须能找到 definition 连接的 prototype。

## 9. 注释

~~~json
{
  "comments": {
    "b_move0001_comment": {
      "blockId": "b_move0001",
      "x": 120,
      "y": 80,
      "width": 200,
      "height": 120,
      "minimized": false,
      "text": "Move the sprite"
    }
  }
}
~~~

规则：

- 附着注释的 map key 和 block 的 <code>comment</code> 都必须精确等于 <code>blockId + "_comment"</code>，comment 的 <code>blockId</code> 等于该 block ID。VM 会无条件合成这个 ID，使用任意旧 ID 会在加载时被改写。
- 每个 block 最多一个附着注释，附着关系必须一对一；禁止两个 comment 共享同一个 <code>blockId</code>。
- 工作区注释：<code>blockId:null</code>，保留自身 comment ID，必须有有限 x/y。
- <code>text</code> 必须是字符串且最长 8000 个字符；<code>minimized</code> 必须是 Boolean。
- x/y 必须为有限数，width/height 必须为正有限数；禁止依赖 <code>Number()</code> 把非法字符串转换为 NaN。
- comment 不能跨 target 附着，也不能引用不存在的 block。

## 10. Monitor

### 10.1 序列化形状

~~~json
{
  "id": "v_score01",
  "mode": "default",
  "opcode": "data_variable",
  "params": {"VARIABLE": "score"},
  "spriteName": null,
  "value": 0,
  "width": 0,
  "height": 0,
  "x": 5,
  "y": 5,
  "visible": true,
  "sliderMin": 0,
  "sliderMax": 100,
  "isDiscrete": true
}
~~~

VM 保存 monitor 时会写：<code>id</code>、<code>mode</code>、<code>opcode</code>、<code>params</code>、<code>spriteName</code>、<code>value</code>、<code>width</code>、<code>height</code>、<code>x</code>、<code>y</code>、<code>visible</code>；非 list mode 还写 <code>sliderMin</code>、<code>sliderMax</code>、<code>isDiscrete</code>。

### 10.2 AI 严格档

默认只允许变量和列表 monitor：

| 对象 | id | mode | opcode | params | spriteName |
|---|---|---|---|---|---|
| 全局变量 | 变量 ID | <code>default</code>/<code>large</code>/<code>slider</code> | <code>data_variable</code> | <code>{"VARIABLE":name}</code> | <code>null</code> |
| 局部变量 | 变量 ID | <code>default</code>/<code>large</code>/<code>slider</code> | <code>data_variable</code> | <code>{"VARIABLE":name}</code> | 角色名 |
| 全局列表 | 列表 ID | <code>list</code> | <code>data_listcontents</code> | <code>{"LIST":name}</code> | <code>null</code> |
| 局部列表 | 列表 ID | <code>list</code> | <code>data_listcontents</code> | <code>{"LIST":name}</code> | 角色名 |

规则：

- <code>spriteName</code> 依赖 target 名查找，因此 target 名必须唯一。
- slider 必须满足有限 <code>sliderMin &lt;= sliderMax</code>。
- list mode 不写 slider 三字段。
- x/y/width/height 必须有限；visible 必须是 Boolean。
- 其他 reporter monitor 的 ID 计算依赖 VM 内部逻辑，AI 默认禁止直接生成；由宿主通过版本化适配器创建。

## 11. 造型、声音与资源

### 11.1 造型 descriptor

~~~json
{
  "assetId": "8d1e4b38ed4f03643ffc324edc9e572f",
  "name": "costume1",
  "bitmapResolution": 1,
  "md5ext": "8d1e4b38ed4f03643ffc324edc9e572f.svg",
  "dataFormat": "svg",
  "rotationCenterX": 48,
  "rotationCenterY": 48
}
~~~

- <code>assetId</code> 必须是 ZIP 中精确资源字节的 32 位小写 MD5。
- <code>md5ext</code> 必须等于 <code>assetId + "." + dataFormat</code>。
- <code>dataFormat</code> 只能是 <code>svg</code>、<code>png</code>、<code>jpeg</code>、<code>jpg</code>、<code>bmp</code>、<code>gif</code>。
- rotation center 必须为有限数。
- SVG 通常使用 <code>bitmapResolution:1</code>；Scratch 3 bitmap 通常使用 2。resolution 1 bitmap 需要 V2 bitmap adapter 转换。
- 每个 target 至少一个造型，且 <code>currentCostume</code> 在范围内。

### 11.2 声音 descriptor

~~~json
{
  "assetId": "b586745b98e94d7574f7f7b48d831e20",
  "name": "default sound",
  "dataFormat": "wav",
  "md5ext": "b586745b98e94d7574f7f7b48d831e20.wav",
  "rate": 22050,
  "sampleCount": 1
}
~~~

该 descriptor 对应 <code>@scratch/scratch-storage</code> 随包的 46 字节单声道 PCM <code>defaultSound.wav</code>；MD5、采样率和 sampleCount 已从文件实测，不是示意哈希。

- <code>dataFormat</code> 只能是 <code>wav</code>、<code>wave</code>、<code>mp3</code>。
- rate 和 sampleCount 必须为非负整数，并应与解码结果一致。
- ZIP 文件的 magic bytes 必须与扩展名一致。
- 没有声音时使用 <code>[]</code>，不能放虚构 descriptor。

### 11.3 SVG 与哈希

VM 会在载入 SVG 时清洗内容。清洗可能改变字节并导致保存后的 <code>assetId</code> 变化。

生产建议：

1. 使用与 VM 兼容的 sanitizer 先清洗 SVG。
2. 对清洗后的 UTF-8 字节计算 MD5。
3. 用该 MD5 写 <code>assetId</code>、<code>md5ext</code> 和 ZIP 文件名。
4. 保存后重新读取“保存结果自身”的 <code>project.json</code>，确认其中每个 <code>md5ext</code> 都有 ZIP entry。
5. 不要假定未经清洗的输入哈希必须等于 VM 保存后的哈希。

如果生产链路已声称输入是 canonical sanitized SVG，但往返后哈希仍变化，应判定为 canonicalization 失败。

### 11.4 broken 资源

资源缺失或损坏不总会使 <code>loadProject</code> reject。VM 可能加载默认资源并把原 descriptor 放入 <code>costume.broken</code> 或 <code>sound.broken</code>。因此加载后必须检查：

~~~text
costume.broken 不存在
sound.broken 不存在
挂载 renderer 时 costume.skinId 非 null
挂载 audio engine 时 sound.soundId 非 null
~~~

## 12. 核心 opcode 白名单

### 12.1 表格记号

- 类型：H=hat，C=command，R=reporter，B=Boolean，L=C 形控制，T=terminal。
- <code>N</code>=数字 input，<code>S</code>=文本/通用圆形 input，<code>B</code>=Boolean input，<code>M</code>=菜单 shadow input，<code>STACK</code>=子栈。
- 表中 inputs 和 fields 是序列化 key，必须精确匹配。
- 未列出的 input/field key 禁止出现。
- “两者”表示可从舞台或角色脚本使用；“角色”表示 GUI 稳定档只在角色使用。

### 12.2 Motion

| opcode | 类型 | inputs | fields | target/值 |
|---|---:|---|---|---|
| <code>motion_movesteps</code> | C | <code>STEPS:N</code> | - | 角色 |
| <code>motion_turnright</code> | C | <code>DEGREES:N</code> | - | 角色 |
| <code>motion_turnleft</code> | C | <code>DEGREES:N</code> | - | 角色 |
| <code>motion_goto</code> | C | <code>TO:M</code> | - | <code>motion_goto_menu.TO</code> |
| <code>motion_gotoxy</code> | C | <code>X:N,Y:N</code> | - | 角色 |
| <code>motion_glideto</code> | C | <code>SECS:N,TO:M</code> | - | <code>motion_glideto_menu.TO</code> |
| <code>motion_glidesecstoxy</code> | C | <code>SECS:N,X:N,Y:N</code> | - | 角色 |
| <code>motion_pointindirection</code> | C | <code>DIRECTION:N</code> | - | 角色 |
| <code>motion_pointtowards</code> | C | <code>TOWARDS:M</code> | - | <code>motion_pointtowards_menu.TOWARDS</code> |
| <code>motion_changexby</code> | C | <code>DX:N</code> | - | 角色 |
| <code>motion_setx</code> | C | <code>X:N</code> | - | 角色 |
| <code>motion_changeyby</code> | C | <code>DY:N</code> | - | 角色 |
| <code>motion_sety</code> | C | <code>Y:N</code> | - | 角色 |
| <code>motion_ifonedgebounce</code> | C | - | - | 角色，需要 renderer bounds |
| <code>motion_setrotationstyle</code> | C | - | <code>STYLE</code> | <code>left-right</code>、<code>don't rotate</code>、<code>all around</code> |
| <code>motion_xposition</code> | R | - | - | 角色 |
| <code>motion_yposition</code> | R | - | - | 角色 |
| <code>motion_direction</code> | R | - | - | 角色 |

### 12.3 Looks

| opcode | 类型 | inputs | fields | target/值 |
|---|---:|---|---|---|
| <code>looks_sayforsecs</code> | C | <code>MESSAGE:S,SECS:N</code> | - | 角色 |
| <code>looks_say</code> | C | <code>MESSAGE:S</code> | - | 角色 |
| <code>looks_thinkforsecs</code> | C | <code>MESSAGE:S,SECS:N</code> | - | 角色 |
| <code>looks_think</code> | C | <code>MESSAGE:S</code> | - | 角色 |
| <code>looks_switchcostumeto</code> | C | <code>COSTUME:M</code> | - | 角色；<code>looks_costume.COSTUME</code> |
| <code>looks_nextcostume</code> | C | - | - | 角色 |
| <code>looks_switchbackdropto</code> | C | <code>BACKDROP:M</code> | - | 两者；<code>looks_backdrops.BACKDROP</code> |
| <code>looks_switchbackdroptoandwait</code> | C | <code>BACKDROP:M</code> | - | 两者；等待背景事件脚本 |
| <code>looks_nextbackdrop</code> | C | - | - | 两者 |
| <code>looks_changeeffectby</code> | C | <code>CHANGE:N</code> | <code>EFFECT</code> | <code>COLOR,FISHEYE,WHIRL,PIXELATE,MOSAIC,BRIGHTNESS,GHOST</code> |
| <code>looks_seteffectto</code> | C | <code>VALUE:N</code> | <code>EFFECT</code> | <code>COLOR,FISHEYE,WHIRL,PIXELATE,MOSAIC,BRIGHTNESS,GHOST</code> |
| <code>looks_cleargraphiceffects</code> | C | - | - | 两者 |
| <code>looks_changesizeby</code> | C | <code>CHANGE:N</code> | - | 角色 |
| <code>looks_setsizeto</code> | C | <code>SIZE:N</code> | - | 角色 |
| <code>looks_show</code> | C | - | - | 角色 |
| <code>looks_hide</code> | C | - | - | 角色 |
| <code>looks_gotofrontback</code> | C | - | <code>FRONT_BACK</code> | <code>front</code>、<code>back</code>；角色 |
| <code>looks_goforwardbackwardlayers</code> | C | <code>NUM:N</code> | <code>FORWARD_BACKWARD</code> | <code>forward</code>、<code>backward</code>；角色 |
| <code>looks_costumenumbername</code> | R | - | <code>NUMBER_NAME</code> | <code>number</code>、<code>name</code>；角色 |
| <code>looks_backdropnumbername</code> | R | - | <code>NUMBER_NAME</code> | <code>number</code>、<code>name</code>；两者 |
| <code>looks_size</code> | R | - | - | 角色 |

### 12.4 Sound

| opcode | 类型 | inputs | fields | 值/说明 |
|---|---:|---|---|---|
| <code>sound_play</code> | C | <code>SOUND_MENU:M</code> | - | <code>sound_sounds_menu.SOUND_MENU</code>；不等待 |
| <code>sound_playuntildone</code> | C | <code>SOUND_MENU:M</code> | - | 等待结束 |
| <code>sound_stopallsounds</code> | C | - | - | 停止所有 target 声音 |
| <code>sound_changeeffectby</code> | C | <code>VALUE:N</code> | <code>EFFECT</code> | <code>PITCH</code>、<code>PAN</code> |
| <code>sound_seteffectto</code> | C | <code>VALUE:N</code> | <code>EFFECT</code> | <code>PITCH</code>、<code>PAN</code> |
| <code>sound_cleareffects</code> | C | - | - | 清除当前 target 音效 |
| <code>sound_changevolumeby</code> | C | <code>VOLUME:N</code> | - | 两者 |
| <code>sound_setvolumeto</code> | C | <code>VOLUME:N</code> | - | VM 规范化到 0..100 |
| <code>sound_volume</code> | R | - | - | 当前 target 音量 |

### 12.5 Events

| opcode | 类型 | inputs | fields | target/值 |
|---|---:|---|---|---|
| <code>event_whenflagclicked</code> | H | - | - | 两者 |
| <code>event_whenkeypressed</code> | H | - | <code>KEY_OPTION</code> | <code>space</code>、箭头、<code>any</code>、a-z、0-9 |
| <code>event_whenthisspriteclicked</code> | H | - | - | 角色 |
| <code>event_whenstageclicked</code> | H | - | - | 舞台 |
| <code>event_whenbackdropswitchesto</code> | H | - | <code>BACKDROP</code> | 必须是已有背景名 |
| <code>event_whengreaterthan</code> | H | <code>VALUE:N</code> | <code>WHENGREATERTHANMENU</code> | <code>LOUDNESS</code>、<code>TIMER</code> |
| <code>event_whenbroadcastreceived</code> | H | - | <code>BROADCAST_OPTION:[name,id]</code> | 两者 |
| <code>event_broadcast</code> | C | <code>BROADCAST_INPUT</code> | - | 推荐 <code>[1,[11,name,id]]</code> |
| <code>event_broadcastandwait</code> | C | <code>BROADCAST_INPUT</code> | - | 等待接收脚本结束 |

### 12.6 Control

| opcode | 类型 | inputs | fields | 值/说明 |
|---|---:|---|---|---|
| <code>control_wait</code> | C | <code>DURATION:N</code> | - | 秒，负数按 0 |
| <code>control_repeat</code> | L | <code>TIMES:N,SUBSTACK:STACK</code> | - | 次数取整 |
| <code>control_forever</code> | L/T | <code>SUBSTACK:STACK</code> | - | 外层没有 next |
| <code>control_if</code> | L | <code>CONDITION:B,SUBSTACK:STACK</code> | - | 空 body 可省略 SUBSTACK |
| <code>control_if_else</code> | L | <code>CONDITION:B,SUBSTACK:STACK,SUBSTACK2:STACK</code> | - | 两个 body 可分别为空 |
| <code>control_wait_until</code> | C | <code>CONDITION:B</code> | - | 条件 false 时 yield |
| <code>control_repeat_until</code> | L | <code>CONDITION:B,SUBSTACK:STACK</code> | - | 条件 true 时结束 |
| <code>control_stop</code> | C/T | - | <code>STOP_OPTION</code> | <code>all</code>、<code>this script</code>、<code>other scripts in sprite</code>；舞台可用 <code>other scripts in stage</code> |
| <code>control_start_as_clone</code> | H | - | - | 角色 clone 脚本 |
| <code>control_create_clone_of</code> | C | <code>CLONE_OPTION:M</code> | - | <code>control_create_clone_of_menu.CLONE_OPTION</code> |
| <code>control_delete_this_clone</code> | C/T | - | - | 角色专用；原始 target 执行时无操作 |

### 12.7 Sensing

| opcode | 类型 | inputs | fields | 值/说明 |
|---|---:|---|---|---|
| <code>sensing_touchingobject</code> | B | <code>TOUCHINGOBJECTMENU:M</code> | - | <code>_mouse_</code>、<code>_edge_</code>、角色名；角色专用 |
| <code>sensing_touchingcolor</code> | B | <code>COLOR</code> | - | code 9 颜色 shadow；角色专用 |
| <code>sensing_coloristouchingcolor</code> | B | <code>COLOR,COLOR2</code> | - | 两个 code 9 颜色 shadow；角色专用 |
| <code>sensing_distanceto</code> | R | <code>DISTANCETOMENU:M</code> | - | <code>_mouse_</code> 或角色名；角色专用 |
| <code>sensing_askandwait</code> | C | <code>QUESTION:S</code> | - | 两者 |
| <code>sensing_answer</code> | R | - | - | 项目全局最近回答 |
| <code>sensing_keypressed</code> | B | <code>KEY_OPTION:M</code> | - | <code>sensing_keyoptions.KEY_OPTION</code> |
| <code>sensing_mousedown</code> | B | - | - | 全局鼠标 |
| <code>sensing_mousex</code> | R | - | - | 全局鼠标 |
| <code>sensing_mousey</code> | R | - | - | 全局鼠标 |
| <code>sensing_setdragmode</code> | C | - | <code>DRAG_MODE</code> | <code>draggable</code>、<code>not draggable</code>；角色 |
| <code>sensing_loudness</code> | R | - | - | 需要音频输入；不可用时为 -1 |
| <code>sensing_timer</code> | R | - | - | 项目全局 |
| <code>sensing_resettimer</code> | C | - | - | 项目全局 |
| <code>sensing_of</code> | R | <code>OBJECT:M</code> | <code>PROPERTY</code> | 见下方属性集合 |
| <code>sensing_current</code> | R | - | <code>CURRENTMENU</code> | <code>YEAR,MONTH,DATE,DAYOFWEEK,HOUR,MINUTE,SECOND</code> |
| <code>sensing_dayssince2000</code> | R | - | - | 时区相关的天数 |
| <code>sensing_online</code> | B | - | - | 宿主网络状态 |
| <code>sensing_username</code> | R | - | - | 宿主 user-data I/O |

<code>sensing_of.PROPERTY</code>：

- OBJECT 为 <code>_stage_</code>：<code>backdrop #</code>、<code>backdrop name</code>、<code>volume</code> 或舞台普通变量名。
- OBJECT 为角色名：<code>x position</code>、<code>y position</code>、<code>direction</code>、<code>costume #</code>、<code>costume name</code>、<code>size</code>、<code>volume</code> 或该角色可见普通变量名。

### 12.8 Operators

| opcode | 类型 | inputs | fields | 值/说明 |
|---|---:|---|---|---|
| <code>operator_add</code> | R | <code>NUM1,NUM2</code> | - | 数值加法 |
| <code>operator_subtract</code> | R | <code>NUM1,NUM2</code> | - | 数值减法 |
| <code>operator_multiply</code> | R | <code>NUM1,NUM2</code> | - | 数值乘法 |
| <code>operator_divide</code> | R | <code>NUM1,NUM2</code> | - | 数值除法 |
| <code>operator_random</code> | R | <code>FROM,TO</code> | - | 两端整数时为含端点整数随机，否则连续随机 |
| <code>operator_gt</code> | B | <code>OPERAND1,OPERAND2</code> | - | Scratch 比较语义 |
| <code>operator_lt</code> | B | <code>OPERAND1,OPERAND2</code> | - | Scratch 比较语义 |
| <code>operator_equals</code> | B | <code>OPERAND1,OPERAND2</code> | - | Scratch 比较语义 |
| <code>operator_and</code> | B | <code>OPERAND1:B,OPERAND2:B</code> | - | Boolean |
| <code>operator_or</code> | B | <code>OPERAND1:B,OPERAND2:B</code> | - | Boolean |
| <code>operator_not</code> | B | <code>OPERAND:B</code> | - | Boolean |
| <code>operator_join</code> | R | <code>STRING1,STRING2</code> | - | 字符串拼接 |
| <code>operator_letter_of</code> | R | <code>LETTER,STRING</code> | - | 1-based，越界为空字符串 |
| <code>operator_length</code> | R | <code>STRING</code> | - | 字符串长度 |
| <code>operator_contains</code> | B | <code>STRING1,STRING2</code> | - | 不区分大小写 |
| <code>operator_mod</code> | R | <code>NUM1,NUM2</code> | - | Scratch 取模语义 |
| <code>operator_round</code> | R | <code>NUM</code> | - | 四舍五入 |
| <code>operator_mathop</code> | R | <code>NUM</code> | <code>OPERATOR</code> | <code>abs,floor,ceiling,sqrt,sin,cos,tan,asin,acos,atan,ln,log,e ^,10 ^</code> |

### 12.9 Data

| opcode | 类型 | inputs | fields | 规则 |
|---|---:|---|---|---|
| <code>data_variable</code> | R | - | <code>VARIABLE:[name,id]</code> | 变量 reporter |
| <code>data_setvariableto</code> | C | <code>VALUE</code> | <code>VARIABLE:[name,id]</code> | 设置变量 |
| <code>data_changevariableby</code> | C | <code>VALUE</code> | <code>VARIABLE:[name,id]</code> | 数值变化 |
| <code>data_showvariable</code> | C | - | <code>VARIABLE:[name,id]</code> | 显示 monitor |
| <code>data_hidevariable</code> | C | - | <code>VARIABLE:[name,id]</code> | 隐藏 monitor |
| <code>data_listcontents</code> | R | - | <code>LIST:[name,id]</code> | 列表 reporter |
| <code>data_addtolist</code> | C | <code>ITEM</code> | <code>LIST:[name,id]</code> | 追加 |
| <code>data_deleteoflist</code> | C | <code>INDEX</code> | <code>LIST:[name,id]</code> | index 可为数、<code>last</code>、<code>random</code>、<code>all</code> |
| <code>data_deletealloflist</code> | C | - | <code>LIST:[name,id]</code> | 清空 |
| <code>data_insertatlist</code> | C | <code>ITEM,INDEX</code> | <code>LIST:[name,id]</code> | 插入 |
| <code>data_replaceitemoflist</code> | C | <code>ITEM,INDEX</code> | <code>LIST:[name,id]</code> | 替换 |
| <code>data_itemoflist</code> | R | <code>INDEX</code> | <code>LIST:[name,id]</code> | 无效 index 返回空字符串 |
| <code>data_itemnumoflist</code> | R | <code>ITEM</code> | <code>LIST:[name,id]</code> | 1-based；找不到返回 0 |
| <code>data_lengthoflist</code> | R | - | <code>LIST:[name,id]</code> | 长度 |
| <code>data_listcontainsitem</code> | B | <code>ITEM</code> | <code>LIST:[name,id]</code> | Scratch 比较语义 |
| <code>data_showlist</code> | C | - | <code>LIST:[name,id]</code> | 显示 monitor |
| <code>data_hidelist</code> | C | - | <code>LIST:[name,id]</code> | 隐藏 monitor |

### 12.10 Procedure 与参数

| opcode | 类型 | inputs | fields | mutation |
|---|---:|---|---|---|
| <code>procedures_definition</code> | H/结构 | <code>custom_block</code> | - | 无 |
| <code>procedures_prototype</code> | 结构 | 必须为空 | - | 完整定义 mutation |
| <code>procedures_call</code> | C | 动态参数 ID | - | <code>proccode,argumentids,warp</code> |
| <code>argument_reporter_string_number</code> | R | - | <code>VALUE:[name]</code> | 无 |
| <code>argument_reporter_boolean</code> | B | - | <code>VALUE:[name]</code> | 无 |

### 12.11 内部 shadow 与 legacy 禁止项

以下 runtime opcode 存在，但 AI 默认禁止作为普通业务块生成：

| 类别 | opcode | 原因 |
|---|---|---|
| Motion | <code>motion_scroll_right</code>、<code>motion_scroll_up</code>、<code>motion_align_scene</code>、<code>motion_xscroll</code>、<code>motion_yscroll</code> | Scratch 2 兼容，多数为 no-op |
| Looks | <code>looks_hideallsprites</code>、<code>looks_changestretchby</code>、<code>looks_setstretchto</code> | legacy/no-op |
| Control | <code>control_while</code>、<code>control_for_each</code>、<code>control_get_counter</code>、<code>control_incr_counter</code>、<code>control_clear_counter</code>、<code>control_all_at_once</code> | legacy，非当前 GUI 标准积木 |
| Event | <code>event_whentouchingobject</code> | runtime 兼容但不在当前 GUI toolbox |
| Sensing | <code>sensing_loud</code>、<code>sensing_userid</code> | legacy，其中 userid 为 no-op |
| Sound | <code>sound_beats_menu</code>、<code>sound_effects_menu</code> | 内部/兼容 menu shadow，不作为独立脚本块 |

<code>sound_sounds_menu</code> 只能作为 <code>SOUND_MENU</code> 的 shadow。其他第 7.5 节菜单 opcode 同样只能 <code>shadow:true</code>，不能成为顶层或 next 链成员。

## 13. 扩展 opcode 与部署白名单

### 13.1 序列化规则

扩展 block 的序列化 opcode 是“扩展 <code>getInfo().id</code> + 下划线 + block metadata opcode”。例如：

~~~text
id = pen, opcode = stamp                 -> pen_stamp
id = text2speech, opcode = speakAndWait  -> text2speech_speakAndWait
id = videoSensing, opcode = videoOn      -> videoSensing_videoOn
~~~

大小写必须保留，尤其是 <code>videoSensing</code> 和 <code>faceSensing</code>。

扩展 argument 编码：

1. 普通 number 使用 code 4；color 使用 code 9；string 使用 code 10。
2. note 使用显式 shadow block <code>note</code>，field 为 <code>NOTE</code>。
3. matrix 使用显式 shadow block <code>matrix</code>，field 为 <code>MATRIX</code>，值是 25 位 0/1 字符串。
4. 带 menu 的 argument 使用 shadow opcode <code>extensionId_menu_menuName</code>，其唯一 field 名为 <code>menuName</code>。
5. 下表用 <code>@menuName</code> 标记菜单元数据名。input key 始终是 argument 名，不是 menu 名。
6. extension menu shadow 必须 <code>shadow:true</code>、<code>next:null</code>、<code>topLevel:false</code>，parent 指向使用它的扩展 block。
7. <code>project.extensions</code> 应精确等于 blocks 以及允许的 monitors 中实际使用的扩展 ID 去重集合；不能依赖该数组加载任意 URL。

经实际转换验证的 Pen 菜单编码：

~~~json
{
  "e_hat0001": {
    "opcode": "event_whenflagclicked",
    "next": "e_pen_change",
    "parent": null,
    "inputs": {},
    "fields": {},
    "shadow": false,
    "topLevel": true,
    "x": 48,
    "y": 48
  },
  "e_pen_change": {
    "opcode": "pen_changePenColorParamBy",
    "next": null,
    "parent": "e_hat0001",
    "inputs": {
      "COLOR_PARAM": [1, "e_pen_menu"],
      "VALUE": [1, [4, 10]]
    },
    "fields": {},
    "shadow": false,
    "topLevel": false
  },
  "e_pen_menu": {
    "opcode": "pen_menu_colorParam",
    "next": null,
    "parent": "e_pen_change",
    "inputs": {},
    "fields": {"colorParam": ["color"]},
    "shadow": true,
    "topLevel": false
  }
}
~~~

### 13.2 可用级别

| 级别 | 扩展 ID | 默认策略 |
|---|---|---|
| 本地、相对稳定 | <code>pen</code>、<code>music</code>、<code>makeymakey</code> | 宿主具备 renderer/audio/keyboard 时可以启用 |
| Scratch Link/外设 | <code>boost</code>、<code>ev3</code>、<code>microbit</code>、<code>gdxfor</code>、<code>wedo2</code> | 只有用户明确选择设备且宿主完成连接能力时启用 |
| 摄像头 | <code>videoSensing</code> | 需要摄像头权限和 video I/O |
| 人脸模型 | <code>faceSensing</code> | 内建映射存在，但必须先满足 TensorFlow/MediaPipe 依赖和模型部署 |
| 托管服务 | <code>text2speech</code>、<code>translate</code> | 需要网络、服务可用性、隐私和地区策略 |
| 源码存在但非内建 | <code>speech2text</code> | 默认禁止；必须由宿主做独立扩展注册和服务安全评审 |

<code>ExtensionManager</code> 的非示例内建映射共有 12 个：<code>pen</code>、<code>wedo2</code>、<code>music</code>、<code>microbit</code>、<code>text2speech</code>、<code>translate</code>、<code>videoSensing</code>、<code>ev3</code>、<code>makeymakey</code>、<code>boost</code>、<code>gdxfor</code>、<code>faceSensing</code>。<code>speech2text</code> 不在该映射中。

### 13.3 Music: music

| opcode | 类型 | arguments | 状态 |
|---|---:|---|---|
| <code>music_playDrumForBeats</code> | C | <code>DRUM:number=1@DRUM; BEATS:number=0.25</code> | 允许 |
| <code>music_midiPlayDrumForBeats</code> | C | <code>DRUM:number=1@DRUM; BEATS:number=0.25</code> | hidden legacy，禁止生成 |
| <code>music_restForBeats</code> | C | <code>BEATS:number=0.25</code> | 允许 |
| <code>music_playNoteForBeats</code> | C | <code>NOTE:note=60; BEATS:number=0.25</code> | 允许 |
| <code>music_setInstrument</code> | C | <code>INSTRUMENT:number=1@INSTRUMENT</code> | 允许 |
| <code>music_midiSetInstrument</code> | C | <code>INSTRUMENT:number=1</code> | hidden legacy，禁止生成 |
| <code>music_setTempo</code> | C | <code>TEMPO:number=60</code> | 允许 |
| <code>music_changeTempo</code> | C | <code>TEMPO:number=20</code> | 允许 |
| <code>music_getTempo</code> | R | - | 允许 |

菜单：<code>DRUM</code> 为字符串 <code>1..18</code>；<code>INSTRUMENT</code> 为字符串 <code>1..21</code>。Music 需要 audio engine 和随包音色资源。

### 13.4 Pen: pen

| opcode | 类型 | arguments | 状态/值 |
|---|---:|---|---|
| <code>pen_clear</code> | C | - | 允许；全局清除笔迹 |
| <code>pen_stamp</code> | C | - | 允许；角色 drawable |
| <code>pen_penDown</code> | C | - | 允许 |
| <code>pen_penUp</code> | C | - | 允许 |
| <code>pen_setPenColorToColor</code> | C | <code>COLOR:color</code> | 允许 |
| <code>pen_changePenColorParamBy</code> | C | <code>COLOR_PARAM:string="color"@colorParam; VALUE:number=10</code> | 允许 |
| <code>pen_setPenColorParamTo</code> | C | <code>COLOR_PARAM:string="color"@colorParam; VALUE:number=50</code> | 允许 |
| <code>pen_changePenSizeBy</code> | C | <code>SIZE:number=1</code> | 允许 |
| <code>pen_setPenSizeTo</code> | C | <code>SIZE:number=1</code> | 允许 |
| <code>pen_setPenShadeToNumber</code> | C | <code>SHADE:number=1</code> | hidden legacy，禁止生成 |
| <code>pen_changePenShadeBy</code> | C | <code>SHADE:number=1</code> | hidden legacy，禁止生成 |
| <code>pen_setPenHueToNumber</code> | C | <code>HUE:number=1</code> | hidden legacy，禁止生成 |
| <code>pen_changePenHueBy</code> | C | <code>HUE:number=1</code> | hidden legacy，禁止生成 |

<code>colorParam</code> 值：<code>color</code>、<code>saturation</code>、<code>brightness</code>、<code>transparency</code>。Pen 需要 renderer；stamp 和移动画线应只用于角色。

### 13.5 Makey Makey: makeymakey

| opcode | 类型 | arguments | 菜单值 |
|---|---:|---|---|
| <code>makeymakey_whenMakeyKeyPressed</code> | H | <code>KEY:string="SPACE"@KEY</code> | <code>SPACE,UP,DOWN,RIGHT,LEFT,w,a,s,d,f,g</code> |
| <code>makeymakey_whenCodePressed</code> | H | <code>SEQUENCE:string="LEFT UP RIGHT"@SEQUENCE</code> | 见下 |

<code>SEQUENCE</code> 值：<code>LEFT UP RIGHT</code>、<code>RIGHT UP LEFT</code>、<code>LEFT RIGHT</code>、<code>RIGHT LEFT</code>、<code>UP DOWN</code>、<code>DOWN UP</code>、<code>UP RIGHT DOWN LEFT</code>、<code>UP LEFT DOWN RIGHT</code>、<code>UP UP DOWN DOWN LEFT RIGHT LEFT RIGHT</code>。此扩展读取键盘状态，不需要 Scratch Link。

### 13.6 LEGO BOOST: boost

| opcode | 类型 | arguments |
|---|---:|---|
| <code>boost_motorOnFor</code> | C | <code>MOTOR_ID:string="A"@MOTOR_ID; DURATION:number=1</code> |
| <code>boost_motorOnForRotation</code> | C | <code>MOTOR_ID:string="A"@MOTOR_ID; ROTATION:number=1</code> |
| <code>boost_motorOn</code> | C | <code>MOTOR_ID:string="A"@MOTOR_ID</code> |
| <code>boost_motorOff</code> | C | <code>MOTOR_ID:string="A"@MOTOR_ID</code> |
| <code>boost_setMotorPower</code> | C | <code>MOTOR_ID:string="ABCD"@MOTOR_ID; POWER:number=100</code> |
| <code>boost_setMotorDirection</code> | C | <code>MOTOR_ID:string="A"@MOTOR_ID; MOTOR_DIRECTION:string="this way"@MOTOR_DIRECTION</code> |
| <code>boost_getMotorPosition</code> | R | <code>MOTOR_REPORTER_ID:string="A"@MOTOR_REPORTER_ID</code> |
| <code>boost_whenColor</code> | H | <code>COLOR:string="any"@COLOR</code> |
| <code>boost_seeingColor</code> | B | <code>COLOR:string="any"@COLOR</code> |
| <code>boost_whenTilted</code> | H | <code>TILT_DIRECTION_ANY:string="any"@TILT_DIRECTION_ANY</code> |
| <code>boost_getTiltAngle</code> | R | <code>TILT_DIRECTION:string="up"@TILT_DIRECTION</code> |
| <code>boost_setLightHue</code> | C | <code>HUE:number=50</code> |

菜单值：

- <code>MOTOR_ID</code>：<code>A,B,C,D,AB,ABCD</code>。
- <code>MOTOR_REPORTER_ID</code>：<code>A,B,C,D</code>。
- <code>MOTOR_DIRECTION</code>：<code>this way</code>、<code>that way</code>、<code>reverse</code>。
- <code>COLOR</code>：<code>red,blue,green,yellow,white,black,any</code>。
- <code>TILT_DIRECTION</code>：<code>up,down,left,right</code>；ANY 版再允许 <code>any</code>。

必须具备 BOOST BLE/Scratch Link 连接。

### 13.7 LEGO EV3: ev3

| opcode | 类型 | arguments |
|---|---:|---|
| <code>ev3_motorTurnClockwise</code> | C | <code>PORT:string=0@motorPorts; TIME:number=1</code> |
| <code>ev3_motorTurnCounterClockwise</code> | C | <code>PORT:string=0@motorPorts; TIME:number=1</code> |
| <code>ev3_motorSetPower</code> | C | <code>PORT:string=0@motorPorts; POWER:number=100</code> |
| <code>ev3_getMotorPosition</code> | R | <code>PORT:string=0@motorPorts</code> |
| <code>ev3_whenButtonPressed</code> | H | <code>PORT:string=0@sensorPorts</code> |
| <code>ev3_whenDistanceLessThan</code> | H | <code>DISTANCE:number=5</code> |
| <code>ev3_whenBrightnessLessThan</code> | H | <code>DISTANCE:number=50</code> |
| <code>ev3_buttonPressed</code> | B | <code>PORT:string=0@sensorPorts</code> |
| <code>ev3_getDistance</code> | R | - |
| <code>ev3_getBrightness</code> | R | - |
| <code>ev3_beep</code> | C | <code>NOTE:note=60; TIME:number=0.5</code> |

<code>motorPorts</code> 和 <code>sensorPorts</code> 的序列化值均为 <code>0,1,2,3</code>；GUI 分别显示 A-D 和 1-4。<code>ev3_whenBrightnessLessThan</code> 的数值 argument 名也确实是 <code>DISTANCE</code>，不能改成 BRIGHTNESS。需要 EV3 连接。

### 13.8 micro:bit: microbit

| opcode | 类型 | arguments |
|---|---:|---|
| <code>microbit_whenButtonPressed</code> | H | <code>BTN:string="A"@buttons</code> |
| <code>microbit_isButtonPressed</code> | B | <code>BTN:string="A"@buttons</code> |
| <code>microbit_whenGesture</code> | H | <code>GESTURE:string="moved"@gestures</code> |
| <code>microbit_displaySymbol</code> | C | <code>MATRIX:matrix="0101010101100010101000100"</code> |
| <code>microbit_displayText</code> | C | <code>TEXT:string="Hello!"</code> |
| <code>microbit_displayClear</code> | C | - |
| <code>microbit_whenTilted</code> | H | <code>DIRECTION:string="any"@tiltDirectionAny</code> |
| <code>microbit_isTilted</code> | B | <code>DIRECTION:string="any"@tiltDirectionAny</code> |
| <code>microbit_getTiltAngle</code> | R | <code>DIRECTION:string="front"@tiltDirection</code> |
| <code>microbit_whenPinConnected</code> | H | <code>PIN:string="0"@touchPins</code> |

菜单值：buttons=<code>A,B,any</code>；gestures=<code>moved,shaken,jumped</code>；tiltDirection=<code>front,back,left,right</code>；tiltDirectionAny 再允许 <code>any</code>；touchPins=<code>0,1,2</code>。MATRIX 必须恰好 25 个字符且每个字符为 0 或 1。需要 micro:bit BLE。

### 13.9 Go Direct Force & Acceleration: gdxfor

| opcode | 类型 | arguments |
|---|---:|---|
| <code>gdxfor_whenGesture</code> | H | <code>GESTURE:string="shaken"@gestureOptions</code> |
| <code>gdxfor_whenForcePushedOrPulled</code> | H | <code>PUSH_PULL:string="pushed"@pushPullOptions</code> |
| <code>gdxfor_getForce</code> | R | - |
| <code>gdxfor_whenTilted</code> | H | <code>TILT:string="any"@tiltAnyOptions</code> |
| <code>gdxfor_isTilted</code> | B | <code>TILT:string="any"@tiltAnyOptions</code> |
| <code>gdxfor_getTilt</code> | R | <code>TILT:string="front"@tiltOptions</code> |
| <code>gdxfor_isFreeFalling</code> | B | - |
| <code>gdxfor_getSpinSpeed</code> | R | <code>DIRECTION:string="z"@axisOptions</code> |
| <code>gdxfor_getAcceleration</code> | R | <code>DIRECTION:string="x"@axisOptions</code> |

菜单值：gestureOptions=<code>shaken</code>、<code>started falling</code>、<code>turned face up</code>、<code>turned face down</code>；pushPullOptions=<code>pushed,pulled</code>；axisOptions=<code>x,y,z</code>；tiltOptions=<code>front,back,left,right</code>；tiltAnyOptions 再允许 <code>any</code>。需要 GDX-FOR BLE。

### 13.10 LEGO WeDo 2.0: wedo2

| opcode | 类型 | arguments | 状态 |
|---|---:|---|---|
| <code>wedo2_motorOnFor</code> | C | <code>MOTOR_ID:string="motor"@MOTOR_ID; DURATION:number=1</code> | 允许 |
| <code>wedo2_motorOn</code> | C | <code>MOTOR_ID:string="motor"@MOTOR_ID</code> | 允许 |
| <code>wedo2_motorOff</code> | C | <code>MOTOR_ID:string="motor"@MOTOR_ID</code> | 允许 |
| <code>wedo2_startMotorPower</code> | C | <code>MOTOR_ID:string="motor"@MOTOR_ID; POWER:number=100</code> | 允许 |
| <code>wedo2_setMotorDirection</code> | C | <code>MOTOR_ID:string="motor"@MOTOR_ID; MOTOR_DIRECTION:string="this way"@MOTOR_DIRECTION</code> | 允许 |
| <code>wedo2_setLightHue</code> | C | <code>HUE:number=50</code> | 允许 |
| <code>wedo2_playNoteFor</code> | C | <code>NOTE:number=60; DURATION:number=0.5</code> | hidden legacy，禁止生成 |
| <code>wedo2_whenDistance</code> | H | <code>OP:string="&lt;"@OP; REFERENCE:number=50</code> | 允许 |
| <code>wedo2_whenTilted</code> | H | <code>TILT_DIRECTION_ANY:string="any"@TILT_DIRECTION_ANY</code> | 允许 |
| <code>wedo2_getDistance</code> | R | - | 允许 |
| <code>wedo2_isTilted</code> | B | <code>TILT_DIRECTION_ANY:string="any"@TILT_DIRECTION_ANY</code> | 允许 |
| <code>wedo2_getTiltAngle</code> | R | <code>TILT_DIRECTION:string="up"@TILT_DIRECTION</code> | 允许 |

菜单值：MOTOR_ID=<code>motor</code>、<code>motor A</code>、<code>motor B</code>、<code>all motors</code>；MOTOR_DIRECTION=<code>this way</code>、<code>that way</code>、<code>reverse</code>；OP=<code>&lt;</code>、<code>&gt;</code>；tilt=<code>up,down,left,right</code>，ANY 版再允许 <code>any</code>。需要 WeDo BLE。

### 13.11 Video Sensing: videoSensing

| opcode | 类型 | arguments |
|---|---:|---|
| <code>videoSensing_whenMotionGreaterThan</code> | H | <code>REFERENCE:number=10</code> |
| <code>videoSensing_videoOn</code> | R | <code>ATTRIBUTE:number="motion"@ATTRIBUTE; SUBJECT:number="this sprite"@SUBJECT</code> |
| <code>videoSensing_videoToggle</code> | C | <code>VIDEO_STATE:number="on"@VIDEO_STATE</code> |
| <code>videoSensing_setVideoTransparency</code> | C | <code>TRANSPARENCY:number=50</code> |

尽管 metadata 把部分菜单 argument 标为 number，实际序列化菜单值是字符串：ATTRIBUTE=<code>motion,direction</code>；SUBJECT=<code>this sprite,Stage</code>；VIDEO_STATE=<code>off,on,on-flipped</code>。生成器必须使用实际字符串。需要摄像头权限和 video I/O。

### 13.12 Face Sensing: faceSensing

| opcode | 类型 | arguments | target |
|---|---:|---|---|
| <code>faceSensing_goToPart</code> | C | <code>PART:string="2"@PART</code> | 角色 |
| <code>faceSensing_pointInFaceTiltDirection</code> | C | - | 角色 |
| <code>faceSensing_setSizeToFaceSize</code> | C | - | 角色 |
| <code>faceSensing_whenTilted</code> | H | <code>DIRECTION:string="left"@TILT</code> | 两者 |
| <code>faceSensing_whenSpriteTouchesPart</code> | H | <code>PART:string="2"@PART</code> | 角色 |
| <code>faceSensing_whenFaceDetected</code> | H | - | 两者 |
| <code>faceSensing_faceIsDetected</code> | B | - | 两者 |
| <code>faceSensing_faceTilt</code> | R | - | 两者 |
| <code>faceSensing_faceSize</code> | R | - | 两者 |

PART 的实际序列化值：left eye=<code>0</code>、right eye=<code>1</code>、nose=<code>2</code>、mouth=<code>3</code>、left ear=<code>4</code>、right ear=<code>5</code>、between eyes=<code>6</code>、top of head=<code>7</code>。TILT 值为 <code>left,right</code>。

该扩展内建映射存在，但隔离安装的 <code>@scratch/scratch-vm@15.0.1</code> 未自动满足 <code>@tensorflow-models/face-detection</code> 与 <code>@mediapipe/face_detection</code>，直接按 ID 加载会抛模块缺失。宿主还必须部署 <code>/chunks/mediapipe/face_detection</code> 或允许源码中的 jsDelivr fallback。未完成依赖、CSP、摄像头和模型验证时必须禁用。

### 13.13 Text to Speech: text2speech

| opcode | 类型 | arguments |
|---|---:|---|
| <code>text2speech_speakAndWait</code> | C | <code>WORDS:string="hello"</code> |
| <code>text2speech_setVoice</code> | C | <code>VOICE:string="ALTO"@voices</code> |
| <code>text2speech_setLanguage</code> | C | <code>LANGUAGE:string="en"@languages</code> |

voices=<code>ALTO,TENOR,SQUEAK,GIANT,KITTEN</code>。languages 精确集合：<code>ar,zh-cn,da,nl,en,fr,de,hi,is,it,ja,ko,nb,pl,pt-br,pt,ro,ru,es,es-419,sv,tr,cy</code>。依赖 <code>https://synthesis-service.scratch.mit.edu</code> 与音频输出。

### 13.14 Translate: translate

| opcode | 类型 | arguments |
|---|---:|---|
| <code>translate_getTranslate</code> | R | <code>WORDS:string="hello"; LANGUAGE:string@languages</code> |
| <code>translate_getViewerLanguage</code> | R | - |

languages 精确去重集合：<code>am,ar,az,eu,bg,ca,zh-cn,zh-tw,hr,cs,da,nl,en,et,fi,fr,gl,de,el,he,hu,is,id,ga,it,ja,ko,ckb,lv,lt,mi,nb,fa,pl,pt,ro,ru,gd,sr,sk,sl,es,sv,th,tr,uk,vi,cy,zu</code>。依赖 <code>https://translate-service.scratch.mit.edu/</code>。

### 13.15 Speech to Text: speech2text

源码目录包含以下 opcode：

| opcode | 类型 | arguments |
|---|---:|---|
| <code>speech2text_listenAndWait</code> | C | - |
| <code>speech2text_whenIHearHat</code> | H | <code>PHRASE:string</code> |
| <code>speech2text_getSpeech</code> | R | - |

它依赖麦克风和 <code>wss://speech.scratch.mit.edu</code>，但不在当前 <code>builtinExtensions</code> 映射中。<code>loadExtensionURL('speech2text')</code> 会进入远程 Worker 路径，而不是加载该源码目录。AI 默认必须禁用这三个 opcode。只有宿主完成显式注册、Worker/CSP、权限、隐私与服务可用性测试后，才可建立单独版本档。

### 13.16 扩展加载规则

1. loader 会从 opcode 第一个下划线前的前缀识别扩展；核心前缀不会当作扩展。
2. <code>loadExtensionURL()</code> 返回 Promise；<code>isExtensionLoaded()</code> 可能在扩展真正 ready 前返回 true，不能代替等待 Promise。
3. <code>project.json.extensions</code> 不承载第三方 URL 映射。向该数组放 URL 不会自动建立可信加载。
4. 禁止 AI 指定远程扩展 URL。
5. 所有扩展必须由宿主白名单启用，并在生成请求中明确给出 <code>allowedExtensions</code>。
6. 硬件、摄像头、麦克风和托管服务扩展必须有不可用状态与用户可理解的错误处理。
7. hidden legacy 扩展 opcode 只允许导入旧项目，禁止 AI 新生成。

## 14. 严格预检

### 14.1 推荐安全限额

下表是宿主应用默认值，不是 Scratch 协议的官方上限；可以按产品配额下调，放宽时必须重新做资源消耗测试。

| 项目 | 默认上限 |
|---|---:|
| SB3 压缩体积 | 50 MiB |
| ZIP 解压后总量 | 200 MiB |
| ZIP entry 数 | 1000 |
| 单个 project.json | 10 MiB |
| JSON 最大深度 | 128 |
| target 数 | 100 |
| 单 target block 数 | 20000 |
| 项目 block 总数 | 100000 |
| 单 mutation 字符串 | 64 KiB |
| 单普通文本 input | 1 MiB |
| comment.text | 8000 字符 |
| 列表项数 | 业务默认 10000；VM 硬限制 200000 |

还必须检查 ZIP 解压缩比、解析时间、图遍历时间和运行冒烟超时，避免 ZIP bomb、深层 JSON、超大图或永不 yield 的生成内容。

### 14.2 预检顺序

1. 在解压前检查压缩体积、entry 数、路径与重复文件名。
2. 流式统计解压后总量和单文件大小，不把不可信 ZIP 一次性无界展开。
3. 读取唯一的 <code>project.json</code>，执行 JSON 大小、深度、字符串和节点数限制。
4. 校验根字段、唯一舞台、target 顺序、唯一名称和 target 专有属性。
5. 建立项目级 ID 索引，检查各命名空间唯一性。
6. 校验变量、列表、广播定义和作用域。
7. 对每个 target 校验 block shape、opcode contract、tuple、field、图闭合、parent 反向一致性和无环性。
8. 校验 procedure mutation 数组和 call/definition 配对。
9. 校验 comments 与 monitors。
10. 从 descriptor 收集资源文件名，逐个检查存在性、扩展名、magic bytes、MD5、尺寸、解码和 SVG 安全性。
11. 计算实际扩展集合，和请求的 <code>allowedExtensions</code> 及 <code>project.extensions</code> 比较。
12. 打包后交给 parser 和隔离 VM；任何 warning、repair、broken 资源、缺失扩展或往返语义变化都必须进入失败处理。

### 14.3 结构图参考验证器

以下函数完整校验严格档的 block 基础形状、tuple、compact primitive、引用所有权、parent 和环。opcode 的 exact input/field/target contract 由调用方以 <code>validateContract</code> 函数传入；该函数应由第 12、13 节的机器可读表生成。

~~~js
const ID_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{7,63}$/;
const BASE_KEYS = new Set([
    'opcode', 'next', 'parent', 'inputs', 'fields', 'shadow', 'topLevel',
    'x', 'y', 'mutation', 'comment'
]);

const fail = message => {
    throw new Error(message);
};

const isPlainObject = value => (
    value !== null &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.getPrototypeOf(value) === Object.prototype
);

const isFiniteNumeric = value => {
    if (typeof value === 'number') return Number.isFinite(value);
    if (typeof value !== 'string' || value.trim() === '') return false;
    return Number.isFinite(Number(value));
};

const validatePrimitive = (value, allowedRole, context) => {
    if (!Array.isArray(value) || !Number.isInteger(value[0])) {
        fail(context + ': compact primitive 必须是数组');
    }

    const code = value[0];
    if (allowedRole === 'shadow' && (code < 4 || code > 11)) {
        fail(context + ': shadow 只允许 code 4-11');
    }
    if (allowedRole === 'active' && code !== 12 && code !== 13) {
        fail(context + ': active compact reporter 只允许 code 12/13');
    }

    if (code >= 4 && code <= 8) {
        if (value.length !== 2 || !isFiniteNumeric(value[1])) {
            fail(context + ': 数字 primitive 非法');
        }
        if ((code === 6 || code === 7) && !Number.isInteger(Number(value[1]))) {
            fail(context + ': 整数 primitive 包含非整数');
        }
        if ((code === 5 || code === 6) && Number(value[1]) < 0) {
            fail(context + ': 正数/自然数 primitive 小于 0');
        }
        return;
    }

    if (code === 9) {
        if (value.length !== 2 || !/^#[0-9A-Fa-f]{6}$/.test(value[1])) {
            fail(context + ': 颜色 primitive 非法');
        }
        return;
    }

    if (code === 10) {
        if (
            value.length !== 2 ||
            !(
                typeof value[1] === 'string' ||
                (typeof value[1] === 'number' && Number.isFinite(value[1]))
            )
        ) {
            fail(context + ': 文本 primitive 非法');
        }
        return;
    }

    if (code === 11 || code === 12 || code === 13) {
        if (
            value.length !== 3 ||
            typeof value[1] !== 'string' ||
            typeof value[2] !== 'string' ||
            value[2] === ''
        ) {
            fail(context + ': 引用 primitive 非法');
        }
        return;
    }

    fail(context + ': 未知 primitive code ' + code);
};

export function validateStrictBlockGraph(blocks, validateContract) {
    if (!isPlainObject(blocks)) fail('blocks 必须是普通对象');
    if (typeof validateContract !== 'function') {
        fail('必须提供 opcode contract 验证函数');
    }

    const ids = Object.keys(blocks);
    const owners = new Map(ids.map(id => [id, []]));
    const edges = new Map(ids.map(id => [id, []]));

    for (const id of ids) {
        if (!ID_PATTERN.test(id)) fail('非法 block ID: ' + id);
        if (!isPlainObject(blocks[id])) {
            fail('严格档禁止顶层 primitive 或非对象 block: ' + id);
        }
    }

    const registerReference = (ownerId, childId, role, expectedShadow) => {
        if (typeof childId !== 'string' || !Object.hasOwn(blocks, childId)) {
            fail(ownerId + ' 的 ' + role + ' 引用不存在: ' + String(childId));
        }
        const child = blocks[childId];
        if (child.shadow !== expectedShadow) {
            fail(ownerId + ' 的 ' + role + ' shadow 状态错误: ' + childId);
        }
        owners.get(childId).push({ownerId, role});
        edges.get(ownerId).push(childId);
    };

    for (const id of ids) {
        const block = blocks[id];
        const keys = Object.keys(block);
        for (const key of keys) {
            if (!BASE_KEYS.has(key)) fail(id + ' 包含未知基础属性: ' + key);
        }

        if (typeof block.opcode !== 'string' || block.opcode === '') {
            fail(id + '.opcode 非法');
        }
        if (!(block.next === null || typeof block.next === 'string')) {
            fail(id + '.next 必须为 string 或 null');
        }
        if (!(block.parent === null || typeof block.parent === 'string')) {
            fail(id + '.parent 必须为 string 或 null');
        }
        if (!isPlainObject(block.inputs) || !isPlainObject(block.fields)) {
            fail(id + '.inputs/fields 必须是普通对象');
        }
        if (typeof block.shadow !== 'boolean' || typeof block.topLevel !== 'boolean') {
            fail(id + '.shadow/topLevel 必须是 Boolean');
        }
        if (Object.hasOwn(block, 'mutation') && !isPlainObject(block.mutation)) {
            fail(id + '.mutation 必须是普通对象');
        }
        if (Object.hasOwn(block, 'comment') && typeof block.comment !== 'string') {
            fail(id + '.comment 必须是 comment ID 字符串');
        }
        if (block.topLevel !== (block.parent === null)) {
            fail(id + ' 的 topLevel 与 parent 不一致');
        }
        if (block.topLevel) {
            if (!Number.isFinite(block.x) || !Number.isFinite(block.y)) {
                fail(id + ' 顶层 block 缺少有限 x/y');
            }
        } else if (Object.hasOwn(block, 'x') || Object.hasOwn(block, 'y')) {
            fail(id + ' 非顶层 block 不应包含 x/y');
        }
        if (block.shadow && (block.topLevel || block.next !== null)) {
            fail(id + ' shadow 不能顶层或拥有 next');
        }

        if (block.next !== null) {
            registerReference(id, block.next, 'next', false);
        }

        for (const [inputName, tuple] of Object.entries(block.inputs)) {
            const context = id + '.inputs.' + inputName;
            if (!Array.isArray(tuple) || ![1, 2, 3].includes(tuple[0])) {
                fail(context + ': tuple status 非法');
            }

            if (tuple[0] === 1) {
                if (tuple.length !== 2) fail(context + ': status 1 长度必须为 2');
                if (typeof tuple[1] === 'string') {
                    registerReference(id, tuple[1], 'input-shadow:' + inputName, true);
                } else {
                    validatePrimitive(tuple[1], 'shadow', context);
                }
                continue;
            }

            if (tuple[0] === 2) {
                if (tuple.length !== 2) fail(context + ': status 2 长度必须为 2');
                if (typeof tuple[1] === 'string') {
                    registerReference(id, tuple[1], 'input-active:' + inputName, false);
                } else {
                    validatePrimitive(tuple[1], 'active', context);
                }
                continue;
            }

            if (tuple.length !== 3) fail(context + ': status 3 长度必须为 3');
            if (JSON.stringify(tuple[1]) === JSON.stringify(tuple[2])) {
                fail(context + ': status 3 的 active 与 shadow 必须不同');
            }
            if (typeof tuple[1] === 'string') {
                registerReference(id, tuple[1], 'input-active:' + inputName, false);
            } else {
                validatePrimitive(tuple[1], 'active', context + '.active');
            }
            if (typeof tuple[2] === 'string') {
                registerReference(id, tuple[2], 'input-shadow:' + inputName, true);
            } else {
                validatePrimitive(tuple[2], 'shadow', context + '.shadow');
            }
        }

        validateContract(id, block);
    }

    for (const id of ids) {
        const block = blocks[id];
        const incoming = owners.get(id);
        if (block.topLevel) {
            if (incoming.length !== 0) fail(id + ' 顶层 block 被其他 block 拥有');
        } else {
            if (incoming.length !== 1) {
                fail(id + ' 必须有且只有一个结构拥有者，实际为 ' + incoming.length);
            }
            if (incoming[0].ownerId !== block.parent) {
                fail(id + '.parent 与反向拥有者不一致');
            }
        }
    }

    const state = new Map(ids.map(id => [id, 0]));
    const visit = id => {
        if (state.get(id) === 1) fail('block 图存在环，涉及 ' + id);
        if (state.get(id) === 2) return;
        state.set(id, 1);
        for (const childId of edges.get(id)) visit(childId);
        state.set(id, 2);
    };
    for (const id of ids) visit(id);

    return true;
}
~~~

opcode contract 验证还必须检查：允许的 input/field key 集合、必需输入的默认 shadow、block 类型、target 限制、field 枚举、变量/列表/广播 ID 与名称、procedure mutation、extension allowlist。不得用“opcode 前缀合法”替代 exact opcode 白名单。

## 15. SB3 打包

以下浏览器/Node ESM 示例要求 <code>jszip</code>，输入 <code>assetFiles</code> 是 <code>Map&lt;md5ext, Uint8Array&gt;</code>。调用前必须已经完成第 14 节校验和实际 MD5 检查。

~~~js
import JSZip from 'jszip';

function collectRequiredAssetNames(projectJson) {
    const names = new Set();
    for (const target of projectJson.targets) {
        for (const descriptor of target.costumes.concat(target.sounds)) {
            const expected = descriptor.assetId + '.' + descriptor.dataFormat;
            if (descriptor.md5ext !== expected) {
                throw new Error('md5ext 与 assetId/dataFormat 不一致: ' + descriptor.name);
            }
            names.add(expected);
        }
    }
    return names;
}

export async function buildSb3(projectJson, assetFiles) {
    if (!(assetFiles instanceof Map)) {
        throw new TypeError('assetFiles 必须是 Map');
    }

    const required = collectRequiredAssetNames(projectJson);
    for (const name of required) {
        const data = assetFiles.get(name);
        if (!(data instanceof Uint8Array)) {
            throw new Error('缺少 Uint8Array 资源: ' + name);
        }
    }
    for (const name of assetFiles.keys()) {
        if (!required.has(name)) {
            throw new Error('SB3 包含未引用资源: ' + name);
        }
    }

    const zip = new JSZip();
    zip.file('project.json', JSON.stringify(projectJson));
    for (const name of required) zip.file(name, assetFiles.get(name));

    return zip.generateAsync({
        type: 'arraybuffer',
        mimeType: 'application/x.scratch.sb3',
        compression: 'DEFLATE',
        compressionOptions: {level: 6}
    });
}
~~~

## 16. scratch-vm 与 scratch-gui 注入

### 16.1 API 稳定性边界

| 分类 | API/对象 | 使用策略 |
|---|---|---|
| 相对稳定 | <code>new VirtualMachine()</code>、<code>start()</code>、<code>stopAll()</code> | 可以使用 |
| 相对稳定 | <code>loadProject()</code>、<code>saveProjectSb3()</code>、<code>toJSON()</code> | 完整项目主路径 |
| 相对稳定 | <code>attachStorage()</code>、<code>attachRenderer()</code>、<code>attachAudioEngine()</code>、<code>attachV2BitmapAdapter()</code> | 自建 VM 时使用 |
| 相对稳定 | <code>setEditingTarget()</code>、<code>refreshWorkspace()</code> | GUI 同步 |
| 相对稳定 | <code>addSprite()</code>、<code>addCostume()</code>、<code>addSound()</code> | 资源级增量导入 |
| 半公开 | <code>shareBlocksToTarget()</code> | 只在锁定版本适配器中使用 |
| 内部 | <code>vm.runtime</code>、<code>vm.extensionManager</code> | 只用于适配器深检和测试 |
| 禁止业务层依赖 | <code>target.blocks._blocks</code>、<code>deserializeBlocks()</code>、<code>runtime.monitorBlocks</code> | 不直接修改 |

包根入口只正式导出 VirtualMachine、ArgumentType 和 BlockType；直接 require <code>src/serialization/sb3.js</code> 不属于稳定包接口。

### 16.2 自建 VM 装配

scratch-gui 已经拥有 VM 时，必须取得 GUI 使用的同一个实例，不能另建第二个 VM 后期待 GUI 自动同步。只有隔离预检才创建第二个 VM。

~~~js
const VirtualMachine = require('@scratch/scratch-vm');

function createVm({storage, renderer, audioEngine, bitmapAdapter}) {
    const vm = new VirtualMachine();
    vm.attachStorage(storage);
    vm.attachRenderer(renderer);
    if (audioEngine) vm.attachAudioEngine(audioEngine);
    if (bitmapAdapter) vm.attachV2BitmapAdapter(bitmapAdapter);
    vm.start();
    return vm;
}
~~~

缺 renderer 时，项目可能加载成功但造型没有 skin；缺 audio engine 时，声音可能加载但不可播放。因此生产 GUI VM 应完整挂载依赖，隔离测试也应尽量使用相同装配。

### 16.3 loadProject 事件顺序

主要顺序：

~~~text
vm.loadProject(input)
  -> scratch-parser validate
  -> vm.clear()
       -> runtime.dispose()
       -> editingTarget = null
       -> targetsUpdate
  -> SB3 资源和 target 反序列化
  -> 加载所需扩展并等待 Promise
  -> 安装 targets、drawable、变量引用
  -> targetsUpdate
  -> workspaceUpdate({xml})
  -> runtime.setEditingTarget
  -> runtime PROJECT_LOADED
  -> loadProject Promise resolve
~~~

公开完成信号是 <code>await vm.loadProject(input)</code>。<code>PROJECT_LOADED</code> 没有由当前 VM constructor 转发为稳定顶层事件；直接监听 <code>vm.runtime</code> 属于内部依赖。

GUI 相关事件：

| 事件名 | 大小写 | 用途 |
|---|---|---|
| <code>targetsUpdate</code> | 精确小写开头 | 更新 target 列表和 editingTarget |
| <code>workspaceUpdate</code> | 精确小写开头 | payload 为 <code>{xml}</code>，重建 Scratch Blocks 工作区 |
| <code>PROJECT_CHANGED</code> | 大写 | 标记项目 dirty |
| <code>MONITORS_UPDATE</code> | 大写 | 更新 monitor UI |
| <code>EXTENSION_ADDED</code> | 大写 | 添加扩展分类 |
| <code>BLOCKSINFO_UPDATE</code> | 大写 | 刷新扩展 block 信息 |

### 16.4 事务式完整加载

~~~js
function normalizeVmError(value) {
    if (value instanceof Error) return value;
    if (typeof value === 'string') {
        try {
            const details = JSON.parse(value);
            const error = new Error(
                details.message || details.validationError || 'Scratch 项目校验失败'
            );
            error.details = details;
            return error;
        } catch {
            return new Error(value);
        }
    }
    const error = new Error('未知 Scratch VM 错误');
    error.details = value;
    return error;
}

function assertLoadedAssets(vm) {
    const errors = [];
    for (const target of vm.runtime.targets) {
        const targetName = target.getName();
        for (const costume of target.sprite.costumes || []) {
            if (costume.broken) errors.push(targetName + '/' + costume.name + ' 造型损坏');
            if (vm.runtime.renderer && costume.skinId == null) {
                errors.push(targetName + '/' + costume.name + ' 缺少 skinId');
            }
        }
        for (const sound of target.sprite.sounds || []) {
            if (sound.broken) errors.push(targetName + '/' + sound.name + ' 声音损坏');
            if (vm.runtime.audioEngine && sound.soundId == null) {
                errors.push(targetName + '/' + sound.name + ' 缺少 soundId');
            }
        }
    }
    if (errors.length > 0) throw new Error(errors.join('; '));
}

export async function transactionallyLoadProject(vm, sb3ArrayBuffer) {
    const snapshotBlob = await vm.saveProjectSb3();
    const snapshotBuffer = await snapshotBlob.arrayBuffer();

    try {
        await vm.loadProject(sb3ArrayBuffer);
        assertLoadedAssets(vm);

        // loadProject 已发最终事件；显式 refresh 作为宿主防御性同步。
        vm.refreshWorkspace();

        return JSON.parse(vm.toJSON());
    } catch (value) {
        const originalError = normalizeVmError(value);
        try {
            await vm.loadProject(snapshotBuffer);
            vm.refreshWorkspace();
        } catch (rollbackValue) {
            originalError.rollbackError = normalizeVmError(rollbackValue);
        }
        throw originalError;
    }
}
~~~

必须在加载前保存快照，因为 <code>deserializeProject()</code> 会先 clear 旧项目；新项目后续失败时，旧项目已经可能被 dispose。

### 16.5 增量积木路径

<code>shareBlocksToTarget</code> 接受的是 hydrated block 数组，不是 SB3 压缩 block map。hydrated input 形状如下：

~~~js
const hydratedBlocks = [
    {
        id: 'b_hat0001',
        opcode: 'event_whenflagclicked',
        next: 'b_move001',
        parent: null,
        inputs: {},
        fields: {},
        shadow: false,
        topLevel: true,
        x: 80,
        y: 80
    },
    {
        id: 'b_move001',
        opcode: 'motion_movesteps',
        next: null,
        parent: 'b_hat0001',
        inputs: {
            STEPS: {
                name: 'STEPS',
                block: 'b_num0001',
                shadow: 'b_num0001'
            }
        },
        fields: {},
        shadow: false,
        topLevel: false
    },
    {
        id: 'b_num0001',
        opcode: 'math_number',
        next: null,
        parent: 'b_move001',
        inputs: {},
        fields: {NUM: {name: 'NUM', value: '10'}},
        shadow: true,
        topLevel: false
    }
];
~~~

事务调用顺序：

~~~js
export async function appendHydratedBlocks(vm, targetId, hydratedBlocks) {
    if (!Array.isArray(hydratedBlocks) || hydratedBlocks.length === 0) {
        throw new TypeError('hydratedBlocks 必须是非空数组');
    }
    if (!vm.runtime.getTargetById(targetId)) {
        throw new Error('目标 target 不存在: ' + targetId);
    }

    const snapshot = await vm.saveProjectSb3();
    const snapshotBuffer = await snapshot.arrayBuffer();

    try {
        await vm.shareBlocksToTarget(hydratedBlocks, targetId);
        vm.setEditingTarget(targetId);
        vm.refreshWorkspace();
    } catch (value) {
        const error = normalizeVmError(value);
        try {
            await vm.loadProject(snapshotBuffer);
            vm.refreshWorkspace();
        } catch (rollbackValue) {
            error.rollbackError = normalizeVmError(rollbackValue);
        }
        throw error;
    }
}
~~~

<code>shareBlocksToTarget</code> 会深拷贝、分配新 block ID、识别并加载扩展、处理跨 target 变量冲突、创建 block，并修复外部来源变量引用。但它不会主动发送最终 <code>workspaceUpdate</code>。<code>setEditingTarget</code> 在 target 已经是当前 target 时会提前返回，因此最后仍必须调用 <code>refreshWorkspace()</code>。

增量路径不得用于：完整 target 重建、复杂 procedure、monitor、多个资源、第三方扩展或要求强事务的项目级修改。此类需求统一走完整 SB3。

### 16.6 角色和资源级 API

- 完整角色增量：<code>await vm.addSprite(sprite3ArrayBuffer)</code>。
- 造型增量：<code>await vm.addCostume(md5ext, costume, targetId)</code>。
- 声音增量：<code>await vm.addSound(sound, targetId)</code>。

调用前必须确认 target 存在。资源 API 仍要求真实 storage asset、renderer/audio 装配和加载后健康检查。

## 17. 完整可打包示例

### 17.1 project.json

以下项目包含：一个舞台、一个角色、全局变量、广播、monitor、repeat 子栈、数字/文本/广播 compact primitive。它使用第 17.2 节的单一 SVG 作为舞台和角色造型。

~~~json
{
  "targets": [
    {
      "isStage": true,
      "name": "Stage",
      "variables": {
        "v_score01": ["score", 0]
      },
      "lists": {},
      "broadcasts": {
        "bc_done01": "done"
      },
      "blocks": {},
      "comments": {},
      "currentCostume": 0,
      "costumes": [
        {
          "assetId": "8d1e4b38ed4f03643ffc324edc9e572f",
          "name": "backdrop1",
          "bitmapResolution": 1,
          "md5ext": "8d1e4b38ed4f03643ffc324edc9e572f.svg",
          "dataFormat": "svg",
          "rotationCenterX": 48,
          "rotationCenterY": 48
        }
      ],
      "sounds": [],
      "volume": 100,
      "layerOrder": 0,
      "tempo": 60,
      "videoTransparency": 50,
      "videoState": "on",
      "textToSpeechLanguage": null
    },
    {
      "isStage": false,
      "name": "Sprite1",
      "variables": {},
      "lists": {},
      "broadcasts": {},
      "blocks": {
        "b_hat_main": {
          "opcode": "event_whenflagclicked",
          "next": "b_set_score",
          "parent": null,
          "inputs": {},
          "fields": {},
          "shadow": false,
          "topLevel": true,
          "x": 48,
          "y": 48
        },
        "b_set_score": {
          "opcode": "data_setvariableto",
          "next": "b_repeat",
          "parent": "b_hat_main",
          "inputs": {
            "VALUE": [1, [4, 0]]
          },
          "fields": {
            "VARIABLE": ["score", "v_score01"]
          },
          "shadow": false,
          "topLevel": false
        },
        "b_repeat": {
          "opcode": "control_repeat",
          "next": "b_broadcast_done",
          "parent": "b_set_score",
          "inputs": {
            "TIMES": [1, [6, 5]],
            "SUBSTACK": [2, "b_change_score"]
          },
          "fields": {},
          "shadow": false,
          "topLevel": false
        },
        "b_change_score": {
          "opcode": "data_changevariableby",
          "next": "b_move001",
          "parent": "b_repeat",
          "inputs": {
            "VALUE": [1, [4, 1]]
          },
          "fields": {
            "VARIABLE": ["score", "v_score01"]
          },
          "shadow": false,
          "topLevel": false
        },
        "b_move001": {
          "opcode": "motion_movesteps",
          "next": "b_say_step",
          "parent": "b_change_score",
          "inputs": {
            "STEPS": [1, [4, 10]]
          },
          "fields": {},
          "shadow": false,
          "topLevel": false
        },
        "b_say_step": {
          "opcode": "looks_say",
          "next": null,
          "parent": "b_move001",
          "inputs": {
            "MESSAGE": [1, [10, "step"]]
          },
          "fields": {},
          "shadow": false,
          "topLevel": false
        },
        "b_broadcast_done": {
          "opcode": "event_broadcast",
          "next": null,
          "parent": "b_repeat",
          "inputs": {
            "BROADCAST_INPUT": [1, [11, "done", "bc_done01"]]
          },
          "fields": {},
          "shadow": false,
          "topLevel": false
        },
        "b_hat_receive": {
          "opcode": "event_whenbroadcastreceived",
          "next": "b_say_done",
          "parent": null,
          "inputs": {},
          "fields": {
            "BROADCAST_OPTION": ["done", "bc_done01"]
          },
          "shadow": false,
          "topLevel": true,
          "x": 320,
          "y": 48
        },
        "b_say_done": {
          "opcode": "looks_say",
          "next": null,
          "parent": "b_hat_receive",
          "inputs": {
            "MESSAGE": [1, [10, "done"]]
          },
          "fields": {},
          "shadow": false,
          "topLevel": false
        }
      },
      "comments": {},
      "currentCostume": 0,
      "costumes": [
        {
          "assetId": "8d1e4b38ed4f03643ffc324edc9e572f",
          "name": "costume1",
          "bitmapResolution": 1,
          "md5ext": "8d1e4b38ed4f03643ffc324edc9e572f.svg",
          "dataFormat": "svg",
          "rotationCenterX": 48,
          "rotationCenterY": 48
        }
      ],
      "sounds": [],
      "volume": 100,
      "layerOrder": 1,
      "visible": true,
      "x": 0,
      "y": 0,
      "size": 100,
      "direction": 90,
      "draggable": false,
      "rotationStyle": "all around"
    }
  ],
  "monitors": [
    {
      "id": "v_score01",
      "mode": "default",
      "opcode": "data_variable",
      "params": {
        "VARIABLE": "score"
      },
      "spriteName": null,
      "value": 0,
      "width": 0,
      "height": 0,
      "x": 5,
      "y": 5,
      "visible": true,
      "sliderMin": 0,
      "sliderMax": 100,
      "isDiscrete": true
    }
  ],
  "extensions": [],
  "meta": {
    "semver": "3.0.0",
    "vm": "15.0.1",
    "agent": "AICM-Scratch-Generator"
  }
}
~~~

### 17.2 资源文件

ZIP 根目录创建 <code>8d1e4b38ed4f03643ffc324edc9e572f.svg</code>，UTF-8 内容必须逐字为：

~~~svg
<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96"><rect width="96" height="96" fill="#4c97ff"/><circle cx="48" cy="48" r="24" fill="#ffffff"/></svg>
~~~

该字节串的 MD5 是 <code>8d1e4b38ed4f03643ffc324edc9e572f</code>。ZIP 只需一份资源文件，两个 target 可以复用。

## 18. 往返验收

### 18.1 必须通过的阶段

1. JSON 解析和宿主严格预检通过。
2. scratch-parser 校验通过。
3. 隔离 VM <code>loadProject</code> resolve。
4. target 数、名称、变量、列表、广播、block ID 和关键图边与输入一致。
5. 所有所需扩展 Promise 完成，实际扩展集合正确。
6. 没有 broken costume/sound；完整装配下 skinId/soundId 有效。
7. 切换每个编辑 target 后都能得到非空 <code>workspaceUpdate.xml</code>，XML 包含其顶层脚本和 procedure。
8. <code>saveProjectSb3</code> 成功；保存 ZIP 的每个 descriptor 都有对应 entry。
9. 新 VM 能加载保存结果。
10. 两次 VM 的规范化语义一致。
11. GUI target 列表、editingTarget、workspace 和 monitors 一致。
12. 受限时间运行冒烟后无宿主捕获的执行异常，并能 <code>stopAll()</code>。

### 18.2 语义比较

不能把 <code>project.json</code> 原始字符串或 ZIP 字节逐字比较，因为序列化可能调整属性顺序、层顺序、primitive 表示和经过清洗的资源 metadata。应比较：

- target 的 isStage/name 和舞台/角色状态。
- 变量、列表、广播的 ID、名称、值和作用域。
- 以顶层脚本为根的 opcode、field、input、next、SUBSTACK 语义树。
- procedure 的 proccode、参数 ID/名称/默认值/warp 和 call 参数。
- monitor 的语义 key、mode、可见性和布局。
- 资源名称、格式、旋转中心及“保存结果 descriptor 与保存 ZIP”的一致性。
- 扩展集合。

以下变化一律失败：悬挂引用被删除、shadow 被补建或移除、parent 被改写、变量 ID 被替换、procedure 参数丢失、未知 opcode 消失、资源变成 broken fallback。若生产输入已预清洗为 canonical SVG，asset ID 意外变化也失败。

### 18.3 已执行验证结果

针对本规范另建的 headless fixture 已实际完成：

~~~json
{
  "status": "ok",
  "vmVersion": "15.0.1",
  "targetCount": 2,
  "ordinaryBlockCount": 19,
  "roundTrips": 2,
  "parserRejectedEmptyCostumes": true,
  "workspaceXmlGenerated": true,
  "builtinExtensionsLoaded": ["pen", "music"]
}
~~~

19 个 block 覆盖普通命令、hat、C 形 SUBSTACK、变量 field、广播 primitive、文本/数字 primitive、当前 procedure 格式、argument reporter、procedure call、Pen menu shadow 和 Music menu shadow。fixture 使用真实 SVG ZIP，并验证保存结果自身的所有 <code>md5ext</code> 都存在。headless 环境未挂 renderer，因此测试中的“无法创建 skin”日志是刻意的环境警告；生产 GUI 验收不能忽略该警告。

第 17 节的 JSON 与 SVG 还被直接从本文 Markdown 围栏提取，不经手工重写，并得到：

~~~json
{
  "status": "ok",
  "vmVersion": "15.0.1",
  "extractedFromManual": true,
  "rawAssetId": "8d1e4b38ed4f03643ffc324edc9e572f",
  "targetCount": 2,
  "blockCount": 9,
  "roundTrips": 2,
  "workspaceXmlGenerated": true,
  "savedAssetsResolved": true,
  "emptyCostumesRejected": true
}
~~~

本文中的可复制代码也已直接抽取执行：

| 代码 | 正例 | 负例 |
|---|---|---|
| 第 14.3 节图验证器 | 完整示例及 3 个文档图片段通过 | 错 parent、非法 tuple、环均拒绝 |
| 第 15 节 SB3 builder | 生成可回读 ZIP | 缺资源、多余资源均拒绝 |
| 第 16.4 节事务加载器 | 完整项目加载成功 | 损坏输入后恢复原 2 个 target |
| 第 16.5 节增量适配器 | 普通 block 从 9 增至 11 | 不存在的 target 被拒绝 |
| 第 9 节附着注释 | canonical ID 连续两次往返保持 | 任意旧 comment ID 被识别为会改写的非生成格式 |

### 18.4 最低测试矩阵

| 类别 | 必测用例 |
|---|---|
| 基础脚本 | flag、顺序栈、repeat、forever、if、if-else、wait |
| 输入 | 数字、文本、颜色、Boolean、菜单 shadow、reporter 覆盖 shadow |
| 图 | next、parent、SUBSTACK、SUBSTACK2、无环、单一拥有者 |
| 数据 | 全局/局部变量和列表、同名拒绝、monitor |
| 广播 | 定义、发送、等待发送、接收 |
| procedure | %s、%b、warp、默认值、多参数、缺定义拒绝 |
| 多 target | 舞台、多个角色、唯一名称、层顺序、editingTarget |
| 资源 | SVG、PNG、WAV、MP3、缺文件、错 hash、坏 magic bytes |
| 扩展 | Pen/Music；每个启用设备或服务扩展至少一个 block |
| GUI | targetsUpdate、workspaceUpdate、monitor、扩展 toolbox |
| 安全 | ZIP bomb、路径穿越、深 JSON、过多 block、超长 mutation |
| 回滚 | parser、资源、扩展、GUI 深检任一失败后恢复原项目 |
| 往返 | load -> save -> 新 VM load -> 语义比较 |

## 19. AI 生成提示词模板

宿主应把用户需求、资源目录、允许扩展和限额作为独立的受信数据段拼接到系统提示词，不让用户文本覆盖规则。下面模板可直接作为系统约束主体：

~~~text
你是 Scratch 3 项目 JSON 生成器。目标运行时固定为 @scratch/scratch-vm@15.0.1、@scratch/scratch-blocks@2.1.19、scratch-parser@6.0.1。

输出协议：
1. 成功时只输出一个严格 JSON 对象，该对象就是完整 project.json。不要输出 Markdown、代码围栏、解释、注释或尾随逗号。
2. 无法满足请求时只输出错误 JSON；例如请求依赖禁用扩展时输出：{"error":{"code":"UNSUPPORTED_SCRATCH_REQUEST","message":"请求需要未启用的 speech2text 扩展"}}。
3. 不输出 SB3 Base64、Scratch Blocks XML、hydrated VM block 或 JavaScript。

项目根规则：
1. targets[0] 必须是唯一舞台，isStage=true 且 name="Stage"；后续 target 都是唯一命名的原始角色。
2. 根对象必须有 targets、monitors、extensions、meta；meta.semver="3.0.0"，meta.vm="15.0.1"。
3. 每个 target 必须显式包含 variables、lists、broadcasts、blocks、comments、currentCostume、costumes、sounds、volume、layerOrder 及其舞台/角色专有状态。
4. 每个 target 至少一个 costume，currentCostume 必须在范围内。
5. 只能逐字复用宿主资源目录中的 descriptor。禁止发明 assetId、md5ext、dataFormat、rate、sampleCount 或资源文件。

ID 与引用规则：
1. 新 ID 必须匹配 ^[A-Za-z][A-Za-z0-9_-]{7,63}$。
2. block ID 在全项目唯一；变量/列表/广播定义 ID 唯一；所有引用必须存在。
3. 舞台变量/列表是全局，角色变量/列表只属于该角色；广播只定义在舞台。
4. VARIABLE、LIST、BROADCAST_OPTION field 必须同时写 [name,id]，二者必须匹配定义。
5. 附着注释 ID 必须为 blockId + "_comment"；该 block ID 最长 56 个字符，block.comment 与 comments map key 必须使用同一个派生 ID。

block 规则：
1. 普通 block 必须显式写 opcode、next、parent、inputs、fields、shadow、topLevel；顶层非 shadow block 还必须写有限 x/y。
2. topLevel=true 当且仅当 parent=null。非顶层 block 必须有且只有一个结构拥有者，parent 必须与 next/input 反向关系一致。
3. 图必须无环，不能共享 reporter，不能跨 target 引用，不能有孤立 shadow 或悬挂引用。
4. 普通可执行脚本以合法 hat 开始。reporter、Boolean、shadow 和 terminal block 的 next 必须为 null。
5. 只使用宿主提供的 exact opcode/input/field 白名单，禁止仅凭前缀猜测 opcode。

input 与 field 规则：
1. input 只能是 [1,S]、[2,B]、[3,B,S]。
2. code 4-11 是 shadow primitive；code 12/13 是可出现在状态 2/3 active 位置的变量/列表 reporter。
3. 数字、文本、颜色、广播分别使用合法 code 4-10 和 11；Boolean 没有 literal primitive。
4. 动态菜单使用规范的 shadow block、field 名和菜单值；shadow=true、topLevel=false、next=null。
5. 普通 dropdown field 用 [value]；变量、列表、广播 field 用 [name,id]。

自定义积木规则：
1. procedures_definition.inputs.custom_block 必须为 [2,prototypeId]。
2. procedures_prototype 必须 shadow=false、inputs={}；prototype 与 call mutation 都必须写 tagName="mutation" 和 children=[]。
3. prototype mutation 的 argumentids、argumentnames、argumentdefaults 必须是合法 JSON 字符串且长度一致；call 只写 argumentids。
4. 只生成 %s 和 %b；call 的动态 input key 必须等于 argument ID；definition 所连接的 prototype 与所有 call 的 proccode 和 warp 必须一致。
5. 禁止生成旧式 [1,prototypeId]、shadow prototype 或 prototype 下的 argument reporter shadow；这些只属于兼容导入。

扩展规则：
1. 只能使用 allowedExtensions 数据段中的 ID；没有明确允许时 extensions=[] 且不生成任何扩展 opcode。
2. 禁止第三方 URL、speech2text、hidden legacy opcode 和未知扩展。
3. 扩展 opcode 大小写、argument 名、menu 名和值必须逐字匹配白名单。

输出前在内部逐项自检，但不要输出自检过程：
- 唯一舞台、target 名、ID 和作用域；
- 每个 next/parent/input/shadow 引用闭合；
- 图无环且每个非顶层 block 只有一个拥有者；
- tuple 长度和 primitive code 正确；
- opcode 的 input/field key 与 target 限制正确；
- procedure 数组和参数占位符对齐；
- 每个资源来自资源目录且每个 target 至少一个造型；
- extensions 与 blocks/允许的 monitors 中实际扩展 opcode 集合相同；
- 输出可由 JSON.parse 直接解析。
~~~

建议宿主追加四个不可混淆的数据段：

~~~text
[USER_REQUIREMENT]
{{USER_REQUIREMENT}}
[/USER_REQUIREMENT]

[ASSET_CATALOG_JSON]
{{ASSET_CATALOG_JSON}}
[/ASSET_CATALOG_JSON]

[ALLOWED_EXTENSIONS_JSON]
{{ALLOWED_EXTENSIONS_JSON}}
[/ALLOWED_EXTENSIONS_JSON]

[GENERATION_LIMITS_JSON]
{{GENERATION_LIMITS_JSON}}
[/GENERATION_LIMITS_JSON]
~~~

这四个双花括号项是宿主运行时模板变量，不是让模型自行补造的数据。资源目录为空时，宿主应先注入可信默认造型，而不是允许 AI 编造哈希。

## 20. 来源与升级策略

### 20.1 主要源码

- [scratch-editor 固定分析 commit](https://github.com/scratchfoundation/scratch-editor/tree/b027676cbf5510edbd386081082c107efa75e4d9/packages/scratch-vm)
- [SB3 serialization/deserialization](https://github.com/scratchfoundation/scratch-editor/blob/b027676cbf5510edbd386081082c107efa75e4d9/packages/scratch-vm/src/serialization/sb3.js)
- [VirtualMachine 顶层 API](https://github.com/scratchfoundation/scratch-editor/blob/b027676cbf5510edbd386081082c107efa75e4d9/packages/scratch-vm/src/virtual-machine.js)
- [Runtime opcode 和扩展转换](https://github.com/scratchfoundation/scratch-editor/blob/b027676cbf5510edbd386081082c107efa75e4d9/packages/scratch-vm/src/engine/runtime.js)
- [Block 容器与 XML 输出](https://github.com/scratchfoundation/scratch-editor/blob/b027676cbf5510edbd386081082c107efa75e4d9/packages/scratch-vm/src/engine/blocks.js)
- [执行时 field/input 取值](https://github.com/scratchfoundation/scratch-editor/blob/b027676cbf5510edbd386081082c107efa75e4d9/packages/scratch-vm/src/engine/execute.js)
- [内建扩展映射](https://github.com/scratchfoundation/scratch-editor/blob/b027676cbf5510edbd386081082c107efa75e4d9/packages/scratch-vm/src/extension-support/extension-manager.js)
- [scratch-vm npm 15.0.1](https://www.npmjs.com/package/@scratch/scratch-vm/v/15.0.1)
- [scratch-blocks npm 2.1.19](https://www.npmjs.com/package/@scratch/scratch-blocks/v/2.1.19)
- [scratch-parser npm 6.0.1](https://www.npmjs.com/package/scratch-parser/v/6.0.1)

### 20.2 权威优先级

遇到冲突时按以下顺序判断：

1. 固定版本的可执行 VM/GUI 往返测试。
2. 固定版本 <code>scratch-vm</code> serializer、runtime 和 block 实现。
3. 固定版本 <code>scratch-blocks</code> block shape 与 GUI toolbox。
4. <code>scratch-parser</code> schema。
5. 非固定版本网页、社区示例和旧 SB3 文件。

旧项目能被兼容 loader 修复，不代表该格式可以用于新生成。

### 20.3 升级检查清单

升级任一 Scratch 包时必须：

1. 固定新的 commit、npm 版本和 gitHead。
2. 比较 <code>sb3.js</code> 的 tuple、primitive、procedure、asset、monitor 和 extension 逻辑。
3. 重新枚举 <code>runtime._primitives</code>、<code>runtime._hats</code> 和全部内建扩展 metadata。
4. 重新提取 GUI toolbox 的默认 shadow、field 和菜单值。
5. 重新检查 <code>builtinExtensions</code> 与可选依赖。
6. 用完整 renderer、storage、audio、bitmap adapter 环境运行第 18.4 节矩阵。
7. 更新机器可读 opcode contract、AI 提示词和本文。
8. 旧版本项目与新版本项目分别跑 load/save/reload，确认迁移不是静默修复。

只有所有契约测试通过，才能把新版本设为生成目标。
