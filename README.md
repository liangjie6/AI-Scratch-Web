# AI Scratch Web

一个本地优先的 Scratch 3 创作工作台。项目在浏览器中运行本地打包的 Scratch 编辑器，并通过 Python/FastAPI 后端提供 AI 生图、文本扩写和 AI 写积木能力。生成图片最终写入 Scratch 项目资源，可导出并重新加载标准 .sb3 文件。

> 当前版本：0.1.0。项目状态：可运行的本地开发版本。

## 目录

- [功能概览](#功能概览)
- [系统架构](#系统架构)
- [快速开始](#快速开始)
- [配置](#配置)
- [功能使用](#功能使用)
- [HTTP API](#http-api)
- [项目结构](#项目结构)
- [开发与测试](#开发与测试)
- [安全与限制](#安全与限制)

## 功能概览

### Scratch 本地编辑器

- Vue 3/Vite 负责应用外壳，Scratch GUI、Scratch VM、Scratch Blocks 和 Renderer 提供标准积木编程体验。
- 默认 Scratch 素材、字体、积木媒体、中文 locale 和 UI 素材从仓库静态目录加载。
- 支持新建、打开项目、积木编辑、绿旗运行、停止、全屏以及标准 SB3 导入导出。
- 项目数据和生成资源由 Scratch VM 管理，生成图片会作为 backdrop 或 sprite costume 写入项目。
- Vite 将前端 /api 请求代理到本机 FastAPI 服务。

### AI 生成素材

“生成素材”面板支持提示词、目标尺寸、风格和导入目标选择。当前尺寸为 480x360、640x360、360x360、360x480；风格为明亮卡通、Scratch 风格、纸艺剪影、像素游戏、柔和水彩和自定义；目标为舞台背景或新角色。

当前实际 API 契约是 1x1 图片布局，不是规划稿中的 2x2 候选雪碧图。后端会下载或解码供应商图片，校验后归一化为 PNG，并以 Base64 返回浏览器。直接导入时，图片通过 Scratch 资源适配器进入当前项目。

### AI 写积木

AI 积木面板提供对话、需求整理和程序生成。程序生成先规划步骤，再返回受校验的 ProgramIR，最后由前端编译为原生 Scratch 积木。支持 modify、append、rewrite 三种模式，普通/高级模型档位和低/中/高思考强度。后端只允许 Scratch 原生 opcode，并限制积木数量、嵌套深度、脚本数量和字段长度。

### 无边画布

无边画布用于在图片进入 Scratch 前进行准备：

- 从 AI 结果、文件、拖放或剪贴板载入图片
- 平移、缩放、适应视图、变换和矩形裁剪
- 画笔、铅笔、橡皮擦、油漆桶、吸管
- 色度抠图、容差、柔化、相邻/全局模式、去色边
- 图层新建、复制、删除、重命名、排序、显隐、锁定、透明度
- 选区复制/剪切为新图层，显式加入造型队列
- 造型顺序、名称、画框和旋转中心调整
- 合成 PNG、造型 ZIP，以及导入为背景或多造型角色
- 撤销、重做和浏览器本地自动恢复

### 本地 AI 素材库

生成结果保存到浏览器 IndexedDB。素材库支持查看、在画布中编辑、直接导入、删除和清空。它不是服务端图片托管，清理浏览器站点数据会删除素材库内容。

## 系统架构

```
Vue 3 + Vite
  |
  +-- ScratchHost.vue
  |     +-- Scratch GUI / React
  |     +-- Scratch VM / Blocks / Renderer
  |     +-- Local Storage adapter
  |     +-- SB3 import/export
  |
  +-- AI panels and canvas workspace
        +-- /api/image/generate  ---> Sui Xiang gpt-image-2
        +-- /api/text/*           ---> Agnes streaming text service
        +-- /api/ai-blocks/*      ---> Agnes or DeepSeek text service
```

核心职责边界：

- ScratchGuiBridge 负责 Vue 与 Scratch GUI/VM 的生命周期桥接。
- frontend/src/scratch/asset-adapter.ts 是生成图片进入 Scratch 的统一入口。
- ImageGenerationService 负责生图参数、超时、供应商响应、图片校验和 PNG 归一化。
- AgnesTextProvider 负责文本流、SSE 解析、普通/高级模型切换和思考参数。
- ProgramIR 是 AI 与 Scratch 编译器之间的受约束中间格式，不直接执行模型原文。
- 浏览器只请求本地 /api，不接触供应商 URL 或 API Key。

## 运行要求

- Node.js 18+，建议使用当前 LTS
- npm 9+
- Python 3.11+
- Chromium、Chrome 或 Edge 等现代浏览器
- 使用 AI 功能时，需要能访问对应外部模型服务

Scratch 编辑、项目运行、SB3 导入导出和画布本地处理不要求 Scratch 官方账号或 Scratch CDN。

## 快速开始

### Windows 一键启动

创建 Python 虚拟环境并安装依赖：

```
python -m venv backend\.venv
backend\.venv\Scripts\python.exe -m pip install -e ".\backend[test]"
npm --prefix frontend install
Copy-Item backend\.env.example backend\.env
```

编辑 backend/.env 填入需要的密钥，然后双击 start-all.bat，或执行：

```
.\start-all.bat
```

默认地址：

- 前端：http://127.0.0.1:5173/
- 健康检查：http://127.0.0.1:8000/health
- FastAPI 文档：http://127.0.0.1:8000/docs

start-all.bat --check 只检查后端虚拟环境和前端依赖。启动器会复用已占用的 8000/5173 端口，并自动打开浏览器。

### 手动启动

后端：

```
cd backend
.\.venv\Scripts\python.exe -m uvicorn app.main:app --host 127.0.0.1 --port 8000 --reload
```

前端：

```
cd frontend
npm install
npm run dev
```

前端默认监听 0.0.0.0:5173，并将 /api 代理到 http://127.0.0.1:8000。后端地址不同时，启动前端前设置：

```
$env:AI_SCRATCH_API_ORIGIN = "http://127.0.0.1:9000"
npm run dev
```

macOS/Linux 可执行：

```
python3 -m venv backend/.venv
backend/.venv/bin/python -m pip install -e 'backend[test]'
npm --prefix frontend install
cp backend/.env.example backend/.env
backend/.venv/bin/python -m uvicorn app.main:app --host 127.0.0.1 --port 8000 --reload
# 另一个终端：npm --prefix frontend run dev
```

## 配置

后端从 backend/.env 读取配置，也支持进程环境变量。完整模板见 [backend/.env.example](backend/.env.example)。

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| SUI_XIANG_API_KEY | 无 | AI 图片生成 |
| AGNES_API_KEY | 无 | 文本扩写、普通 AI 积木 |
| DEEPSEEK_API_KEY | 无 | 高级 AI 积木 |
| IMAGE_REQUEST_TIMEOUT_SECONDS | 90 | 生图请求超时，范围 5-180 |
| IMAGE_MAX_DOWNLOAD_BYTES | 12582912 | 供应商图片下载上限 |
| IMAGE_MAX_REQUEST_BYTES | 16384 | 图片接口请求体上限 |
| IMAGE_MAX_CONCURRENCY | 2 | 图片并发数 |
| AI_BLOCKS_MAX_REQUEST_BYTES | 262144 | AI 积木请求体上限 |
| AI_BLOCKS_MAX_CONCURRENCY | 2 | AI 积木并发数 |
| AI_BLOCKS_PROVIDER_TIMEOUT_SECONDS | 180 | AI 积木供应商超时 |

固定配置位于 backend/app/config.py：图片使用 https://sui-xiang.com/v1 的 gpt-image-2；普通文本使用 Agnes 的 agnes-2.5-flash；高级文本使用 DeepSeek 的 deepseek-v4-flash。

不要把密钥写入 Vue、Vite 前端变量、浏览器存储、构建产物或 SB3 文件。

## 功能使用

### 生成并导入图片

1. 打开“生成素材”，填写场景或角色描述。
2. 选择尺寸、风格和目标类型，点击生成。
3. 直接导入会将图片写入当前 Scratch 项目；也可以打开“无边画布”继续裁剪、抠图或拆分。
4. 在画布中导出 PNG/造型 ZIP，或点击“导入 Scratch”。
5. 使用 Scratch 保存功能下载 SB3，再重新加载验证图片资源仍在项目内。

角色目标会提供纯色背景/取色提示，便于色度抠图；AI 不保证生成结果天然透明，导入前应检查边缘和画框。

### 使用 AI 写积木

1. 在 Scratch 中选中舞台或角色。
2. 打开“AI 写积木”，确认目标和 modify、append、rewrite 模式。
3. 输入自然语言需求；信息不足时先执行需求整理。
4. 等待规划、生成、校正等流式阶段完成。
5. 检查程序后再应用到当前目标，并在舞台中运行验证。

### 无边画布快捷键

- Ctrl/Cmd + Z：撤销
- Ctrl/Cmd + Shift + Z 或 Ctrl/Cmd + Y：重做
- Alt + J：选区复制为新图层
- Alt + Shift + J：选区剪切为新图层
- F：适应视图
- 1：实际尺寸
- Space：临时抓手平移
- Enter：确认裁剪
- Escape：取消选择/返回

## HTTP API

FastAPI 自动生成 OpenAPI 文档：http://127.0.0.1:8000/docs。

### 健康检查

`GET /health` 返回服务状态和密钥是否配置：

```json
{"status":"ok","provider_configured":true,"agnes_configured":true}
```

### 图片生成

`POST /api/image/generate` 请求示例：

```json
{
  "prompt": "夜晚的森林小屋，适合 Scratch 舞台",
  "style": "cartoon",
  "width": 480,
  "height": 360,
  "target": "backdrop",
  "sheet_layout": "1x1"
}
```

style 可取 cartoon、scratch、paper-cut、pixel、watercolor、custom；custom 必须同时提供 custom_style。尺寸必须是 480x360、640x360、360x360、360x480 之一。成功响应包含 request_id、PNG 类型、图片宽高、sheet_layout、image_base64、target 和 requested_canvas。

常见错误：413 REQUEST_TOO_LARGE、422 INVALID_REQUEST、503 CONFIGURATION_ERROR、502 PROVIDER_ERROR 或 TIMEOUT。

### 文本扩写

`POST /api/text/expand` 接收 kind（scene 或 style）和 text，返回 SSE：

```
event: content_delta
data: {"text":"..."}

event: message_complete
data: {}
```

### AI 积木

| 方法 | 路径 | 返回 |
| --- | --- | --- |
| POST | /api/ai-blocks/chat | SSE 对话流 |
| POST | /api/ai-blocks/requirements | 结构化需求 JSON |
| POST | /api/ai-blocks/generate | SSE：阶段、计划、ProgramIR |

生成接口会发送 phase、plan_ready、reasoning_delta、program_ready、message_complete 或 message_error 事件。请求包含 session_id、request_id、messages、target、thinking 和可选的 model_tier。

## 项目结构

```
AI-Scratch-web/
├─ backend/app/
│  ├─ main.py                         # FastAPI、CORS、健康检查、请求限制
│  ├─ config.py                       # 环境变量与固定供应商配置
│  ├─ routes/                          # image、text、ai-blocks API
│  ├─ schemas/                         # Pydantic 请求/响应和 ProgramIR
│  ├─ services/                        # 生图业务服务
│  ├─ providers/                       # 图片/文本供应商适配器
│  └─ scratch_block_catalog.py         # 原生 Scratch opcode 契约
├─ backend/tests/                      # 后端测试
├─ frontend/src/
│  ├─ app/                             # 应用编排
│  ├─ scratch/                         # Scratch Host、VM 桥接、资源导入
│  ├─ ai/                              # 生图、扩写、素材库
│  ├─ ai-blocks/                       # AI 积木、IR、编译器
│  ├─ canvas/                           # 无边画布、像素处理、导出
│  └─ styles/                           # 页面样式
├─ frontend/public/assets/             # 本地 UI 与 Scratch 素材
├─ frontend/tests/                     # Vitest 和 Playwright 测试
├─ docs/                                # 架构与部署说明
├─ package.json                         # 根级脚本
├─ start-all.bat                        # Windows 启动器
└─ README.md
```

## 开发与测试

根目录脚本：

```
npm run dev
npm run build
npm run test:unit
npm run test:e2e
npm run prepare:frontend
```

完整验证：

```powershell
backend\.venv\Scripts\python.exe -m pytest backend/tests -q
npm --prefix frontend run test:unit
npm --prefix frontend run build
npm --prefix frontend run test:e2e
```

后端测试覆盖图片校验、供应商响应、文本流和 AI 积木契约；前端测试覆盖 API/SSE、Scratch 资源导入、素材库、画布、ProgramIR 编译和目标上下文；Playwright 测试使用 mock 供应商，避免依赖真实密钥。

## 安全与限制

- API Key 只由后端读取，浏览器不接触供应商授权头。
- 远程图片由后端下载并转成 PNG Base64；前端和 SB3 不保存第三方图片 URL。
- 图片和 AI 积木接口具有请求体上限、超时和并发限制。
- AI 积木使用严格模型、原生 opcode 白名单、脚本/积木深度限制和程序契约校验。
- 素材库和画布恢复数据仅保存在浏览器本地。
- 生图当前仅支持 1x1，不支持规划稿中的 2x2 候选雪碧图。
- AI 功能依赖外部服务；Scratch 编辑、画布处理和 SB3 操作可本地完成。
- 无边画布 MVP 支持 PNG/JPEG/WebP，不支持 GIF 和 SVG 输入。
- 当前没有账号、云端协作、社区分享、官方 Backpack、云变量或遥测。
- 生产部署应使用 HTTPS、限制反向代理来源，并避免将后端端口直接暴露到公网。

## 相关文档

- [架构说明](docs/architecture.md)
- [本地部署说明](docs/deployment.md)
- [Scratch 语法规范](docs/scratch语法规范.md)
- [Scratch 代码规范](docs/scratch代码规范.md)
- [AI 积木生成提示词](docs/scratch代码生成提示词.md)
- [无边画布功能设计](无边画布功能文档设计.md)
- [实施计划](计划大纲.md)
