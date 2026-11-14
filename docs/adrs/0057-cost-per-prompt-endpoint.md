# ADR 0057: Cost per prompt endpoint

Status: Completed

Date: 2026-11-14

## Context

The cost view (ADR 0035) has a "Cost per prompt" table. The usage endpoint (ADR 0021, ADR 0024) cannot group by prompt, so the view read one page of `GET /admin/calls` with `limit=500`, the API maximum, and grouped those calls by prompt id in the browser. On a range with more than 500 calls the table covered only the newest 500 calls. Prompts were missing, and the cost and call count of the prompts shown were too low, while every other panel of the view summed the whole range. Nothing in the table said so beyond a note under its title.

The fix must give the same answer for any range, so the aggregate moves to the server.

Two shapes were considered:

- **`group_by=prompt` on `GET /admin/usage`.** Rejected. The usage endpoint returns a time series: one bucket per group and time. A prompt table needs one row per prompt, and each row needs fields a bucket does not have: the session of the prompt and an API-equivalent marker. Prompt ids are also unbounded in number, so a series with one bucket per prompt and time can be very large. The `group_by` whitelist is shared with the CLI and its tests.
- **A dedicated endpoint.** Chosen. It returns one row per prompt, ranked, with a limit.

## Decision

- **`GET /admin/usage/prompts`** returns the cost of each prompt over every call that matches the call-log filters. It takes the same filter parameters as `GET /admin/calls` (`from`, `to`, `key`, `client`, `provider`, `model`, `endpoint`, `streamed`, `status`, `error_class`, `session`, `agent`, `agent_type`, `prompt`, `request_class`, `tool`, `tool_error`, `latency_ms_gte`, `cost_micro_usd_gte`) and `limit`. It takes no `cursor`, `group_by` or `grain`. `limit` is 1 to 500 and defaults to 50, as for the other lists.
- **The response** has `prompts`, `prompt_count` and `calls_without_prompt`. `prompt_count` is the number of prompts in the range, including those past `limit`. `calls_without_prompt` is the number of matching calls whose prompt id is null or empty. Such a call belongs to no prompt.
- **A prompt row** has `prompt_id`, `session_key` (the session of the prompt's first call, empty when it has none), `calls`, `cost_micro_usd`, `unpriced_calls` and `api_equivalent`.
- **Unknown is not zero** (ADR 0052), with the semantics of the usage buckets. `cost_micro_usd` sums the calls with a known cost only, `unpriced_calls` counts the calls it leaves out, and `cost_micro_usd` is null when no call of the prompt is priced. A priced call of cost 0 is known.
- **`api_equivalent`** is true when at least one priced call of the prompt has `auth_mode = client_passthrough`. The calls table already records the auth mode of each call (ADR 0014, ADR 0047), so the server needs no provider list and the client no longer joins the config view to find it. An unpriced passthrough call sets nothing: it has no cost to mark.
- **Order and bound.** Prompts with a known cost come first, highest first, then prompts with an unknown cost. Ties go to the prompt with more calls, then to the lower prompt id. The server returns the first `limit` prompts of that order. The result is bounded in size, and `prompt_count` says how many were left out, so a client never has to imply full coverage.
- **A new port method**, `CallReader.PromptCosts(ctx, PromptCostQuery) (PromptCosts, error)`, derived at read time like every other view (ADR 0020). The PostgreSQL adapter runs one grouped select over `calls` with the filter of ADR 0019, ranks and limits it in SQL, and counts the calls without a prompt id in a second query of the same read-only repeatable-read transaction. There is no new column, index or migration. An invalid query (an empty range or a `limit` below 1) returns `ErrInvalidFilter`.
- **The UI** asks for `limit=25`, the rows the table shows, removes the 500-call sample and the client-side grouping, and says plainly when the table lists fewer prompts than the range has: "Showing the 25 most costly of N prompts; N-25 more are not shown." The panel note reads "25 of N prompts".

## Consequences

- **The table is right on any range.** Its prompts, call counts and costs come from the same calls as the other panels.
- **The cost of one more aggregate.** The query reads the calls of the range once for the prompts and once for the calls without a prompt id, like the usage query. It is meant for an interactive range and, like the usage endpoint, can take a few seconds on a very long one. There is no new index. If it becomes slow, the answer is a rollup table, which the architecture already lists as a later step.
- **The API grows by one path.** It is additive: an older client does not call it. The CLI of ADR 0055 does not use it and is not changed.
- **The earlier `PromptSummary` of a session** (ADR 0024) is unchanged. It lists the prompts of one session and has no `unpriced_calls` or `api_equivalent`. Adding them is not part of this ADR.

## Work required

- `internal/core`: add `PromptCostQuery` and its `Validate`, `PromptCost` and `PromptCosts`, and `PromptCosts` on the `CallReader` port. Add it to the `coretest` fake.
- `internal/store/postgres/usage.go`: implement `Store.PromptCosts` as above.
- `internal/api`: add `GET /admin/usage/prompts` (`getPromptCosts`, `parsePromptCostParams`, the wire types).
- `api/openapi.yaml`: add the path and the `PromptCost` and `PromptCosts` schemas, then regenerate `web/src/api/schema.gen.ts` with `make ui-gen`.
- `internal/store/storetest`: cases for more calls than one page of the call list, a mix of priced and unpriced calls, calls without a prompt id, the limit, the API-equivalent marker and an invalid query. Add the API handler, parameter and conformance tests.
- `web/src/views`: read the endpoint in `CostView.tsx`, remove `PROMPT_SAMPLE` and `promptCosts`, and say when the table is bounded.
- Mark this ADR Completed in the change that lands the work above.

## Depends on

- ADR 0019 (the call filter), ADR 0021 (the usage aggregate and its unpriced count), ADR 0024 (the usage endpoint), ADR 0014 (the call record), ADR 0035 (the cost view), ADR 0047 (the auth modes), ADR 0052 (unknown is not zero).

## References

- `internal/store/postgres/usage.go` (`Usage` and `PromptCosts`), `internal/api/handlers_views.go` (`getPromptCosts`).
- `web/src/views/CostView.tsx`, the "Cost per prompt" panel.
