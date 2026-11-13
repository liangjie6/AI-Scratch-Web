# AI 写积木源码流水线重构与验收

2026-10-16。设计依据：[基于 Scratch 源码的流水线设计](./基于Scratch源码的AI写积木流水线设计.md)。实际模型记录：[测试证据](../evidence/ai-blocks-agnes-source-evaluation/README.md)。

## 已实现的链路

`Scratch 源码与策略 overlay → 唯一能力目录 → IntentSpec → 冻结接口 → 逐脚本紧凑 JSON → 原 ProgramIR → 原编译器、事务和恢复流程`

- 从已安装的 Scratch GUI/VM、scratch-blocks/Blockly 提取形状、端口、菜单、动态菜单来源及版本、源文件指纹。`contracts/scratch-capabilities.json` 生成前后端目录；`--check` 会拒绝缺失或过时产物。当前支持 138 个原有 opcode。
- 新请求显式携带 `pipeline_version: scratch-source/1`。模型只规划目标、状态、资源、触发器和脚本用途；代码生成逻辑身份、版本、快照绑定、请求绑定、截止时间及哈希，派生现有 Blueprint v2 / Manifest v3，不伪造旧 TaskSpec action tree。
- 每个脚本单独生成命令体。代码插入事件帽、展开 typed literal/reporter、绑定逻辑状态与资源、生成 ID 和布局，并再次通过原 ProgramIR 及整个目标的校验。modify 只发送所选脚本的原代码，完整项目和旧需求树不在每个脚本请求中重复发送。
- 目录放在稳定 system 前缀，目标、槽位、冻结目标和当前用户需求放在动态上下文。保留具体次数、位置、时序，避免缩短上下文时把有限重复变成无限循环。
- JSON 检查限制字节、嵌套、节点及积木数，拒绝重复键和多余字段。L0 只剥离完整单层 JSON 围栏、补代码固定的缺省 Intent 版本，以及按精确可信 handle 绑定既有状态；不猜测缺括号、名称或行为。
- 数值与文本保持原类型。按原生 Scratch 语义，变量赋值与列表 append/insert/replace 的值端口允许 `any`，保留 text fallback shadow；普通文本端口仍严格校验。
- Agnes 非思考路径启用经实测接受的 `json_object` 与低温参数；思考路径保留原传输。此功能不宣称 opcode 严格 JSON Schema 保证。DeepSeek 使用其 JSON object 传输。归一化器始终校验端口、菜单、目标、扩展、作用域、读写权限和资源引用。
- 出错只修该脚本，已通过脚本不重发；所有槽位和目标共享调用、保守 Token 预算及 30 分钟截止时间。HTTP 429/502/503/504 最多退避重试两次，遵守不超过 60 秒的 Retry-After；取消会中断等待。
- 冻结产物绑定原 session/request ID，防止同一产物被误用于另一请求。截止时间保存在产物中，预算 GC 或重启不能让同一产物获得新期限。公开 SHA 提供完整性和关联校验，不等同于签名或面向恶意客户端的计费鉴权。
- durable run 保存私有 source 合同、原始请求和单调预算；恢复不读取旧 TaskSpec。项目目标按两个一批并发，运行时广播不是生成依赖。公开运行摘要不泄露私有请求和代码。

## 兼容与边界

旧请求、旧草稿与旧 run 不带新版本时保留原路径。新路径失败会明确终止，不静默换用旧生成流程。通过 `AI_BLOCKS_SOURCE_PIPELINE_ENABLED=false` 可关闭新路径；前端按 capabilities 协商。

append 不删除原脚本；modify 必须至少明确选择一个授权 edit_ref；rewrite 保留受保护脚本。自定义积木仍需旧路径，新路径会明确拒绝不支持的能力或请求合同修订。资源检索、确认、准备及真正写入仍走现有事务边界。本轮未启用未经完整浏览器资源回归的生产 verification V1，也未把既有 SB3 结构检查说成语义证明。

