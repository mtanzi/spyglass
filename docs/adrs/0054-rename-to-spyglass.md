# ADR 0054: Rename llm-proxy to Spyglass

Status: Accepted

Date: 2026-11-13

## Context

ADR 0053 named the product **Spyglass** in the UI and the README, and kept every technical name `llm-proxy`: the Go module, the binary and its commands, the Docker image, the key header, the environment variables, the Prometheus metrics and the repository. Release 0.1.0 (2026-11-12) shipped with those names: binary `llm-proxy`, image `ghcr.io/mtanzi/llm-proxy`, header `x-llm-proxy-key`, variables `LLM_PROXY_*` and metrics `llm_proxy_*`.

Two names for one thing confuse users and keep costing effort in docs. Few people depend on the old names yet, so the rename is cheapest now. The GitHub repository moves from `mtanzi/llm-proxy` to `mtanzi/spyglass` after this change merges; that move is done on GitHub, not in this repository.

## Decision

- **Every technical name becomes `spyglass`.** The Go module is `github.com/mtanzi/spyglass`. The command is `cmd/spyglass` and the binary `spyglass`. Release binaries are `spyglass-<tag>-<os>-<arch>`. The image is `ghcr.io/mtanzi/spyglass`; the release workflow keeps `ghcr.io/${{ github.repository }}`, which follows the repository rename. The compose service is `spyglass`, and the config mounts at `/etc/spyglass/config.yaml` in the container.
- **Key header: `x-spyglass-key`.** The deprecated `x-llm-proxy-key` is still accepted with the same checks: it is the passthrough key only when `x-spyglass-key` is absent, a `proxy_key` provider rejects either header with `401`, both are removed before the upstream request, and both are redacted from logs and fixtures. When a request carries both, `x-spyglass-key` is used. The first request that authenticates with the old header logs one warning per process. The warning names the header and the key name, never the key value.
- **Environment variables: `SPYGLASS_*`.** Each `SPYGLASS_` variable falls back to its `LLM_PROXY_` name when it is unset: `SPYGLASS_CONFIG`, every `SPYGLASS_<SECTION>_<KEY>` override, the `_ENV` and `_FILE` parts of a secret reference (read as one pair: when either `SPYGLASS_` part is set, the `LLM_PROXY_` parts are ignored), and a secret reference that names a `SPYGLASS_` variable, such as `{ env: SPYGLASS_DATABASE_URL }`. `serve` logs one warning per old variable it reads, with both names and never the value. The fallback goes one way only: a secret reference that still names an `LLM_PROXY_` variable is not mapped to its `SPYGLASS_` name, so the upgrade renames those references too. The compose variables (`SPYGLASS_IMAGE`, `SPYGLASS_CONFIG_FILE`, `SPYGLASS_SECRETS_DIR`, `SPYGLASS_PORT`) fall back the same way; compose cannot warn. The contributor test variables `SPYGLASS_TEST_DATABASE_URL` and `SPYGLASS_SIZING_ROWS` are renamed without a fallback: they are not part of any deployment.
- **Metrics: `spyglass_*`, renamed outright.** No metric is emitted under both names. Dual emission would double the series for a short transition and still break every query once it ends. The release notes call the rename out as breaking.
- **The aliases end in 0.4.0.** `x-llm-proxy-key` and the `LLM_PROXY_*` names are accepted through every 0.2.x release and removed in 0.4.0. The compose file's `LLM_PROXY_CONFIG` entry, kept so a rollback to the 0.1.0 image finds the config, goes with them.
- **Stored data keeps its names.** The compose project stays `llm-proxy`, because it prefixes the Postgres volume (`llm-proxy_pgdata`); a new project name would start an existing deployment on an empty database. The database and user stay `llmproxy` in the compose file and the examples, because Postgres creates them only on the first start of an empty volume. The virtual key prefix `lp_` stays: issued keys keep working.
- **History is not rewritten.** Existing ADR bodies, the 0.1.0 release and its notes, the branding proposals and the design document's file name (`llm-proxy-design.md`, which completed ADRs cite by name) keep the old name.

## Consequences

- An existing 0.1.0 deployment upgrades in place and keeps its data. The compose service rename leaves the 0.1.0 container as an orphan that holds port 8080, so the upgrade stops it and runs `docker compose up -d --remove-orphans`. The runbook's "Upgrading from llm-proxy 0.1.0" lists the steps.
- Prometheus queries, recording rules, alerts and dashboards must be updated in the same upgrade. The `llm_proxy_*` series stop and the `spyglass_*` series start empty.
- Clients that send `x-llm-proxy-key` keep working until 0.4.0. Operators find them from the warning in the proxy log.
- After the repository moves, GitHub redirects the old URLs, and Go fetches the new module path. The new image is a new GHCR package: its visibility must be set to public after the first push. The 0.1.0 image stays at `ghcr.io/mtanzi/llm-proxy:0.1.0`. Until a release is published under the new name, the weekly scan of the latest released image looks for `ghcr.io/mtanzi/spyglass:0.1.0`, which does not exist.
- ADR 0053 stays as written. This ADR replaces only its statement that the technical names stay `llm-proxy`.

## Work required

- The module path, the `cmd/spyglass` directory, the Makefile, the Dockerfile, the workflows and the scripts.
- The header alias in `internal/proxy` with its one-time warning, the redaction lists in `internal/httpmw` and `cmd/fixturecap`, and tests for both header names.
- The environment fallback in `internal/config` (`Options.Deprecated`), the warning in `cmd/spyglass`, and tests for the fallback.
- The metric names and `cmd/spyglass/testdata/metrics.golden`.
- Docs: the README, CONTRIBUTING, the architecture documents, the runbook (with the upgrade steps), the personal setup, the examples and `CHANGELOG.md`.

## Depends on

- ADR 0053.

## References

- ADR 0053 (the Spyglass brand, which kept the technical names).
- ADR 0026 (the metrics), ADR 0012 and ADR 0047 (the passthrough key header), ADR 0004 (environment overrides), ADR 0036 (the image and the release build), ADR 0037 (the compose deployment).
- docs/runbook.md, "Upgrading from llm-proxy 0.1.0".
