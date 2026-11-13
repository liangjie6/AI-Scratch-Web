# 私人部署访问边界

2026-10-28：按已确认的私人共享口令用途修复。一个共享身份可以访问全部数据和服务端模型额度；不提供多人数据隔离或按用户计费。`X-AI-Run-Scope`、Origin、data epoch 都不是登录凭据。

后端启动前必须配置 `AI_SCRATCH_ACCESS_PASSWORD`，至少 16 字节，建议使用密码管理器生成的随机口令。`AI_SCRATCH_ACCESS_USER` 默认 `owner`。缺口令或将 HTTP 后端绑定到非回环 IP 时直接拒绝启动；允许 `127.0.0.1` / `::1`。唯一免登录接口是最小就绪探针 `GET /healthz`，不返回文档、历史、密钥或 data epoch。

环境文件：`backend-go/.env（用户级）或 /etc/ai-scratch-web/private.env（systemd）`，权限 600，不提交 Git。后端与 Nginx 必须使用相同用户名和口令：

```text
AI_SCRATCH_ACCESS_USER=owner
AI_SCRATCH_ACCESS_PASSWORD=由管理员填写的随机口令
```

Nginx 密码文件：`deploy/nginx/private.htpasswd（用户级）或 /etc/nginx/ai-scratch-web.htpasswd（系统级）`。使用 `htpasswd -c 文件路径 owner` 交互创建（该命令属于 apache2-utils；已有文件不要使用 `-c`，避免覆盖其他条目），输入与后端相同口令。密码文件仅让 Nginx 运行用户可读，禁止加入 Git。不要使用 `htpasswd -b` 把明文口令放进命令行。可通过 `openssl passwd -6` 交互生成 SHA-512 crypt，再写入 `owner:哈希`；后端读取的是原始口令，不能填该哈希。

公开入口必须使用浏览器信任的 HTTPS，证书与私钥路径为 `/etc/nginx/ssl/scratch.example.com.pem /etc/nginx/ssl/scratch.example.com.key`，新主机按实际域名更换；私钥只对 Nginx 所需用户开放。自签证书只用于已安装信任的设备或隔离测试。浏览器显示原生 HTTP Basic 登录框，输入 owner 和共享口令；不要把口令拼进 URL。Basic 凭据由浏览器缓存，退出共享设备应关闭独立浏览器会话。轮换时同时更新环境文件与密码文件，重启后端并重载经过 `nginx -t` 的代理配置。

发布前保留旧二进制、站点配置与数据备份；确认活动 Nginx root、端口和证书，不覆盖其他服务。首次切换需先准备凭据，否则后端会按设计拒绝启动。本次只修改源码与模板，没有创建实际部署口令或修改运行服务。

验收使用独立数据库、假 Provider 与临时文件：IPv4/IPv6 匿名读取页面、静态资源、API、附件、导出和维护入口均应返回 401（最小 healthz 除外）；正确口令可进入应用，跨站写入仍受拒绝。凭据校验在业务 Handler 之前，SSE 不包装 ResponseWriter，原有流式/取消接口保持可用。已有 revision、epoch 和同源检查继续生效。

性能：后端启动时预计算凭据 SHA-256，逐请求恒时比较，不新增查库、定时器或远端认证。Nginx 对入口逐请求校验密码哈希，有 CPU 成本；2026-10-28 隔离环境 30 次单连接请求，含新建 TLS、SHA-512 crypt 与读取接口的 P95 约 15–16 ms，仅为本机样本，不是纯认证增量或生产容量。上线应测冷启动静态资源并发、SSE、P95/P99 与 CPU；不要用关闭认证换性能。

用户级 Nginx 默认 TLS=1；显式 `AI_SCRATCH_TLS=0` 只监听回环地址。系统级两份模板都要求 TLS 和密码文件；安装脚本在改服务前检查必需文件。`deploy/scripts/smoke-test.sh` 无 netrc 时只检查匿名拒绝；设置权限 600 的 `AI_SCRATCH_NETRC_FILE` 后才做功能检查，使用有效域名/可信 CA，勿为带口令请求加 `-k`。此选择按私有入口关闭匿名付费路径；没有新增多用户预算系统。
