# AI Scratch Web

AI Scratch Web 是一个本地优先的 Scratch 3 创作工作台。浏览器运行仓库内打包的 Scratch 编辑器，Python/FastAPI 后端提供 AI 生图、文本扩写和 AI 写积木服务。生成的图片可以进入 Scratch 项目、无边画布或本地素材库，项目可以导出为标准 SB3 文件。

当前版本：0.1.0。当前文档以仓库中的实际实现为准。项目不依赖账号系统、云端协作或远程项目存储。

## 目录

- [功能概览](#功能概览)
- [系统架构](#系统架构)
- [快速开始](#快速开始)
- [配置](#配置)
- [AI 写积木](#ai-写积木)
- [AI 生图与素材库](#ai-生图与素材库)
- [无边画布](#无边画布)
- [HTTP API](#http-api)
- [数据与安全](#数据与安全)
- [开发与检查](#开发与检查)
- [人工验收清单](#人工验收清单)
- [相关文档](#相关文档)

## 功能概览

### Scratch 编辑器

- Vue 3 和 Vite 提供应用外壳，Scratch GUI、VM、Blocks 和 Renderer 提供标准积木编辑体验。
- Scratch 运行时、默认素材、字体、积木媒体和中文界面资源从前端静态资源加载。
- 支持新建、打开、编辑、绿旗运行、停止、全屏以及标准 SB3 导入和导出。
- AI 生成结果最终通过 Scratch VM 的生命周期 API 写入；应用不拼接或直接加载 project.json。

### AI 写积木

- 支持 modify、append、rewrite 三种编辑模式。
- 支持单目标和整个项目两种生成范围。
- 项目模式先生成小型 ProjectManifest，再为每个 program target 单独生成一个 ProgramIR。
- 后端对格式、结构、语义和 Scratch VM 兼容性进行校验；失败时按受控轮次要求 AI 修复。
- 前端完成项目资源自动匹配或用户确认后，按清单顺序请求、编译并注入每个目标；事务失败时恢复生成前的项目备份。
- 模型档位固定为三档：普通、高级、超级。思考强度固定为低、中、高、超高、最大五档。

### AI 生图

- 提示词、尺寸、风格和导入目标由前端面板控制。
- 后端只接受 1x1 图片布局，校验供应商返回的图片后统一转换为 PNG Base64。
- 图片可以导入为舞台背景或新角色，也可以先进入无边画布继续处理。

### 本地素材库

- AI 生成、Scratch 素材库、画布派生和用户导入的图片可以保存到浏览器本地素材库。
- 支持素材预览、删除和再次使用；素材不会自动上传到项目之外的服务。

### 无边画布

- 支持从 AI 结果、文件、拖放、剪贴板或 Scratch 导入图片。
- 支持移动、缩放、适应视图、原始尺寸、变换、矩形裁剪、画笔、铅笔、橡皮擦、油漆桶和吸管。
- 支持色度抠图、容差、柔化、相邻或全局模式、去色边、图层排序、锁定、显隐、透明度和选区复制。
- 支持合成 PNG、造型 ZIP，以及导入为舞台背景或多造型角色。

## 系统架构

~~~text
浏览器
  Vue 3 应用外壳
    ├─ Scratch GUI / VM / Blocks / Renderer
    ├─ AI 写积木状态机与 SSE 客户端
    ├─ 项目资源准备、审核和补偿
    ├─ ProgramIR 编译器与 VM 写入器
    ├─ 无边画布
    └─ IndexedDB 本地素材库
          │ /api 代理
          ▼
Python 3.11+ FastAPI
  ├─ /api/image/generate
  ├─ /api/text/expand
  ├─ /api/ai-blocks/chat
  ├─ /api/ai-blocks/generate
  ├─ /api/ai-blocks/generate-unified
  └─ /api/ai-blocks/validate
          │ 服务端保存 API Key
          ▼
Sui Xiang 图片服务 / Agnes / DeepSeek / GPT
~~~

Vite 开发服务器默认监听 5173，并把 /api 转发到 127.0.0.1:8000。前端代码使用相对 API 路径，因此生产或局域网部署只需要调整代理来源，不需要把密钥放进浏览器构建产物。

### AI 写积木数据流

单目标模式保持一条生成流：

~~~text
需求 -> requirements -> plan -> 一个 ProgramIR -> 服务端校验 -> 前端编译 -> 逐块注入 -> 失败回滚
~~~

项目模式拆成两个接口契约：

~~~text
需求
  -> project_manifest 流：requirements + plan + ProjectManifest
  -> 前端校验清单
  -> 资源审核、素材准备、资源事务提交
  -> project_program 流：每个目标一次，严格按 program_targets 顺序
  -> 单个 ProgramIR 校验、编译、逐块注入
  -> 所有目标完成，提交最终项目
~~~

项目模式不会让模型一次输出所有角色的积木正文，也不会由前端把多个程序拼成 project.json。每个目标请求都携带最新的项目上下文、清单、需求和计划。客户端在发起请求前会再次检查 project revision 和每个 target revision；服务端则校验 target_index、program_target 与 manifest.program_targets[index] 的一致性，但不替代客户端的 revision 快照检查。资源和程序写入共享同一份可恢复事务；任意目标生成或 VM 注入失败，应用会恢复生成前备份并清理本轮产生的本地素材。

## 快速开始

### 环境要求

- Windows 10/11 或等效的 Node/Python 开发环境。
- Python 3.11 或更高版本。
- Node.js 与 npm，建议使用当前 LTS 版本。
- 可用的 AI 服务 API Key。AI 写积木的三个模型分别需要 Agnes、DeepSeek 或 GPT 对应密钥；生图需要 Sui Xiang 密钥。

### 安装依赖

在仓库根目录执行：

~~~bat
python -m venv backend\\.venv
backend\\.venv\\Scripts\\python.exe -m pip install -e ".\\backend"
npm --prefix frontend install
if not exist backend\\.env copy backend\\.env.example backend\\.env
~~~

编辑 backend/.env，填入服务端密钥和需要覆盖的配置。backend/.env 由后端在 backend 工作目录中读取；不要把它复制到 frontend，也不要把密钥写入 Vite 环境变量或前端源代码。

### 一键启动

~~~bat
start-all.bat
~~~

脚本会检查 backend/.venv 和 frontend/node_modules，按需启动：

- 后端：http://127.0.0.1:8000
- 前端：http://127.0.0.1:5173

如果 8000 或 5173 已被占用，脚本会复用已有服务；Vite 的 strictPort 为 false，端口冲突时可能顺延到下一个端口。打开实际输出的前端地址即可。

只检查依赖而不启动服务：

~~~bat
start-all.bat --check
~~~

### 分别启动

后端命令要在 backend 目录执行：

~~~bat
cd backend
.venv\\Scripts\\python.exe -m uvicorn app.main:app --host 127.0.0.1 --port 8000 --log-level debug --access-log
~~~

另开终端启动前端：

~~~bat
cd frontend
npm run dev
~~~

如果后端不在默认地址，在启动前端前设置 Vite 代理来源：

~~~powershell
$env:AI_SCRATCH_API_ORIGIN = 'http://127.0.0.1:8000'
npm run dev
~~~

启动后可以访问 http://127.0.0.1:8000/health 查看服务状态。

## 配置

### API Key

| 环境变量 | 用途 | 对应功能 |
| --- | --- | --- |
| SUI_XIANG_API_KEY | Sui Xiang OpenAI 兼容生图服务 | AI 生图 |
| AGNES_API_KEY | Agnes 对话服务 | 普通模型、文本扩写 |
| DEEPSEEK_API_KEY | DeepSeek 对话服务 | 高级模型 |
| GPT_API_KEY | GPT Responses 兼容服务 | 超级模型 |

密钥只在 FastAPI 进程中使用。/health 只返回是否配置，不返回密钥内容。

### 固定模型档位

以下是 AI 写积木和 AI 对话使用的唯一模型档位。请求中的 model_tier 只能是 normal、advanced 或 super。

| 档位 | 前端名称 | Provider | 模型 | 必需密钥 |
| --- | --- | --- | --- | --- |
| normal | 普通 | Agnes | agnes-2.5-flash | AGNES_API_KEY |
| advanced | 高级 | DeepSeek | deepseek-v4-flash | DEEPSEEK_API_KEY |
| super | 超级 | GPT | gpt-5.6-sol | GPT_API_KEY |

图片服务使用固定的 Sui Xiang 模型 gpt-image-2，不受 model_tier 选择影响。文本扩写固定使用 Agnes。

### 思考强度

请求中的 thinking.strength 只能是 low、medium、high、xhigh 或 max，默认值为 medium。关闭 thinking.enabled 后，服务不发送思考参数，但界面仍保留已选强度。

| 强度 | Agnes budget_tokens | DeepSeek reasoning_effort | GPT reasoning effort |
| --- | ---: | --- | --- |
| low | 1024 | low | low |
| medium | 2048 | medium | medium |
| high | 4096 | high | high |
| xhigh | 8192 | high | xhigh |
| max | 16384 | high | max |

DeepSeek 当前只接受 low、medium、high，因此 xhigh 和 max 在服务端映射为 high；这不会改变前端和公共 API 的五档契约。服务端不再读取独立的 GPT 思考强度覆盖配置，请求选择始终以 thinking.strength 为准。

### 运行参数

配置模板为 backend/.env.example。未设置时使用下表默认值：

| 环境变量 | 默认值 | 说明 |
| --- | ---: | --- |
| IMAGE_REQUEST_TIMEOUT_SECONDS | 90 | 生图供应商请求超时，范围 5-180 秒 |
| IMAGE_MAX_DOWNLOAD_BYTES | 12582912 | 供应商图片下载上限 |
| IMAGE_MAX_REQUEST_BYTES | 16384 | 生图请求体上限 |
| IMAGE_MAX_CONCURRENCY | 2 | 同时进行的生图请求数 |
| AI_BLOCKS_MAX_REQUEST_BYTES | 262144 | AI 写积木请求体上限 |
| AI_BLOCKS_MAX_CONCURRENCY | 2 | 同时运行的 AI 写积木请求数 |
| AI_BLOCKS_PROVIDER_TIMEOUT_SECONDS | 480 | 单次 AI 写积木供应商超时，范围 10-720 秒 |
| AI_BLOCKS_MAX_REPAIR_ROUNDS | 2 | 每个生成段的最大修复轮次，范围 0-3 |

配置使用 pydantic-settings 校验。未知环境变量不会变成新的模型档位或思考档位。

## AI 写积木

### 面板流程

1. 选择当前目标、生成范围、编辑模式、素材策略、模型档位和思考强度。
2. 输入自然语言需求。发送前，前端把当前目标、脚本、变量、列表、广播、造型、声音和项目目标快照发送到后端。
3. 服务端先生成需求整理和计划，随后生成受约束的结构化输出。
4. 单目标结果直接进入 ProgramIR 校验。项目结果先检查 Scratch 内置素材匹配：匹配成功的资源自动使用，不要求用户确认；明确的生图资源或未匹配资源才进入资源提示。
5. 资源提示完成后，应用按 manifest.resource_ops 顺序准备和提交资源，再按 manifest.program_targets 顺序逐个请求 ProgramIR。
6. 所有目标写入后，面板显示结果并保留撤销入口。用户在生成后继续编辑项目时，撤销会拒绝执行，避免覆盖新修改。

### 编辑模式

- append：在当前上下文中新增程序。
- modify：只允许当前目标，按 script id 修改可编辑脚本。
- rewrite：只允许当前目标，重写可编辑脚本；受保护脚本不会被删除。

项目清单中的 stage、existing_sprite 和 created_sprite 目标只能使用 append。只有 current_target 可以使用 modify 或 rewrite。

### 项目素材策略

- builtin_first：按清单的 search_terms 自动匹配 Scratch 内置素材；匹配成功直接使用，找不到时弹出生成替代素材的确认提示。
- existing_only：只能使用已有的 Scratch 素材，不允许生图；找不到匹配项时需要手动选择其他素材。
- allow_generation：沿用自动匹配规则；匹配失败时允许用户确认后调用生图接口。

匹配成功的 Scratch 素材不会弹出确认。明确的 image_generation 资源和没有匹配项的资源才需要用户确认；在确认完成前不会调用生图接口。用户也可以在提示中打开素材选择器手动更换，取消不会修改 Scratch 项目。

### 输出合同

- ProjectManifest 的 schema_version 固定为 2，只包含 summary、resource_ops、program_targets 和 shared_state 等清单字段，不包含 blocks、scripts 或完整程序。
- ProgramIR 的 schema_version 固定为 1。每个脚本必须有唯一 id、purpose、位置和 blocks；程序通过服务端校验后才交给前端编译器。
- 只允许 Scratch 原生 opcode。模型不能写入 VM ID、JavaScript、URL、base64、任意外部资源内容或未经合同声明的字段。
- 项目目标的 shared_state 必须重复清单中的全局变量、列表和广播；目标私有变量和列表应声明为 local，且不得与 manifest 全局名称冲突。sprite 目标省略 scope 时按 local 处理，stage 目标始终按 global 处理。

### 限制

| 限制 | 值 |
| --- | ---: |
| 单个 ProgramIR 积木数 | 120 |
| 最大嵌套深度 | 12 |
| 单个 ProgramIR 脚本数 | 12 |
| 普通字符串长度 | 2000 |
| 程序摘要长度 | 1000 |
| 项目资源操作数 | 24 |
| 项目程序目标数 | 12 |

达到限制时，服务端返回结构化错误。修复轮次只会重新请求当前失败的生成段，不会让未经校验的输出进入 VM。

## AI 生图与素材库

### 生图选项

AI 生图接口当前固定为 1x1 布局，允许以下尺寸：

- 480 x 360
- 640 x 360
- 360 x 360
- 360 x 480

前端提供以下风格：明亮卡通、Scratch 风格、纸艺剪影、像素游戏、柔和水彩和自定义。目标可以是舞台背景或角色。自定义风格必须至少包含两个字符。

后端会检查请求尺寸、提示词长度、响应 MIME、图片内容、图片大小和图片解码尺寸，并将供应商返回的图片转换为 PNG。项目清单中的 image_generation 资源也必须使用上面四组尺寸，避免资源准备阶段才出现尺寸 422。

### 本地素材库

素材库使用 IndexedDB：

- 数据库名：ai-scratch-assets
- 对象仓库：generatedAssets
- 数据库版本：2
- 记录包含来源、标签、分组、派生关系、提示词、图片参数和 PNG 结果。

IndexedDB 仅在当前浏览器配置文件中保存。清理浏览器站点数据会删除本地素材库，但不会自动修改已经导出的 SB3 文件。

## 无边画布

### 输入与输出

- 输入格式：PNG、JPEG、WebP。
- 不接受 GIF 和 SVG 作为画布导入文件。
- 图片最大单边 8192 像素，总像素不超过 3200 万。
- 可以导出合成 PNG 或造型 ZIP；导入 Scratch 前可以指定为舞台背景或角色造型。

### 工具与快捷键

| 操作 | 快捷键 |
| --- | --- |
| 移动 | V |
| 变换 | T |
| 选择 | M |
| 裁剪 | C |
| 色度抠图 | K |
| 画笔 | B |
| 铅笔 | N |
| 橡皮擦 | E |
| 吸管 | I |
| 手型 | H |
| 撤销 | Ctrl/Cmd+Z |
| 重做 | Ctrl/Cmd+Shift+Z 或 Ctrl/Cmd+Y |
| 适应视图 | Ctrl/Cmd+0 |
| 原始尺寸 | Ctrl/Cmd+1 |
| 选区复制 | Alt+J |
| 选区剪切为新图层 | Alt+Shift+J |
| 裁剪确认 | 裁剪工具下按 Enter |
| 退出当前选择或画布 | Esc |
| 按住空格临时平移 | Space |

输入框正在编辑时，画布快捷键不会拦截文本输入。

## HTTP API

所有接口都在本机 FastAPI 服务上。除 /health 外，前端通过 Vite /api 代理访问。FastAPI 也会在服务运行时提供 /docs OpenAPI 页面。

### 统一错误格式

HTTP 错误通常返回：

~~~json
{
  "detail": {
    "code": "AI_PROVIDER_NOT_CONFIGURED",
    "message": "AI 服务暂未配置"
  }
}
~~~

常见 HTTP code：

- 413 REQUEST_TOO_LARGE：请求体超过对应接口上限。
- 422 INVALID_REQUEST 或 Pydantic 校验错误：字段缺失、类型不符或超出合同。
- 503 AI_PROVIDER_NOT_CONFIGURED：选择的模型密钥未配置。
- 502 PROVIDER_ERROR 或 TIMEOUT：上游服务失败或超时。

### GET /health

返回服务和各模型密钥是否配置，不返回密钥：

~~~json
{
  "status": "ok",
  "provider_configured": true,
  "agnes_configured": true,
  "deepseek_configured": false,
  "gpt_configured": true
}
~~~

provider_configured 指 Sui Xiang 生图服务；agnes_configured、deepseek_configured 和 gpt_configured 分别对应三个 AI 写积木模型。

### POST /api/image/generate

请求字段：

~~~json
{
  "prompt": "明亮的 Scratch 森林背景",
  "style": "scratch",
  "width": 480,
  "height": 360,
  "target": "backdrop",
  "sheet_layout": "1x1"
}
~~~

style 可以是 cartoon、scratch、paper-cut、pixel、watercolor 或 custom。style 为 custom 时必须提供 custom_style。成功响应包含 request_id、image_base64、PNG MIME、实际图片尺寸、请求画布尺寸和 1x1 布局。

### POST /api/text/expand

请求字段为 kind 和 text：

~~~json
{
  "kind": "scene",
  "text": "一个角色在森林里发现宝箱"
}
~~~

响应为 SSE，事件为 content_delta、message_complete 或 message_error。kind 只能是 scene 或 style，text 最大 800 字符。

### POST /api/ai-blocks/chat

用于面板中的普通 AI 对话，不产生 ProgramIR。请求包含 model_tier、session_id、request_id、messages、target 和 thinking。

SSE 事件：

- assistant_delta：AI 可见回答增量，data.text 为文本。
- reasoning_delta：思考增量，data.text 为文本；关闭思考时不会发送。
- message_complete：对话完成。
- message_error：对话失败。

### POST /api/ai-blocks/generate

这是项目渐进生成的推荐接口，output_contract 是判别字段。

project_manifest 请求在统一请求字段上增加：

~~~json
{
  "output_contract": "project_manifest",
  "scope": "project",
  "asset_policy": "builtin_first",
  "project_context": {
    "project_id": "scratch-project",
    "version": 10,
    "targets": [{
      "target_id": "target-1",
      "revision": 10,
      "name": "角色1",
      "is_stage": false,
      "has_scripts": false,
      "mode": "append",
      "costumes": [],
      "sounds": [],
      "sprite_names": ["角色1"],
      "current_program": ""
    }],
    "shared_state": {}
  }
}
~~~

实际请求仍必须包含有效的 session_id、request_id、messages、target 和 thinking。该阶段只返回 requirements、plan 和 ProjectManifest；清单位于 plan 阶段的 data.manifest 中。

project_program 请求增加以下字段。下面只展示字段形状，requirements、plan、manifest 和 program_target 必须替换为同一份已校验清单中的完整对象，不能直接发送空对象：

~~~json
{
  "output_contract": "project_program",
  "scope": "project",
  "target_index": 0,
  "requirements": {},
  "plan": {},
  "manifest": {},
  "program_target": {}
}
~~~

requirements、plan、manifest 和 program_target 必须来自同一份已校验清单，target_index 必须与 manifest.program_targets 的位置一致。target 是当前实际目标的最新快照。成功完成事件的 data.program 是已经通过后端校验的完整 ProgramIR。

### POST /api/ai-blocks/generate-unified

统一兼容入口。当前前端用它处理单目标 tagged stream；项目渐进流程使用 /generate。该接口返回统一 SSE 流，旧调用者仍可使用 project scope 的完整分段模式：它会在一个请求内完成清单和全部目标程序输出，调用方不能把它当作新接口的逐目标提交事务；新代码不应把完整项目程序依赖在一次响应中。

### 统一生成 SSE 格式

每条 SSE 消息使用以下外层结构：

~~~text
event: phase_complete
data: {"request_id":"...","phase":"plan","data":{...}}
~~~

phase 取 requirements、plan 或 program。事件包括：

- cache_hit：提示缓存键和命中率。
- phase_start、phase_complete：阶段开始和完成。
- content_delta、reasoning_delta：上游内容或思考增量。
- metadata、shared_state、script_ready：ProgramIR 的渐进帧。
- repair：当前阶段正在按错误提示自动修复。
- validation_error：结构化校验问题，包含 path、operation、expected 和 repair_scope。
- complete：生成完成；project_program 完成事件额外包含 data.program。
- error：生成终止，data 中包含 code 和 message。

常见流错误码包括 AI_PROVIDER_TIMEOUT、AI_PROVIDER_ERROR、AI_GENERATION_ERROR、AI_CHAT_ERROR、OUTPUT_VALIDATION_ERROR、OUTPUT_INCOMPLETE、REQUIREMENTS_INCOMPLETE、MANIFEST_MISSING、PROGRAM_MISSING、PROGRAMS_INCOMPLETE、INVALID_PROJECT_PROGRAM_REQUEST、PROGRAM_CONTRACT_INVALID、SCHEMA_VALIDATION_ERROR、STREAM_PROTOCOL_ERROR 和 UNSUPPORTED_SCHEMA_VERSION。

### POST /api/ai-blocks/validate

用于独立检查 ProgramIR 或 ProjectManifest，不写入 VM。请求：

~~~json
{
  "kind": "program",
  "payload": {},
  "target_kind": "sprite",
  "mode": "append",
  "current_program": "{}"
}
~~~

kind 为 manifest 时只需要 payload，并且不应传 program-only 字段。成功返回 valid、issues 和可能的 normalizations；失败返回结构化 issues，包含 JSON Pointer path 和修复范围。

## 数据与安全

- Scratch 项目状态由浏览器中的 Scratch VM 管理，SB3 导入导出由 Scratch 序列化流程完成。
- AI 生成素材保存于 IndexedDB ai-scratch-assets/generatedAssets；没有账号级云同步。
- 后端默认只监听 127.0.0.1:8000。局域网访问需要明确调整监听地址、Vite host、代理和 CORS，不能只暴露前端端口。
- FastAPI CORS 默认允许 http://127.0.0.1:5173 和 http://localhost:5173。
- 生产部署应使用 HTTPS、限制反向代理来源、限制后端端口暴露，并通过日志脱敏检查确认没有打印 API Key。
- 本仓库面向私有部署时可以保留服务端环境文件；如果复制到公开仓库或公开日志，必须删除并轮换所有密钥。

## 开发与检查

### 常用命令

前端类型检查和生产构建：

~~~bat
npm --prefix frontend run build
~~~

后端字节码和语法检查：

~~~bat
python -m compileall -q backend\\app
~~~

资源操作合同同步检查：

~~~bat
python scripts\\generate-resource-contract.py --check
~~~

前端构建包含 vue-tsc -b 和 Vite build。资源合同由 docs、后端模型和 frontend/src/ai-blocks/generated/resource-contract.ts 共同维护，修改源合同后必须执行生成脚本检查。

本次重构不新增或修改测试，也不要求在完成前自动运行测试；人工验收项见下一节。

## 人工验收清单

以下内容需要用户在本机配置真实服务后亲自验证。建议每一项都观察浏览器界面、Network SSE、Scratch 舞台和导出文件，而不只看接口返回。

1. 启动与健康检查：执行 start-all.bat，确认前端可打开；访问 /health，确认四个配置状态符合 backend/.env。
2. 生图正常路径：分别使用四组尺寸、普通风格和自定义风格生成；确认返回 PNG、预览尺寸正确，并能导入舞台背景和角色。
3. 生图异常路径：输入非法尺寸、缺少 custom_style、超长提示词和空提示词，确认请求被拒绝且页面能显示可理解的错误。
4. 文本扩写：分别验证 scene 和 style，确认 content_delta 连续到达、完成后文本完整，失败时不会一直停留在加载状态。
5. 模型档位：分别选择普通、高级、超级执行聊天和写积木，确认请求中的 model_tier 与后端实际 Provider 一致；未配置对应密钥时确认提示清晰。
6. 思考等级：在每个模型上分别验证低、中、高、超高、最大，并验证关闭思考；确认页面不会出现额外模型或额外等级，DeepSeek 的超高和最大仍能完成请求。
7. 单目标 append：对角色和舞台生成简单程序，确认需求、计划、ProgramIR 校验、编译、逐块注入、运行和撤销都正常。
8. 单目标 modify/rewrite：准备至少两个脚本，确认可编辑脚本能修改或重写，受保护脚本不会被误删；生成失败或取消后项目与变量保持原状。
9. 单目标流式状态：观察需求、计划、程序、修复和完成状态，确认 reasoning、脚本进度、错误和取消状态不会互相覆盖。
10. 项目无资源路径：准备舞台和至少两个角色，生成多个 program target；确认先拿到清单，再按 target_index 顺序发起多个 project_program 请求，并且每个目标完成后才进入下一个目标。
11. 项目资源处理：分别验证能自动使用匹配的 Scratch 角色/造型/背景、没有匹配时弹出生成确认、手动更换素材和明确生图资源；确认未同意生成前不调用生图，取消处理不修改项目，资源准备完成后才进入 VM。
12. 项目渐进提交：在两个目标中使用不同脚本，确认每个 ProgramIR 独立校验、编译并写入对应目标，前端显示目标名称和脚本进度，最终项目可以运行和导出 SB3。
13. 项目回滚：让后续目标请求失败、超时或返回非法程序，确认之前已经注入的目标、资源和本轮生成素材都被恢复，页面显示具体失败阶段。
14. 项目一致性：生成期间切换目标、修改积木、添加角色或导入项目，确认快照保护会拒绝继续提交，而不会覆盖用户修改。
15. 资源和画布：验证 PNG/JPEG/WebP 导入、拖放、剪贴板、色度抠图、裁剪、图层操作、撤销重做、PNG 和造型 ZIP 导出，再导入 Scratch 检查旋转中心和造型顺序。
16. 本地持久化：刷新浏览器后确认对话记录和素材库符合预期；清理站点数据后确认本地素材被清除，已导出的 SB3 不受影响。
17. 响应式和快捷键：在桌面和较窄窗口验证面板、资源审核、画布属性栏不重叠；验证画布快捷键在输入框编辑时不会误触发。

## 相关文档

审计、设计和实施文档包含方案演进、已修复问题和历史验收记录；阅读当前接口时以上文档说明和当前代码优先。

- [AI 写积木架构重构设计](docs/实施文档/cc-AI写积木架构重构设计方案.md)
- [AI 写积木大 JSON 拆分与渐进提交方案](docs/实施文档/AI写积木大JSON拆分与渐进提交实施方案.md)
- [AI 写积木资源功能实施文档](docs/实施文档/AI写积木扩展角色与舞台资源功能实施文档.md)
- [AI 写积木生成逻辑审计报告](docs/实施文档/AI写积木生成逻辑深度审计报告.md)
- [ProjectManifest 生成提示词](docs/scratch项目清单生成提示词.md)
- [ProgramIR 生成提示词](docs/scratch代码生成提示词.md)
- [Scratch 项目计划生成提示词](docs/scratch项目计划生成提示词.md)
- [Scratch 语法规范](docs/scratch语法规范.md)
- [Scratch 代码规范](docs/scratch代码规范.md)
- [无边画布功能设计](docs/开发文档/无边画布功能文档设计.md)
- [开发计划](docs/开发文档/计划大纲.md)
- [Scratch VM 研究报告](docs/scratch-vm/01-scratch-vm研究报告.md)
- [Scratch VM 说明](docs/scratch-vm/README.md)
