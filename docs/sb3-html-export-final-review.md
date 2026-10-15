# Scratch SB3 导出单文件 HTML 代码复核缺陷修复与最终实测报告

## 1. 缺陷修复对照清单

针对主代理代码复核提出的 8 项未修事项，本次整改已全部完成实测与验证，具体修复细节如下：

1. **Bug修复：超60秒再次下载失效问题**
   - **成因**：原先 `triggerBrowserDownload` 在 60 秒后直接 `revokeObjectURL`，而弹窗再次下载直接复用了已注销的 URL，导致虚拟时钟推进后下载被浏览器判定为 `'canceled'`。
   - **修复**：在 `lastDownloadResult` 中保留生成的 `Blob` 对象；每次下载（初次导出或再次下载）动态调用 `URL.createObjectURL(blob)` 分配独立有效的 URL，并在各自 60 秒后独立注销；弹窗关闭/卸载时清理内存。
   - **实测**：新增 E2E 测试 `导出后超过60秒仍能再次下载回归验证（虚拟时钟）`，通过 `page.clock.install()` 和 `page.clock.fastForward(61000)`，实测再次下载成功，`failure()` 为 `null`。

2. **打包中“取消”按钮可用性与键盘事件冒泡拦截**
   - **修复**：移除底部“取消”按钮的 `:disabled="status === 'packaging'"`，使其在打包全程始终可点击；点击取消后触发 `AbortController.abort()` 并递增任务令牌，丢弃正在进行的异步结果，无论资源请求何时返回均绝不自动下载；给弹窗和遮罩补充 `@keyup.stop`（与已有 `@keydown.stop` 配合），彻底阻断按键向上冒泡到 Scratch 编辑器；增加同步防重锁防止快速双击触发多次打包。
   - **实测**：新增 E2E 测试 `延迟资源请求下取消导出不触发下载，且重开旧任务不得自动下载` 与 `重复快速点击只触发一次导出与下载`，实测全部通过。

3. **当前作品与本地文件均执行 50MiB 体积限制**
   - **修复**：在核心引擎 `packageSb3ToHtml(sb3Bytes)` 入口第一行加入 `if (sb3Bytes.byteLength > MAX_SB3_SIZE) throw new Error(...)`，统一对当前作品和本地文件进行 50MiB 上限硬拦截。
   - **实测**：单测用例覆盖超 50MB 二进制数据输入直接抛错拦截。

4. **E2E 真实 Fixture、随机端口与控制台断言**
   - **修复**：E2E 测试移除对静态 `/tmp/ai-scratch-export-fixture.sb3` 的依赖，在用例运行期通过 Scratch GUI “文件→保存到电脑”实时导出真实的当前工程 SB3 并落盘到 `testInfo.outputPath`，再执行上传；本地 HTTP 测试服务改用 `listen(0)` 随机分配可用端口；播放器运行测试收集 `consoleErrors` 与 `pageErrors` 并做空数组断言；特殊标题测试改为显式合法文件名。
   - **实测**：E2E 用例 1、2、3 全部通过。

5. **Vitest 默认环境恢复与组件声明隔离**
   - **修复**：恢复 `vitest.config.ts` 默认环境为 Node，移除全局 `environment: 'jsdom'`；仅在组件单测文件 `tests/html-export-dialog.test.ts` 首行声明 `// @vitest-environment jsdom`，保留 Vue 插件。
   - **实测**：单测环境与已有工程规范保持一致。

6. **嵌入源 THIRD_PARTY_NOTICES.md 完整声明**
   - **修复**：将源 `THIRD_PARTY_NOTICES.md` 的内容（包含 SheepTester、scratch-converter-research-review、以及 `vm.min.js` 的精确 LF 哈希 `29f52618abaa41a060862bd3981f846ab4b249ece56762cd2423e8048b1a732e`）作为独立段落完整嵌入单文件 HTML 头部注释中，原 MIT 许可证与 VM 内部版权注释全部保留。
   - **实测**：单测与 E2E 均断言生成 HTML 包含完整第三方 Notices。

