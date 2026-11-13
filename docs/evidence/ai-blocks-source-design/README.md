# 源码驱动流水线设计：可复现实验

对应 [设计草案](../../实施文档/基于Scratch源码的AI写积木流水线设计.md)。这些脚本不调用模型、不修改应用／依赖、不连接部署；仅加载当前依赖，构造隔离 fixtures，输出 JSON。不是生产实现，也不代表已完成全部目录或新协议。

从仓库根目录运行，使用现有 frontend/node_modules：

```sh
node docs/evidence/ai-blocks-source-design/scratch-catalog-probe.mjs
node docs/evidence/ai-blocks-source-design/scratch-vm-semantics-probe.cjs
node docs/evidence/ai-blocks-source-design/verify-draft.mjs
```

| 实验 | 保存的结果 | 证明范围 |
| --- | --- | --- |
| ScratchBlocks＋GUI＋Runtime 探测 | [catalog-probe-result.json](catalog-probe-result.json) | 当前版本的部分形状、菜单、shadow 和扩展目录来源可实际提取 |
| 无 renderer 的 VM 源码执行 | [vm-probe-result.json](vm-probe-result.json) | 绿旗／repeat／move 小样例真实执行；碰撞显式 SKIPPED |
| [紧凑脚本 JSON](draft-example.json) 转换 | [draft-probe-result.json](draft-probe-result.json) | 样例可规范化为既有 ProgramIR，编译并通过图校验；错误样例被拒绝 |

catalog 探测仅覆盖选定的积木和 fixtures，不是全部 171 类型的完整生成器。原始菜单结果可能含 fixture 初始化产生的随机变量 ID；最终能力目录必须清除这些值并记录 provider。GUI 工具箱摘录使用最小 fixture，不能据此声称已经完成整个工具箱导入。

VM 实验直接使用包内 Runtime、Blocks、Sprite 源码，不依赖当前存在 stylesheet 路径问题的公共 Node bundle。它没有通过 loadProject 载入完整 SB3、没有渲染器、没有验证浏览器 Worker，也没有验证图像碰撞／音频／时间相关玩法。

紧凑脚本实验只实现数字／字符串、嵌套 reporter 与子栈的演示适配。状态、资源、广播、过程、严格供应商 schema、后端 Go normalizer、事务及持久化均未实现。输入规模限制和端口／图验证仍由现有编译器执行；演示适配器不能直接作为生产代码。

实验额外记录了现有合同差异：前端 `validateProgramIR` 会接受 cap 后仍有积木的序列，后端 Go 的 `program_contracts.go` 则显式拒绝。演示 normalizer 自己检查 body 中的帽子和 cap 顺序，不能把这项能力归给未修改的生产前端校验器。

输出的 UTF-8 字节数只说明表示长度差异，不是 Token、收费或真实模型收益。结果记录当前源文件 hash 以便追溯，不是新能力目录的正式 catalog hash。
