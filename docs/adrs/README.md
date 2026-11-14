# Spyglass Architecture Decision Records

This folder breaks the v1 work of Spyglass into architecture decision records (ADRs). Each ADR is one reviewable implementation unit: a decision that is already taken, or an open question that needs a decision before the related work is built. Together, the "Work required" lists of the ADRs cover all of v1.

The architecture documents describe the design: [llm-proxy-design.md](../llm-proxy-design.md), [backend.md](../architecture/backend.md), [frontend.md](../architecture/frontend.md), [upstream-connection.md](../architecture/upstream-connection.md) and [deployment.md](../architecture/deployment.md), with the decision list in [architecture/README.md](../architecture/README.md). An ADR summarizes a decision, lists the work it needs, and points to the document sections for detail. When sources disagree, one rule decides: landed code wins, then Completed ADRs, then the architecture documents for work not yet built, then ADRs not yet built. The order ranks sources as descriptions of current behaviour, and the lower source needs a fix. An Accepted ADR that amends landed code records the planned target: its decision wins over the code until its Work required lands, after which code and ADR agree. A Completed ADR is never edited; a change to one is a new ADR that names the Completed ADR it amends ([ADR 0049](0049-source-precedence-and-corrections-to-completed-adrs.md)).

Format: Michael Nygard style, one Markdown file per ADR, named `NNNN-kebab-title.md`. Numbers follow a logical build order: repository and core, configuration, the data plane (auth, correlation, passthrough, tap, extractor, recorder), storage, the admin API, the UI, deployment and tests, then firstmate integration, then the accepted decisions, then the amendments to Completed ADRs.

## Template

```markdown
# ADR NNNN: Title

Status: Accepted | Proposed | Completed | Deferred | Superseded by ADR NNNN

Date: YYYY-MM-DD

## Context
Why the decision is needed. Forces and constraints.

## Decision
What we do. For a Proposed ADR: the recommended option if the documents give one, and the alternatives.

## Consequences
What gets easier, what gets harder, what stays open.

## Work required
Small work items that can become tasks.

## Depends on
ADRs that must land first.

## References
Document sections (file and section name), not restated detail.
```

## Status meanings

