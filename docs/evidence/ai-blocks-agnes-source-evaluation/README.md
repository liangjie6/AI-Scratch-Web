# Model source-pipeline execution evidence

The public evidence copy replaces the deployment hostname with `scratch.example.com`.
Browser records include sanitized compressed logs. Archive manifest hashes and
sizes describe the sanitized decoded records. Captured outcomes, model attribution,
usage counters, and failure history retain their recorded values.

The release DeepSeek acceptance run passed all six independent behavior checks
with no local repair. All six used catalog
`da4c6cc52db7f5471a7fa7fb3ac69a49ea2beb2375b7f8974f898f626a08ce3f`.
The backend binary fingerprint was unchanged throughout the run; its SHA-256
and UTC capture times are recorded in [the environment record](deepseek-release/environment.json).
These are DeepSeek results, not Agnes results.

The final deployed-page check separately used **Agnes**: [browser report](browser-release/report.json),
[actual usage-provider verification](browser-release/postcheck.json), and
[independent final-position/SB3 check](browser-release/vm-result.json).
Generation, commit, movement to `(30,0)` and undo passed with no page errors.
This one page scenario used 6,916 reported tokens across its plan/body calls;
it is outside the six-case DeepSeek totals below and does not establish an
all-six Agnes pass. [Deployment](deployment.json) verifies the served frontend
and tested backend fingerprints; [local checks](validation.json) include all
299 frontend tests. [Browser regression history](browser-history/README.md)
retains application and harness failures, including explicit corrections for
earlier browser reports that mislabeled default `normal` requests as DeepSeek.

| Case | DeepSeek result | Reported total tokens | Repairs | Seconds including VM verification |
| --- | --- | ---: | ---: | ---: |
| Finite movement | PASS | 9,370 | 0 | 17.321 |
| Variable loop | PASS | 13,637 | 0 | 38.852 |
| Held key | PASS | 9,732 | 0 | 19.424 |
| Three-target broadcast | PASS | 21,717 | 0 | 25.041 |
| Modify one script | PASS | 11,782 | 0 | 25.980 |
| Rewrite editable scripts | PASS | 11,249 | 0 | 26.716 |

[The release DeepSeek summary](deepseek-release/summary.json) records 14 budgeted
model calls, 77,487 reported tokens, and 153.334 seconds including independent
verification. Backend request time totals 135.797 seconds. All calls returned
usage. The 17,897 reasoning tokens are a subset of output tokens; the 49,536
cached input tokens are a subset of input tokens. Neither is added again to the
total. Provider transport retries are separate from the source-call journal count.
[The earlier DeepSeek run](deepseek/summary.json), with 75,731 reported tokens,
is retained separately and is not included in this release-run total.

Agnes has **not** passed all six cases. [Its preserved suite history](final/summary.json)
currently contains 15 attempts across implementation revisions and 172,470
reported tokens. Movement, variables, and held-key behavior passed; the latest
modify attempt was rejected by the independent edit-set oracle because the
planner omitted the required old-script reference. The latest broadcast attempt
stopped safely after invalid body-hat and broadcast-handle responses followed
by a contract-revision request. Rewrite previously failed at the provider
boundary with HTTP 429, without a behavior result. The separate investigation
records contain another 40,448 reported tokens. These figures exclude calls
without usage and other standalone diagnostics; they are not a billing total.

Reproduce from the repository root against a locally configured backend:

```sh
python3 scripts/test-ai-blocks-agnes.py --backend http://127.0.0.1:18000 --workers 1
python3 scripts/test-ai-blocks-agnes.py --backend http://127.0.0.1:18000 --model-tier advanced --workers 1 --output-dir /tmp/scratch-deepseek-recheck
```

This explicitly sends real generation requests with `pipeline_version=scratch-source/1`.
`--model-tier normal` selects Agnes; `--model-tier advanced` selects DeepSeek.
Each run records its tier and provider separately, and the driver refuses to mix
different model tiers in one output directory. It does not read provider credentials.
Project cases first request the unified plan, then request each frozen target
through `/api/ai-blocks/generate-source-program`. Independent expected behavior
is supplied to [the real Scratch VM checker](../../../frontend/scripts/verify-ai-program.README.md).

The six cases cover finite movement, numeric variable loops, held-key behavior,
stage/global state plus sprite broadcast coordination, modifying one authorized
script, and rewriting editable scripts while retaining a protected script.
Case directories contain requests, raw/parsed SSE, normalized ProgramIR inputs,
VM results, and aggregate token/repair/latency reports.
The release model run requested SB3 export/new-VM reload checks for the first
four cases. Modify/rewrite use execution and strict existing-block preservation
checks; their release model results do not claim an SB3 roundtrip.

[The Agnes suite summary](final/summary.json) includes failed cases. A rerun
creates a new attempt directory and preserves the previous summary; it does not
erase failures or silently convert skipped checks to PASS. Calls without usage
are explicitly recorded as missing usage, rather than assumed free. Cached and
reasoning tokens are reported separately; total tokens are taken from the
provider and are not reconstructed by adding overlapping counters.

[Investigation records](investigation/) preserve the original legacy protocol
failure, a source-pipeline failure where a short slot purpose lost the finite
repeat count, its corrected movement retry, and the initial variable/keyboard
attempts. These were produced during implementation and are separate from the
recorded suite attempts across implementation revisions. The legacy baseline did not complete and returned
no usage, so this evidence cannot establish a fair token-reduction percentage.

Motion and variable expectations are exact. Held-key tests compare no-key
stationary behavior with repeated movement while the key remains held; they do
not equate a VM tick with an iteration. The checker uses actual VM keyboard IO,
actual compiled blocks and the production graph writer. It runs without a
renderer and makes no collision, visual quality, audio, or extension claim.
One sample per case is operational evidence, not a statistical success-rate
estimate.

Raw SSE and parsed event logs are losslessly archived as `.gz` to avoid storing repeated character-level reasoning frames uncompressed. [The archive manifest](event-archives.json) records original SHA-256 and sizes. Read an event log with `gzip -dc path/to/unified-events.json.gz`; requests, VM inputs/results, and summaries remain ordinary JSON. No failed attempts were discarded.
