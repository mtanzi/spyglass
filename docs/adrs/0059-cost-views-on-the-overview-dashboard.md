# ADR 0059: Cost views on the overview dashboard

Status: Proposed

Date: 2026-11-24

## Context

The overview at `/ui` (ADR 0052) answers "how is the proxy doing right now". It works on one short preset range (1h, 24h, 7d or 30d) and its cost content is a headline tile, the activity chart with the `cost` metric, and the "Cost breakdowns" lists by model, client and key. The top 5 sessions by cost come from `GET /admin/usage?group_by=session&grain=day`, which returns one bucket per session and day. On the sizing dataset (1.5 million calls in 30 days, 4,000 sessions) that is 120,000 buckets and the slowest usage query measured (1.5 s median, 1.9 s slowest; deployment.md, "Sizing"). The browser then ranks them.

The captain wants the landing page to answer a second question: where does the money go over months, projects, hours of the week, sessions and tools. The wanted views are:

- a chart of spend per month, and the most costly month;
- spend per project, and the most costly project;
- a punchcard of cost by hour of day across the days of the week;
- a list of the most costly sessions;
- a list of the most costly tools;
- other information worth showing, as suggestions.

The captain also wants the final look to be produced later in the implementation, with Claude design. This ADR decides the data, the API, the layout slots, the states and the phases. It does not decide the visual design.

What the current code gives, view by view:

| View | Existing data | Gap |
|---|---|---|
| Spend per month | `GET /admin/usage` has grains minute, hour and day, aligned in UTC. | No month grain. A month in the reader's time zone cannot be built from UTC days. The same query also computes three percentiles per bucket with `percentile_cont`, which a cost chart does not need. |
| Spend per project | None. The `calls` table has no project column. The nearest fields are `key_name`, `client` and `session_key`. | A project is not recorded anywhere. Request bodies are never stored (ADR 0002), so the working directory that Claude Code puts in its system prompt is read by nothing and kept by nothing. |
| Punchcard | None. | Needs a grouping by weekday and hour of day in a time zone. |
| Costly sessions | `GET /admin/sessions` is ordered by last call time, with a keyset cursor. `GET /admin/usage?group_by=session` returns every session. | No cost ranking with a limit. The session list rows already carry `cost_micro_usd` and `unpriced_calls` (ADR 0056). |
| Costly tools | `GET /admin/tool-calls/summary` has counts, errors and durations per tool, and no cost. `tool_events` rows have no cost. | A tool call has no cost of its own: the proxy sees model calls, and tools run in the client. A rule is needed that gives a tool a cost. |

Constraints that carry over:

- **Unknown is not zero** (ADR 0052, ADR 0056, ADR 0057). Cost sums the calls with a known cost, `unpriced_calls` counts the rest, and cost is null when no call is priced.
- **The API-equivalent label** (ADR 0012, ADR 0047). A cost that includes a priced `client_passthrough` call is not a bill.
- **Aggregates are read-time queries over raw rows** (ADR 0021). Rollups return at the triggers in backend.md "Storage and scale": usage p95 above 1 s, or about 20 million rows.
- **Retention is 400 days** (ADR 0022), so 12 months of history exist and 24 months do not.
- **Charts are plain SVG with a table fallback and brass and teal series colours** (ADR 0035, ADR 0052, ADR 0053). No chart library is a dependency.

This ADR amends no Completed behaviour in a breaking way. It adds one column, one filter, one request header, one config field, one extractor rule, a one-off backfill and five endpoints. ADR 0010 (correlation headers), ADR 0014 (extractor and call record), ADR 0017 (schema), ADR 0019 (call filter), ADR 0021 (usage) and ADR 0024 (usage endpoints) are Completed and are not edited; this ADR names them as the ones it extends (ADR 0049). It also amends two statements of Completed ADRs, under the rules of ADR 0049:

- ADR 0010 says "the proxy never parses the body for correlation" and "the extractor hashes no tools, system prompt or first user message", and ADR 0014 says "the extractor hashes nothing". From this ADR on, the extractor reads the `system` field of the request to derive a project name (decision 3). It still hashes nothing and keeps nothing of the system prompt but that name.
- ADR 0002 says bodies are never stored. That stays true: no body, no system prompt text and no path is stored. The project name is a derived fact, like the model name or a tool name that the extractor already keeps.

It extends ADR 0052 and replaces its "top 5 sessions by cost" breakdown.

## Decision

### 1. Layout: a cost section on the overview

- The overview gets a **"Cost" section** between the activity row and the live feed. It has its own period, set in the `cost_range` search parameter: `3m` (default), `6m` or `12m`. A period is whole calendar months in the reader's time zone: the current month so far plus the previous N-1 months. The dashboard `range` keeps controlling everything else on the page. A 30-day preset cannot show "per month", and the two periods answer different questions, so they get two controls.
- Slots, in reading order:
  1. Two **headline tiles**: most costly month and most costly project. Each shows the name, the cost, its share of the period, and a link.
  2. **Spend per month**: a bar chart of the months of the period.
  3. **Spend per project**: a ranked list, top 10.
  4. **Punchcard**: cost by weekday (rows) and hour of day (columns).
  5. **Most costly sessions**: top 10.
  6. **Most costly tools**: top 10.