| Status | Meaning |
|---|---|
| Accepted | The documents state this as decided. |
| Completed | The ADR's implementation work has landed on main. |
| Proposed | An open question or gap. The ADR lists the recommended option where the documents give one, and the alternatives. A gap with no recommendation stays Proposed with its options. |
| Deferred | Out of v1 and kept for the record. The ADR names its trigger for being picked up, in [backend.md, Later versions](../architecture/backend.md#later-versions). No v1 work depends on it. |
| Superseded | Replaced by a later ADR (none yet). |

An ADR changes status when a decision is made. Record the outcome in the ADR and in the architecture document it touches. A Completed ADR is never edited: where a decision changes one, write a new ADR whose Context names the Completed ADR it amends and whose "Work required" lists the code change. An ADR moves to Completed in the same change that lands its implementation work.

## Index

"Depends on" lists the ADRs that must land first.

| # | Title | Status | Depends on |
|---|---|---|---|
| [0001](0001-go-module-and-repository-skeleton.md) | Go module and repository skeleton | Completed | - |
| [0002](0002-v1-scope-and-metadata-only-storage-model.md) | v1 scope and the metadata-only storage model | Completed | 0001 |
| [0003](0003-core-domain-and-storage-ports.md) | Core domain types and storage ports | Completed | 0001, 0002 |
| [0004](0004-configuration-file-and-secret-references.md) | Configuration file and secret references | Completed | 0001, 0003 |
| [0005](0005-config-snapshot-and-sighup-reload.md) | Config snapshot and `SIGHUP` reload | Completed | 0004 |
| [0006](0006-http-listeners-timeouts-and-request-lifecycle.md) | HTTP listeners, timeouts and request lifecycle | Completed | 0004, 0003 |
| [0007](0007-anthropic-error-classes-and-error-bodies.md) | Error classes and protocol-correct error bodies | Completed | 0003 |
| [0008](0008-virtual-key-auth-and-keyring.md) | Virtual-key auth and the keyring | Completed | 0003, 0005, 0007 |
| [0009](0009-cli-keygen-and-version.md) | CLI subcommands: `keygen` and `version` | Completed | 0001, 0004, 0008 |
| [0010](0010-correlation-headers.md) | Correlation headers | Completed | 0003, 0006 |
| [0011](0011-proxy-passthrough-and-streaming.md) | Proxy passthrough and streaming | Completed | 0005, 0006, 0007, 0008, 0010 |
| [0012](0012-client-passthrough-and-subscription-setup.md) | Client passthrough mode and subscription setup | Completed | 0004, 0008, 0011 |
| [0013](0013-tap-incremental-response-scanner.md) | Tap: incremental response scanner | Completed | 0011 |
| [0014](0014-extractor-and-call-record.md) | Extractor and the call record | Completed | 0003, 0007, 0010, 0013 |
| [0015](0015-pricing-and-cost-computation.md) | Pricing and cost computation | Completed | 0003, 0004 |
| [0016](0016-recorder-queue-and-batch-writer.md) | Recorder: live fan-out, bounded queue and batch writer | Completed | 0003, 0014 |
| [0017](0017-postgres-store-schema-and-write-path.md) | Postgres store: schema, migrations and write path | Completed | 0002, 0003, 0004 |
| [0018](0018-contract-test-suite-and-postgres-test-harness.md) | Contract test suite and Postgres test harness | Completed | 0003, 0017 |
| [0019](0019-postgres-callreader-lists-filters-and-pagination.md) | Postgres `CallReader`: lists, filters, pagination and call detail | Completed | 0017, 0018 |
| [0020](0020-postgres-read-time-derivation-tools-sessions-agents.md) | Postgres read-time derivation: tool pairing, sessions and agents | Completed | 0019 |
| [0021](0021-postgres-usage-aggregation.md) | Postgres usage aggregation | Completed | 0019 |
| [0022](0022-retention-job.md) | Retention job | Completed | 0004, 0017, 0018 |
| [0023](0023-admin-plane-skeleton-openapi-and-call-log-endpoints.md) | Admin plane skeleton, OpenAPI contract and call-log endpoints | Completed | 0003, 0004, 0006, 0015, 0019 |
| [0024](0024-session-tool-call-and-usage-endpoints.md) | Session, tool-call and usage endpoints | Completed | 0020, 0021, 0023 |
| [0025](0025-live-tail-sse-stream.md) | Live tail (`GET /admin/stream`) | Completed | 0003, 0016, 0023 |
| [0026](0026-observability-health-readiness-and-metrics.md) | Observability: health, readiness and Prometheus metrics | Completed | 0006, 0016, 0017 |
| [0027](0027-graceful-shutdown-and-drain.md) | Graceful shutdown and drain | Completed | 0006, 0011, 0016, 0026 |
| [0028](0028-composition-root-and-serve-startup.md) | Composition root and `serve` startup | Completed | 0005, 0008, 0011, 0016, 0017, 0022, 0023, 0026, 0027 |
| [0029](0029-ui-scaffold-embedding-and-serving.md) | UI scaffold, embedding and serving | Completed | 0001, 0006, 0023 |
| [0030](0030-ui-foundation-routing-api-client-and-tables.md) | UI foundation: routing, typed API client, query state and tables | Completed | 0023, 0029 |
| [0031](0031-ui-v1-call-log-and-live-toggle.md) | UI view V1: call log and live toggle | Completed | 0024, 0025, 0030 |
| [0032](0032-ui-v3-call-detail.md) | UI view V3: call detail | Completed | 0023, 0030 |
| [0033](0033-ui-v2-session-list-and-session-view.md) | UI view V2: session list and session view | Completed | 0024, 0030 |
| [0034](0034-ui-v4-tool-call-explorer.md) | UI view V4: tool-call explorer | Completed | 0024, 0030 |
| [0035](0035-ui-v5-cost-and-cache-and-config-page.md) | UI view V5: cost and cache, and the read-only config page | Completed | 0024, 0030 |
| [0036](0036-container-image-and-release-build.md) | Container image and release build | Completed | 0028, 0029 |
| [0037](0037-deployment-guide-compose-runbook-and-personal-setup.md) | Deployment guide: compose file, runbook and personal local setup | Completed | 0009, 0012, 0036 |
| [0038](0038-test-fixtures-fake-upstream-and-end-to-end-tests.md) | Test fixtures, fake upstream and end-to-end tests | Accepted | 0011, 0013, 0014, 0016, 0017, 0028 |
| [0039](0039-performance-targets-and-load-verification.md) | Performance targets and load verification | Accepted | 0011, 0013, 0038 |
| [0040](0040-firstmate-integration-claude-code-routing-and-ledger-reporter.md) | Firstmate integration: Claude Code routing and the ledger-join reporter | Accepted | 0008, 0009, 0037, 0024 |
| [0041](0041-per-task-key-injection-into-crewmates.md) | Per-task key injection into firstmate crewmates | Deferred | 0040 |
| [0042](0042-claude-code-hint-headers-for-crewmates.md) | Claude Code hint headers for firstmate crewmates (D5) | Accepted | 0010, 0040 |
| [0043](0043-short-tool-snippets.md) | Short tool snippets (D11) | Accepted | 0014, 0034 |
| [0044](0044-provider-error-message-text.md) | Provider error message text (D12) | Accepted | 0014, 0032 |
| [0045](0045-opt-in-per-key-body-capture.md) | Opt-in per-key body capture (D13) | Accepted | 0002, 0014 |
| [0046](0046-adopt-gap-review-defaults.md) | Adopt the gap-review defaults (D9 and D10) | Completed | 0002 |
| [0047](0047-credential-modes-one-fixed-auth-mode-per-provider.md) | Credential modes: one fixed auth mode per provider | Completed | 0004, 0008, 0012 |
| [0048](0048-call-record-and-filter-changes.md) | Call record and filter changes | Completed | 0003, 0010, 0014, 0017 |
| [0049](0049-source-precedence-and-corrections-to-completed-adrs.md) | Source precedence and corrections to Completed ADRs | Completed | 0001, 0003, 0004 |
| [0050](0050-upstream-identity-encoding.md) | Upstream identity encoding | Completed | 0011, 0013 |
| [0051](0051-recorder-isolates-unwritable-records.md) | Recorder isolates unwritable records | Completed | 0003, 0016, 0017, 0026 |
| [0052](0052-ui-overview-dashboard-as-landing-page.md) | UI overview dashboard as the landing page | Accepted | 0024, 0025, 0030, 0031, 0033 |
| [0053](0053-spyglass-brand-palettes-and-theme-switching.md) | Spyglass brand, palettes and theme switching | Accepted | 0029, 0033, 0052 |
| [0054](0054-rename-to-spyglass.md) | Rename llm-proxy to Spyglass | Accepted | 0053 |
| [0055](0055-agent-facing-cli-axi-toon-output.md) | Agent-facing CLI with AXI (TOON) output | Completed | 0009, 0019, 0021, 0023, 0024, 0052, 0054, 0056 |
| [0056](0056-session-unpriced-calls.md) | Unpriced calls per session | Completed | 0020, 0021, 0024, 0052 |
| [0057](0057-cost-per-prompt-endpoint.md) | Cost per prompt endpoint | Completed | 0014, 0019, 0021, 0024, 0035, 0047, 0052 |

## Suggested implementation order

The phase numbers below are the v1 plan, with these meanings: 1 proxy core, 2 cost and tool events, 3 admin API and UI, 5 firstmate routing. Phase 4 (cache diagnosis), 6 (alerts), 7 (per-task key injection, ADR 0041) and 8 (MCP gateway) are out of v1. Items inside a phase can run in parallel where "Depends on" allows.

1. **Foundations:** 0001, 0002, 0003, then 0004 and 0005 (config and reload), 0006 (listeners).
2. **Proxy core (phase 1):** 0007 (error bodies), 0008 (auth), 0009 (keygen), 0010 (correlation), 0011 (passthrough and streaming). In parallel, the storage track: 0017 (schema and write path) and 0018 (contract suite and harness).
3. **Recording (phases 1 and 2):** 0013 (tap), 0014 (extractor), 0015 (pricing), 0016 (recorder). Then 0012 (client passthrough, needed for the personal setup), 0026 (health and metrics), 0027 (graceful shutdown), 0028 (composition root). The first end-to-end run is possible here.
4. **Read side (phase 3):** 0019, 0020, 0021 (readers), 0022 (retention), then 0023, 0024, 0025 (admin API and live tail).
5. **UI (phase 3):** 0029, 0030, then 0031 to 0035 (views, mostly in parallel), then 0052 (the overview dashboard at `/ui`, after the views it links to), then 0053 (the Spyglass brand, the light theme and the theme toggle). 0054 (the rename of every technical name from `llm-proxy` to `spyglass`) follows release 0.1.0.
6. **Ship and verify:** 0036 (image), 0037 (deployment guide), 0038 (e2e tests and fixtures), 0039 (performance).
7. **Firstmate (phase 5):** 0040 (routing and ledger reporter), with keys issued by hand. v1 ends at 0040.
8. **Accepted decisions:** 0042 to 0045 are Accepted with their recommended options: hint headers on for crewmates (0042, D5), no tool snippets (0043, D11), no provider error text (0044, D12), and no per-key body capture (0045, D13). The gap-review defaults (0046, D9, D10) are Completed: their decisions are recorded in the ADR table. They need no phase: the ADRs they touch already assume them. Set the hint-header variable when 0040 first routes Claude Code through the proxy.
9. **Amendments to Completed ADRs:** 0047 (credential modes) and 0048 (call record and filter changes) are Completed: they changed code that had already landed, and their "Work required" lists landed with the ADRs they edit in place: 0047 with 0012, 0048 with 0010, 0014, 0016, 0019 and 0020. 0048 lands before 0025, whose stream carries `call.finished` only. 0049 (precedence and corrections) is Completed: it needs no code. 0050 (upstream identity encoding) is Completed: it landed after 0011 and 0013. 0051 (recorder isolates unwritable records) is Completed: it amends 0016 and the `WriteBatch` error contract of 0003 and 0017.
10. **Agent-facing CLI (after v1):** 0056 (the `unpriced_calls` field on the session summary), then 0055, after 0054 (the `spyglass` binary name) and the admin API of 0023 and 0024. 0055 covers the shared decisions and phase 1 (orient and look up). Install for an agent (`setup hooks`, agent skill) and the analysis commands are listed under "Later" in 0055, and each becomes its own ADR when its trigger is met.
11. **Cost per prompt (after v1):** 0057 (`GET /admin/usage/prompts`, so the cost view's prompt table covers the whole range), after 0035 and 0024.
12. **Deferred:** 0041 (per-task key injection) is out of v1. Its trigger is "manual key issuing becomes the bottleneck".

The test harness of 0018 and the fixtures of 0038 are used by most other ADRs, so start them early.

## Out of v1 scope

These items are deferred and have no ADR, except per-task key injection (Deferred ADR 0041). Each has a trigger for adding it back in [backend.md, Later versions](../architecture/backend.md#later-versions); the v1 design leaves room for all of them.

- Gateway features: fallback, retries and circuit breakers with `request_attempts`; model aliases and request rewriting (C6); budgets, rate limits, concurrency limits and key expiry; alerts, `/admin/alerts` and the firstmate alert adapter.
- Admin sign-in, admin scopes and OIDC (C7). The admin plane stays loopback-only.
- OTel span export.
- Cache-miss diagnosis: segment hashing, `call_segments`, `CacheDiff`, the cache-analysis endpoint and the UI cache view (C5).
- Storage and scale: rollup tables, daily partitioning, `CopyFrom` staging, a materialised tool-call pairing table, `sync.Pool` buffers in the tap, the multi-replica tier with a `LiveFeed` port, `Last-Event-ID` catch-up, backward paging and exact counts above 10,000, the sub-agent link to the spawning `Task` call.
- `store/sqlite`, the MCP gateway (`source = mcp`) and OIDC.
- The OpenAI family (v2): `family/openai`, `include_usage` handling, Codex CLI and OpenCode setup and their session headers (C4, D3, D4).
- Derived session keys for clients without a session header, live-tail rows for in-flight calls (`call.started`), and per-task key injection into crewmates (ADR 0041).
- Saved views, annotations and scores, editing pricing in the UI, static HTML export of a session.
