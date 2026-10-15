# SB3 导出 HTML 部署记录

2026-10-11 已更新正式站点：https://kfcv50.icu:9966/ 。前端发布到 `/var/www/ai-scratch-web`，由现有系统 nginx 托管；Go 后端健康检查正常。

## 部署复验修复

- 点击导出后，表单控件被禁用会使浏览器焦点离开弹窗。现在在打包状态下将焦点移到仍可操作的关闭按钮，确保 Escape 可以关闭并取消任务。新增回归单测先在原实现中失败，修复后通过；正式浏览器也验证了下载后的 Escape 关闭。
- Scratch 内置帮助图标请求 `/static/assets/`，文件实际位于 `/assets/scratch/static/assets/`。在正式 HTTPS 配置、其他 nginx 部署模板以及 Vite 开发/预览配置中补齐路径映射，消除三个图标的 404，无需复制第二份资源。

## 验证结果

- `npm --prefix frontend run build` 通过。
- 导出相关单测 31 项通过：打包器 21 项、弹窗 10 项。本轮没有重新运行已有 24 项失败的全量单测；基线说明见最终审查报告。
- 正式 HTTPS 页面返回 200，Scratch 编辑器正常，浏览器处于安全上下文，播放器与 VM 仍按需加载。
- 当前项目和通过 GUI 保存的本地 SB3 均能下载单文件 HTML；两个产物分别为 5,434,525 和 5,434,531 字节。
- VM、模板、许可证、第三方声明、manifest 的已部署响应内容哈希与项目内原文件一致。
- 下载的播放器舞台与绿旗可操作，没有页面错误、控制台错误或外部网络请求。
- 正式站点页面没有控制台错误或失败 HTTP 响应；`/health` 返回 200 和 `status: ok`。
- nginx 配置检查通过，nginx 与后端服务均处于运行状态；开发环境的图标路由也返回正确 SVG。

机器证据：`docs/evidence/html-export/deployment-smoke-results.json`。截图：`deployed-desktop.png` 与 `deployed-player.png`，均位于同一证据目录。

本机 Mihomo 将域名解析到 fake-IP，普通域名 curl 遇到 SSL EOF。浏览器复验使用域名到本机的解析覆盖，直连正式 nginx 并保留 TLS 证书校验。另经 DNS-over-HTTPS 查询真实 AAAA 地址 `240e:379:3b15:d600::e83`，与本机公网 IPv6 一致；通过该地址进行 HTTPS 页面及健康检查均成功。此轮没有从外部网络进行探测。

## 研究目录可删除

`/home/HwHiAiUser/Documents/scratch-converter-research-review/` 已不属于运行或构建依赖。导出代码位于本项目的 `frontend/src/scratch-export/`；VM、模板、许可证和声明位于 `frontend/public/assets/htmlifier/`，部署时一并复制为常规文件。

项目全目录软链接检查没有发现指向研究目录的链接。代码、构建和部署配置中出现的研究项目名称仅用于来源及版权说明。方案、实施和验证文档也都已保存在本项目中。用户可以自行删除上述研究目录，不会影响导出功能。

## 回滚备份

备份独立保存在研究目录之外：

- 发布前静态文件：`/var/backups/ai-scratch-web/frontend-20261011T153409Z.tar`
- 修改前 nginx 站点配置：`/var/backups/ai-scratch-web/nginx-20261011T154128Z.conf`
