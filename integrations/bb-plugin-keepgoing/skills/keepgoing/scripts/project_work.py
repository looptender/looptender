#!/usr/bin/env python3
"""Coordinator-owned Markdown work register; explicit IO, no runtime actions."""
import argparse
import copy
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile
import time

sys.dont_write_bytecode = True
import coordinator_coverage as coverage
from coordinator_state import Refused, decode, digest, private_read, require

WORK_LIMIT = 256 * 1024
CHECKPOINT_LIMIT = 4 * 1024 * 1024
ID = re.compile(r"[A-Za-z][A-Za-z0-9_-]{0,63}\Z")
EXTRA = {"title", "owner", "acceptanceRefs", "nextAction", "evidenceRefs", "blockingObligation"}
INVENTORY_KEYS = "version id revision binding assignmentRecord assignmentRevision scopeComplete workSources authoritySources items"


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def fenced(raw, language, limit):
    """Exactly one reserved fence. Prose is preserved, never interpreted."""
    require(len(raw) <= limit, "input_bound")
    try:
        text = raw.decode("utf-8")
    except UnicodeError:
        raise Refused("invalid_utf8") from None
    require("\x00" not in text, "invalid_markdown")
    lines = text.splitlines(keepends=True)
    starts = [n for n, line in enumerate(lines) if line.strip().startswith("```" + language)]
    require(len(starts) == 1 and lines[starts[0]].strip() == "```" + language, "machine_section_required_once")
    start = starts[0] + 1
    ends = [n for n in range(start, len(lines)) if lines[n].strip() == "```"]
    require(bool(ends), "machine_section_unclosed")
    return decode("".join(lines[start:ends[0]]).encode(), limit)


def markdown(value, language, intro):
    # Escape backticks in JSON strings: references cannot terminate the fence.
    body = json.dumps(value, ensure_ascii=True, indent=2).replace("`", "\\u0060")
    return (intro + "\n\n```" + language + "\n" + body + "\n```\n").encode()


def valid_id(value):
    return isinstance(value, str) and ID.fullmatch(value) is not None


def inventory(work):
    result = copy.deepcopy(work)
    result["items"] = [{k: v for k, v in row.items() if k not in EXTRA} for row in result["items"]]
    return result


def validate(work):
    require(coverage.keys(work, INVENTORY_KEYS), "register_schema")
    require(valid_id(work["id"]), "register_identity")
    require(work["binding"] is None or coverage.binding(work["binding"]), "register_binding")
    require((work["assignmentRecord"] is None) == (work["assignmentRevision"] is None), "assignment_binding_pair")
    require(work["assignmentRecord"] is None or coverage.workflow.text(work["assignmentRecord"], 256) and coverage.positive(work["assignmentRevision"]), "assignment_binding")
    require(isinstance(work["items"], list) and len(work["items"]) <= coverage.MAX_ITEMS, "item_bound")
    projected = inventory(work)
    # Reuse coverage's exact row/source/edge schemas. Placeholders are ONLY for
    # validation of nullable metadata, never exported or passed to projection.
    projected["binding"] = work["binding"] or {k: 1 if k in {"generation", "goalRevision"} else "unavailable" for k in coverage.BINDING}
    projected["assignmentRecord"] = work["assignmentRecord"] or "unavailable"
    projected["assignmentRevision"] = work["assignmentRevision"] or 1
    for row, out in zip(work["items"], projected["items"]):
        require(set(row) == set(out) | EXTRA, "work_item_schema")
        require(valid_id(row["id"]) and coverage.workflow.text(row["title"], 256) and bool(row["title"].strip()) and coverage.workflow.text(row["nextAction"], 2048) and bool(row["nextAction"].strip()), "work_item_text")
        require(coverage.refs(row["acceptanceRefs"], True) and coverage.refs(row["evidenceRefs"]), "work_item_refs")
        owner = row["owner"]
        require(owner is None or coverage.keys(owner, "kind id") and owner["kind"] in {"coordinator", "assignment"} and coverage.workflow.text(owner["id"], 256), "owner_schema")
        if row["assignment"] is not None:
            require(isinstance(row["assignment"], dict) and owner == {"kind": "assignment", "id": row["assignment"].get("id")}, "owner_assignment_conflict")
        elif owner:
            require(owner["kind"] == "coordinator", "owner_assignment_missing")
        if owner and owner["kind"] == "coordinator" and work["binding"]:
            require(owner["id"] == work["binding"]["coordinator"], "owner_binding_conflict")
        require(row["dependsOn"] is None or isinstance(row["dependsOn"], list) and all(isinstance(d, dict) and valid_id(d.get("id")) for d in row["dependsOn"]), "dependency_identity")
        block = row["blockingObligation"]
        require(block is None or coverage.keys(block, "id revision") and valid_id(block["id"]) and coverage.positive(block["revision"]) and block["id"] != row["id"], "blocking_obligation_schema")
        require((row["state"] == "waiting") == (block is not None), "waiting_requires_linked_obligation")
        require(row["state"] == "waiting" or not row["waitRefs"], "unexpected_wait_refs")
        require(row["state"] != "waiting" or bool(row["waitRefs"]), "wait_evidence_required")
        require(row["acceptance"] is None or row["state"] == "closed", "acceptance_state_conflict")
        if row["workSource"] is None:
            require(row["acceptance"] is None, "acceptance_source_missing")
            out["workSource"] = {"id": "unavailable", "revision": 1, "sha256": "0" * 64}
    coverage.validate_inventory(projected)
    mapped = [r["assignment"]["id"] for r in work["items"] if r["assignment"]]
    require(len(mapped) == len(set(mapped)), "duplicate_assignment_mapping")
    return work


