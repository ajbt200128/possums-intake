# Development-agent failure-record guidance

Failure entries have moved to the operator-authorized **external, local-only** log `/Users/mb5/projects/possums-intake-agent-failures.md`. This repository file is guidance only: do not append entries, development task/thread/run IDs or transcript locations here or elsewhere in the repository.

Read [PRIVACY.md](../PRIVACY.md#development-agent-failure-records) and `AGENTS.md` before recording a failure. Only that specific external file is authorized; no arbitrary sibling writes. Preserve all production privacy restrictions and safe diagnostic requirements. Do not copy transcripts, prompts, responses, raw exceptions, full tool output, secrets, customer data, headers, URLs, account/payment/provider-request identifiers, exact request timestamps, monetary values or telemetry payloads.

## External entry template

Append one manually inspected record per observed failed development packet/attempt. Separate service/tool/terminal failures from implementation/test failures; a completed native run can still contain a failed development attempt. Record unknowns rather than manufacture codes, statuses, causes or accounting outcomes.

- **Entry / coarse date:** local sequence and UTC calendar date.
- **Packet / attempt:** locally authored label, attempt number and expected bounded deliverable, not a quoted prompt.
- **Selected model / baseline:** configured provider/model, public dependency versions and source commit or uncommitted baseline; no environment dump or private configuration.
- **Failure class / safe diagnostic:** evidence-supported category, known safe code/stage/constraint and observed HTTP status if available; otherwise unavailable/unknown. No raw exceptions or offending values.
- **Expected versus observed / source location:** short locally authored description and repository-relative component/file/symbol, not copied output.
- **Work state / disposition:** original task's actual state, reviewed partial edits, separately authorized fallback, fix and focused verification; preserve unknowns and unresolved objections.
- **Reproduction / evidence:** named synthetic/offline tests and credential-free commands, sanitized assertion/exit category and evidence limits. A live provider failure does not authorize another request to reproduce it.
- **Inference/accounting evidence:** authoritative observed state only, otherwise unknown; uncertain delivery does not establish cancellation/refund or safe retry. No financial amounts or receipts.
- **Native references (external log only):** verified development task/thread/run IDs and task-to-storage mappings. Check actual file existence before recording a native transcript pointer. If records are database-backed, say so and use the verified database/native reference; if storage is unlocated, record that limit and retain the verified native task reference. Never invent a path or copy transcript contents.

Native T3/Pi history remains a separate, potentially sensitive host-controlled persistence boundary. Pointers do not establish whole-host transience or authorize sharing, export or transcript duplication. Keep the external log outside version control and local; sharing requires separate authorization.

Logging or inspecting failures initiates no inference, retry, telemetry export or gateway changes and changes no accounting/recovery policy. Confirm original work is terminal before fallback; inspect and preserve reviewed partial edits rather than racing active work. A separately authorized subsequent attempt may incur another charge after unknown delivery.