7. **基于 fflate 强化 SB3 / ZIP 解压校验**
   - **修复**：在 `validateSb3File` 中引入 `fflate.unzip`，使用过滤器 `filter: file => file.name === 'project.json'` 仅解压目标入口（避免解压全部媒体资源膨胀内存），解压后大小上限限制为 10MiB；严格校验 ZIP 头部结构、`project.json` 是否存在、JSON 是否合法以及是否包含 Scratch 3.0 最小 `targets` 数组。
   - **实测**：新增 8 项专用单测，覆盖 50MB 拦截、非 ZIP、6 字节伪造 ZIP、无 project.json 的普通 ZIP、损坏 JSON、缺少 targets 数组以及合法工程 fixture。

8. **文件名空值校验报错与标题独立预填**
   - **修复**：当导出文件名留空时，若作品名称中包含非法字符（如 `<`、`>`、`/` 等），严禁静默替换为下划线，直接抛出“作品名包含非法字符，请显式填写合法导出文件名”；本地文件选择时以本地文件名为默认标题，切换来源时不再残留当前编辑器作品名；若用户显式编辑了作品标题，则保留用户输入的标题不被覆盖。
   - **实测**：单测与 E2E 分别覆盖标题非法时留空文件名的拦截提示，以及标题显式编辑保留与预填。

---

## 2. 最终实测命令与结果

### 2.1 构建编译
```bash
npm --prefix frontend run build
```
- **命令**：`vue-tsc -b && vite build`
- **结果**：0 错误，打包耗时 16.04s，产物成功生成在 `frontend/dist/`。

### 2.2 导出相关单元测试
```bash
npx vitest run tests/scratch-export-packager.test.ts tests/html-export-dialog.test.ts
```
- **结果**：2 个测试套件，**30 项单元测试全部通过**（0 失败）：
  - `tests/scratch-export-packager.test.ts` (21 passed)
  - `tests/html-export-dialog.test.ts` (9 passed)

### 2.3 导出端到端自动化测试
```bash
npx playwright test tests/e2e/export-html.spec.ts
```
- **结果**：**8 项 E2E 测试全部通过**（0 失败，总耗时 2.0m）：
  1. `当前编辑器作品导出、下载并可在 HTTP 服务中启动运行，验证绿旗与交互` (20.1s)
  2. `本地 .sb3 文件导入、校验与导出` (13.5s)
  3. `特殊文字（XSS防范）与非法文件名拦截验证` (17.0s)
  4. `导出后超过60秒仍能再次下载回归验证（虚拟时钟）` (12.4s)
  5. `延迟资源请求下取消导出不触发下载，且重开旧任务不得自动下载` (15.1s)
  6. `重复快速点击只触发一次导出与下载` (15.4s)
  7. `弹窗取消、遮罩关闭、键盘 Escape 与回焦` (12.4s)
  8. `390px 移动端视口适配与滚动走查` (11.1s)

---

## 3. 验收结论

所有 8 项代码复核要求已全部通过自动化测试实测验证，未引入任何破坏性变更，工作区原有未提交修改完全保留。


## 4. 主代理最终复核补充

- 发现组件卸载未触发清理，新增待序列化项目在卸载后丢弃的回归测试；原实现失败，补充 `onBeforeUnmount(cleanup)` 后通过。最终专用单测为 21 + 9 = 30 项。
- 最终重新构建通过。全量单测 139 项通过、24 项失败；用原 Node 配置排除导出测试时为 109 项通过、同样的 24 项失败。失败名称逐项一致，没有新增失败。基线对照见 `docs/evidence/html-export/unit-baseline-results.json`。
- 独立生产预览确认运行时按需加载、模板无远程图片、导出实际运行、特殊文字完整保留且无脚本注入、连击只下载一次、61 秒后再次下载成功、生成中取消并立即重开无旧下载；桌面与 390px 页面均通过。
- 独立播放器运行的页面错误、控制台错误和外部 HTTP 请求均为空。浏览器自动化仅忽略特定 AudioContext 硬件环境错误，不能据此声明所有项目与扩展均离线兼容。
- 原有未提交代码经哈希核对完整保留，源仓库无修改。

## 5. 正式部署补充

正式站点已更新。部署复验补充修复了导出控件禁用后的焦点保持，以及 Scratch 帮助图标的静态资源路径映射。导出专用单测目前为 31 项，正式 HTTPS 上的当前项目导出、本地 SB3 导出与播放器运行均通过。部署证据、回滚备份及研究目录删除条件见 [部署记录](sb3-html-export-deployment.md)。