def parse_work(raw):
    return validate(fenced(raw, "looptender-work", WORK_LIMIT))


def template(key):
    require(valid_id(key), "register_identity")
    return {"version": 1, "id": key, "revision": 1, "binding": None,
            "assignmentRecord": None, "assignmentRevision": None, "scopeComplete": False,
            "workSources": [], "authoritySources": [], "items": []}


def parse_checkpoint(raw):
    value = fenced(raw, "looptender-handoff", CHECKPOINT_LIMIT)
    require(coverage.keys(value, "version registerId revision workSha256 workMarkdown report"), "checkpoint_schema")
    require(type(value["version"]) is int and value["version"] == 1 and isinstance(value["workMarkdown"], str) and isinstance(value["report"], dict), "checkpoint_schema")
    saved = value["workMarkdown"].encode("utf-8")
    work = parse_work(saved)
    require(value["registerId"] == work["id"] and type(value["revision"]) is int and value["revision"] == work["revision"] and value["workSha256"] == sha(saved), "checkpoint_binding")
    report = value["report"]
    require(coverage.keys(report, "version verdict objectiveComplete assessability evidence items findings missingInputs coverage limitations") and type(report["version"]) is int and report["version"] == 1 and report["objectiveComplete"] is False, "checkpoint_report_schema")
    require(isinstance(report["evidence"], dict) and report["evidence"].get("workSha256") == sha(saved) and report["evidence"].get("registerRevision") == work["revision"], "checkpoint_report_binding")
    expected_items = [{k: row[k] for k in ("id", "state", "owner", "nextAction", "blockingObligation")} for row in work["items"]]
    require(report["items"] == expected_items and isinstance(report["findings"], list) and isinstance(report["missingInputs"], list), "checkpoint_report_items")
    return work, saved


def source_ready(row, sources, pins):
    source = row["workSource"]
    if source is None:
        return False
    declared = sources.get(source["id"])
    return bool(declared and source == {k: declared[k] for k in ("id", "revision", "sha256")} and row["id"] in declared["itemIds"] and coverage.pin_state(pins, "workSources", source["id"]) == "current")