## 验收方式与结果

模型验收使用同一组独立预期：有限运动、变量循环、持续按键、三目标广播、指定脚本修改、重写并保留保护脚本。六类输出均交给生产编译器和 graph writer，运行真实 Scratch VM；前四类还保存 SB3 并用另一台 VM 重新载入。modify/rewrite 验证真实执行与指定原脚本、积木的保留，不包含此轮模型输出的 SB3 重载检查。原值和必须保留的脚本由测试 fixture 独立定义，生成声明或“删除后重建”等价代码不能改写预期。

最终后端版本的 DeepSeek 六场景已全部通过，零模型修复。Agnes 的迭代测试中，运动、变量、持续按键曾通过，修改与广播出现过规划或格式错误；其全部失败及可获得的 usage 已保留，未宣称 Agnes 在最终版本完成六场景复测。模型结果不能证明总体成功率，也不能据此宣称 Token 节省百分比；旧 Agnes 基线因 tagged 协议错误提前停止，没有完整 usage 可作公平比较。

本轮已通过前端 36 套、299 项单元回归、类型检查及生产构建；后端完整测试与 go vet；authoring/runmanager 及预算并发的 race 回归；目录生成一致性检查。命令与结果见 [检查记录](../evidence/ai-blocks-agnes-source-evaluation/validation.json)。

已更新后端服务和 Nginx 实际静态目录，核对线上前端字节与构建一致、后端二进制与 DeepSeek 验收版本一致，新协议已启用，见 [部署记录](../evidence/ai-blocks-agnes-source-evaluation/deployment.json)。最终 [Agnes 真实页面测试](../evidence/ai-blocks-agnes-source-evaluation/browser-release/report.json) 通过生成、提交、运行到 `(30,0)`、撤销后恢复空白积木与角色选择；无页面脚本异常。响应 usage 独立确认实际 provider 为 Agnes，共 6,916 个 reported tokens，见 [响应补验](../evidence/ai-blocks-agnes-source-evaluation/browser-release/postcheck.json)；同次输出另通过真实 VM 终值及 SB3 重载检查。这是一个页面场景，不等同于 Agnes 六场景全部通过。

页面回归发现并修复了撤销缺陷：VM 重载期间同步发布空角色事件，而面板没有进入本地忙碌状态，快照读取抛错中止重载；同时改用逻辑目标恢复选择，避免 SB3 重载后的新 ID 导致旧选择失效。失败页面记录、测试脚本请求数误判和模型档位误选的原始报告及更正均保存在 [浏览器历史](../evidence/ai-blocks-agnes-source-evaluation/browser-history/README.md)。

测试 VM 没有 renderer，碰撞和画面效果明确列为未覆盖；本轮行为结论仅适用于列出的场景。

## 复现

```sh
npm run check:scratch-capabilities
npm run test:scratch-capabilities
npm --prefix frontend run test:unit
npm --prefix frontend run build
go -C backend-go test ./...
go -C backend-go vet ./...
go -C backend-go test ./internal/authoring ./internal/runmanager ./internal/orchestration -race
```

以下命令会真实调用已配置的模型；`normal` 为 Agnes，`advanced` 为 DeepSeek，报告与目录不能混用：

```sh
python3 scripts/test-ai-blocks-agnes.py --backend http://127.0.0.1:18000 --model-tier advanced --cases motion,variables,keyboard,broadcast,modify,rewrite --workers 1 --output-dir /tmp/source-live-evaluation
node scripts/test-ai-blocks-browser.mjs --live-model-calls --model-tier advanced --base-url http://127.0.0.1:18066 --output /tmp/source-browser-evaluation
```

独立 VM 验收工具说明见 [verify-ai-program.README](../../frontend/scripts/verify-ai-program.README.md)。模型脚本仅读取后端 HTTP 接口，不读取或保存提供商密钥。
