# AI Scratch Web — 部署说明（nginx + Go 后端）

本目录包含把 AI Scratch Web 部署到本机 **9966** 端口所需的全部内容。
当前公网入口是 `https://scratch.example.com:9966/`：系统 nginx 托管已发布到
`/var/www/ai-scratch-web` 的前端，`/api/` 与 `/health` 反向代理到由 systemd
管理的 Go 后端（`127.0.0.1:8000`）。

## 目录结构

| 路径 | 说明 |
| --- | --- |
| `nginx/nginx.conf.template` | 用户级 nginx 配置模板（无需 root，pid/日志/临时目录全部在 `nginx/` 内） |
| `scripts/start-backend.sh` | 编译并后台启动 Go 后端（setsid 脱离当前会话） |
| `scripts/start-nginx.sh` | 渲染配置并启动/重载 nginx |
| `scripts/stop.sh` | 停止 nginx 与后端 |
| `scripts/status.sh` | 查看端口、健康检查、防火墙与日志位置 |
| `system/ai-scratch-web-https.conf` | 当前公网 HTTPS 站点配置，复用已有的 Let's Encrypt 证书 |
| `system/publish-frontend.sh` | 将构建产物发布到系统 nginx 可读取的目录 |
| `system/ai-scratch-web.conf` | 使用仓库 dist 路径的系统 HTTPS 站点配置 |
| `system/ai-scratch-web-backend.service` | systemd 单元（开机自启） |
| `system/install-system.sh` | 全新环境 HTTPS 安装脚本；当前站点启用时拒绝执行 |
| `system/open-firewall.sh` | 放行 9966/TCP（IPv4 + IPv6，需要 root） |

## 私人访问与独立部署

先完成[口令与 HTTPS 配置](../docs/private-access.md)。后端缺少 `AI_SCRATCH_ACCESS_PASSWORD` 会拒绝启动；Nginx 缺少密码文件会拒绝启动。默认使用 HTTPS；本机测试显式 `AI_SCRATCH_TLS=0` 时只监听回环地址，不再提供公开明文入口。

```bash
npm --prefix frontend install
npm --prefix frontend run build
# 先在 backend-go/.env 填入随机访问口令，再以相同口令创建 deploy/nginx/private.htpasswd
bash deploy/scripts/start-backend.sh
# 先准备受信任证书；隔离环境可用下方自签证书脚本
bash deploy/scripts/start-nginx.sh
bash deploy/scripts/status.sh
```

访问 `https://127.0.0.1:9966/`（证书必须包含访问地址并已被设备信任）。匿名入口应为 401；浏览器输入共享口令后访问。现有 9966 若被系统 nginx 占用，不启动另一实例。

## HTTPS 部署

### 公网域名 `scratch.example.com`

当前公网入口 `https://scratch.example.com:9966/` 由系统 nginx 托管，复用
`/etc/nginx/ssl/scratch.example.com.pem` 和 `.key` 的 Let's Encrypt 证书。
`deploy/system/ai-scratch-web-https.conf` 是已安装站点配置，前端文件发布到
`/var/www/ai-scratch-web`。每次重新构建前端后执行：

```bash
sudo bash deploy/system/publish-frontend.sh
```

后端由 `ai-scratch-web-backend.service` 管理。查看运行状态和日志：

```bash
systemctl status ai-scratch-web-backend.service
sudo journalctl -u ai-scratch-web-backend.service -n 50 --no-pager
bash deploy/scripts/status.sh
```

证书由 root 的 acme.sh 定时任务续期，续期后会重载系统 nginx。站点没有设置
HSTS，因为同一域名的其他端口仍有 HTTP 服务。HTTP 访问 9966 会跳转到上述
HTTPS 地址。

### 本机自签证书（其他独立部署环境）

\`\`\`bash
bash deploy/scripts/make-self-signed-cert.sh      # 生成含本机 IPv4/IPv6 SAN 的自签证书
AI_SCRATCH_TLS=1 bash deploy/scripts/start-nginx.sh
# 仅本机回环 HTTP 调试：
AI_SCRATCH_TLS=0 bash deploy/scripts/start-nginx.sh
\`\`\`

证书会自动带上 \`DNS:localhost\`、\`127.0.0.1\`、\`::1\`、局域网 IPv4 与全局 IPv6 的
\`subjectAltName\`。自签证书不受 iPhone Safari 默认信任，需要在设备上安装并信任
证书；公网域名请使用上面的正式证书部署。

## IPv6

nginx 同时监听 `0.0.0.0:9966` 与 `[::]:9966`（双栈），无需额外配置即可通过
链路本地、ULA 以及运营商分配的全局 IPv6 地址访问。验证：

```bash
curl -6 -I https://scratch.example.com:9966/
curl -6 -I http://scratch.example.com:9966/   # 301 跳转到 HTTPS
```

## 防火墙

当前主机的 ufw 已启用，9966/TCP 的 IPv4 与 IPv6 入站规则均已放行。
在新主机上部署时，用 root 执行：

```bash
sudo bash deploy/system/open-firewall.sh
```

该脚本会自动识别 ufw / firewalld / nftables / iptables，并为 IPv4 与 IPv6 同时放行
9966/TCP。手工等价命令：

```bash
sudo ufw allow 9966/tcp          # ufw 在 IPV6=yes（Ubuntu 默认）时同时下发 v4/v6 规则
sudo ufw status verbose
```

当前主机可用 `sudo ufw status numbered` 检查 9966/TCP 规则。

## 全新环境系统安装

准备好 private.env、htpasswd 与证书后才可运行 `sudo bash deploy/system/install-system.sh`。安装脚本会更改 systemd 和 Nginx，须先确认端口归属并备份；已有系统 HTTPS 站点时只更新对应配置，不重复安装。此轮修复没有运行安装、开放端口或重启服务。

## 环境变量

后端读取 `backend-go/.env`（可用 `AI_SCRATCH_ENV_FILE` 覆盖路径）。
另有若干部署专用变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AI_SCRATCH_HOST` | `127.0.0.1` | 后端监听地址（由 nginx 反代，勿暴露公网） |
| `AI_SCRATCH_PORT` | `8000` | 后端监听端口 |
| `AI_SCRATCH_PORT`（nginx 脚本） | `9966` | nginx 对外端口 |
| `AI_SCRATCH_WEB_ROOT` | `frontend/dist` | nginx 静态根目录 |
| `AI_SCRATCH_LOG_LEVEL` | `INFO` | 业务日志级别 |
| `HTTPS_PROXY` | 空 | 上游 AI 服务代理；本机需经 `http://127.0.0.1:7890` 才能访问外网 |

系统级安装的 systemd 单元已内置代理环境变量（`HTTPS_PROXY/HTTP_PROXY/NO_PROXY`）。
用户级启动时如需代理：

```bash
HTTPS_PROXY=http://127.0.0.1:7890 HTTP_PROXY=http://127.0.0.1:7890 \
NO_PROXY=127.0.0.1,localhost,::1 bash deploy/scripts/start-backend.sh
```

## 端口与进程

```bash
ss -tlnp | grep -E '9966|8000'
sudo tail -f /var/log/nginx/error.log
sudo journalctl -fu ai-scratch-web-backend.service
```
