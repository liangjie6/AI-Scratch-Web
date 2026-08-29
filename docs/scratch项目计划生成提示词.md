# 已移除：旧 ProjectPlan 生成提示词

本文件对应的一次性 ProjectPlan（在 block_programs 中嵌入多个完整 ProgramIR）已移除，不再被后端加载。

当前项目模式使用以下分阶段协议：

1. docs/scratch项目清单生成提示词.md：只生成资源操作和 program_targets，不包含程序正文。
2. docs/scratch代码生成提示词.md：每次只为一个 target 生成一个 ProgramIR。
3. 前端逐 target 校验、编译并写入 Scratch VM，最后由 VM 生成 project.json。

不要恢复 block_programs、project_ready 或把多个 ProgramIR 拼接到 project.json。
