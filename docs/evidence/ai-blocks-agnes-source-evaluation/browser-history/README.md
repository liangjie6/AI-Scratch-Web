# Browser regression history

These UI attempts are separate from the six-case release model suite and its token totals. Their original report labels incorrectly say DeepSeek: the driver looked for a select, while the UI uses radio inputs. All actual captured requests used `model_tier: normal` (Agnes). [The attribution corrections](model-attribution-corrections.json) record the actual tier without overwriting the original failed reports. The six-case API-driven DeepSeek release suite correctly used `advanced` and is unaffected.

The first three preserved failures all reached a generated/committed/running script. Undo then failed with `请等待 Scratch 编辑器加载完成`. The instrumented [Vite trace](browser-trace/report.json) records the actual synchronous exception: Scratch VM project deserialization clears all targets and emits `targetsUpdate`; the panel read a snapshot while `editingTarget` was empty, interrupting the reload. The panel emitted parent busy events during undo but did not set its own busy state.

The fixes track undo in the panel's busy state, ignore the valid empty selection during project reload, and select the original logical target after SB3 reload allocates fresh IDs. Component/bridge event regression tests and real VM reload tests cover both causes.

Public HTTP request/response records replace the deployment hostname with `scratch.example.com` before compression into `generation.json.gz`. [archives.json](archives.json) records SHA-256 and size of each decoded public record. The final deployed-browser result is stored separately in `../browser-release/` when complete. Captured failure outcomes remain recorded.

The later [request-count assertion run](request-count-assertion/report.json) successfully generated, ran and undid the project with the fixed deployed bundle. Its harness then failed an incorrect single-stream assertion: project scope correctly sends one planning request plus one target request. This harness-only failure is preserved separately; the corrected driver checks the scope-specific request count and captures both response streams.

The [model-tier assertion run](model-tier-assertion/report.json) also passed commit/run/undo but then correctly rejected `normal` versus its requested `advanced`. The driver now explicitly clicks and verifies the real radio before sending. The final browser acceptance intentionally selects `normal` and is attributed to Agnes.
