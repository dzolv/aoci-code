#!/usr/bin/env python3
"""Opt-in task-context lifecycle against real stdio processes and a disposable
Git fixture. Reuses the scenario suite's public client/fixture helpers. No
Overview call or private Go API participates. Fixed fixture Entries are test
data; this script never authors cognition for the host repository.
"""
import argparse
import collections
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import tempfile


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def run(scenes, binary, output):
    scenes.BIN = str(binary)
    root = scenes.make_fixture("task-context", 4)
    # Long comments make source cost govern the batch while keeping the fixed
    # fixture's line-count bands small. These are deliberate test bytes.
    for number in range(1, 5):
        path = Path(root, "pkg", f"f{number:03}.go")
        path.write_text(f"package pkg\n// {'x' * 12000}\nfunc F{number:03}() int {{ return {number} }}\n", encoding="utf-8")
    config_path = Path(root, ".aoci", "config.json")
    config_before = config_path.read_bytes()
    calls = collections.Counter()
    pids, exits, batches, phases = [], [], [], []
    task_id = "stdio-task-context"

    class TaskSession(scenes.Session):
        def __init__(self):
            super().__init__(root, mcp_args=["--task-context"])
            pids.append(self.p.pid)

        def call(self, tool, args=None, timeout=180):
            calls[tool] += 1
            return super().call(tool, args, timeout)

    def invoke(session, tool, arguments):
        raw, failed = scenes.text_of(session.call(tool, arguments))
        require(not failed, f"{tool} rejected: {raw[:500]}")
        return json.loads(raw)

    def task(session, action, **kwargs):
        return invoke(session, "aoci_get_entries", {"task": {"action": action, "task_id": task_id, **kwargs}})

    def read_all(session, kind, path=None):
        cursor, texts, evidence = "", [], []
        for _ in range(100):
            identity = {"refs": [path]} if kind == "entry" else ({"path": path} if path else {})
            page = task(session, "read", kind=kind, **identity, cursor=cursor)
            require(page.get("status") == "ok" and page.get("items"), f"bounded {kind} read stopped: {page}")
            for item in page["items"]:
                texts.append(item.get("text", ""))
                if item.get("evidence"):
                    evidence.append(item["evidence"])
            cursor = page.get("next_cursor", "")
            if not cursor:
                return "".join(texts), evidence
        raise AssertionError("bounded reader did not finish")

    def maintain(session):
        result = invoke(session, "aoci_maintain", {"scope": "code"})
        if result.get("status") == "output_deferred":
            retained, _ = read_all(session, "result", result["result_ref"])
            envelope = json.loads(retained)
            require(not envelope.get("isError"), "retained Maintain was an error")
            result = json.loads("\n".join(item.get("text", "") for item in envelope["content"]))
        require(not result.get("stop"), f"Maintain explicitly stopped: {result.get('stop')}")
        return result

    def apply(session, result):
        candidates = result.get("candidates", [])
        plan = result.get("code_plan") or {}
        require(candidates and len(candidates) == plan.get("included"), "complete issued batch missing")
        require(result.get("task_reading"), "task-specific source planning absent")
        entries = []
        for candidate in candidates:
            entry = scenes.entry_line(candidate["path"])
            if Path(candidate["path"]).name == ".gitattributes":
                entry = ".gitattributes[CG5T]: F:Defines fixture line ending rules | R:- | A:- | S:-"
            bands = candidate.get("expected_e") or ["T"]
            entry = re.sub(r"\[CG5T\]", f"[CG5{bands[0]}]", entry)
            entries.append({"path": candidate["path"], "source_sha256": candidate["source_sha256"],
                            "candidate_id": candidate["candidate_id"], "new_entry": entry})
        applied = invoke(session, "aoci_update_entry", {"code_batch_id": plan["batch_id"], "entries": entries})
        require(applied.get("status") == "applied" and applied.get("applied") == len(entries),
                f"complete batch did not apply: {str(applied)[:1000]}")
        batches.append({"batch_id": plan["batch_id"], "total_targets": plan["total_targets"], "included": len(entries), "remaining": plan["remaining"],
                        "source_bytes": result["task_reading"]["selected_source_bytes"],
                        "paths": [candidate["path"] for candidate in candidates]})
        return candidates

    first = TaskSession()
    try:
        started = task(first, "start", goal="Establish fixed fixture cognition through bounded source reading", event_id="host-event-1")
        require(started["state"]["budget_bytes"] == 96 * 1024, "default task budget changed")
        source, source_evidence = read_all(first, "source", "pkg/f001.go")
        meta, meta_evidence = read_all(first, "meta")
        require("func F001() int { return 1 }" in source and "#AOCI-META-VOLUME" in meta, "real fixture evidence missing")
        mapped = task(first, "map", map_text="Observed pkg/f001.go provides fixture constant 1. Meta supplies the code tag dictionary. Other sources and all relationships remain unknown until read.", map_evidence=source_evidence + meta_evidence)
        require(mapped["state"]["map_valid"], "delivered evidence did not bind the task map")
        initial = maintain(first)
        require(initial["code_plan"]["remaining"] > 0, "source budget failed to split the fixture into multiple batches")
        written = apply(first, initial)
        checkpoint = task(first, "checkpoint", note="First complete machine batch applied; remaining candidates need authoring.", pending=["maintain and apply remaining fixture candidates"])
        phases.append({"event": "host-event-1", "progress": checkpoint["state"]["progress"], "map_valid": checkpoint["state"]["map_valid"], "used_bytes": checkpoint["state"]["used_bytes"]})
    finally:
        first.close()
        exits.append(first.p.poll())
    require(exits[0] == 0, "first stdio process did not close cleanly")

    second = TaskSession()
    try:
        resumed = task(second, "resume", event_id="host-event-2")
        require(resumed["state"]["progress"] >= phases[0]["progress"] and resumed["state"]["map_valid"],
                "new process lost successful Apply progress or evidence-bound map")
        require(resumed["state"]["pending_count"] == 1, "checkpoint pending work did not survive")
        entry, _ = read_all(second, "entry", written[0]["object_ref"])
        require(" | R:" in entry and " | A:" in entry, "bounded formal Entry read failed")
        for _ in range(8):
            following = maintain(second)
            if not following.get("candidates"):
                require(following.get("aligned"), f"empty batch hid incomplete work: {following}")
                break
            apply(second, following)
        else:
            raise AssertionError("fixture authoring did not finish")
        final = task(second, "checkpoint", note="All fixture machine batches applied; final governance verified separately.", pending=[])
        phases.append({"event": "host-event-2", "progress": final["state"]["progress"], "map_valid": final["state"]["map_valid"], "used_bytes": final["state"]["used_bytes"]})
    finally:
        second.close()
        exits.append(second.p.poll())

    aligned, verify = scenes.fixture_aligned(root)
    require(aligned, f"public CLI verify did not report governance alignment: {verify}")
    require(calls["aoci_overview"] == 0 and len(set(pids)) == 2, "experiment did not use two independent no-Overview processes")
    require(exits == [0, 0], "stdio processes did not close cleanly")
    authored = [path for batch in batches for path in batch["paths"]]
    require(len(authored) == len(set(authored)) == batches[0]["total_targets"], "candidate work was dropped or replayed")
    require(config_path.read_bytes() == config_before, "task planning altered team configuration")
    report = {"status": "passed", "binary": str(binary), "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "fixture": root, "fixture_removed_after_run": True, "process_ids": pids, "process_exit_codes": exits,
              "tool_calls": dict(sorted(calls.items())), "whole_overview_calls": calls["aoci_overview"],
              "batches": batches, "phases": phases, "final_governance_aligned": aligned,
              "limits": ["Fixed fixture semantics; no claim of an autonomous model understanding arbitrary code.",
                         "Metering covers this server's deliveries; arbitrary external Host tools and process memory are not hard-capped."]}
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(report, indent=2))


def main():
    repo = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=Path(os.environ.get("AOCI_BIN", repo / "build" / ("aoci.exe" if os.name == "nt" else "aoci"))))
    parser.add_argument("--output", type=Path, default=repo / "build" / "task-context-report.json")
    args = parser.parse_args()
    binary = args.binary.resolve()
    require(binary.is_file(), f"build a binary first: {binary}")
    with tempfile.TemporaryDirectory(prefix="aoci-task-stdio-") as work:
        os.environ["AOCI_SCENARIO_WORK"] = work
        import mcp_scenarios as scenes
        run(scenes, binary, args.output.resolve())
    return 0


if __name__ == "__main__":
    sys.exit(main())
