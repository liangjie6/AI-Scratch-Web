# SB3 导出 HTML 接入方案（审定实施版）

Agy 使用 `gemini-3.8-flash-high` 完成两仓库调查与初稿；主代理审查方案后安排实施，并完成代码、页面和生产预览复核。功能已接入。原始调研稿保存在本次 Agy 调查任务的 artifacts 目录。

## 接入位置与页面

在 AI Scratch Web 右上角现有工具栏末尾新增“导出 HTML”入口，沿用紫色图标按钮和悬停展开标签。点击后打开完整导出弹窗；桌面居中，390px 窄屏使用可滚动的底部面板。

```text
导出单文件 HTML                         关闭
项目来源：当前编辑器作品 / 本地 .sb3 文件
本地文件选择（选择本地来源时显示）
作品名称
导出文件名（可留空，自动补 .html）
作品说明（可留空）
运行选项：兼容模式 / 加速模式 / 自动运行
生成进度、错误提示或成功信息及“再次下载”
                            取消 / 开始导出并下载
```

当前作品通过已有 `bridge.saveProject()` 获取完整 SB3。AI 事务锁定或忙碌时禁用导出；锁定状态中途变化也会取消任务。弹窗包含初始焦点、Tab 约束、Escape、遮罩关闭和返回入口焦点，编辑器背景设为 inert。

## 技术方案与源代码依据

源仓库 `scratch-converter-research-review/src/packager.js` 读取固定 VM、播放器模板和项目数据，再进行 Base64 嵌入与模板替换。其 Node 文件系统 API 不能直接用于浏览器，目标实现保留组装逻辑，改用浏览器 File/Blob、FileReader、fetch 和下载链接。

```text
当前项目序列化 / 本地 SB3 校验
  → Base64 项目数据
  → 按需读取本地固定 VM 与模板
  → 组装单文件 HTML，内嵌许可声明
  → Blob 下载
```

资源位于 `frontend/public/assets/htmlifier/`，随 Vite 生产构建分发；首次导出时加载并缓存成功结果，失败请求可重试。应用启动不会提前请求额外的约 5MB VM。

## 行为与边界

- 第一版支持当前作品及本地 **SB3**。本地文件检查后缀、压缩包可读性、`project.json` 存在与有效 JSON，以及最小 `targets` 数组结构；只解压项目入口。
- 项目输入上限为 50MiB，项目 JSON 解压上限为 10MiB。HTML 大小约为固定播放器 5.4MB 加项目大小的 1.33 倍。
- 文件名不能包含路径、控制字符或非法字符；标题非法且文件名留空时提示填写合法文件名。作品说明仅由用户填写。
- 标题和说明进行 HTML 转义；脚本中的结束标签进行转义；模板替换使用函数，保留 `$&` 等字面文本。
- 生成中可取消。AbortController 与任务令牌阻止关闭、重开或组件卸载后旧任务下载。再次下载每次使用新链接，链接独立延迟释放。
- 普通作品可本地打开；浏览器、音频权限与扩展存在差异，异常时使用 HTTP/HTTPS 预览。翻译、语音等在线功能仍需网络。当前导出使用固定研究版 VM，复杂扩展的兼容性需单独验证。

## 固定资源与许可证

VM 从源仓库原样复制，大小为 **5,322,872 字节**，LF SHA-256 为：

```text
29f52618abaa41a060862bd3981f846ab4b249ece56762cd2423e8048b1a732e
```

源文档的 `42B75B...` 对应相同内容转为 CRLF 后的哈希。播放器模板仅移除了无用的远程图片和 `og:image`。导出 HTML 内嵌凉介和 Sean 的完整 MIT 许可及第三方声明，VM 原有注释保留。

## 文件位置

| 文件 | 职责 |
| --- | --- |
| `frontend/src/app/App.vue` | 右上角入口、弹窗状态、背景 inert 与回焦 |
| `frontend/src/scratch-export/HtmlExportDialog.vue` | 完整导出表单与任务取消 |
| `frontend/src/scratch-export/packager-core.ts` | SB3 校验、组装、转义和下载 |
| `frontend/src/scratch-export/html-export-types.ts` | 参数与结果类型 |
| `frontend/public/assets/htmlifier/` | 固定播放器、模板和许可证 |
| `frontend/src/styles/local-ui.css` | 桌面及小屏样式 |
| `frontend/tests/*export*.test.ts` | 导出核心和组件单元测试 |
| `frontend/tests/e2e/export-html.spec.ts` | 实际下载、播放器运行和页面回归 |

## 验收结果

生产构建通过；导出专用单元测试 **30 项通过**；浏览器测试 **8 项通过**。独立生产预览验证了实际下载与 HTTP 播放、一分钟后再次下载、连击去重、取消后重开、特殊文本、按需资源、小屏和回焦。

全量单元测试为 139 项通过、24 项失败；使用原测试配置并排除新增导出测试后，同样存在这 24 项失败，未出现新增失败。失败集中在现有 AI 事务、资源契约和积木目录相关测试。

[实施报告](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/sb3-html-export-implementation.md) · [最终复核](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/sb3-html-export-final-review.md)