def reconcile(work, work_sha, pins, context=None, assignments=None, previous=None, now=None, max_age=300):
    """Pure projection. Local findings survive unavailable runtime evidence."""
    validate(work)
    now = now or datetime.now(timezone.utc)
    rows = {r["id"]: r for r in work["items"]}
    sources = {s["id"]: s for s in work["workSources"]}
    findings, missing = [], []
    bindings = {"workSha256": work_sha, "registerRevision": work["revision"], "context": digest(context), "assignments": digest(assignments), "pins": digest(sorted((k[0], k[1], v) for k, v in pins.items()))}

    def finding(code, key, action, unknown=False):
        row = rows.get(key)
        f = {"class": code, "subject": key, "evidence": dict(bindings, itemRevision=row["revision"] if row else None), "confidence": "declared_evidence_only", "unknowns": ["semantic_acceptance_not_verified", "captures_not_live_authenticated"], "recommendedAction": action, "autoAction": "none"}
        f["id"] = digest(f)[:24]
        findings.append(f)
        if unknown:
            missing.append({"reason": code, "subject": key})

    if not rows:
        finding("empty_declared_inventory", "register", "inventory_existing_authorized_work_without_inventing_completeness", True)
    if not work["scopeComplete"]:
        finding("scope_coverage_incomplete", "register", "review_declared_scope_against_existing_authorized_sources", True)
    if not work["binding"]:
        finding("current_binding_unavailable", "register", "supply_existing_goal_and_assignment_binding", True)
    if not work["workSources"]:
        finding("work_source_missing", "register", "select_and_pin_already_authorized_work_source", True)
    for kind in ("workSources", "authoritySources"):
        for source in work[kind]:
            state = coverage.pin_state(pins, kind, source["id"])
            if state != "current":
                finding(state, source["id"], "reconcile_and_repin_existing_source_without_inventing_authority", True)

    # Local acceptance is explicitly mapped declaration, not completion proof.
    def accepted(row):
        a, src = row["acceptance"], row["workSource"]
        return bool(row["state"] == "closed" and a and a["accepted"] and source_ready(row, sources, pins) and a["itemRevision"] == row["revision"] and a["workSourceRevision"] == src["revision"] and a["workSourceDigest"] == src["sha256"])

    # At most 32 rows; memoize final reasons to avoid exponential DAG walks.
    problems = {}
    def dependency_problem(key, visiting=frozenset()):
        if key in visiting:
            return "dependency_cycle"
        if key in problems:
            return problems[key]
        row = rows[key]
        result = "dependency_evidence_unavailable" if row["dependsOn"] is None else None
        edges = (row["dependsOn"] or []) + ([row["blockingObligation"]] if row["blockingObligation"] else [])
        for edge in edges:
            if result:
                break
            target = rows.get(edge["id"])
            result = "dependency_missing" if not target else "dependency_revision_changed" if target["revision"] != edge["revision"] else dependency_problem(edge["id"], visiting | {key})
            if result:
                break
        problems[key] = result
        return result

    accepted_cache = {}
    def settled(key):
        if key not in accepted_cache:
            row = rows[key]
            accepted_cache[key] = bool(not dependency_problem(key) and accepted(row) and row["personGate"]["state"] == "none" and all(settled(d["id"]) for d in row["dependsOn"]))
        return accepted_cache[key]

    local = []
    for key, row in rows.items():
        problem = dependency_problem(key)
        if problem:
            finding(problem, key, "reconcile_exact_dependency_revision_and_acceptance", True)
        if not source_ready(row, sources, pins):
            finding("item_work_source_unavailable", key, "pin_existing_scope_source_without_inferring_authority", True)
        authorization = row["authorization"]
        if authorization["state"] != "authorized" or not authorization["refs"] or any(coverage.pin_state(pins, "authoritySources", ref) != "current" for ref in authorization["refs"]):
            finding("authorization_evidence_unavailable", key, "reconcile_existing_authority_without_inventing_new_grants", True)
        if row["personGate"]["state"] == "unknown":
            finding("person_gate_evidence_unavailable", key, "identify_the_actual_person_gate_for_this_action", True)
        elif row["personGate"]["state"] == "held":
            finding("person_gate_held", key, "preserve_person_gate_and_review_other_independent_work")
        if row["state"] != "closed" and row["owner"] is None:
            finding("unfinished_unowned_work", key, "review_existing_authority_and_record_responsible_owner")
        if row["state"] == "closed" and not accepted(row):
            finding("item_acceptance_unavailable", key, "review_and_map_acceptance_to_exact_item_and_source_revision", True)
        if row["state"] == "unknown":
            finding("item_state_unavailable", key, "reconcile_declared_work_state", True)
        if row["dependsOn"] and not problem and any(not settled(d["id"]) for d in row["dependsOn"]):
            finding("dependency_unmet", key, "retain_dependency_and_review_its_acceptance")
        block = row["blockingObligation"]
        if block and not problem and settled(block["id"]):
            finding("wait_obligation_resolved", key, "reconcile_obsolete_wait_and_next_action_without_clearing_runtime_holds")
        local.append({"id": key, "state": row["state"], "owner": row["owner"], "nextAction": row["nextAction"], "blockingObligation": block})

    if previous:
        old, old_raw = previous
        require(old["id"] == work["id"], "previous_register_identity_changed")
        if sha(old_raw) != work_sha:
            finding("checkpoint_stale", "register", "review_changes_and_write_a_new_checkpoint")
        if work["revision"] < old["revision"] or work != old and work["revision"] <= old["revision"]:
            finding("register_revision_not_advanced", "register", "retain_history_and_advance_register_revision", True)
        for prior in old["items"]:
            current = rows.get(prior["id"])
            if current is None and prior["state"] != "closed":
                finding("unfinished_item_removed", prior["id"], "restore_or_explicitly_reconcile_unfinished_obligation", True)
            elif current and current != prior and current["revision"] <= prior["revision"]:
                finding("item_revision_not_advanced", current["id"], "advance_item_revision_and_reconcile_dependents_and_acceptance", True)

    bridge = None
    ready = work["binding"] is not None and work["assignmentRecord"] is not None and all(r["workSource"] for r in rows.values())
    if ready and context is not None and assignments is not None:
        bridge = coverage.project(inventory(work), context, assignments, pins, now, max_age)
        findings.extend(bridge["findings"])
        missing.extend(bridge["missingInputs"])
        by_id = {r["id"]: r for r in bridge["items"]}
        # Existing coverage already validates record identity, handoff revision,
        # acknowledgement, lane state and complete outcome before this finding.
        for key, row in rows.items():
            out = by_id[key]
            if row["assignment"] and (out["disposition"] in {"independent_open", "dependency_waiting", "person_gated", "declared_external_wait"} or out["reason"] == "item_acceptance_unavailable"):
                finding("child_complete_follow_through_open", key, "retain_work_item_and_review_acceptance_or_remaining_next_action")
    else:
        finding("coverage_inputs_unavailable", "register", "supply_current_bound_context_and_assignment_capture_for_coverage", True)
    held = context is not None and (not context["controls"]["enabled"] or any(context["controls"][k] for k in coverage.CONTROLS - {"enabled"}))
    report = {"version": 1, "verdict": "runtime_held" if held else "reconciliation_required", "objectiveComplete": False,
              "assessability": {"localRegister": "declared_items_only", "runtimeCoverage": bridge["assessability"]["workAvailability"] if bridge else "cannot_assess", "objectiveAcceptance": "cannot_assess"},
              "evidence": bindings, "items": local, "findings": findings, "missingInputs": missing, "coverage": bridge,
              "limitations": ["no_authority_or_completion_inferred", "no_runtime_mutation", "no_automatic_turn_end_hook", "single_coordinator_editor", "captures_not_live_authenticated"]}
    if held and not bridge:
        finding("runtime_hold", "register", "preserve_runtime_hold")
    return report


