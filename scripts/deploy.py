#!/usr/bin/env python3
"""Distinct ledger replacement gate; never weakens gateway blue-green policy.

Executable algorithm with an injected, authenticated control-plane port for
synthetic tests. Live port/target/disk/key delivery are intentionally unselected.
The CLI can only report status/refuse activation. This is NOT qualified live
Tinfoil automation; see docs/delivery.md. No SDK, downloads, retries or rollback.
"""
import hashlib
import json
import os
import sys

import release as r

r.CODES.update({
    "policy": ("approval_policy", "existing_protected_main_environment", "Approve an existing restricted environment/operator policy; no environment was created."),
    "approval": ("operator_approval", "authenticated_exact_run_plan_approval", "Require the selected operator's authenticated approval of this exact candidate, original disk/key policy and replacement plan."),
    "rerun": ("mutation", "first_attempt_no_prior_mutation", "Reconcile previous state privately; a rerun cannot authorize a second update."),
    "target": ("control_plane", "single_stable_selected_authority", "Reconcile pending/debug/copied or mismatched ledger authority privately; do not replay an update."),
    "plan": ("replacement_plan", "approved_downtime_original_volume_key", "Approve a supported replacement with downtime on the original disk/key, unchanged opaque configuration and qualified unlock policy."),
    "control_read": ("control_plane", "authenticated_bounded_read", "Control state unavailable; underlying cause unknown. Inspect privately; status never retries or mutates."),
    "update_unknown": ("mutation", "one_nonreplayed_update", "Update outcome unknown. Reconcile authoritative state privately; never retry, roll back, restore, or initialize state automatically."),
    "completion_unknown": ("completion", "authoritative_terminal_update_evidence", "Completion unknown. Running alone is insufficient. Reconcile privately without a second update or database restore."),
    "configuration_changed": ("completion", "original_authority_and_configuration_preserved", "Privately reconcile changed disk/key/configuration; no replay or rollback. Do not start another spendable ledger."),
    "readiness": ("readiness", "original_state_unlocked_ready", "Replacement completed but readiness is unconfirmed. Preserve original state; inspect unlock/migration privately, without replay or init."),
})

# Opaque values stay transient in port memory, never receipts/logs/artifacts.
STABLE = ("target", "repository", "cpus", "memory_mb", "gpus", "volume", "key_policy", "configuration",
          "held", "debug", "confidential", "authority_count")


def policy_gate(view, policy):
    r.require(all(policy.get(k) for k in ("environment", "environment_id", "operator_id", "target", "volume", "key_policy"))
              and r.boolean(policy.get("unlock_qualified"), True)
              and r.boolean(policy.get("replacement_adapter_qualified"), True), "prerequisites")
    r.require(view["name"] == policy["environment"] and view["id"] == policy["environment_id"]
              and view["reviewers"] == [{"type": "User", "id": policy["operator_id"]}]
              and view["branches"] == [{"name": "main", "type": "branch"}]
              and r.boolean(view["protected"], True), "policy")


def stable(view, policy):
    r.require(all(k in view for k in STABLE) and view["target"] == policy["target"]
              and view["volume"] == policy["volume"] and view["key_policy"] == policy["key_policy"]
              and view["cpus"] == 2 and type(view["cpus"]) is int
              and view["memory_mb"] == 8192 and type(view["memory_mb"]) is int
              and view["gpus"] == 0 and type(view["gpus"]) is int
              and view["status"] == "running" and r.boolean(view["pending"], False)
              and r.boolean(view["held"], False) and r.boolean(view["debug"], False)
              and r.boolean(view["confidential"], True) and r.pin(view.get("tag"), r.VERSION)
              and view["authority_count"] == 1 and type(view["authority_count"]) is int, "target")


def plan_gate(plan, before, candidate, policy):
    # Plan is read BEFORE any mutation. A supported adapter must prove its
    # read is passive. Do not adapt the gateway CLI's post-call plan approach.
    r.require(r.boolean(plan["read_only"], True) and plan["strategy"] == "replacement"
              and r.boolean(plan["downtime_required"], True) and r.boolean(plan["hold"], False)
              and plan["target"] == policy["target"] and plan["from_tag"] == before["tag"]
              and plan["to_tag"] == candidate["tag"] and plan["to_tag"] != plan["from_tag"]
              and plan["image"] == candidate["image"] and plan["manifest"] == candidate["manifest"]
              and plan["original_volume"] == before["volume"] == policy["volume"]
              and plan["key_policy"] == before["key_policy"] == policy["key_policy"]
              and r.boolean(plan["detach_before_attach"], True) and r.boolean(plan["copied_authority"], False)
              and plan["configuration_changes"] == [] and r.boolean(plan["unlock_policy_broadened"], False)
              and r.boolean(plan["initialize_missing"], False) and r.boolean(plan["gateway_replacement"], False), "plan")
    # Hash is only a transient integrity binding in operator approval. Never
    # emit/persist it as a stable account/config pseudonym or support artifact.
    return hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def approval_gate(review, run, candidate, policy, plan_hash):
    r.require(r.boolean(review["authenticated"], True) and review["operator_id"] == policy["operator_id"]
              and review["environment_id"] == policy["environment_id"]
              and review["run"] == run and review["attempt"] == 1 and type(review["attempt"]) is int
              and review["state"] == "approved" and r.boolean(review["bypassed"], False)
              and review["candidate"] == candidate and review["plan_hash"] == plan_hash
              and r.boolean(review["downtime_approved"], True) and r.boolean(review["original_volume_key_approved"], True), "approval")


