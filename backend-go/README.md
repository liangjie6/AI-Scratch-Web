# AI Scratch Web — Go 后端

这是 `backend/`（Python + FastAPI）的 **Go 重写版本**，行为按原实现 1:1 移植：
同样的 HTTP 接口、同样的 SSE 协议、同样的校验契约与错误码、同样的提示词文本。

## 为什么

- 原实现要求 Python ≥ 3.11（使用了 `enum.StrEnum`），运行环境只有 3.10，无法直接启动。
- Go 版本编译为单个静态二进制，无虚拟环境、无运行时依赖，便于用 nginx + systemd 托管。

## 目录

| 包 | 对应 Python 模块 | 说明 |
| --- | --- | --- |
| `cmd/server` | `app/main.py` + uvicorn 入口 | 进程入口、日志、优雅退出 |
| `internal/config` | `app/config.py` | 环境变量 / `.env` 配置与范围校验 |
| `internal/blockcatalog` | `app/scratch_block_catalog.py` | 原生积木契约表（由 Python 数据生成） |
| `internal/resourcecontract` | `app/resource_contract.py` | 资源操作契约与提示词渲染 |
| `internal/schema` | `app/schemas/ai_blocks.py` | 全部请求/响应模型、严格解码与契约校验 |
| `internal/prompt` | `app/prompt_builder.py`、`app/prompts.py`、`app/orchestration/error_messages.py`、`cache_manager.py` | 分层提示词、知识库、修复提示、提示词缓存 |
| `internal/provider` | `app/providers/*.py` | Agnes / DeepSeek / ChatGPT Responses / 生图 上游客户端 |
| `internal/orchestration` | `app/orchestration/streaming_parser.py`、`orchestrator.py` | 标签流解析、分段生成、修复循环 |
| `internal/runstore` | `app/orchestration/run_state_machine.py`、`run_store.py` | SQLite 持久化任务与事件流 |
| `internal/runmanager` | `app/orchestration/run_manager.py` | 持久化任务编排 |
| `internal/imageservice` | `app/services/image_generation.py` | 生图归一化（RGBA → PNG）与并发闸门 |
| `internal/httpapi` | `app/main.py`、`app/routes/*.py` | 路由、CORS、请求体限制、SSE 输出 |

## 构建与运行

```bash
cd backend-go
go build -o bin/ai-scratch-web-backend ./cmd/server
# 读取当前目录下的 .env
./bin/ai-scratch-web-backend --host 127.0.0.1 --port 8000
```

参数：`--host`、`--port`、`--log-level`；也可用 `AI_SCRATCH_HOST` / `AI_SCRATCH_PORT` /
`AI_SCRATCH_LOG_LEVEL` 环境变量。`AI_SCRATCH_ENV_FILE` 可覆盖 `.env` 路径。

## 测试

```bash
cd backend-go
go test ./...
```

其中若干测试直接与 Python 参考实现比对（golden 文件位于各包的 `testdata/`）：

- `internal/prompt`：`BLOCK_CATALOG` 知识库、7 个 segment 的 JSON Schema、资源操作契约
  与 Python 输出**逐字节一致**。
- `internal/resourcecontract`：资源操作契约文本与 JSON 与 Python **逐字节一致**。
- `internal/orchestration`：真实线上样本（缺失 `}` 的单行 ProgramIR）的自愈解析。
- `internal/runstore`：schema、迁移、幂等、事件序列、指标与 Python 输出一致；
  `request_hash` 与 CPython 计算的摘要相同。

## 与 Python 版本的差异

1. `enum.StrEnum` → Go 常量字符串；`RunStatus` 等取值不变。
2. pydantic 的 `extra='forbid'`、默认值填充、必填校验由 `schema.DecodeStrict` 复现；
   校验失败返回 `422 {"detail":{"code":"INVALID_REQUEST","message":"请求参数不正确"}}`
   （Python 的 FastAPI 返回的是 `detail` 数组；前端两种形式都能处理，Go 版给出更具体的错误码）。
3. 上游调用使用 `net/http`：`http.ProxyFromEnvironment` 会自动遵循 `HTTPS_PROXY`，
   与 httpx/openai 的行为一致。
4. 图像归一化使用标准库 `image/png`（支持 PNG/JPEG/GIF 解码），不再依赖 Pillow。
5. `to_strict_json_schema` 的结果在构建期从 pydantic 生成并内嵌，避免运行期依赖 Python。
