# Real Scratch verification tools

Run from the repository root, using the installed frontend dependencies:

```sh
node frontend/scripts/verify-ai-program.mjs --input case.json --output result.json
node frontend/scripts/probe-verification-browser.mjs
```

The first command compiles a generated ProgramIR with the production compiler,
installs it through the production Scratch graph writer, and executes the real
Scratch VM in a disposable Node worker. Expectations must be supplied separately
from the generated code. It does not contact a model or modify the editor.

Minimal input, with a complete ProgramIR in `program`:

```json
{
  "name": "Mover",
  "program": {
    "schema_version": 1,
    "summary": "Move ten steps",
    "remove_script_ids": [],
    "variables": [],
    "lists": [],
    "broadcasts": [],
    "scripts": [{
      "id": "script_move", "purpose": "Move", "x": 0, "y": 0,
      "blocks": [
        {"opcode": "event_whenflagclicked"},
        {"opcode": "motion_movesteps", "inputs": {"STEPS": {"kind": "number", "value": 10}}}
      ]
    }]
  },
  "roundTrip": true,
  "scenarios": [{"name": "movement", "trigger": {"kind": "green_flag"}, "maxTicks": 10, "expect": {"x": 10, "y": 0}}]
}
```

Each scenario starts with fresh real VM instances. Use `programs` instead of
`program` to install several targets before triggering them together:
`[{"name":"Stage","targetKind":"stage","program":{...}},
{"name":"Receiver","program":{...}}]`.
Use `expect.targets.Receiver.x` and `expect.targets.Receiver.variables.score`
for per-target assertions. The first entry remains the primary editing target.

`fixture.states` accepts `{ref_id, kind, name, scope, value, owner?}`. Kinds are
`variable`, `list`, and `broadcast`; global states are installed on the actual
stage. Local states can specify a target name in `owner`. A supplied Manifest
also installs its shared/local state and runs production preflight contracts.
New sprite/resource operations are not executed by this tool.
Independent `fixture.variables`/`lists`/`broadcasts` are installed first; a later
logical binding or generated declaration cannot reset an existing value.

`fixture.program` installs existing primary-target scripts. `mode` may be
`append`, `modify`, or `rewrite`. In modify mode `remove_script_ids` references
the fixture ProgramIR script IDs. `fixture.protectedScripts` accepts
`[{"id":"protected","opcode":"unsupported_fixture_hat"}]` and verifies that
the production policy treats them as protected. Every unaffected block is
compared exactly after applying the generated program. This checks the real
graph writer and preservation policy, independently of production transactions
and snapshot alias resolution, which have their own unit tests.
Supply independent `fixture.expectedPreservedScriptIds` and
`fixture.expectedRemovedScriptIds` arrays to check the authorized edit set.
Removing and recreating a preserved script fails even if its behavior matches.

Scenario triggers support `green_flag`, `key` (`key` or `value`), `broadcast`
(`name` or `value`), and `sprite_click`. Alternatively, supply
`events:[{"tick":0,"kind":"key","key":"ArrowRight","isDown":true},
{"tick":3,"kind":"key","key":"ArrowRight","isDown":false}]`.
Keyboard events use actual VM keyboard IO so both hats and sensing reporters
observe them. Expected keys use `keys:{"right arrow":false}`; variables and
lists use `variables:{"score":3}` and `lists:{"items":["a"]}`.

The optional round trip loads a candidate SB3 in a new real VM, exports it, then
loads it in another new VM. It compares normalized graphs, state, and costume
references. Fixtures use a tiny real sanitized SVG asset; there is no renderer.
Protected extension fixtures skip this round trip because that extension has
not been installed. The browser probe separately bundles the package's actual
browser exports and performs a tiny SB3 load/export/load in Chromium.

Results are `PASS`, `FAIL`, `SKIPPED`, or `TIMEOUT`; failures and timeouts exit 1.
No renderer is attached: collision expectations and renderer-dependent programs
are marked `SKIPPED`. A skipped behavior or requested round trip cannot produce
overall PASS. Core Scratch is supported;
extension, audio, resource preparation, full visual behavior, and production
transaction correctness are not claimed. A finite sample does not prove that
all possible game states are correct.

The worker has a wall-clock deadline (default 15 seconds), a heap limit, an
execution-count budget, and a tick budget. `stepDelayMs` optionally advances real
wall-clock time between ticks; zero does not simulate timers or timed glides.
Forbid implicit claims of deterministic time or rendering from these checks.
