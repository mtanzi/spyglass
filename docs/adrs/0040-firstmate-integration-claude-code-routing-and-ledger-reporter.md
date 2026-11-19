# ADR 0040: Firstmate integration: Claude Code routing and the cost reporter

Status: Accepted

Date: 2026-10-21

## Context

The first real consumer of v1 is firstmate, which runs many Claude Code crewmates in parallel. Per-key attribution pays off there. The design plan has two v1 steps after the admin API: route one firstmate harness (Claude Code) through the proxy, and write a reporter that joins a task list with the proxy's call data. Firstmate's fleet ledger is the first task list, but Spyglass must also serve people who do not run firstmate, so the ledger is one input among several. Firstmate is not a plugin target: the proxy runs as an independent service.

## Decision

- The v1 firstmate route is `proxy_key` with a Console API key held by the proxy, for crewmates. `client_passthrough` stays for one person's own interactive Claude Code (ADR 0012). Crewmates set `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1` (ADR 0042).
- Each crewmate gets its own virtual key named `fm:<task-id>`, issued by hand: one config entry per key plus `SIGHUP`. Names stay stable. v1 ends at this ADR; per-task key injection is deferred (ADR 0041).
- A standalone reporter, `cmd/spyglass-report` in the same module, produces cost per task and cost per merged PR. It has three parts with one seam between them:
  - **Cost attribution** (`internal/costreport`) is one deep module with a small interface: `Attribute(tasks, calls)` takes tasks and calls and returns a plain-data `Report`. It joins a call to a task when the key names match and the call time lies in the task's half-open window `[Start, End)`. A call that fits several windows is counted once, for the task with the latest start. It reports calls with no task and tasks with no calls, counts an unknown cost separately from zero, and sums the tasks of a PR into the cost per merged PR. Records with the same task id are one task, so a duplicated record changes nothing. The module knows no firstmate, no file format and no SQL, and does no I/O.
  - **`TaskSource`** is the seam for the task list. Each adapter turns one kind of input into tasks (id, key name, window, optional merged PR) and treats its input as untrusted: it skips a record it cannot use and names it in the warnings.
  - **Output adapters** take a finished `Report` and write it as a table, JSON or CSV. They compute nothing.
- Two `TaskSource` adapters ship:
  - The **firstmate ledger** adapter reads `state/fleet-ledger.jsonl` (events `task.dispatched`, `task.status`, `task.pr_ready`, `task.merged`, `task.cleaned_up`). It tolerates duplicate records (at-least-once delivery), and it alone owns the `fm:<task-id>` key naming. A task's window starts at its first record and ends at `task.cleaned_up`, else `task.merged`, else it stays open. A `task.merged` record with `via` `pr` names the merged PR.
  - The **file** adapter reads a CSV or JSON file with, per task, an id, a start, an optional end, an optional merged PR and an optional key name (default: the task id). This is the input for anyone who tracks tasks some other way.
- The reporter picks the input and the output with flags: `--tasks firstmate:<path>` or `--tasks file:<path>`, `--from` and `--to` for the call window (default: from the earliest task start until now), and `--format table|json|csv`.
- The reporter reads call data through the admin API (`GET /admin/calls`, which `CallReader` serves), never SQL directly. It needs no admin API change: it reads every call in the window and joins by key name itself.
- The alert adapter (`process-event-adapter`) is a later item because alerts are deferred.

## Consequences

- A call whose key name no task claims is reported, not hidden: this includes traffic of keys the task list does not know. A task with no calls usually means the crewmate bypassed the proxy or used another key.
- Harnesses that authenticate through a vendor subscription login may bypass the proxy. Each harness must be verified before relying on it; this is the largest integration risk.
- Keys are issued by hand until manual key issuing becomes the bottleneck. Injecting the environment and a key into each crewmate at launch is Deferred ADR 0041.

## Work required

- Verify end to end that Claude Code started with the documented environment appears in the call log, with session, agent and the hint-header fields filled. Capture the fixtures for ADR 0038.
- Write the key naming and issuing procedure for `fm:<task-id>` keys into the runbook (ADR 0037).
- Implement the reporter: the attribution module, the firstmate ledger and file adapters, and the table, JSON and CSV output.
- Test attribution through its interface with fake tasks and calls (tasks without calls, calls without tasks, duplicate task records, overlapping tasks, a task without a merged PR). Test each adapter on its own: the ledger parser against synthetic ledgers with duplicates and malformed lines, the file adapter, and each formatter against a fixed `Report`.
- Document known traffic that bypasses the proxy (fast-mode check, WebFetch domain check, telemetry).

## Depends on

- ADR 0008, ADR 0009, ADR 0037, ADR 0024.

## References

- llm-proxy-design.md, sections 2.1 to 2.4.
- architecture/upstream-connection.md, "Claude Code with a proxy-held API key".
- architecture/backend.md, "Package layout" (later executables under `cmd/`).
