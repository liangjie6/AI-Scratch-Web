# AI Scratch Web — 部署说明（nginx + Go 后端）

本目录包含把 AI Scratch Web 部署到本机 **9966** 端口所需的全部内容。
当前公网入口是 `https://kfcv50.icu:9966/`：系统 nginx 托管已发布到
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
| `system/ai-scratch-web.conf` | 旧的系统级 HTTP 站点配置 |
| `system/ai-scratch-web-backend.service` | systemd 单元（开机自启） |
| `system/install-system.sh` | 旧版 HTTP 系统部署脚本；当前 HTTPS 站点启用时会拒绝执行 |
| `system/open-firewall.sh` | 放行 9966/TCP（IPv4 + IPv6，需要 root） |

## 独立 HTTP 部署（9966 空闲时，无需 root）

当前机器的 9966 已由系统 nginx 使用；更新当前站点请按下面的“公网域名”一节
发布前端，后端由 systemd 管理。以下步骤用于尚未占用 9966 的环境。

```bash
# 1) 构建前端
npm --prefix frontend install
npm --prefix frontend run build

# 2) 启动 Go 后端（读取 backend-go/.env）
bash deploy/scripts/start-backend.sh

# 3) 启动 nginx
bash deploy/scripts/start-nginx.sh

# 4) 查看状态
bash deploy/scripts/status.sh
```

访问：

- IPv4：http://127.0.0.1:9966/
- IPv6：http://[::1]:9966/
- 局域网：http://<本机 IP>:9966/

## 通过非 localhost 地址打开（独立 HTTP 部署）

浏览器只在**安全上下文**（HTTPS、\`localhost\`、\`127.0.0.1\`、\`[::1]\`）里提供
\`crypto.subtle\`。用 \`http://192.168.x.x:9966/\` 或 \`http://[240e:...]:9966/\` 打开时
\`crypto.subtle\` 是 \`undefined\`，直接调用会抛：

\`\`\`
Cannot read properties of undefined (reading 'digest')
\`\`\`

项目对契约、检查点和 Scratch 编辑器产物做 SHA-256 完整性校验，原先直接调用
\`crypto.subtle.digest\`，因此在非安全上下文里整页打不开。现在统一走
\`frontend/src/shared/sha256.ts\`：优先用 WebCrypto，缺失时退回内置的纯 JS SHA-256，
所以 **HTTP 下任意主机名/IP 都能正常打开**，摘要结果与后端一致。

> 如果你更希望页面运行在安全上下文里（例如将来需要 service worker、Clipboard API
> 等只在安全上下文可用的能力），可以启用 HTTPS：见下一节。

用真实浏览器复验：

\`\`\`bash
node deploy/scripts/probe-page.mjs http://192.168.10.235:9966/
node deploy/scripts/probe-page.mjs "http://[240e:379:3b1e:a200::e83]:9966/"
\`\`\`

输出里 \`secureContext:false\` / \`subtleCrypto:false\` 就是会触发上述问题的环境，
\`ok:true\` 表示页面与编辑器都正常、且没有任何 console/page error。

## HTTPS 部署

### 公网域名 `kfcv50.icu`

当前公网入口 `https://kfcv50.icu:9966/` 由系统 nginx 托管，复用
`/etc/nginx/ssl/kfcv50.icu.pem` 和 `.key` 的 Let's Encrypt 证书。
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
# 换回 HTTP：
AI_SCRATCH_TLS=0 bash deploy/scripts/start-nginx.sh
\`\`\`

证书会自动带上 \`DNS:localhost\`、\`127.0.0.1\`、\`::1\`、局域网 IPv4 与全局 IPv6 的
\`subjectAltName\`。自签证书不受 iPhone Safari 默认信任，需要在设备上安装并信任
证书；公网域名请使用上面的正式证书部署。

## IPv6

nginx 同时监听 `0.0.0.0:9966` 与 `[::]:9966`（双栈），无需额外配置即可通过
链路本地、ULA 以及运营商分配的全局 IPv6 地址访问。验证：

```bash
curl -6 -I https://kfcv50.icu:9966/
curl -6 -I http://kfcv50.icu:9966/   # 301 跳转到 HTTPS
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

## 旧版系统级 HTTP 安装（仅全新环境）

以下旧脚本只适用于尚未安装当前 HTTPS 站点的环境：

```bash
sudo bash deploy/system/install-system.sh   # 安装并启用服务，同时停掉用户级实例
sudo bash deploy/system/open-firewall.sh    # 放行 9966
```

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