- On wide screens the tiles form one row; the month chart and the project list share the next row, then the punchcard spans the full width, then the two lists sit side by side. On a narrow screen the slots stack in the same order. The grid reuses `dash-grid` and `Panel`; the ranked lists reuse `RankedList`, which moves from `Dashboard.tsx` to `components/`.
- The existing "Cost breakdowns" (model, client, key) stay as they are. Their top-5 sessions list goes away, because slot 5 replaces it.
- **Panels load independently.** Each panel is its own query with its own loading, error and empty state (the `QueryState` pattern of ADR 0052). The cost section requests its data after the headline tiles have rendered, so a slow aggregate never blocks the first paint.

### 2. Time zone

- The new endpoints take a `tz` query parameter, an IANA name such as `Europe/Rome`. It defaults to `UTC`. The UI sends the browser's zone, `Intl.DateTimeFormat().resolvedOptions().timeZone`, and shows it in the section header ("Months and hours in Europe/Rome"). The zone is not a URL parameter, so a shared link shows each reader their own calendar.
- The server validates `tz` with `time.LoadLocation` and imports `time/tzdata`, so a scratch container image without a system zone database still works. An unknown name is a `400` with the existing error body. Postgres receives the name as a bind parameter and uses it in `date_trunc('month', c.ts, $tz)` (Postgres 12 and later; the compose file runs 16) and in `c.ts AT TIME ZONE $tz`. The name is never put in the SQL text.
- The UI computes the period boundaries with `Date` in the browser zone, which is the zone it sends, so `from` is the start of a local month and `to` is now. The response never asks the client to do calendar arithmetic in a zone other than its own.
- **Months are filled by the server.** Only the server can list the months of a range in an arbitrary zone without a date library, so `months` returns one row per month of `[from, to)`, including months with no calls (`calls: 0`, cost `0`). The usage endpoint leaves gaps (ADR 0021); this endpoint does not, because a chart of months with a missing bar reads as a missing month.
- **The punchcard groups by local clock time.** A cell is "Tuesday, 14:00 to 14:59 in the zone". On a DST change one hour of one day does not exist and one repeats; the repeated hour adds into the same cell and the skipped hour adds nothing. This is the meaning a reader gives to "what do I spend at 14:00", and the section header names the zone so the reading is not ambiguous.
- Existing charts format axis labels in the browser zone but aggregate in UTC (the day grain of ADR 0021). That mismatch exists today and this ADR does not change it. The new views are consistent with themselves: they aggregate and label in the same zone.

### 3. Project: what it is and where it comes from

The captain asks for cost per project, and Spyglass records no project. The key name does not say it (a firstmate key names a task, `fm:<task-id>`, not a project), and a header alone needs a settings change in every project. Claude Code already says where it runs: the environment block of its system prompt carries the primary working directory. Spyglass derives the project from it at ingest and stores only the derived name.

