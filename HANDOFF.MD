# HANDOFF

## 2026-10-28 审查修复 #28 / #31

- 本轮开始已有大量源码生成、Provider、前端、测试等未提交改动，保留原实现，不回滚。详见 Documents 下本轮进度报告及临时起始 diff。
- 用户已选择私人工具共享口令用途，见 `docs/private-access.md`。AI_SCRATCH_ACCESS_PASSWORD 通过 config.Load 的 env map 读取，必须在 .env 读取之后校验：该加载器不会把 .env 项导入 os.Environ，提前调用 os.Getenv 会误判缺口令。
- 真正的启动入口 cmd/server 在 Server.Startup 和任务恢复之前创建访问中间件，防止缺口令先打开数据库/执行旧任务。后端只允许回环 IP。两份系统 Nginx 模板与用户级模板覆盖静态和 API；公开模式必须 HTTPS，TLS=0 只绑定回环，不能留下旧 HTTP 安装入口。/healthz 仅通用就绪，/health 的配置标记仍需认证。
- 共享口令可以访问全部数据并消耗共享额度，scope 只做任务分组，不是用户身份。私有模式不引入多用户预算层；任务排队与回收问题 #29/#30 尚待处理。保留现有 SSE、客户端取消与业务限额。
- 流式数据 channel 必须读完，再读取终态 error channel；同时 select 关闭数据和缓冲错误会随机假成功。consumeTextStream 处理上下文取消，避免 closed channel 热循环。不要因前半段有文本就 complete。
- 对话测试夹具必须显式关闭 thinking，才会走所构造的 OpenAI 流；默认 thinking 会进入 Anthropic 协议，不能把夹具误配修成生产协议变更。
- `go test -race -count=1 ./internal/httpapi ./internal/privateaccess ./internal/config` 通过；覆盖反复的部分内容+终态失败、正常结束、无身份付费路径在 Provider 前401、跨站简单 JSON 拒绝403。cmd/server 编译通过，隔离真实二进制+Nginx/Chrome/IPv4+IPv6边界验证通过；未运行全项目完整测试。
- smoke-test 无 netrc 只检查匿名拒绝；权限600的 netrc 才执行功能检查。不能把返回401的正常私人入口判成服务失效；readiness 用 /healthz。系统安装脚本只检查文件存在，不替代实际配置和有效密码验收。
- 未部署、重启、提交或推送。不要把隔离测试当当前公网入口已经受保护。

- 最后代理回归发现：拒绝非法Origin之后，旧Nginx用$host会丢失9966端口，使合法浏览器POST变403；三份模板均必须使用$http_host，验证需带真实Origin的POST，不能只测GET metadata。

## 2026-10-28 #29/#30 收尾
- #29：SQLite写事务在CreateRun之前检查全局32/每scope8个未结束任务，配置AI_RUNS_MAX_ACTIVE与AI_RUNS_MAX_ACTIVE_PER_SCOPE；满时429+Retry-After。scope是共享私人会话分组，仍非账号。Provider排队最多120秒，AI_QUEUE_TIMEOUT与执行超时分开，取消不会启动后续Provider。40并发跨两个Store测试只接入4个；实际Manager饱和只接8个。
- #30已有每分钟清理、24小时待确认TTL、终态唤醒/请求上下文淘汰，保留活跃waiter；1000唤醒收敛、过期证据与保留期有临时SQLite验证。PurgeExpired仍是整批SQL删除，历史大库下的分批策略/持续SSE重放与资源验收未完成，状态保留待补验证。Startup新增ticker应只由生命周期调用一次。
- runstore/runmanager/httpapi最终race通过，候选后端已构建；用户随后停止，未配置真实共享口令或更新现网代理/服务。

## 2026-11-14 功能梳理与 GitHub 脱敏同步

- 当前 AI 写积木主流程由 capabilities 协商 `scratch-source/1`，模型生成 IntentSpec，后端冻结合同并派生 Blueprint/Manifest，再逐脚本槽位生成紧凑 JSON，归一化为 ProgramIR。旧 README 与架构手册中的 Python 后端、固定六段生成、自动生图和无头 VM 行为验收说明需要结合代码核实。
- `verification-service.ts` 当前覆盖 SB3/ZIP、项目结构和资源完整性，`behavior-harness.ts` 返回空测试结果。解释当前功能时不能将保存、编译或格式检查写成玩法行为验收。
- 本次读取运行后端 capabilities，源码流水线、项目/声音素材、检查点启用，自动素材匹配、durable runs、AI 生音、画笔/音乐/朗读扩展关闭。运行进程的匿名本机能力接口仍返回 200，当前工作区 Basic Auth 修改尚不能当作运行部署已生效。
- 公开同步时将文档、部署模板和验收记录中的实际域名替换为 `scratch.example.com`，个人路径改为示例路径。压缩浏览器记录也必须解压检查，并同步更新归档清单的散列和字节数。模板安装前填写实际域名与证书路径，真实环境文件、密码文件、证书私钥和数据库由忽略规则保护。
- 本轮重新执行 Go 全包测试、前端 36 文件 299 项单测、生产构建、资源合同检查与能力目录 3 项测试，全部通过。Vite 提示主入口压缩前超过 500 kB，当前记录约 512.89 kB、gzip 165.27 kB。本轮模型付费生成与生产性能验收未执行。