def safe_path(path):
    path = Path(path)
    require(path.is_absolute() and ".." not in path.parts, "absolute_safe_path_required")
    # Reject symlinked parent components too. A hostile rename after validation
    # remains outside the cooperative single-editor boundary.
    for parent in path.parents:
        require(stat.S_ISDIR(parent.lstat().st_mode), "non_symlink_directory_required")
    return path


def read_file(path, limit):
    return private_read(safe_path(path), limit)


def inspect(path, context_path=None, assignments_path=None, previous_path=None, max_age=300):
    require(type(max_age) is int and 1 <= max_age <= 3600, "invalid_max_age")
    raw = read_file(path, WORK_LIMIT)
    work = parse_work(raw)
    captures = {}
    for name, p, limit in (("context", context_path, coverage.LIMIT), ("assignments", assignments_path, coverage.LIMIT), ("previous", previous_path, CHECKPOINT_LIMIT)):
        if p:
            captures[name] = read_file(p, limit)
    require(sum(len(v) for k, v in captures.items() if k != "previous") + len(raw) <= coverage.LIMIT, "aggregate_input_bound")
    context = decode(captures["context"], coverage.LIMIT) if "context" in captures else None
    assignments = decode(captures["assignments"], coverage.LIMIT) if "assignments" in captures else None
    if "context" in captures:
        coverage.validate_context(context)
    if "assignments" in captures:
        require(isinstance(assignments, dict), "assignment_capture_schema")
    previous = parse_checkpoint(captures["previous"]) if "previous" in captures else None
    pins = coverage.pin_sources(work)
    report = reconcile(work, sha(raw), pins, context, assignments, previous, max_age=max_age)
    require(raw == read_file(path, WORK_LIMIT), "work_changed_during_read")
    for name, p, limit in (("context", context_path, coverage.LIMIT), ("assignments", assignments_path, coverage.LIMIT), ("previous", previous_path, CHECKPOINT_LIMIT)):
        if p:
            require(captures[name] == read_file(p, limit), "capture_changed_during_read")
    require(pins == coverage.pin_sources(work), "source_changed_during_read")
    return work, raw, report