class Deployment:
    def __init__(self, port, policy, candidate, run, attempt=1, enabled=False):
        self.port, self.policy, self.candidate = port, policy, candidate
        self.run, self.attempt, self.enabled = run, attempt, enabled
        self.used = False

    def preflight(self):
        r.require(r.boolean(self.enabled, True), "disabled")
        r.require(self.attempt == 1 and type(self.attempt) is int and not self.used
                  and r.boolean(self.port.prior_mutation(self.run, self.candidate), False), "rerun")
        try:
            policy_gate(self.port.policy(), self.policy)
        except r.Stop:
            raise
        except Exception:
            raise r.Stop("policy") from None
        r.candidate_gate(self.port, self.candidate)
        try:
            before = self.port.get()
        except Exception:
            raise r.Stop("control_read") from None
        try:
            stable(before, self.policy)
            r.require(before["repository"] == self.candidate["repository"], "target")
        except r.Stop:
            raise
        except Exception:
            raise r.Stop("target") from None
        try:
            plan = self.port.plan(self.candidate)
            plan_hash = plan_gate(plan, before, self.candidate, self.policy)
        except r.Stop:
            raise
        except Exception:
            raise r.Stop("plan") from None
        return before, plan, plan_hash

    def approved(self, plan_hash):
        try:
            approval_gate(self.port.approval(self.run), self.run, self.candidate, self.policy, plan_hash)
        except r.Stop:
            raise
        except Exception:
            raise r.Stop("approval") from None

    def protected(self):
        before, plan, plan_hash = self.preflight()
        self.approved(plan_hash)
        # Re-read all authority AFTER authenticated approval and directly before
        # invocation: main, signed provenance, protection, target and plan.
        current, current_plan, current_hash = self.preflight()
        r.require(current == before and current_plan == plan and current_hash == plan_hash, "plan")
        self.approved(current_hash)
        self.used = True  # An exception/response loss cannot reopen this session.
        try:
            receipt = self.port.update_once(self.candidate, plan)
        except Exception:
            raise r.Stop("update_unknown", outcome="unknown") from None
        if not (isinstance(receipt, dict) and set(receipt) == {"accepted"} and r.boolean(receipt["accepted"], True)):
            raise r.Stop("update_unknown", outcome="unknown")
        try:
            after = self.port.get()  # No polling, no automatic mutation retry.
            if not r.boolean(after.get("mutation_complete"), True):
                raise r.Stop("completion_unknown", outcome="unknown")
            if any(after[k] != before[k] for k in STABLE):
                raise r.Stop("configuration_changed", outcome="completed")
            if after.get("tag") != self.candidate["tag"] or after.get("status") != "running" or not r.boolean(after.get("pending"), False):
                raise r.Stop("completion_unknown", outcome="unknown")
            # Authoritative completion and later application readiness are
            # separate. Readiness does not establish endpoint attestation.
            if not r.boolean(after.get("original_volume_unlocked"), True) or not r.boolean(self.port.ready(self.candidate), True):
                raise r.Stop("readiness", outcome="completed")
        except r.Stop as error:
            if error.outcome == "no_effect":
                error.outcome = "unknown"
            raise
        except Exception:
            raise r.Stop("completion_unknown", outcome="unknown") from None
        return {"stage": "replacement", "outcome": "completed", "mutation_count": 1,
                "original_volume_preserved": True, "ready": True, "runtime_verified": False}

    def status(self):
        # Passive read only. No preflight, plan, approval, update or readiness RPC.
        try:
            view = self.port.get()
            return {"stage": "status", "running": view.get("status") == "running",
                    "completion_confirmed": r.boolean(view.get("mutation_complete"), True),
                    "readiness": "unknown", "runtime_verified": False}
        except Exception:
            raise r.Stop("control_read") from None


def main():
    if sys.argv[1:] == ["status"]:
        print(json.dumps({"stage": "local_artifacts", "deploy_enabled": False,
                          "live_adapter": False, "production_readiness": "unknown"}))
    elif sys.argv[1:] in (["preflight"], ["protected"]):
        r.require(os.environ.get("PRODUCTION_DEPLOYMENT_ENABLED") == "true"
                  and os.environ.get("RELEASE_AUTOMATION_ENABLED") == "true", "disabled")
        raise r.Stop("prerequisites")
    else:
        raise r.Stop("arguments")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(json.dumps(r.diagnostic(error)))
        sys.exit(1)
