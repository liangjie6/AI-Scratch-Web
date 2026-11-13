#!/usr/bin/env python3
"""Agnes or DeepSeek through the backend, with independent real Scratch VM oracles.

No provider credentials are read. Paid calls are explicit when this script runs.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import copy
import datetime
import hashlib
import json
import pathlib
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
PIPELINE = "scratch-source/1"
PROVIDERS = {"normal": "Agnes", "advanced": "DeepSeek"}


def save(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def sse(backend, endpoint, request, directory, label, timeout):
    save(directory / f"{label}-request.json", request)
    started = time.monotonic()
    payload = json.dumps(request, ensure_ascii=False).encode()
    req = urllib.request.Request(backend.rstrip("/") + endpoint, data=payload,
                                 headers={"Content-Type": "application/json", "Accept": "text/event-stream"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            status = response.status
            content_type = response.headers.get("Content-Type")
            raw = response.read().decode()
    except urllib.error.HTTPError as error:
        body = error.read().decode()
        (directory / f"{label}-http-error.txt").write_text(body)
        save(directory / f"{label}-transport.json", {"http_status": error.code, "duration_seconds": round(time.monotonic() - started, 3)})
        raise RuntimeError(f"HTTP {error.code}: {body[:800]}") from error
    elapsed = time.monotonic() - started
    (directory / f"{label}-events.sse").write_text(raw)
    events = []
    for frame in raw.replace("\r\n", "\n").split("\n\n"):
        kind, data = "message", []
        for line in frame.splitlines():
            if line.startswith("event:"):
                kind = line[6:].strip()
            elif line.startswith("data:"):
                data.append(line[5:].strip())
        if data:
            events.append({"type": kind, "payload": json.loads("\n".join(data))})
    save(directory / f"{label}-events.json", events)
    save(directory / f"{label}-transport.json", {"http_status": status, "content_type": content_type,
                                                 "duration_seconds": round(elapsed, 3), "event_count": len(events)})
    return events, elapsed


def require_complete(events):
    terminal = next((event for event in reversed(events) if event["type"] in ("complete", "error")), None)
    if not terminal or terminal["type"] != "complete":
        raise RuntimeError("generation did not complete: " + json.dumps(terminal, ensure_ascii=False))


def program(scripts, **rest):
    return {"schema_version": 1, "summary": "independent preservation fixture", "remove_script_ids": [],
            "variables": [], "lists": [], "broadcasts": [], "scripts": scripts, **rest}


def script(identifier, blocks):
    return {"id": identifier, "purpose": identifier, "x": 0, "y": 0, "blocks": blocks}


def flag():
    return {"opcode": "event_whenflagclicked"}


def number(value):
    return {"kind": "number", "value": value}


def normalized_block(block):
    fields = copy.deepcopy(block.get("fields", {}))
    inputs = {}
    for name, value in block.get("inputs", {}).items():
        if value["kind"] == "block":
            inputs[name] = normalized_block(value["block"])
        else:
            kind = value["kind"]
            inputs[name] = {"opcode": "math_number" if kind == "number" else "text",
                            "fields": {"NUM" if kind == "number" else "TEXT": value["value"]}, "inputs": {}, "next": None}
    for name, body in block.get("substacks", {}).items():
        inputs[name] = normalized_stack(body)
    return {"opcode": block["opcode"], "fields": fields, "inputs": inputs, "next": None}


def normalized_stack(blocks):
    result = None
    for block in reversed(blocks):
        item = normalized_block(block)
        item["next"] = result
        result = item
    return result


def context(name="Probe", mode="append", stage=False, current=None):
    return {"name": name, "is_stage": stage, "has_scripts": bool(current and current.get("scripts")), "mode": mode,
            "costumes": ["backdrop1" if stage else "costume1"], "backdrops": ["backdrop1"], "sounds": [],
            "sprite_names": ["Probe"], "current_program": json.dumps(current or {"scripts": [], "local_variables": [], "global_variables": []}, ensure_ascii=False)}


def build_case(name, model_tier="normal"):
    request = {"pipeline_version": PIPELINE, "model_tier": model_tier, "session_id": "source-eval-" + model_tier + "-" + name,
               "request_id": str(uuid.uuid4()), "thinking": {"enabled": False, "strength": "low"}, "scope": "single_sprite",
               "asset_policy": "existing_only", "proceed_with_defaults": True, "permitted_extensions": [], "target": context()}
    fixture = {}
    if name == "motion":
        message = "点击绿旗后移动到x=0、y=0，然后重复3次，每次将x增加10。必须是有限重复3次，不能永远循环。只修改当前角色，不添加资源。"
        scenarios = [{"name": "exactly three moves", "maxTicks": 12, "expect": {"x": 30, "y": 0}}]
    elif name == "variables":
        message = "复用现有全局变量counter。点击绿旗后把counter设为0，再重复5次，每次counter增加2，最后counter必须为10。不移动角色、不添加资源，不使用永远循环。"
        snapshot = {"scripts": [], "local_variables": [], "global_variables": [{"id": "existing_counter", "name": "counter", "type": "", "value": 42}]}
        request["target"] = context(current=snapshot)
        fixture["variables"] = [{"name": "counter", "scope": "global", "value": 42}]
        scenarios = [{"name": "five counter increments", "maxTicks": 15, "expect": {"variables": {"counter": 10}, "x": 0}}]
    elif name == "keyboard":
        message = "点击绿旗时将x设为0，然后在同一个绿旗脚本里永远循环：如果右方向键被持续按住，就将x增加2；未按住时不移动。使用按键是否按下侦测积木，不要仅写一次按键事件。不添加资源。"
        scenarios = [
            {"name": "no key means no motion", "maxTicks": 5, "expect": {"x": 0}},
            {"name": "held key produces repeated movement", "maxTicks": 8, "events": [{"tick": 0, "kind": "green_flag"}, {"tick": 1, "kind": "key", "key": "ArrowRight", "isDown": True}], "expect": {"x": {"min": 10, "max": 10000000}, "keys": {"right arrow": True}}},
        ]
    elif name == "broadcast":
        message = "只使用现有舞台Stage和现有角色Sender、Receiver。绿旗时舞台把全局变量rounds设为0。Sender绿旗时广播go一次。Receiver收到go后，先移到x=0、y=0，再重复3次，每次x增加5并且rounds增加1。最后Receiver x=15，rounds=3。不要新建角色或素材，广播go在Sender发送并在Receiver接收。"
        request["scope"] = "project"
        request["target"] = context("Stage", stage=True)
        request["target"]["sprite_names"] = ["Sender", "Receiver"]
        targets = []
        for target_name, stage in [("Stage", True), ("Sender", False), ("Receiver", False)]:
            item = context(target_name, stage=stage)
            item["sprite_names"] = ["Sender", "Receiver"]
            targets.append({**item, "target_id": target_name.lower(), "revision": 1})
        request["project_context"] = {"project_id": "isolated-agnes-broadcast-fixture", "version": 1, "targets": targets, "shared_state": {"variables": [], "lists": [], "broadcasts": []}}
        scenarios = [{"name": "sender broadcast drives receiver", "maxTicks": 20, "expect": {"targets": {"Receiver": {"x": 15, "y": 0, "variables": {"rounds": 3}}}}}]
    elif name in ("modify", "rewrite"):
        old = script("script_1", [flag(), {"opcode": "motion_changexby", "inputs": {"DX": number(10)}}])
        spare = script("script_3", [flag(), {"opcode": "motion_changeyby", "inputs": {"DY": number(3)}}])
        snapshot = {"scripts": [
            {"id": "script_1", "protected": False, "comment": "旧x移动脚本", "code": normalized_stack(old["blocks"])},
            {"id": "script_2", "protected": True, "comment": "禁止改动的扩展脚本", "code": {"opcode": "unsupported_fixture_hat", "fields": {}, "inputs": {}, "next": None}},
            {"id": "script_3", "protected": False, "comment": "保留的y移动脚本", "code": normalized_stack(spare["blocks"])},
        ], "local_variables": [], "global_variables": [{"id": "untouched", "name": "untouched", "type": "", "value": 7}]}
        request["target"] = context(mode=name, current=snapshot)
        fixture = {"program": program([old, spare]), "protectedScripts": [{"id": "script_2", "opcode": "unsupported_fixture_hat"}], "variables": [{"name": "untouched", "scope": "global", "value": 7}],
                   "expectedPreservedScriptIds": ["script_2", "script_3"] if name == "modify" else ["script_2"],
                   "expectedRemovedScriptIds": ["script_1"] if name == "modify" else ["script_1", "script_3"]}
        if name == "modify":
            message = "只替换script_1旧x移动脚本：绿旗时x增加20，替换后不能再执行旧的增加10。script_2受保护，script_3必须完整保留，untouched变量保持7，不要新资源。"
        else:
            message = "重写当前角色所有可编辑脚本，只保留一个新脚本：绿旗时x增加20。原script_1和script_3都应移除；受保护script_2必须完整保留，untouched变量保持7，不要新资源。"
        scenarios = [{"name": name + " preserves unrelated code/state", "maxTicks": 10, "expect": {"x": 20, "y": 3 if name == "modify" else 0, "variables": {"untouched": 7}}}]
    else:
        raise ValueError("unknown case: " + name)
    request["messages"] = [{"role": "user", "content": message}]
    return request, fixture, scenarios


def run_case(name, options):
    directory = options.output_dir / name
    if directory.exists() and any(directory.iterdir()):
        directory = options.output_dir / (name + "-attempt-" + uuid.uuid4().hex[:8])
    directory.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    all_events, latencies = [], []
    report = {"case": name, "model_tier": options.model_tier, "provider": PROVIDERS[options.model_tier], "pipeline_version": PIPELINE,
              "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "backend": options.backend,
              "artifact_directory": str(directory.relative_to(ROOT)) if directory.is_relative_to(ROOT) else str(directory)}
    try:
        request, fixture, scenarios = build_case(name, options.model_tier)
        report.update(request_id=request["request_id"], request_log_ref=hashlib.sha256(request["request_id"].encode()).hexdigest()[:16])
        events, elapsed = sse(options.backend, "/api/ai-blocks/generate-unified", request, directory, "unified", options.timeout)
        all_events += events
        latencies.append(elapsed)
        require_complete(events)
        plan = next(event["payload"]["data"] for event in events if event["type"] == "phase_complete" and event["payload"].get("phase") == "plan")
        frozen = plan["source_contract"]
        report["catalog_hash"] = frozen["catalog_hash"]
        names = {target["handle"]: target["name"] for target in frozen["targets"]}
        # The VM input materializes the frozen logical state declarations, not
        # any invented names from generated block JSON.
        fixture["states"] = [{"ref_id": item["ref_id"], "kind": item["kind"], "name": item["name"], "scope": item["scope"], "value": item["value"], **({"owner": names[item["owner"]]} if item.get("owner") else {})} for item in frozen["states"]]
        generated = []
        if request["scope"] == "project":
            active_owners = {slot["owner"] for slot in frozen["script_slots"]}
            targets = [target for target in frozen["targets"] if target["handle"] in active_owners]
            for index, target in enumerate(targets):
                actual = copy.deepcopy(target["context"])
                actual["mode"] = target["mode"]
                body = {"request": request, "source_contract": frozen, "target_index": index, "target": actual}
                part, duration = sse(options.backend, "/api/ai-blocks/generate-source-program", body, directory, f"target-{index}", options.timeout)
                all_events += part
                latencies.append(duration)
                require_complete(part)
                completed = next(event["payload"]["data"] for event in reversed(part) if event["type"] == "complete")
                generated.append({"name": target["name"], "targetKind": target["kind"], "program": completed["program"]})
        else:
            completed = next(event["payload"]["data"] for event in reversed(events) if event["type"] == "complete")
            generated = [{"name": request["target"]["name"], "targetKind": "stage" if request["target"]["is_stage"] else "sprite", "program": completed["program"]}]
        vm_input = {"programs": generated, "mode": request["target"]["mode"], "fixture": fixture, "scenarios": scenarios,
                    "roundTrip": name not in ("modify", "rewrite"), "maxPrimitiveCalls": 10000000, "timeoutMs": 30000}
        if request["scope"] == "project":
            vm_input["manifest"] = plan["manifest"]
        save(directory / "vm-input.json", vm_input)
        completed_vm = subprocess.run(["node", str(ROOT / "frontend/scripts/verify-ai-program.mjs"), "--input", str(directory / "vm-input.json"), "--output", str(directory / "vm-result.json")], cwd=ROOT, capture_output=True, text=True, timeout=45)
        (directory / "vm-stderr.txt").write_text(completed_vm.stderr)
        vm_result = json.loads(completed_vm.stdout)
        report.update(status=vm_result["status"], behavior=vm_result, source_contract_hash=frozen["hash"])
        if vm_result["status"] != "PASS":
            problems = [vm_result["error"]] if vm_result.get("error") else []
            for scenario in vm_result.get("scenarios", []):
                if scenario.get("error"):
                    problems.append(scenario["error"])
                problems += [f"{item['name']}: actual={item.get('actual')} expected={item.get('expected')}" for item in scenario.get("assertions", []) if item["status"] == "FAIL"]
            report["error"] = "; ".join(problems) or "independent VM verification returned " + vm_result["status"]
        if name == "motion":
            def opcodes(blocks):
                for block in blocks:
                    yield block["opcode"]
                    for substack in block.get("substacks", {}).values():
                        yield from opcodes(substack)
            if any("control_forever" in list(opcodes(script["blocks"])) for item in generated for script in item["program"]["scripts"]):
                report.update(status="FAIL", error="finite three-iteration request generated control_forever")
    except Exception as error:
        report.update(status="FAIL", error=str(error))
    real_usage = [event["payload"]["data"] for event in all_events if event["type"] == "usage" and not event["payload"].get("data", {}).get("budget_only")]
    token_totals = {}
    for usage in real_usage:
        for key, value in usage.get("usage", {}).items():
            if isinstance(value, int):
                token_totals[key] = token_totals.get(key, 0) + value
    started_calls = sum(event["type"] == "usage" and bool(event["payload"].get("data", {}).get("source_call_started")) for event in all_events)
    report.update(duration_seconds=round(time.monotonic() - started, 3), backend_request_seconds=[round(value, 3) for value in latencies],
                  tokens=token_totals, usage=real_usage,
                  observed_providers=sorted({usage["provider"] for usage in real_usage if usage.get("provider")}),
                  model_calls=started_calls, missing_usage_events=max(0, started_calls - len(real_usage)),
                  repairs=sum(event["type"] == "repair" for event in all_events))
    save(directory / "report.json", report)
    print(json.dumps({key: report.get(key) for key in ("case", "status", "duration_seconds", "tokens", "repairs", "error")}, ensure_ascii=False), flush=True)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--backend", default="http://127.0.0.1:18000")
    parser.add_argument("--model-tier", choices=tuple(PROVIDERS), default="normal", help="normal=Agnes, advanced=DeepSeek; use separate output directories")
    parser.add_argument("--output-dir", type=pathlib.Path, default=ROOT / "docs/evidence/ai-blocks-agnes-source-evaluation")
    parser.add_argument("--cases", default="motion,variables,keyboard,broadcast,modify,rewrite")
    parser.add_argument("--workers", type=int, choices=(1, 2), default=1)
    parser.add_argument("--timeout", type=int, default=240)
    parser.add_argument("--summarize-only", action="store_true", help="aggregate saved attempts without any network/model calls")
    options = parser.parse_args()
    options.output_dir = options.output_dir.resolve()
    options.output_dir.mkdir(parents=True, exist_ok=True)
    for previous in options.output_dir.glob("*/report.json"):
        if json.loads(previous.read_text()).get("model_tier", "normal") != options.model_tier:
            parser.error("output directory contains another model tier; use a separate --output-dir to preserve attribution")
    previous_summary = options.output_dir / "summary.json"
    if previous_summary.exists():
        (options.output_dir / ("summary-before-" + uuid.uuid4().hex[:8] + ".json")).write_bytes(previous_summary.read_bytes())
    reports = []
    if not options.summarize_only:
        with concurrent.futures.ThreadPoolExecutor(max_workers=options.workers) as pool:
            reports = list(pool.map(lambda name: run_case(name, options), options.cases.split(",")))
    attempts = [json.loads(item.read_text()) for item in sorted(options.output_dir.glob("*/report.json"), key=lambda item: item.stat().st_mtime_ns)]
    latest = {item["case"]: item for item in attempts}
    ordered = [latest[name] for name in ("motion", "variables", "keyboard", "broadcast", "modify", "rewrite") if name in latest]
    reported_tokens = {}
    for attempt in attempts:
        for key, value in attempt.get("tokens", {}).items():
            reported_tokens[key] = reported_tokens.get(key, 0) + value
    summary = {"pipeline_version": PIPELINE, "model_tier": options.model_tier, "provider": PROVIDERS[options.model_tier], "cases": ordered, "this_invocation": reports,
               "passed": sum(item["status"] == "PASS" for item in ordered), "total": len(ordered),
               "attempt_count": len(attempts), "reported_tokens_across_all_attempts": reported_tokens,
               "attempts": [{key: item.get(key) for key in ("case", "status", "artifact_directory", "duration_seconds", "tokens", "model_calls", "missing_usage_events", "repairs", "error")} for item in attempts],
               "limitations": ["One sample per case, not a statistical quality estimate", "No renderer: no collision or visual correctness claim", "Legacy baseline did not complete, so no fair token reduction percentage", "Failed calls without usage do not imply zero billing", "Source pipeline currently uses provider JSON transport or validated plain JSON, not an asserted strict-schema guarantee"]}
    save(options.output_dir / "summary.json", summary)
    raise SystemExit(0 if all(item["status"] == "PASS" for item in reports) else 1)


if __name__ == "__main__":
    main()