def exclusive(path, raw):
    # Publish a complete regular file without replacing any destination. A
    # same-directory hard link avoids a partial first WORK/checkpoint on crash.
    name = None
    try:
        with tempfile.NamedTemporaryFile(dir=Path(path).parent, prefix=".project-work-", delete=False) as stream:
            name = stream.name
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        os.link(name, path, follow_symlinks=False)
    finally:
        if name is not None:
            os.unlink(name)


def output_parent(path):
    path = safe_path(path)
    s = path.parent.lstat()
    require(s.st_uid == os.getuid() and not s.st_mode & 0o077, "private_output_directory_required")
    return path


def save_checkpoint(path, raw, expected):
    """Cooperative lock + exact prior digest; preserve prior bytes before replace."""
    import fcntl
    parse_checkpoint(raw)
    path = output_parent(path)
    require(expected is None or coverage.HASH.fullmatch(expected), "invalid_expected_digest")
    fd = os.open(str(path) + ".lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
    temp = None
    try:
        s = os.fstat(fd)
        require(stat.S_ISREG(s.st_mode) and s.st_uid == os.getuid() and not s.st_mode & 0o077, "private_regular_lock_required")
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise Refused("checkpoint_busy") from None
        try:
            prior = read_file(path, CHECKPOINT_LIMIT)
        except FileNotFoundError:
            prior = None
        if prior is None:
            require(expected is None, "checkpoint_prior_missing")
            exclusive(path, raw)
        else:
            prior_work, _ = parse_checkpoint(prior)
            next_work, _ = parse_checkpoint(raw)
            require(prior_work["id"] == next_work["id"], "checkpoint_register_identity_changed")
            require(expected == sha(prior), "checkpoint_expected_digest_required_or_stale")
            archive = path.with_name(path.name + "." + sha(prior) + ".md")
            try:
                exclusive(archive, prior)
            except FileExistsError:
                require(read_file(archive, CHECKPOINT_LIMIT) == prior, "checkpoint_archive_conflict")
            with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".handoff-", delete=False) as stream:
                temp = stream.name
                stream.write(raw)
                stream.flush()
                os.fsync(stream.fileno())
            require(read_file(path, CHECKPOINT_LIMIT) == prior, "checkpoint_changed_during_write")
            os.replace(temp, path)
            temp = None
        return sha(raw)
    finally:
        if temp is not None:
            os.unlink(temp)
        os.close(fd)


