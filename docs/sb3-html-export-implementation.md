# SB3 导出 HTML 实施报告

已在右上角工具栏接入完整导出弹窗，支持当前编辑器作品和本地 SB3，含作品名称、文件名、简介、运行选项、进度、错误、取消和再次下载。采用纯前端打包，内嵌固定运行时及作品数据。

## 实施与复核

Agy 完成调研、页面和实现，并根据主代理复核修正再次下载链接过期、生成中取消、连击、文件验证、标题预填及测试环境问题。主代理补充组件卸载时取消任务，并用失败后通过的回归测试验证。

[审定方案](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/sb3-html-export-plan.md) · [详细复核](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/sb3-html-export-final-review.md)

## 最终验证

命令从仓库根目录执行：

```bash
npm --prefix frontend run build
npm --prefix frontend run test:unit
npm --prefix frontend run test:e2e -- tests/e2e/export-html.spec.ts
```

| 检查 | 结果 |
| --- | --- |
| vue-tsc 与 Vite 生产构建 | 通过 |
| 打包核心单元测试 | 21 项通过 |
| 弹窗组件单元测试 | 9 项通过，含卸载取消回归 |
| 导出浏览器回归 | 8 项通过 |
| 全量单元测试 | 139 项通过、24 项既有失败；与原配置基线相比无新增失败 |
| 独立生产预览 | 当前及本地文件实际下载、HTTP 运行、再次下载、连击、取消、小屏、转义和回焦通过 |

播放器 E2E 对页面错误和控制台错误作断言，仅排除无物理声卡环境的特定 AudioContext 硬件错误；主代理独立生产运行中两类错误均为空。

既有 24 项失败包括资源契约测试期待 v2 而代码为 v3、积木目录与扩展覆盖，以及 AI 事务测试。原配置基线和最终失败名称完全相同，详见 [基线对照](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/evidence/html-export/unit-baseline-results.json)。

## 页面和运行证据

- [桌面导出页](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/evidence/html-export/final-desktop.png)
- [390px 导出页](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/evidence/html-export/final-mobile.png)
- [实际运行播放器](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/evidence/html-export/final-player.png)
- [生产预览独立结果](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/evidence/html-export/final-reviewer-results.json)
- [取消后立即重开的结果](/home/HwHiAiUser/Documents/AI-Scratch-Web/docs/evidence/html-export/final-cancellation-results.json)

## 运行边界

项目输入上限 50MiB，项目 JSON 解压上限 10MiB；生成文件约 5.4MB 起。普通作品可本地打开，在线扩展仍需联网，浏览器权限或兼容问题可通过 HTTP/HTTPS 预览排查。导出文件保留完整 MIT 许可、第三方声明及 VM 注释。

源仓库保持完整；目标项目原有未提交修改经哈希核对保留。