- **A call gets a `project`**: a nullable `text` column on `calls`. Only the derived name is stored. The request body, the system prompt text and the path are never stored.
- **Detection at ingest.** The extractor (`internal/family/anthropic`, ADR 0014) reads the `system` field of the request it already holds and parses: a string, or the text of an array of text blocks. It reads no other field and buffers nothing new: the tap already holds the request bytes for the existing extraction, and the scan uses `gjson` on them as the model lookup does. The scan is bounded: it reads at most the first 32 KiB of system text and stops at the first match. It looks for the working-directory line of Claude Code's environment block (a small table of known line prefixes, such as `Primary working directory:` and `Working directory:`, tested against a captured fixture under ADR 0038).
- **Reduction to a name.** When the block names a git remote, the name is the repository name of that remote (the last path segment, without `.git`). Otherwise it is the final directory name of the working directory (both `/` and `\` separate). The name is trimmed, capped at 128 characters, and must be printable characters only. A request with no system field, no match, or a value that fails these rules stores null. Detection never fails a request and never marks the record partial.
- **This reads body content at ingest and discards it.** The extractor already reads the body for the model, tool names and sizes and then discards the bytes (ADR 0002). Project detection is one more such read. What is stored is a name derived from a path, not the path. This is the amendment to ADR 0010 and ADR 0014 named in the context.
- **Precedence**, resolved once in the proxy before the record is written:
  1. The optional request header `x-spyglass-project`. It is read by the correlation function (ADR 0010) and follows the same rules: trimmed, at most 128 characters, printable only, and an invalid value counts as absent. It is **removed before the request goes upstream**, as `x-spyglass-key` is (upstream-connection.md, "Header handling"). A user sets it in `ANTHROPIC_CUSTOM_HEADERS`, in the shell or the untracked local settings file, to name a project that detection gets wrong.
  2. The detected name.
  3. The default of the key: an optional `project` field on a key in the config file (`keys[].project`). It is for firstmate per-task keys (`fm:<task-id>`) and other keys that serve one project. The config file is the only source of this mapping. The proxy already loads keys from it and reloads it on `SIGHUP`. The firstmate ledger adapter of ADR 0040 runs in the offline reporter, not in the proxy, so it is not a source.
  4. Otherwise the call has no project (null).
- **`project` joins the call filter** (`project`, repeated, like `client`), so the project list can link to the call log and the usage endpoints can filter by project.
- **One-off backfill of older calls.** An operator subcommand, `spyglass backfill-projects`, sits beside `serve` and `keygen`, not in the agent CLI of ADR 0055. It reads the same config file and database settings as `serve` and runs two SQL `UPDATE`s, in this order, each only on rows whose `project` is null:
  1. A call of a key with a `keys[].project` in the current config gets that project. This is the value ingest would have given the call, since an old call has neither a header nor a detected name.
  2. A call in a session (`session_key`) whose other calls have exactly one distinct non-null project gets that project. A session with no project or with two or more stays as it is.

  It is a command and not a goose migration, because step 1 needs the config, which migrations do not read. It is an update and not a read-time rule, because a read-time rule would have to be repeated in the five aggregates, the call filter, the call log and the call detail. The backfill cannot re-read bodies, so it never detects a new name: it only spreads names already known. It is idempotent: it writes only null rows, and after one run no null row meets either rule, so a second run with the same config changes nothing. It never overwrites a project that is set. Every other call stays "No project".

### 4. Endpoints: five dedicated aggregates

All five follow ADR 0057: one path per view, one row per group, ranked and bounded on the server, aggregated in SQL over `calls` (and `tool_events`), derived at read time with no rollup. All take `from`, `to` and the call-log filters of `GET /admin/calls`, and `tz` where the view depends on the calendar. All are `GET`, read-only and `/admin`-only like every other admin path. Cost fields follow the semantics of ADR 0057: `cost_micro_usd` sums the known costs and is null when no call is priced; `unpriced_calls` counts the rest; `api_equivalent` is true when at least one priced call has `auth_mode = client_passthrough`.

| Path | Rows | Order and bound | Extra fields |
|---|---|---|---|
| `GET /admin/usage/months` | One per calendar month of `[from, to)` in `tz`, gaps filled. | Oldest first. At most 36 months; a longer range is a `400`. | `month` (`2026-11`), `start`, `end` (UTC instants), `calls`, `cost_micro_usd`, `unpriced_calls`, `api_equivalent`, `partial` (true when the range cuts the month). |
| `GET /admin/usage/projects` | One per project. | Known cost first, highest first, then unknown cost; ties go to more calls, then lower name. `limit` 1 to 100, default 10. | `project`, `calls`, `sessions`, cost fields. Also `project_count`, and `unassigned` (`calls`, `cost_micro_usd`, `unpriced_calls`) for calls with no project. |
| `GET /admin/usage/punchcard` | One per non-empty (weekday, hour) cell, at most 168. | Weekday 1 (Monday) to 7 (Sunday), then hour 0 to 23. | `weekday`, `hour`, `calls`, cost fields. |
| `GET /admin/usage/sessions` | One per session. | As projects, `limit` 1 to 100, default 10. | The `SessionSummary` fields (ADR 0024, ADR 0056) plus `api_equivalent`; and `session_count`, the sessions of the range. |
| `GET /admin/usage/tools` | One per tool name. | As projects. | `tool`, `uses`, `errors`, `result_bytes`, cost fields (the attributed cost below). Also `tool_count`. |

Rules shared by the five:

- **Time range.** Half-open `[from, to)`, required, as everywhere. The call filters apply to the `calls` rows. For `tools`, the range applies to the time of the tool use (`tool_events.ts`), as in the tool-call list (ADR 0024), and the other filters to the call that carried the use.
- **Most costly month and project need no endpoint of their own.** The UI takes the month with the highest known cost from `months` and the first row of `projects`. A month or project whose `unpriced_calls` is above zero shows "partial". Ranking by known cost is stated in the tile ("by known cost"). When every row has an unknown cost the tile reads "unknown", not zero.
- **A project list with unassigned calls.** `unassigned` is reported beside the ranking, and the UI shows it as a final "No project" row. It is never the "most costly project": it is not a project.
- **Sessions reuse the session aggregate.** The query is the `sessionColumns` aggregate of ADR 0056, ranked and limited in SQL, so a session's cost, unpriced count and error count are the same numbers the session list and session view show. It replaces the 120,000-bucket request on the overview.
- **Tools get an attributed cost.** A tool call has no cost: a tool runs in the client, and the proxy sees only model calls. The rule is: the cost of a model call is split **equally among the tool uses that call requested**, and a tool's cost is the sum of its shares. A call with no tool use contributes to no tool.
  - **Rounding.** `cost_usd` is `numeric(12,6)`, so a call's cost `c` is a whole number of micro-USD. For a call with `n` uses, of which `k` are uses of tool `t`, the share of `t` is `floor(c × k / n)` micro-USD, computed in `numeric`. This is the one rounding. The remainder of the call, `c` minus the sum of its shares, is less than the number of distinct tools in the call, and it goes to the tool of that call that is first by name. A tool's cost is the sum of its whole-micro-USD shares, with no further rounding. So the tool costs plus the cost of calls without tool use equal the total cost exactly.
  - **Unpriced calls.** An unpriced call adds its uses to `uses` and adds nothing to cost. A tool's `unpriced_calls` counts the distinct unpriced calls that requested the tool at least once. A tool's cost is null when every call that requested it is unpriced.
  - **Label.** The cost is an estimate, and the UI says so. The column is labelled "Estimated cost of the calls that requested it", with a tooltip: "An estimate: a call's cost is split equally among its tool uses. This is not the cost the tool's result adds to later calls." The equal split is a convention. It is deterministic, it works for server tools whose results come in the same response, and the rounding rule above keeps it additive.
- **New port methods.** `CallReader` gains `MonthlyCosts`, `ProjectCosts`, `Punchcard`, `SessionCosts` and `ToolCosts`, each taking a query struct with a `Validate` that returns `ErrInvalidFilter` (an empty range, a bad `limit`, an unknown or empty `tz`, a month range over 36). The PostgreSQL adapter implements them with `buildWhere` of ADR 0019, so the filters behave as in the call log. The `coretest` fake gets the same methods.

### 5. Cost for large databases

Each endpoint reads the calls of its range once. The measured cost of one pass is about 1 s per 1.5 million rows (deployment.md, "Sizing"); the usage query adds percentile sorts that these queries do not need, so they are cheaper per row. At 50,000 calls a day the largest period, 12 months, is about 18 million rows, and a pass takes more than 10 s. Decisions:

- **Aggregate in SQL, return few rows.** Every response is at most a few hundred rows (36 months, 168 cells, 100 ranked rows). Nothing is ranked or summed in the browser.
- **No new index in the first version.** An aggregate over months reads most of the heap whatever the index, and `calls_ts_id_idx (ts DESC, id DESC)` already serves the range bound. For `tools`, `tool_events_uses_idx (tool_name, ts DESC) WHERE kind = 'use'` serves the range and the grouping, and the join to `calls` is on the primary key. The tool query counts the uses per call before the join (one `GROUP BY call_id` over the range), so the join is once per call that used tools, not once per use. A covering index such as `(ts) INCLUDE (cost_usd, session_key, project)` would allow index-only scans, but it adds write cost to every call and the 1.5-million-row measurement has to show the need first.
- **The default period is 3 months**, about a quarter of the rows of 12 months. 6 and 12 months are one click away and carry a note that they can take longer.
- **Client caching and request discipline.** The five responses keep `Cache-Control: no-store`, as every admin JSON response sends today, so cost data, session keys and project names stay out of the browser's disk cache. The UI gives the queries a TanStack Query `staleTime` of 60 s, which stops repeat requests within a minute. The five requests go out in parallel after first paint and the pool (`database.max_conns`, default 20) has room for them next to the page's other queries; the work list includes a check that a cold page load with a 12-month period leaves headroom.
- **Timeouts and bounds.** The admin plane has no global write timeout (ADR 0006), so each of the five handlers runs its query under the request context, which a reader who leaves the page cancels, and under a 30-second `context.WithTimeout`. A query that hits the cap returns the standard `5xx` error body, and the panel shows its error state with a Retry button; the other panels are unaffected.
- **Measure, then roll up.** The work list extends the sizing test (`TestUsageSizing`) to the five queries over 1.5 million rows and records the result in deployment.md, "Sizing". If a query passes the 1 s trigger on the default 3-month period, the answer is the hourly rollup of backend.md, "Storage and scale" (`date`, `hour`, `project`, `key_name`, `auth_mode`, calls, cost, unpriced calls). Months, projects and the punchcard can all be read from it, and the response shapes do not change. Sessions and tools cannot, and they stay on raw rows: both are bounded by `limit`, and the session query is already one scan.
- **Not an option: one combined endpoint.** See "Alternatives considered".

### 6. Charting approach

- **Plain SVG components, no chart library**, as `ActivityChart`, `VolumeChart` and `Sparkline` are (ADR 0052). Three new components: `MonthlyCostChart`, `Punchcard` and a ranked table for sessions and tools. Projects use `RankedList`. Pure helpers for the numbers (most costly month, shares, marker sizes, formatting) live in a `costOverviewData.ts` next to `dashboardData.ts` and `costData.ts`, so they are tested without a DOM.
- **Month chart.** One series, so brass (ADR 0053), bars with a value label above each bar, the zone-local month names on the axis, and the most costly month marked with a text label as well as a stronger outline. A partial month (the current one) has a dashed outline and "so far" in its tooltip and label. A month with unpriced calls carries a "partial" mark and the bar tooltip gives the count. A month with `cost_micro_usd` null draws no bar and says "unknown".
- **Punchcard.** A 7-by-24 grid. Each cell with cost draws a circle whose **area** is proportional to the cost (radius proportional to its square root), so size carries the value and colour is only brass. A cell with calls and no known cost draws a small hollow circle and reads "unknown" in its tooltip. The most costly cell is named in the figure caption. Rows are Monday to Sunday, columns are hours, and axis labels give every third hour so they fit on a narrow screen.
- **Colour.** Only existing tokens (`--accent`, `--bar`, `--muted`, `--border`). No new colour is added. Both themes work through the existing `light-dark()` tokens (ADR 0053, ADR 0058).
- **No library means no animation, no zoom and no brush.** Tooltips are SVG `<title>` elements as in `ActivityChart`.

### 7. Accessibility

- Each chart is an `<svg role="img">` with an `aria-label` that states the totals and the maximum in words ("Cost per month: $412.30 in 3 months; most costly March, $180.10").
- Each chart has a **table fallback** in a `<details>` element, as `ChartTable` does: months as rows with calls, cost and unpriced calls; the punchcard as a list of non-empty cells, sorted by cost, with weekday, hour, calls and cost (a 7-by-24 table is too wide to read). The fallback is how a screen-reader user reaches every value.
- Colour is never the only cue. Size carries the punchcard value, text labels mark the maximum and the partial months, and "unknown" and "partial" are words, not tints.
- The tiles and lists are real text, links are keyboard-focusable, and the ranked tables use `<th scope>` as the recent-sessions table does. Hover-only tooltips repeat in the table fallback.
- The marks keep their own colours under forced colours: the new classes join the `forced-color-adjust: none` rule of `styles.css`, and their text uses system colours.
- A change of period updates a polite live region ("Showing the last 6 months") so the change is announced.
- Contrast: brass on both backgrounds is at least 3:1 for graphical marks (ADR 0058 recorded 5.43:1 for light brass on `--bg`). The jsdom axe checks cannot measure contrast, so the phase that adds the punchcard checks the ratios in a browser.

### 8. Empty, partial and error states

| Situation | Behaviour |
|---|---|
| No calls in the period | Each panel shows its own empty state ("No calls in the last 3 months."). The tiles read "no data", not zero. |
| Every call unpriced | Cost panels say "Cost is unknown: no price matched the calls in this period." and link to the config page. The punchcard still shows call counts as hollow markers. |
| Some calls unpriced | The value is shown with a "partial" mark and the count, and the tile says so. |
| Passthrough calls in the cost | The section note says "Includes API-equivalent cost of client_passthrough providers" when any row has `api_equivalent`, as the breakdowns do today. |
| No project recorded | The project panel shows "No calls have a project yet" and a short note: projects are detected from Claude Code's working directory for new calls, and older calls get one only from the backfill. It links to the runbook (the header override and `keys[].project`). The unassigned row still shows the calls and cost. |
| Only one month of data | The chart shows one bar. The most costly month tile still names it. |
| No tool uses | "No tool calls in this period." |
| History shorter than the period (retention or a new install) | The empty months show as zero-call bars labelled "no calls", not as cost 0. |
| A panel's query fails | That panel shows `ErrorMessage` with Retry. Others stay. |
| Loading | `LoadingState` per panel, no layout jump: each panel reserves its height. |

### 9. Test strategy

No test is added for what a type or a constant already guarantees. Each test below asserts behaviour against an expected value written independently of the code under test.

- **Store contract cases** (ADR 0018), against real Postgres:
  - `MonthlyCosts`: a call 30 minutes before midnight UTC on the last day of a month lands in the next month for `Pacific/Auckland` and in the same month for `UTC` and `America/Los_Angeles`; a month with no calls is returned with zero calls; a range that starts mid-month marks the first month partial; priced and unpriced calls give the known sum and the unpriced count; a month with only unpriced calls has null cost; a priced passthrough call sets `api_equivalent` and an unpriced one does not.
  - `Punchcard`: known timestamps fall in the expected weekday and hour for a zone with a half-hour offset (`Asia/Kolkata`) and across a DST change (the repeated hour adds into one cell); a Sunday maps to weekday 7; the sum of the cells equals the usage total of the same filter.
  - `ProjectCosts`: ranking, ties, null-cost-last, `unassigned`, and that `project_count` includes projects past `limit`.
  - `SessionCosts`: a session's cost and counts equal the session list's for the same range; ranking and `session_count`.
  - `ToolCosts`: a call with two uses of different tools gives each half its cost; a call with two uses of one tool gives that tool all of it; a call of 10 micro-USD with uses of tools `a`, `b` and `c` gives `a` 4 and `b` and `c` 3 each (worked by hand from the rounding rule); over a dataset with odd costs and uneven use counts, the sum of tool costs plus the cost of calls without tool use equals the total cost exactly, with no tolerance (the invariant that guards the split); an unpriced call with two uses of one tool adds 2 to `uses`, 1 to `unpriced_calls` and nothing to cost; the range applies to the use time.
  - Invalid queries (empty range, `limit` 0, unknown `tz`, 37 months) return `ErrInvalidFilter`.
- **Project detection** (extractor, table tests on request bodies): a system prompt as a string and as text blocks with a working-directory line gives its final directory name; a block with a git remote gives the repository name; Windows and trailing-slash paths; no system field, no match, an over-long or control-character name, and a body that is not JSON give null and the request still succeeds; a working-directory line after the first 32 KiB is not found. The captured Claude Code fixture (ADR 0038) gives the expected name.
- **Project capture and precedence** (proxy): a valid header wins over a detected name and is stripped from the upstream request; an invalid header falls back to the detected name; a detected name wins over `keys[].project`; a key default applies when neither is present; a request with none of the three stores null. The "no bodies" marker test of ADR 0002 gets a case with the marker in the parent directories of the working-directory path: the marker is in no stored value, so only the final name is kept.
- **Backfill** (store contract, real Postgres): a mapped key fills its null calls and leaves set ones; a session with one distinct project fills its null calls; a session with two projects and a session with none stay null; a second run changes no row.
- **API tests**: parameter mapping, `tz` validation, limits, and conformance of the responses to `api/openapi.yaml` (the existing JSON-Schema conformance test).
- **UI unit tests** of the pure helpers: most costly month with a tie, with a partial month, with unknown months and with an empty list; share rounding; marker radius from cost (area proportional, zero and unknown handled); punchcard weekday order.
- **UI component tests**: each panel in the loading, error, empty, all-unknown, partial and normal states from fixtures; the `cost_range` parameter and its default; the table fallback holds the same numbers as the chart; links carry the right search (a project row links to the call log with `project`). An axe check in light and dark, as the dashboard has today.
- **Sizing**: the extended `TestUsageSizing` (off by default, as now) records the five timings.
- No test checks a constant against itself, that a component renders what it was passed without a rule, or that a mock was called.

### 10. The final look is a later phase

This ADR fixes what is shown, where it sits, how it behaves and which tokens it may use. It does not fix pixels. The visual design is a **later phase, produced with Claude design**, on top of the working, plainly styled version of phases 10 and 11 below. That phase may change markup structure, CSS, spacing, type, and the form of the tiles and marks, within three limits: it uses the palette tokens of ADR 0053, it keeps the accessibility rules and states above, and it does not change the API or the data helpers. Its acceptance is that the tests of phases 10 and 11 pass unchanged, except for selectors the redesign moves, and that the axe and contrast checks pass in both themes. The design brief for it is this ADR's sections 1, 6, 7 and 8, with the list of suggested widgets that were accepted.

### 11. Suggested additional widgets

Recommendation first, then the reasons.

| Widget | Data | Cost | Recommendation |
|---|---|---|---|
| **Month so far and run-rate**: this month's cost to date, and "at the current pace: $X" with the change against the previous full month | The `months` rows already fetched. | None: no endpoint. A linear pace ignores weekday patterns, so the label says "at the current pace". | **Include** in the first UI phase, beside the month chart. It answers "will this month be worse", which a bar of a partial month cannot. |
| **Average cost per session and per call** | `session_count` from `sessions` and the known total cost of `months`. | None. The average is over known cost, and the tile says "partial" when unpriced calls exist. | **Include** as one small tile: it reads as a trend over periods and costs nothing. |
| **Cache savings**: estimated spend avoided by cache reads | `GET /admin/usage?group_by=model` (cache-read tokens per model) and the prices in `GET /admin/config`. | No endpoint. It is an estimate: prices have effective dates (`pricing_version`), so the saving is computed at the price in force today and says "estimate". | **Include in a later phase**, after the first release of the section, when the price lookup has been checked against `pricing_version`. High value, but its correctness is the least certain of the list. |
| **Cost per model or provider** | Already on the overview (`By model`) and the cost page. | None. | **Do not add.** It exists. Add provider only when more than one provider is configured, and then as a third breakdown list. |
| **Cost per key** | Already on the overview (`By key`) and `/ui/cost`. | None. | **Do not add.** It exists. |
| **Daily trend and month-over-month change** | The activity chart with the `cost` metric already gives the daily trend; the change per month comes from the `months` rows. | None. | **Fold into the run-rate tile** and the month chart tooltips ("+12% on February"). No separate widget. |
| **Cost by agent type or request class** (main agent against sub-agents and compaction) | New `group_by` values in the usage whitelist (`agent_type`, `request_class`). | One whitelist entry per value and the existing contract cases. | **Later**, after the section has been used. Useful for firstmate crewmates. |
| **Most costly single calls** | A ranked calls list. `GET /admin/calls` is ordered by time, so this needs an endpoint. | A new ranked endpoint. | **Later**, if sessions and tools do not already explain a spike. |
| **Spend on failed calls** | `GET /admin/usage?group_by=error_class` has cost per class. | None, though it is only meaningful where the provider bills failed calls. | **Do not add** until a provider is known to bill them. |

The first release includes the two zero-cost tiles (run-rate and average per session). Cache savings and the agent-type grouping are follow-ups under phase 13 below, each decided when reached.

## Alternatives considered

- **One combined endpoint, `GET /admin/usage/overview`, returning all five views.** One round trip and one snapshot. Rejected: the slowest view would hold up the rest, one failure would fail the whole section, the response would be a large union of unrelated shapes in the OpenAPI schema, and the browser could not cache a stable part and refetch a changing one. Five endpoints cost five requests, which the page already does today (about ten).
- **Add `grain=month`, `tz` and a weekday-hour group to `GET /admin/usage`.** Reuses one handler. Rejected for the reason ADR 0057 gave: `group_by` and `grain` produce a time series with percentile columns, the whitelist is shared with the CLI (ADR 0055) and its tests, and a punchcard or a ranked list is not a series. Percentile sorts would also make the month chart several times slower than it needs to be.
- **Compute months, the punchcard and projects in the browser from `GET /admin/usage` with `grain=hour`.** No API change. Rejected: 12 months of hourly buckets is about 8,760 rows per group, the hour grain is aligned in UTC, so a half-hour zone such as `Asia/Kolkata` cannot be rebuilt from it, and every browser would repeat the sum.
- **Define a project as the key name.** No schema change, and it works today (`By key`). Rejected as the source: keys name a task or a credential and not a project (`fm:<task-id>`), and renaming a key splits its history (ADR 0035). Key name stays available as the "By key" list.
- **The `x-spyglass-project` header only.** No body read at ingest. Rejected as the only source: it needs a settings change in every project, older calls and every unconfigured project stay empty, and the panel would be empty for most users. It is kept as the override.
- **A `project` field in the key config only.** Works for one project per key and needs no client change. Rejected as the only source: it cannot tell two projects apart when one key serves both, which is the common setup for a person with one key. It is kept as the last fallback, for firstmate per-task keys.
- **Store the working-directory path, or the system prompt or a hash of it.** Gives the most precise grouping. Rejected: a path holds user and directory names, and a stored prompt or a hash of it is outside the metadata-only model (ADR 0002). Only the final name is needed to group spend.
- **Infer a project from the first user message.** Rejected: it is free text with no stable form, and reading it is a larger body read than the one environment line.
- **Read the firstmate ledger in the proxy to map `fm:<task-id>` keys to projects.** Rejected: the ledger belongs to the offline reporter of ADR 0040, and the proxy would gain a file dependency on firstmate. The config field covers the mapping.
- **Tool cost by the result's bytes or by the cost of the next call.** The cost a tool adds is its result re-read in every later call. Bytes are not tokens, and the next call's cost is mostly cached context unrelated to the tool. Both give a number that looks exact and is not. The equal split of the requesting call is simple, additive and checkable, and its label says it is an estimate. A better model is a later ADR if the list proves useful.
- **A rollup table now.** It would make every view fast. Rejected until measured: ADR 0021 and backend.md set the trigger (usage p95 above 1 s or about 20 million rows), and the five queries are bounded by period and `limit`. The decision above keeps the response shapes stable so a rollup can replace the raw read without a UI change.
- **A charting library (for example Recharts or visx).** Gives a punchcard and axes quickly. Rejected: ADR 0052 chose plain SVG, there is no chart dependency to maintain, the marks here are simple, and a library would bring its own colours and accessibility behaviour that ADR 0053 and the table fallback already settle.
- **Tie the cost section to the dashboard `range`.** One control. Rejected: the range tops out at 30 days, which holds at most two months and a punchcard of 30 days has few cells filled.

## Consequences

- **The overview gains a heavy section.** Five aggregates over up to 12 months are the most expensive queries on the page. The defaults (3 months, loading after first paint, panels independent, a 60-second `staleTime`) keep the page responsive on the sizing dataset. A very large database may need the hourly rollup, and the design is ready for it.
- **The extractor reads one more part of the body.** It reads the `system` field of each request to find the working directory, and discards it. This amends ADR 0010 and ADR 0014 (see the context). No body, prompt text or path is stored, so ADR 0002 holds, but body reading is no longer limited to the model and tool facts.
- **A project name can reveal a directory or repository name.** It is derived from a path the user chose, and it is stored in the `calls` table beside key names and session keys, with the same access (`/admin` only) and the same retention. A user who does not want it stored sets the header to a neutral name.
- **Detection names the directory, not always the project.** A git worktree or a scratch directory gives its own directory name unless the block names a git remote. Firstmate crewmates in worktrees are the main case; they set the header or rely on the remote. The header is the fix for any wrong name.
- **History gets a project only where the backfill can spread one.** Older calls get a project only from a mapped key or a session that already has one. The rest stay "No project". The first release explains this on the page, because a large "No project" row looks like a bug.
- **The attributed tool cost is an estimate.** It ranks tools by the spend of the model calls that asked for them. It overstates tools used alongside others in the same call and ignores the context cost of large results. The label says so. The rounding rule keeps it additive: tool costs plus the cost of calls without tool use equal the total exactly, and the remainder of a call goes to one tool, so a tool's cost can differ from its exact share by less than one micro-USD per call. Treat the list as "where to look", not an invoice.
- **Months and the punchcard follow the reader's zone.** Two people in different zones see different month totals for the same data, and a link does not carry a zone. A shared screenshot must show the zone, which the section header does.
- **The usage API grows by five paths and one filter.** It is additive: an older client does not call them. The agent CLI of ADR 0055 is not changed; the backfill is an operator subcommand beside `serve`. `GET /admin/calls`, `GET /admin/usage` and the call detail gain a `project` field and filter. The config gains an optional `keys[].project`.
- **Retention limits the longest period.** 12 months fit in the default 400 days. A shorter retention shows empty early months.
- ADR 0052 said "Frontend only. There is no API change." This ADR ends that for the cost section and replaces its top-5 sessions list. ADR 0052 stays as it is.
- The overview page sends about five more requests per period change.

## Work required

Phases are small, separate pull requests, in this order. Phases 5 to 8 can run in parallel once phase 4 has landed; phase 6 also needs phase 2.

1. **This ADR** and the README index entry and build-order note. Docs only.
2. **Project detection and capture.** Migration `00002_calls_project.sql` (`ALTER TABLE calls ADD COLUMN project text`, with the matching `DROP COLUMN`; adding a nullable column rewrites no rows). `core.CallRecord.Project`; the bounded `system` scan and name reduction in the extractor; the `x-spyglass-project` header read in the correlation function and its removal before the upstream request; the optional `keys[].project` config field and its validation; the precedence in the proxy; the write path; the `Project` filter in `core` and `buildWhere`; `project` on the call summary and detail in `api/openapi.yaml`, and `make ui-gen`. The call detail shows the project. Extractor, proxy and contract tests as in the strategy. Runbook entry for detection, the header override and the key default.
3. **Backfill.** The `spyglass backfill-projects` command with its two updates, its contract tests, and a runbook entry for running it once after phase 2 has landed.
4. **`GET /admin/usage/months` and the `tz` plumbing.** `MonthlyCostQuery`, the `tz` validation helper with `time/tzdata`, the port method, the adapter, the handler, the schema, and the contract and API tests. The shared cost row type (cost, `unpriced_calls`, `api_equivalent`) is introduced here.
5. **`GET /admin/usage/punchcard`**, with its tests.
6. **`GET /admin/usage/projects`**, with its tests.
7. **`GET /admin/usage/sessions`**, with its tests. The overview's top-5 sessions request is switched to it in phase 11, not here.
8. **`GET /admin/usage/tools`**, with its tests, the use-count-per-call subquery, and the invariant test.
9. **Sizing.** Extend `TestUsageSizing` to the five queries, run it over 1.5 million rows, record the numbers in deployment.md, "Sizing", and decide from them whether an index or the rollup is due. It lands before phase 10, so the default period is chosen from numbers.
10. **UI, first version (functional styling).** The `cost_range` search parameter and its validator, `costOverviewData.ts` with its tests, the "Cost" section with the two tiles, the month chart (with the table fallback), the project list, the run-rate and average-per-session tiles, and the per-panel states. `RankedList` moves to `components/`.
11. **UI, punchcard, sessions and tools.** The punchcard component with its table fallback, the sessions and tools tables, the removal of the top-5 sessions breakdown and its `group_by=session` request, and the axe checks in both themes.
12. **Design pass with Claude design.** The final look of the cost section, within the limits of decision 10. Contrast checked in a browser in both themes.
13. **Follow-ups, each decided when reached:** cache savings, agent-type and request-class grouping, the hourly rollup if phase 9 says it is due.
14. **Docs.** frontend.md, "Views" (the Overview row and the overview section), architecture/backend.md (the endpoint table) and deployment.md. Mark this ADR Completed in the change that lands the last of phases 2 to 12.

## Depends on

- ADR 0002 (metadata-only storage), ADR 0010 (correlation headers), ADR 0014 (extractor and call record), ADR 0017 (schema), ADR 0019 (call filter), ADR 0021 (usage aggregation), ADR 0024 (session, tool-call and usage endpoints), ADR 0035 (cost view), ADR 0040 (firstmate keys), ADR 0049 (corrections to Completed ADRs), ADR 0052 (overview), ADR 0053 (palettes), ADR 0056 (unpriced calls per session), ADR 0057 (cost per prompt endpoint), ADR 0058 (modern web guidance).

## References

- `web/src/views/Dashboard.tsx` and `dashboardData.ts` (the overview and its helpers), `web/src/components/ActivityChart.tsx`, `ChartTable.tsx` and `Sparkline.tsx` (the SVG components and the table fallback), `web/src/views/costData.ts`.
- `internal/store/postgres/usage.go` (`Usage`, `PromptCosts`), `sessions.go` (the session aggregate), `tools.go` (the tool pairing), and `migrations/00001_calls_and_tool_events.sql` (the tables and indexes).
- `api/openapi.yaml`, the `/admin/usage`, `/admin/usage/prompts`, `/admin/sessions` and `/admin/tool-calls/summary` paths.
- architecture/deployment.md, "Sizing" (the measured aggregates); architecture/backend.md, "Storage and scale" (the rollup triggers); architecture/frontend.md, "Views" and "Look and themes"; architecture/upstream-connection.md, "Header handling".
- ADR 0002 (metadata-only storage), ADR 0012 and ADR 0047 (the API-equivalent label), ADR 0049 (corrections to Completed ADRs), ADR 0055 (the CLI that shares the usage whitelist).
