# Changelog

Notable changes to Spyglass. Release 0.1.0 and earlier are described in their [GitHub release notes](https://github.com/mtanzi/spyglass/releases).

## [0.6.1](https://github.com/mtanzi/spyglass/compare/v0.6.0...v0.6.1) (2026-11-19)


### Bug Fixes

* **ui:** label main-thread calls as main in the Calls Agent column and filter ([#150](https://github.com/mtanzi/spyglass/issues/150)) ([fd6d88e](https://github.com/mtanzi/spyglass/commit/fd6d88e0be5d92f2a00e78f9eb4a541537860916))

## [0.6.0](https://github.com/mtanzi/spyglass/compare/v0.5.0...v0.6.0) (2026-11-18)

### Added

- **Model time versus tool time** ([#142](https://github.com/mtanzi/spyglass/issues/142)). The session summary in the admin API and the CLI now has `llm_ms` (sum of the call durations), `tool_ms` (sum of the known tool call durations) and `tool_ms_estimated`. The session list, the session summary strip and the prompt and agent rows of the session tree show the split, for example `LLM 42.00 s (60%) · tools 28.00 s (40%)`. Both values are summed busy time: parallel calls and sub-agents overlap in wall time and still add up, so the split is not a share of the session duration.
- **LLM and tool call counts in the session view** ([#143](https://github.com/mtanzi/spyglass/issues/143)). Each prompt row and agent row in the session tree now shows a count summary on its own line, for example "8 LLM calls · 23 tool calls · 2.9 tools per call". The counts cover the row's whole subtree, so an agent includes its sub-agents. The ratio is left out when the row has no LLM call. The session list has a new sortable "Tools per call" column: the session's tool calls divided by its calls, with one decimal.

### Fixed

- **Text sorts in byte order on every Postgres image.** Usage groups, the tool summary, the models of a session, and the prompt and agent tie-breaks now sort with `COLLATE "C"`. On the Debian `postgres:16` image that `docker-compose.yml` deploys, they used the database locale, so mixed-case values sorted differently than on the Alpine image the tests used. The store tests now run on `postgres:16`.
- **`usage_raw` in the admin API schema allows its keys.** `api/openapi.yaml` declared `usage_raw` as an object with no `additionalProperties`, so the generated TypeScript type forbade every key the server sends. It now allows any property and describes the merged usage object.

## 0.5.0 - 2026-11-16

### Changed

- **Semantics and layout of the UI** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#130](https://github.com/mtanzi/spyglass/issues/130)). Explanations that were only in a hover `title` (the approximate latency, API-equivalent cost, unpriced and unknown markers, the duration source of a tool call, the count of failed tool calls, idle gaps on the session axis) are now visible text or a small button that opens a note, so keyboard and touch users can read them. Filter controls are in a `<search>` landmark, and the chip filter no longer adds half-composed text when an IME confirms a candidate with Enter. The "Loading…" state is no longer announced by screen readers, only the sorted table column carries `aria-sort`, and the sort arrow is not read as part of the column name. The skip link and visually hidden text use `clip-path`. The shell and trace use `100dvh`, the dashboard, cost and call detail grids follow the width of their container, and scrolling past the end of a nested scroll area no longer scrolls the page.
- **Type in `rem` and larger targets** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#128](https://github.com/mtanzi/spyglass/issues/128)). The text of the UI is now sized in `rem` from the root, so it follows the text size set in the browser. At the default size nothing changes in size. Buttons, inputs, selects, segmented items and the remove buttons of filter chips are at least 24px high, so a few controls are slightly taller. Table and trace rows keep a fixed height but now scale with the text size, and the session tree columns and the sidebar width scale too. Layout breakpoints use `em`, and the page declares `text-scale` for browsers that support it.
- **Contrast of controls and links** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#127](https://github.com/mtanzi/spyglass/issues/127)). Inputs, selects and buttons now have a border with at least 3:1 contrast against the page and the surface in both themes, through the new `--border-control` token. Other borders stay subtle. Light brass is darker (`#965d0c`, from `#a8690f`), so links reach 5.06:1 on the surface, where they had 4.17:1. This closes the open contrast item of ADR 0053.
- **Native popovers for the filter panel and the column menu** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#129](https://github.com/mtanzi/spyglass/issues/129)). Both now use the browser's `popover` support: they open above the page, close on Escape or a click outside, and give focus back to their button. They sit under their button where the browser supports CSS anchor positioning, and show centred where it does not. The "Columns" control is now a button instead of a `<details>` summary. The hand-written outside-click and Escape code and the `z-index` are gone.
- **Focus ring, forced colours and reduced motion** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#126](https://github.com/mtanzi/spyglass/issues/126)). The keyboard focus ring in the UI is now 2px and sits outside the element, also on table and trace rows, where it used to be a 1px line inside the element or only an inset bar. In Windows High Contrast and other forced-colour modes, the focus ring and the current page in the sidebar now show in the system highlight colour, and the chart swatches and bars keep their own colours. The loading spinner turns only when the system does not ask for reduced motion.
- **Theme tokens with `light-dark()`** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#125](https://github.com/mtanzi/spyglass/issues/125)). Each colour token in the UI stylesheet is now written once as `light-dark(<light>, <dark>)`, and the duplicated light block and the colour media query are gone. The colours do not change. The theme toggle now follows a change made in another tab, and the System button names the scheme in use, for example "System (dark)", and updates when the system setting changes.
- **Page titles and focus on navigation** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#124](https://github.com/mtanzi/spyglass/issues/124)). Each page of the UI now has its own title, unique part first, for example "Sessions · Spyglass" and "Session <key> · Spyglass", where the tab and the history showed "Spyglass" for every page. After a client-side navigation to another page, focus moves to the new page's heading, so a screen reader announces it. The first load and a change of filter, sort or range do not move focus.

### Security

- **Trusted Types on the UI** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#123](https://github.com/mtanzi/spyglass/issues/123)). The UI's Content Security Policy now ends with `require-trusted-types-for 'script'`, so the browser refuses any plain string written to an HTML or script sink. The UI defines no Trusted Types policy, because it writes to no such sink. Every view was checked in Chrome with the header on and logs no violation. Browsers without Trusted Types ignore the directive.
- **The admin plane refuses cross-site browser requests** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#122](https://github.com/mtanzi/spyglass/issues/122)). A request to the admin listener with `Sec-Fetch-Site: cross-site` now gets `403`, unless it is a top-level `GET` navigation to `/ui/`, so a link from another site still opens the UI. Same-origin requests, and requests without `Sec-Fetch-*` headers such as those of `curl`, Prometheus and the CLI, are unchanged. Every admin response now sends `Vary: Sec-Fetch-Site, Sec-Fetch-Mode, Sec-Fetch-Dest`. This stops another web page from reading or changing data through the loopback admin plane, which has no sign-in. The data plane is unchanged.
- **Browser security headers on the admin plane** ([ADR 0058](docs/adrs/0058-align-web-ui-with-modern-web-guidance.md), [#121](https://github.com/mtanzi/spyglass/issues/121)). Every response of the admin listener (`/ui/*`, `/admin/*`, `/metrics`, `/healthz`, `/readyz`) now sends `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Resource-Policy: same-origin`, `Cross-Origin-Opener-Policy: same-origin`, `X-Frame-Options: DENY` and a `Permissions-Policy` that turns off camera, microphone, geolocation and similar features. The UI's Content Security Policy is now `default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`, so another page cannot frame the UI. The headers only change what a browser does: Prometheus, `curl` and the CLI see the same responses. The data plane is unchanged.

## 0.4.0 - 2026-11-15

### Breaking changes

The aliases deprecated in 0.2.0 end ([ADR 0054](docs/adrs/0054-rename-to-spyglass.md), [#116](https://github.com/mtanzi/spyglass/issues/116)). Migrate before you upgrade; the steps are in the [runbook](docs/runbook.md#upgrading-from-llm-proxy-010).

- **The `x-llm-proxy-key` header is no longer read.** A client that sends only this header now carries no virtual key and gets `401`. Send `x-spyglass-key`. The old header is still removed before the upstream request and is still redacted from logs and fixtures, so a key from an old client does not leak.
- **The `LLM_PROXY_*` environment variables are no longer read**, including `LLM_PROXY_CONFIG` and the `_ENV` and `_FILE` forms of a secret reference, and the compose variables `LLM_PROXY_IMAGE`, `LLM_PROXY_CONFIG_FILE`, `LLM_PROXY_SECRETS_DIR` and `LLM_PROXY_PORT`. Use the `SPYGLASS_*` names. `serve` no longer logs the deprecation warnings. The compose file no longer sets `LLM_PROXY_CONFIG`, so a rollback to the 0.1.0 image needs it set by hand.

### Fixed

- **Cost per prompt covers the whole range.** The cost view's prompt table grouped one page of 500 calls in the browser, so a range with more calls showed missing prompts and understated costs and call counts. The server now aggregates every call of the range by prompt id with the new `GET /admin/usage/prompts` ([ADR 0057](docs/adrs/0057-cost-per-prompt-endpoint.md)). The table shows the top 25 prompts by cost and says when the range has more.

## 0.3.0 - 2026-11-14

### Added

- **Agent CLI, phase 1** ([ADR 0055](docs/adrs/0055-agent-facing-cli-axi-toon-output.md)). The `spyglass` binary is now also a read-only client of the admin API, made for agents such as Claude Code and usable by people and scripts. Output is [TOON](https://toon-format.org) on stdout. Exit codes: 0 success, 1 error, 2 usage error. The client reads `SPYGLASS_ADMIN_URL` (default `http://127.0.0.1:9090`). Run:
  - `spyglass` with no arguments: today's cost, calls, errors and the most expensive sessions.
  - `spyglass describe`: every field with its unit.
  - `spyglass config`: the running configuration.
  - `spyglass sessions list` and `spyglass sessions show <key>`: sessions, or one session with its agents, prompts and calls.
  - `spyglass calls list` and `spyglass calls show <id>`: calls, newest first, with the total count. Both list commands take `--since` and `--limit`.
  - Every command has `--help` with examples.
- **`unpriced_calls` on the session summary** ([ADR 0056](docs/adrs/0056-session-unpriced-calls.md)). The admin API and the CLI now show how many calls of a session have no cost. `cost_micro_usd` is unchanged and still leaves those calls out.

### Deprecations

- The removal of the deprecated `x-llm-proxy-key` header and `LLM_PROXY_*` variables, promised for 0.3.0, moves to 0.4.0. Nothing changes for existing setups: both still work, with the same warnings. Migrate before 0.4.0.

### Documentation

- Added the CLI architecture document and amended ADR 0055 after design review.

## 0.2.0 - 2026-11-13

### Renamed from llm-proxy to Spyglass

Every technical name changes from `llm-proxy` to `spyglass` ([ADR 0054](docs/adrs/0054-rename-to-spyglass.md)). Existing deployments keep their data. Upgrade steps are in the [runbook](docs/runbook.md#upgrading-from-llm-proxy-010).

- **Breaking: Prometheus metrics are renamed** from `llm_proxy_*` to `spyglass_*`, for example `llm_proxy_requests_total` is now `spyglass_requests_total`. There is no alias. Update queries, recording rules, alerts and dashboards when you upgrade.
- **Key header: `x-spyglass-key`.** The old `x-llm-proxy-key` is deprecated. It still works, with the same checks, and the proxy logs one warning per process the first time a client uses it. When a request carries both, `x-spyglass-key` is used.
- **Environment variables: `SPYGLASS_*`**, for example `SPYGLASS_CONFIG`, `SPYGLASS_DATABASE_URL`, `SPYGLASS_LOG_LEVEL` and `SPYGLASS_IMAGE`. The old `LLM_PROXY_*` names are deprecated. Each one is still read when its `SPYGLASS_` name is unset, and `serve` logs a warning that names it. The fallback only goes from a `SPYGLASS_` name to its `LLM_PROXY_` name: when you rename an exported variable, also update any secret reference in `config.yaml` that names it, for example `{ env: LLM_PROXY_DATABASE_URL }` to `{ env: SPYGLASS_DATABASE_URL }`. The contributor test variables are renamed without a fallback: `SPYGLASS_TEST_DATABASE_URL` and `SPYGLASS_SIZING_ROWS`.
- **Image: `ghcr.io/mtanzi/spyglass`.** The 0.1.0 image stays at `ghcr.io/mtanzi/llm-proxy:0.1.0`.
- **Binary: `spyglass`.** Release binaries are named `spyglass-<tag>-<os>-<arch>`, and the commands are `spyglass serve`, `spyglass keygen` and `spyglass version`. The Go module is `github.com/mtanzi/spyglass`.
- **Compose:** the service is now `spyglass`. The project name stays `llm-proxy` and the database stays `llmproxy`, so the existing Postgres volume is kept. Run `docker compose up -d --remove-orphans` once to remove the 0.1.0 container.
- The deprecated `x-llm-proxy-key` header and `LLM_PROXY_*` variables are removed in 0.4.0.

### Security

- Pull requests and a weekly schedule now run `govulncheck` and a Trivy scan of the source and the latest released image. The release workflow runs the same gates before it builds the image.

### Documentation

- The README shows the Overview dashboard.
