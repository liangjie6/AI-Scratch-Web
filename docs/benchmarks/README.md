# AI 写积木校准题库

`ai-blocks-calibration-v1.json` 是第一批固定 50 题语料（文件名中的 v1 是题集标识，内部合同已升级到 schema 2）。它包含题目文本，仅用于受控的离线评测输入；评测结果文件不得复制题目、对话、模型原文、ProgramIR、资源名称或 VM ID。

结果文件使用以下摘要结构：

```json
{
  "schema_version": 2,
  "runs": [
    {
      "case_id": "basic_move",
      "contract_valid": true,
      "program_valid": true,
      "program_first_pass": true,
      "end_to_end_completed": true,
      "task_field_total": 3,
      "task_field_matched": 3,
      "criterion_outcomes": {
        "executable": {"total": 1, "must_total": 1, "passed": 1, "must_passed": 1},
        "structural": {"total": 0, "must_total": 0, "passed": 0, "must_passed": 0},
        "manual": {"total": 0, "must_total": 0, "approved": 0, "must_approved": 0, "reviewed": 0, "must_reviewed": 0},
        "unsupported": {"total": 0, "must_total": 0, "accepted": 0, "must_accepted": 0}
      },
      "asset_match": "not_applicable",
      "repair_count": 0,
      "latency_ms": 1200,
      "token_count": 300,
      "cost_usd": 0.002
    }
  ]
}
```

`asset_match` 只能是 `not_applicable`、`top1`、`candidate_recall`、`review_required` 或 `missing`。每个 case ID 在一个结果文件中只能出现一次。

使用 `backend/tools/evaluate_ai_blocks_benchmark.py` 聚合结果。它要求每个题目的四类 criterion 分桶（`executable`、`structural`、`manual`、`unsupported`）总数与题库严格一致，并输出题意字段召回率、ProgramIR 首过率、完成率、四类 criterion 通过/批准/接受率、`all_must_criterion_completion_rate`、素材匹配分布、修复次数、延迟、token 和成本的 P50/P95 或总计。只有完整题库的端到端完成率和所有 must criterion 均达到 90% 才标记通过。