def minimized(report):
    return {"version": 1, "verdict": report["verdict"], "objectiveComplete": False, "assessability": report["assessability"], "evidence": report["evidence"],
            "itemCount": len(report["items"]), "findings": [{"class": f["class"], "subjectDigest": digest(f["subject"]), "recommendedAction": f["recommendedAction"], "autoAction": "none"} for f in report["findings"]],
            "missingInputs": sorted({m["reason"] for m in report["missingInputs"]}), "limitations": report["limitations"]}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    init = commands.add_parser("init", help="create an empty private WORK.md without overwrite")
    init.add_argument("--work", required=True); init.add_argument("--id", required=True)
    read = commands.add_parser("read", help="validate/read WORK.md without interpreting prose")
    read.add_argument("--work", required=True); read.add_argument("--private", action="store_true")
    for name in ("check", "checkpoint"):
        p = commands.add_parser(name)
        p.add_argument("--work", required=True)
        p.add_argument("--context"); p.add_argument("--assignments"); p.add_argument("--previous")
        p.add_argument("--max-age", type=int, default=300); p.add_argument("--private", action="store_true")
        if name == "checkpoint":
            p.add_argument("--output", required=True)
            p.add_argument("--expect-previous", help="exact SHA256 of existing HANDOFF.md; prior bytes are retained")
    args = parser.parse_args(argv)
    try:
        require(sys.platform in {"darwin", "linux"}, "unsupported_platform")
        coverage.workflow.OPERATION_END = time.monotonic() + 10
        if args.command == "init":
            raw = markdown(template(args.id), "looptender-work", "# Project work\n\nCoordinator-owned declarations. Empty does not mean complete. Keep private context here; only the fenced JSON is interpreted.")
            exclusive(output_parent(args.work), raw)
            result = {"version": 1, "action": "initialized", "workSha256": sha(raw)}
        elif args.command == "read":
            raw = read_file(args.work, WORK_LIMIT); work = parse_work(raw)
            result = {"version": 1, "workSha256": sha(raw), "revision": work["revision"], "itemCount": len(work["items"])}
            if args.private:
                result.update(work=work, markdown=raw.decode("utf-8"))
        else:
            previous_path = args.previous
            if args.command == "checkpoint":
                try:
                    safe_path(args.output).lstat()
                    require(previous_path is None or safe_path(previous_path) == safe_path(args.output), "checkpoint_previous_must_match_output")
                    previous_path = args.output
                except FileNotFoundError:
                    pass
            work, raw, report = inspect(args.work, args.context, args.assignments, previous_path, args.max_age)
            result = report if args.private else minimized(report)
            if args.command == "checkpoint":
                require(safe_path(args.output) != safe_path(args.work), "checkpoint_cannot_replace_work")
                saved = markdown({"version": 1, "registerId": work["id"], "revision": work["revision"], "workSha256": sha(raw), "workMarkdown": raw.decode("utf-8"), "report": report}, "looptender-handoff", "# Project handoff\n\nGenerated snapshot. WORK.md is authoritative. This receipt never grants authority or proves completion.")
                require(len(saved) <= CHECKPOINT_LIMIT, "checkpoint_bound")
                result["checkpointSha256"] = save_checkpoint(args.output, saved, args.expect_previous)
        print(json.dumps(result, sort_keys=True))
        # A valid local report is useful even when captures are unavailable.
        # Check exits 1 for reconciliation/unknowns, never a completion signal.
        return 1 if args.command == "check" and (result["findings"] or result["missingInputs"]) else 0
    except (Refused, OSError, ValueError, TypeError, KeyError, AttributeError, RecursionError, UnicodeError) as error:
        print(json.dumps({"version": 1, "verdict": "cannot_assess", "objectiveComplete": False, "reason": str(error) if isinstance(error, Refused) else "input_or_output_unavailable", "autoAction": "none"}))
        return 1


if __name__ == "__main__":
    sys.exit(main())
