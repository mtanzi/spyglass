# Changelog

Notable changes to Spyglass. Release 0.1.0 and earlier are described in their [GitHub release notes](https://github.com/mtanzi/spyglass/releases).

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
