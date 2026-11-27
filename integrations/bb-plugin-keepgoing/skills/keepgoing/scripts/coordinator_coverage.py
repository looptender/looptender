#!/usr/bin/env python3
"""Read-only declared-work coverage at a coordinator boundary; no host commands."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import sys
import time

sys.dont_write_bytecode = True
import coordinator as workflow
from coordinator_state import Refused, decode, digest, private_read, require

LIMIT = 1024 * 1024
MAX_ITEMS = 32
BINDING = {"loop", "config", "runtime", "coordinator", "owner", "generation", "goal", "goalRevision"}
CONTROLS = {"enabled", "paused", "stop", "awaitingUser", "completed", "superseded", "pendingTransfer"}
HASH = re.compile(r"[0-9a-f]{64}\Z")


def keys(value, names):
    return isinstance(value, dict) and set(value) == set(names.split())


def positive(value):
    return type(value) is int and value > 0


def refs(value, required=False):
    return isinstance(value, list) and (not required or bool(value)) and len(value) <= 32 and all(workflow.text(v, 256) for v in value) and len(set(value)) == len(value)


def binding(value):
    return isinstance(value, dict) and set(value) == BINDING and all(workflow.text(value[k]) for k in BINDING - {"generation", "goalRevision"}) and all(positive(value[k]) for k in ("generation", "goalRevision"))


def fresh(value, now, age):
    try:
        at = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return at.tzinfo is not None and 0 <= (now - at).total_seconds() <= age
    except (ValueError, TypeError, AttributeError):
        return False


def validate_inventory(i):
    require(keys(i, "version id revision binding assignmentRecord assignmentRevision scopeComplete workSources authoritySources items"), "inventory_schema")
    require(type(i["version"]) is int and i["version"] == 1 and workflow.text(i["id"], 256) and positive(i["revision"]) and binding(i["binding"]) and workflow.text(i["assignmentRecord"], 256) and positive(i["assignmentRevision"]) and type(i["scopeComplete"]) is bool, "inventory_binding")
    require(isinstance(i["items"], list) and len(i["items"]) <= MAX_ITEMS, "item_bound")
    seen = set()
    for row in i["items"]:
        require(keys(row, "id revision workSource authorization scopeRefs dependsOn personGate state waitRefs assignment acceptance"), "item_schema")
        require(workflow.text(row["id"], 64) and row["id"] not in seen and positive(row["revision"]), "item_identity")
        seen.add(row["id"])
        require(keys(row["workSource"], "id revision sha256") and workflow.text(row["workSource"]["id"], 256) and positive(row["workSource"]["revision"]) and isinstance(row["workSource"]["sha256"], str) and HASH.fullmatch(row["workSource"]["sha256"]), "item_source_schema")
        require(keys(row["authorization"], "state refs") and row["authorization"]["state"] in {"authorized", "unknown", "scope_expansion"} and refs(row["authorization"]["refs"]), "authorization_schema")
        require(keys(row["personGate"], "state refs") and row["personGate"]["state"] in {"none", "held", "unknown"} and refs(row["personGate"]["refs"]), "gate_schema")
        require(refs(row["scopeRefs"], True) and refs(row["waitRefs"]) and row["state"] in {"open", "waiting", "closed", "unknown"} and (row["dependsOn"] is None or isinstance(row["dependsOn"], list) and len(row["dependsOn"]) <= MAX_ITEMS), "item_schema")
        deps = set()
        for dep in row["dependsOn"] or []:
            require(keys(dep, "id revision") and workflow.text(dep["id"], 64) and positive(dep["revision"]) and dep["id"] not in deps, "dependency_schema")
            deps.add(dep["id"])
        a = row["assignment"]
        require(a is None or keys(a, "id revision handoffRevision") and workflow.text(a["id"], 64) and positive(a["revision"]) and positive(a["handoffRevision"]), "assignment_mapping_schema")
        a = row["acceptance"]
        require(a is None or keys(a, "accepted itemRevision workSourceRevision workSourceDigest evidenceRefs") and type(a["accepted"]) is bool and positive(a["itemRevision"]) and positive(a["workSourceRevision"]) and isinstance(a["workSourceDigest"], str) and HASH.fullmatch(a["workSourceDigest"]) and refs(a["evidenceRefs"], True), "acceptance_schema")
    for name in ("workSources", "authoritySources"):
        require(isinstance(i[name], list) and len(i[name]) <= MAX_ITEMS, "source_bound")
        seen = set()
        for src in i[name]:
            fields = "id revision path sha256 itemIds" if name == "workSources" else "id revision path sha256"
            require(keys(src, fields) and workflow.text(src["id"], 256) and src["id"] not in seen and positive(src["revision"]) and workflow.text(src["path"]) and Path(src["path"]).is_absolute() and isinstance(src["sha256"], str) and HASH.fullmatch(src["sha256"]), "source_schema")
            if name == "workSources":
                require(refs(src["itemIds"]), "source_items")
            seen.add(src["id"])


def validate_context(c):
    require(keys(c, "version binding observedAt health coordinatorState controls objectiveAcceptance"), "context_schema")
    require(type(c["version"]) is int and c["version"] == 1 and binding(c["binding"]) and workflow.text(c["observedAt"], 64) and c["health"] in {"complete", "partial", "unknown"} and c["coordinatorState"] in {"waiting", "working", "unknown"}, "context_schema")
    require(isinstance(c["controls"], dict) and set(c["controls"]) == CONTROLS and all(type(v) is bool for v in c["controls"].values()), "control_schema")
    a = c["objectiveAcceptance"]
    require(a is None or keys(a, "goal goalRevision scopeRevision inventoryDigest accepted evidenceRefs") and workflow.text(a["goal"], 256) and positive(a["goalRevision"]) and positive(a["scopeRevision"]) and isinstance(a["inventoryDigest"], str) and HASH.fullmatch(a["inventoryDigest"]) and type(a["accepted"]) is bool and refs(a["evidenceRefs"], True), "objective_acceptance_schema")


def pin_sources(i):
    """Hash only; do not parse repository prose or discover/authorize sources."""
    status, total = {}, 0
    for kind in ("workSources", "authoritySources"):
        for src in i[kind]:
            try:
                hashed, raw = workflow.file_hash(src["path"], LIMIT - total, collect=True)
                total += len(raw)
                status[(kind, src["id"])] = {"state": "current" if hashed == src["sha256"] else "source_drift", "observedDigest": hashed}
            except (OSError, Refused):
                status[(kind, src["id"])] = {"state": "source_unavailable", "observedDigest": None}
    return status


def pin_state(pins, kind, key):
    value = pins.get((kind, key))
    return value.get("state", "source_unavailable") if isinstance(value, dict) else "source_unavailable"


def project(i, c, record, pins, now, max_age=300, boundary="wait"):
    """Pure projection of validated declarations/captures; never an executor."""
    validate_inventory(i); validate_context(c)
    require(type(max_age) is int and 1 <= max_age <= 3600 and boundary in {"wait", "phase"}, "invalid_options")
    findings, missing, rows_out = [], [], []
    evidence = {"binding": digest(i["binding"]), "inventory": digest(i), "context": digest(c), "assignments": digest(record), "pins": digest(sorted((k[0], k[1], v) for k, v in pins.items()))}

    def finding(code, subject, action, unknowns=(), confidence="declared_evidence"):
        ev = dict(evidence, inventoryRevision=i["revision"])
        item = next((r for r in i["items"] if r["id"] == subject), None)
        if item:
            ev.update(itemRevision=item["revision"], workSource=dict(item["workSource"]))
        f = {"class": code, "subject": subject, "evidence": ev, "confidence": confidence, "unknowns": sorted(set(unknowns) | {"semantic_scope_not_verified", "captures_not_live_authenticated"}), "recommendedAction": action, "autoAction": "none", "estimatedWaste": "unknown", "verificationMetric": "new_bound_item_progress_or_reconciled_evidence", "status": "open"}
        f["id"] = digest(f)[:24]
        findings.append(f)

    def unknown(code, subject="scope", action="reconcile_bound_coverage"):
        missing.append({"reason": code, "subject": subject})
        finding(code, subject, action, [code])

    runtime_current = c["binding"] == i["binding"] and fresh(c["observedAt"], now, max_age) and c["health"] == "complete"
    if not runtime_current:
        unknown("current_binding_or_health_unavailable", "context", "refresh_same_binding_capture")
    held = not c["controls"]["enabled"] or any(c["controls"][k] for k in CONTROLS - {"enabled"})
    if held:
        finding("runtime_hold", "context", "preserve_runtime_hold", ["hold_release_not_authorized"])
    acceptance = c["objectiveAcceptance"]
    objective_accepted = bool(acceptance and acceptance["accepted"] and acceptance["goal"] == i["binding"]["goal"] and acceptance["goalRevision"] == i["binding"]["goalRevision"] and acceptance["scopeRevision"] == i["revision"] and acceptance["inventoryDigest"] == digest(i))
    sources = {s["id"]: s for s in i["workSources"]}
    declared = [item for s in sources.values() for item in s["itemIds"]]
    if not sources:
        unknown("work_source_missing", action="select_and_pin_already_authorized_work_source")
    if not i["scopeComplete"] or len(declared) != len(set(declared)):
        unknown("scope_coverage_incomplete")
    items = {r["id"]: r for r in i["items"]}
    if len(set(declared)) > MAX_ITEMS:
        unknown("scope_item_bound")
    for item in sorted(set(declared) - set(items)):
        unknown("scope_item_uncovered", item)
    for item in sorted(set(items) - set(declared)):
        unknown("item_not_in_pinned_source", item)
    for src in i["workSources"] + i["authoritySources"]:
        kind = "workSources" if src in i["workSources"] else "authoritySources"
        if pin_state(pins, kind, src["id"]) != "current":
            unknown(pin_state(pins, kind, src["id"]), src["id"], "repin_existing_source_and_reconcile_scope_without_inventing_authority")

    # Same shapes/revision limits as the packaged Commands.record; supplied
    # captures remain declarations, not authenticated current host observations.
    x, assignments, assignments_current = {}, {}, False
    if isinstance(record, dict) and type(record.get("version")) is int and record["version"] in (1, 2) and isinstance(record.get("revisions"), list) and 1 <= len(record["revisions"]) <= 32:
        x = record["revisions"][-1]
        if isinstance(x, dict) and isinstance(x.get("inventory"), dict) and isinstance(x.get("rows"), list) and len(x["rows"]) <= 200:
            assignments_current = record.get("id") == i["assignmentRecord"] and positive(x.get("revision")) and x.get("revision") == i["assignmentRevision"] and x.get("binding") == i["binding"] and (x.get("inventory") or {}).get("coverage") == "observed_direct_children" and fresh((x.get("inventory") or {}).get("observedAt"), now, max_age)
            lanes = x["inventory"].get("lanes")
            if not isinstance(lanes, list) or len(lanes) > 200 or not all(isinstance(l, dict) and workflow.text(l.get("thread"), 256) and l.get("execution") in {"idle", "active", "error", "pending", "starting", "stopping"} and type(l.get("archived")) is bool for l in lanes) or len({l["thread"] for l in lanes}) != len(lanes):
                assignments_current = False
            for row in x["rows"]:
                key = row["spec"].get("id") if isinstance(row, dict) and isinstance(row.get("spec"), dict) else None
                if not workflow.text(key, 64) or key in assignments or not workflow.text(row["spec"].get("lane"), 256):
                    assignments_current = False
                    continue
                assignments[key] = row
    if not assignments_current:
        unknown("assignment_inventory_unavailable", "assignments", "refresh_exact_assignment_inventory")
    mapped = [r["assignment"]["id"] for r in items.values() if r["assignment"]]
    if len(mapped) != len(set(mapped)):
        unknown("assignment_mapping_ambiguous")
    for key in sorted(set(assignments) - set(mapped)):
        unknown("missing_claimed_item", key, "reconcile_claim_against_existing_authorized_source")

    def closed(row):
        a = row["acceptance"]; src = row["workSource"]
        return row["state"] == "closed" and a is not None and a["accepted"] and a["itemRevision"] == row["revision"] and a["workSourceRevision"] == src["revision"] and a["workSourceDigest"] == src["sha256"]

    dependency_state, dependency_reason = {}, {}

    def cycle_or_missing(key):
        if dependency_state.get(key) == "visiting":
            return "dependency_cycle"
        if key in dependency_reason:
            return dependency_reason[key]
        dependency_state[key] = "visiting"
        if items[key]["dependsOn"] is None:
            dependency_reason[key] = "dependency_evidence_unavailable"
            return dependency_reason[key]
        reason = None
        for dep in items[key]["dependsOn"]:
            target = items.get(dep["id"])
            if target is None or target["revision"] != dep["revision"]:
                reason = "dependency_evidence_unavailable"
                break
            reason = cycle_or_missing(dep["id"])
            if reason:
                break
        dependency_state[key] = "done"
        dependency_reason[key] = reason
        return reason

    for key, row in items.items():
        reason, disposition = None, "unknown"
        src = sources.get(row["workSource"]["id"])
        if src is None or key not in src["itemIds"] or row["workSource"] != {k: src[k] for k in ("id", "revision", "sha256")} or pin_state(pins, "workSources", src["id"]) != "current":
            reason = "item_work_source_unavailable"
        elif row["authorization"]["state"] != "authorized" or not row["authorization"]["refs"] or any(pin_state(pins, "authoritySources", ref) != "current" for ref in row["authorization"]["refs"]):
            reason = "authorization_scope_expansion_held" if row["authorization"]["state"] == "scope_expansion" else "authorization_evidence_unavailable"
        elif row["personGate"]["state"] == "unknown" or row["personGate"]["state"] == "held" and not row["personGate"]["refs"]:
            reason = "person_gate_evidence_unavailable"
        else:
            reason = cycle_or_missing(key)
        mapping = row["assignment"]
        if not reason and mapping:
            saved = assignments.get(mapping["id"], {})
            if not assignments_current or not positive(saved.get("revision")) or saved.get("revision") != mapping["revision"] or not positive(saved.get("handoffRevision")) or saved.get("handoffRevision") != mapping["handoffRevision"] or not positive(saved.get("acknowledged")) or saved.get("acknowledged") != mapping["handoffRevision"]:
                reason = "assignment_mapping_stale"
            elif not any(l["thread"] == saved["spec"]["lane"] and l["execution"] == "idle" and not l["archived"] for l in x["inventory"]["lanes"]):
                reason = "assignment_lane_not_idle_or_unavailable"
            elif not isinstance(saved.get("handoff"), dict) or saved["handoff"].get("assignment") != mapping["id"] or not positive(saved["handoff"].get("assignmentRevision")) or saved["handoff"].get("assignmentRevision") != mapping["revision"] or saved["handoff"].get("outcome") not in {"complete", "unfinished", "waiting", "superseded"} or not isinstance(saved["handoff"].get("remainingRefs"), list) or saved["handoff"].get("outcome") == "complete" and saved["handoff"]["remainingRefs"]:
                reason = "assignment_handoff_unavailable"
            elif saved["handoff"].get("outcome") != "complete":
                # An unfinished assignment already has an owner; never propose
                # duplicate implementation from an objective-only declaration.
                disposition = "existing_obligation"
                finding("existing_assignment_obligation", key, "reconcile_existing_owner_handoff_without_duplicate_work")
        if not reason and disposition != "existing_obligation":
            if row["state"] == "closed":
                if closed(row):
                    disposition = "declared_closed"
                else:
                    reason = "item_acceptance_unavailable"
            elif row["state"] == "unknown":
                reason = "item_state_unavailable"
            elif row["personGate"]["state"] == "held":
                disposition = "person_gated"
            elif any(not closed(items[d["id"]]) for d in row["dependsOn"]):
                disposition = "dependency_waiting"
            elif row["state"] == "waiting":
                if row["waitRefs"]:
                    disposition = "declared_external_wait"
                else:
                    reason = "dependency_evidence_unavailable"
            else:
                disposition = "independent_open"
        if reason:
            unknown(reason, key)
        rows_out.append({"id": key, "disposition": disposition, "reason": reason})

    # A raw closed declaration is not a usable dependency result until its
    # source, authorization, acceptance and assignment mapping all validate.
    by_id = {r["id"]: r for r in rows_out}
    for _ in range(MAX_ITEMS):
        changed = False
        for key, row in items.items():
            out = by_id[key]
            if out["reason"] or not row["dependsOn"]:
                continue
            targets = [by_id[d["id"]] for d in row["dependsOn"]]
            if any(t["reason"] for t in targets) or out["disposition"] == "declared_closed" and any(t["disposition"] != "declared_closed" for t in targets):
                out.update(disposition="unknown", reason="dependency_acceptance_unavailable")
                unknown("dependency_acceptance_unavailable", key)
                changed = True
            elif out["disposition"] == "independent_open" and any(t["disposition"] != "declared_closed" for t in targets):
                out["disposition"] = "dependency_waiting"
                changed = True
        if not changed:
            break
    objective_accepted = objective_accepted and runtime_current and i["scopeComplete"] and bool(sources) and not missing and all(r["disposition"] == "declared_closed" for r in rows_out)
    if not objective_accepted:
        missing.append({"reason": "objective_acceptance_unavailable", "subject": "objective"})

    eligible = [r["id"] for r in rows_out if r["disposition"] == "independent_open"]
    if eligible and runtime_current and not held:
        for key in eligible:
            finding("unjustified_coordinator_wait" if boundary == "wait" and c["coordinatorState"] == "waiting" else "independent_work_available", key, "advance_existing_authorized_item_without_duplicating_assignment")
    # Missing objective acceptance is a blind spot, never a completion inference.
    lanes = x["inventory"].get("lanes") if isinstance(x, dict) and isinstance(x.get("inventory"), dict) else None
    healthy_terminal = assignments_current and isinstance(lanes, list) and bool(lanes) and len(lanes) <= 200 and all(isinstance(l, dict) and workflow.text(l.get("thread"), 256) and l.get("execution") == "idle" and l.get("archived") is False for l in lanes) and len({l.get("thread") for l in lanes}) == len(lanes)
    covered_lanes = {a.get("spec", {}).get("lane") for a in assignments.values()}
    healthy_terminal = healthy_terminal and {l.get("thread") for l in lanes} == covered_lanes and all(isinstance(a.get("handoff"), dict) and a["handoff"].get("outcome") == "complete" and a.get("acknowledged") == a.get("handoffRevision") and positive(a.get("handoffRevision")) for a in assignments.values())
    coverage_unknowns = [m for m in missing if m["reason"] != "objective_acceptance_unavailable"]
    if healthy_terminal and runtime_current and not held and c["coordinatorState"] == "waiting" and not objective_accepted and not coverage_unknowns and sources:
        finding("suspected_stranded_objective", "objective", "reconcile_objective_acceptance_and_remaining_work", ["objective_acceptance_unavailable"], "suspected")
    if held:
        verdict = "runtime_held"
    elif not runtime_current:
        verdict = "cannot_assess"
    elif eligible:
        verdict = "continue_authorized_work"
    elif coverage_unknowns:
        verdict = "cannot_assess"
    elif rows_out and all(r["disposition"] in {"person_gated", "dependency_waiting", "declared_external_wait", "declared_closed"} for r in rows_out) and any(r["disposition"] != "declared_closed" for r in rows_out):
        verdict = "declared_wait_supported"
    else:
        verdict = "completion_review_required"
    report = {"version": 1, "boundary": boundary, "verdict": verdict, "waitJustified": verdict == "declared_wait_supported", "objectiveComplete": False, "confidence": "declared_evidence_only", "assessability": {"workAvailability": "cannot_assess" if not runtime_current else "partially_assessable" if coverage_unknowns and eligible else "cannot_assess" if coverage_unknowns else "assessable_declared_scope", "objectiveAcceptance": "mapped_declaration_only" if objective_accepted else "cannot_assess"}, "missingInputs": missing, "evidence": evidence, "items": rows_out, "findings": findings, "limitations": ["no_semantic_completeness_proof", "captures_not_live_authenticated", "no_dispatch_or_hold_authority", "no_measured_cost_reduction"]}
    report["fingerprint"] = digest(report)
    return report


def exit_code(report):
    return 0 if report["waitJustified"] else 1


def inspect(paths, now, max_age=300, boundary="wait"):
    raw = [private_read(p, LIMIT) for p in paths]
    require(sum(map(len, raw)) <= LIMIT, "aggregate_input_bound")
    i, c, record = [decode(b, LIMIT) for b in raw]
    validate_inventory(i); validate_context(c)
    pins = pin_sources(i)
    report = project(i, c, record, pins, now, max_age, boundary)
    require(raw == [private_read(p, LIMIT) for p in paths] and pins == pin_sources(i), "evidence_changed_during_read")
    return report


def public_report(report):
    # Counts/reasons and opaque evidence digests only; no task/source names,
    # paths, authorization refs or assignment/goal/thread identities.
    result = {k: v for k, v in report.items() if k not in {"items", "findings", "missingInputs"}}
    result["items"] = {d: sum(r["disposition"] == d for r in report["items"]) for d in sorted({r["disposition"] for r in report["items"]})}
    result["missingInputs"] = sorted({m["reason"] for m in report["missingInputs"]})
    result["findings"] = [dict({k: v for k, v in f.items() if k not in {"subject", "evidence"}}, subjectDigest=digest(f["subject"]), evidence=report["evidence"]) for f in report["findings"]]
    return result


def main(argv=None):
    workflow.OPERATION_END = time.monotonic() + 10
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", required=True, help="private declared work inventory")
    parser.add_argument("--context", required=True, help="private dated binding/control capture")
    parser.add_argument("--assignments", required=True, help="private assignment show capture")
    parser.add_argument("--boundary", choices=("wait", "phase"), default="wait")
    parser.add_argument("--max-age", type=int, default=300, help="capture age limit in seconds (1..3600)")
    parser.add_argument("--previous", help="previous private or minimized receipt; never skips revalidation")
    parser.add_argument("--private", action="store_true")
    args = parser.parse_args(argv)
    try:
        require(1 <= args.max_age <= 3600 and sys.platform != "win32", "unsupported_or_invalid_options")
        report = inspect([args.inventory, args.context, args.assignments], datetime.now(timezone.utc), args.max_age, args.boundary)
        report["unchanged"] = False
        if args.previous:
            previous = decode(private_read(args.previous, LIMIT), LIMIT)
            require(isinstance(previous, dict) and isinstance(previous.get("fingerprint"), str), "previous_receipt_schema")
            report["unchanged"] = previous["fingerprint"] == report["fingerprint"]
        print(json.dumps(report if args.private else public_report(report), sort_keys=True))
        return exit_code(report)
    except Refused as error:
        return unavailable(str(error))
    except OSError:
        return unavailable("input_file_unavailable")
    except (ValueError, TypeError, KeyError, AttributeError, RecursionError):
        return unavailable("invalid_input_schema")


def unavailable(reason):
    # Fixed codes only, never private OS exception messages or partial success.
    print(json.dumps({"version": 1, "verdict": "cannot_assess", "waitJustified": False, "objectiveComplete": False, "assessability": {"workAvailability": "cannot_assess", "objectiveAcceptance": "cannot_assess"}, "missingInputs": [reason], "recommendedAction": "reconcile_private_inputs_and_pin_existing_authorized_sources", "autoAction": "none"}, sort_keys=True))
    return 1



if __name__ == "__main__":
    sys.exit(main())
