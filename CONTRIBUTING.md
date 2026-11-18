# Contributing

Design and decisions live in [docs/](docs/): start with [docs/llm-proxy-design.md](docs/llm-proxy-design.md), the [architecture documents](docs/architecture/) (the dependency rule is in `backend.md`, "Layers and the dependency rule") and the [ADRs](docs/adrs/README.md).

## Layout

One Go module, one static binary `spyglass` built from `cmd/spyglass`. Everything else is under `internal/`:

| Package | Role | May import |
|---|---|---|
| `core` | Domain types, pure rules, storage ports | standard library only |
| `core/coretest` | Fakes of the core ports for consumer tests (test support only) | `core` |
| `family/anthropic` | Anthropic protocol knowledge (pure) | `core`, `gjson` |
| `recorder`, `keyring` | Application use cases | `core` |
| `proxy` | Data-plane adapter | `core`, application packages, `family/*`, `config`; no `store` package |
| `api` | Admin-plane adapter | `core`, `recorder`, `config`; no database driver |
| `ui` | Embedded web UI | `core`, application packages |
| `config` | Configuration | `core`, `yaml` (`go.yaml.in/yaml/v3`) |
| `store/postgres` | Postgres adapter | `core`, drivers (`pgx`, goose); its tests and `pgtest` also `testcontainers-go` |
| `store/storetest` | Shared store contract tests | `core`, `store/postgres` |
| `cli` | Agent-facing CLI: read-only client of the admin API, commands and output (ADR 0055) | standard library, `toon`; tests also the root `api` package (embedded spec), `jsonschema`, `yaml` |
| `toon` | Encode-only TOON 3.3 encoder for the CLI output | standard library only |
| `fakeupstream` | Fake Anthropic upstream that replays fixtures (test support only) | standard library only |

Only `cmd/spyglass` and the test-only contract suite `store/storetest` import `store/postgres`. A new package needs a reason.

## Checks

```
make build          # static binary in bin/
make vet
make test
make test-race     # the same tests under the race detector (CI runs it; needs cgo)
make lint           # golangci-lint, including depguard rules (.golangci.yml)
make check-imports  # depguard rules only
make check-openapi  # lint api/openapi.yaml with Redocly (needs Node)
make ui-build       # build the web UI into internal/ui/dist (needs Node 22+); make build then embeds it
make ui-dev         # Vite dev server, proxies /admin to localhost:9090
make ui-check       # lint, type-check and unit-test web/, and check the generated API types
make ui-gen         # regenerate web/src/api/schema.gen.ts after changing api/openapi.yaml
make goldens        # rewrite the golden call records under testdata/fixtures; read the diff
make e2e            # end-to-end tests: real wiring, fake upstream, test Postgres (needs Docker)
```

Without `internal/ui/dist`, `make build`, `make vet`, `make test` and `make lint` use the `noui` build tag, so a fresh clone needs no Node and the binary serves a stub page at `/ui/`.

The store tests in `internal/store/...` need a real Postgres. They start a `postgres:16` container (the Debian image `docker-compose.yml` deploys) with testcontainers-go when Docker is running, or use the server in `SPYGLASS_TEST_DATABASE_URL` (a URL whose user may create databases); without either they skip. Each test gets its own throwaway database.

The contract suite `storetest.Run` runs against the Postgres adapter from `internal/store/postgres/storetest_test.go`. To run it alone, or all store tests against a local Postgres:

```
go test ./internal/store/postgres -run TestContract -v
SPYGLASS_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./internal/store/...
```

CI runs it in the `postgres` job against a Postgres service container. A new filter, port method or behaviour starts as a case in a group under `internal/store/storetest`; the suite also scans every stored text and `jsonb` value for the fake prompt, response and tool text of its record generator.

The end-to-end tests (`cmd/spyglass/e2e_test.go`, ADR 0038) start the real wiring against the fake upstream in `internal/fakeupstream` and a test Postgres, use `anthropic-sdk-go` as the client and read results through the admin API. They find Postgres the same way the store tests do and skip without one. CI runs them in the `e2e` job, which fails if any of them skipped. `anthropic-sdk-go` and `fakeupstream` are for `_test.go` files only; depguard enforces it. Fixtures, their format and how to refresh them from real traffic are in [testdata/fixtures/README.md](testdata/fixtures/README.md).

The `depguard` rules in `.golangci.yml` encode the table above. CI runs build, vet and tests on the latest two Go releases, and lint, the import check and the OpenAPI lint once. The `web` job runs `make ui-check`, then builds and tests Go with the real UI embedded. The `image` job builds the container image, then runs `scripts/check-image.sh` (size budget and non-root user) and `scripts/smoke-image.sh` (`/healthz`, `/readyz` and `/ui/` against Postgres); both take an image name, so they also run against a local `docker build`. It then runs `scripts/compose-test.sh` (the repository's `docker-compose.yml` against a fake upstream: health, a proxied call, the call log, the UI and `SIGHUP`) and `scripts/restore-test.sh` (`pg_dump`, restore into a fresh Postgres, `serve`, open a recent call). `internal/config` tests load the example configs and check them against the compose file.

## Review tiers

Every PR carries one review label:

- `review: tier A`: no human code review needed. Docs, tests and fixtures, plus small application changes we are confident do not break the application, for example a narrow bug fix covered by tests, or a build or config tweak with no behaviour change. Never auth, migrations, the API spec, config defaults, compose files or `.github/`.
- `review: tier B`: light human review.
- `review: tier C`: full human review, for example changes to CI, auth, storage or who can merge.

The `tier-a-automerge` workflow (`.github/workflows/tier-a-automerge.yml`) enables GitHub auto-merge (squash) on a PR with the `review: tier A` label, so GitHub merges it once the required CI checks pass. It disables auto-merge again when a PR without the label has auto-merge that the workflow itself enabled (for example after the `review: tier A` label is removed). It never touches auto-merge a maintainer enabled, so auto-merge on a Tier B or C PR stays on. The label alone decides: the workflow does not check which files the PR changes, so apply it only to changes that meet the Tier A definition above. If the workflow cannot read its auto-merge state, it disables auto-merge and fails. Do not label `.github/` changes Tier A; this is a labelling rule, not a workflow check. PRs from forks are skipped.

### Dependabot PRs

The `dependabot-automerge` workflow (`.github/workflows/dependabot-automerge.yml`) handles PRs authored by `dependabot[bot]`; `tier-a-automerge` skips them, because Dependabot PRs follow their own rules, described here. It reads the update type and dependency type with `dependabot/fetch-metadata` (pinned by commit SHA). For a grouped PR, every dependency in the group must meet an auto-merge rule.

| Update | Label | Auto-merge |
| --- | --- | --- |
| Patch, any dependency | `review: tier A` | yes |
| Minor, development dependency (the `web-dev-tools` group) | `review: tier A` | yes |
| Minor, GitHub Actions | `review: tier A` | yes |
| Minor, runtime dependency (Go modules, React, TanStack) | `review: tier B` | no |
| Major, anything | `review: tier B` | no |

An auto-merged PR gets GitHub auto-merge (squash), pinned to the head commit, and merges only after the required checks on main pass. A PR that does not qualify gets the tier B label, and the workflow disables auto-merge that it enabled itself; it never touches auto-merge a maintainer enabled. A run triggered by anyone other than Dependabot (a push or a reopen) counts as not qualifying, so a maintainer's fix commit never merges unattended. A later Dependabot push (for example a rebase or `@dependabot recreate`) checks the rules again. An unknown update type, or metadata the workflow cannot read, counts as not qualifying. If the workflow cannot read the auto-merge state, it disables auto-merge and fails.

`.github/dependabot.yml` adds a cooldown before Dependabot proposes a new version: 3 days for patch, 7 for minor and 7 for major, with a 7-day default for versions Dependabot cannot classify by semver. It also groups runtime patch updates into one weekly PR per ecosystem; npm development-dependency patches ride in the `web-dev-tools` group instead. The cooldown gives the community time to find and yank a compromised release before it reaches `main` unattended. Auto-merge is limited to low-risk update types, and the required CI checks still gate every merge.

## Releases

Releases are automated with [Release Please](https://github.com/googleapis/release-please-action) (`.github/workflows/release-please.yml`, `release-please-config.json`, `.release-please-manifest.json`). A release is a merge, not a hand-made tag.

1. Squash-merged PR titles on `main` follow Conventional Commits (`feat(web): ...`, `fix: ...`). After each merge, Release Please keeps one open release PR that bumps the version in `.release-please-manifest.json` and adds the new entries to `CHANGELOG.md`. `feat` bumps the minor version, `fix` the patch version. Before 1.0 a breaking change (`feat!:` or a `BREAKING CHANGE:` footer) also bumps the minor version (`bump-minor-pre-major`).
2. Review the release PR, edit the generated changelog text if needed, and merge it. Release Please then creates the tag `vX.Y.Z` (no component prefix) and the GitHub release.
3. In the same workflow run, the `release` job calls `.github/workflows/release.yml`: security gates, then the image (checked and smoke tested before push, tagged with the version only, never `latest`), then the binaries attached to the release.

A tag made with the built-in `GITHUB_TOKEN` does not trigger other workflows, so `release.yml` no longer runs on a tag push. It is a reusable workflow called from `release-please.yml` when `release_created` is true. This avoids a GitHub App token or personal access token.

To repeat a failed publish, run `Release` from the Actions tab (`workflow_dispatch`) and give the existing tag, for example `v0.6.0`. Do not create release tags by hand.

`CHANGELOG.md` keeps its hand-written history. Release Please adds each new version above the newest existing version heading, so the `Unreleased` section stays where it is until a maintainer folds it into the release PR's entry.

## Release security gates

The `release` workflow (called by Release Please) runs `.github/workflows/security-gates.yml` before anything else. The `image` and `binaries` jobs `need` it, so a failed gate stops the release before any image is pushed or binary attached. Run the same gates by hand on any branch from the Actions tab (`Security gates`, `workflow_dispatch`); this needs no release.

| Gate | Tool | Fails on |
| --- | --- | --- |
| Go vulnerabilities | [govulncheck](https://go.dev/doc/security/vuln/) (`v1.9.0`, on the latest stable Go) | a known vulnerability the code can reach |
| Image vulnerabilities | [Trivy](https://github.com/aquasecurity/trivy) (`0.76.2`) on an amd64 image built from the `Dockerfile` | a critical or high finding that has a fixed version |
| Secrets | [gitleaks](https://github.com/gitleaks/gitleaks) (`8.30.1`) over the full git history | any finding |

- Trivy runs with `--ignore-unfixed`. A finding with no fixed version cannot be resolved yet, and failing on it would block a release on something the maintainers cannot act on. Once a fix exists, the finding fails the gate.
- Trivy and gitleaks are downloaded as release binaries, pinned by version and checked against a SHA-256. To upgrade, change the version and checksum together: Trivy in `.github/actions/trivy-scan/action.yml`, gitleaks in `security-gates.yml`. The govulncheck and Trivy steps live in those shared actions (`.github/actions/govulncheck` and `trivy-scan`), so the release gates, the PR scans and the weekly scan all use one pinned copy. The workflow does not use `gitleaks-action`, which needs a license for organisations. New actions in the workflow are pinned by commit SHA.
- `.gitleaks.toml` extends the default gitleaks rules. Its one allowlist entry covers fake session keys in `internal/proxy/correlation_test.go` (rule `generic-api-key` only). Add an entry only for a verified false positive, scoped to a path and a rule, with a comment. Never allowlist a real secret: rotate it.
- To run a gate locally, use the same commands as the workflow: `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`, `trivy image --severity CRITICAL,HIGH --ignore-unfixed --exit-code 1 <image>`, and `gitleaks git --redact --config .gitleaks.toml .`.
- Secret scanning with push protection is a repository setting the maintainer turns on (Settings, Code security). It is the routine secret check; gitleaks runs at release only.

## Routine security scans

The release gates only run at release time. Two routine scans catch a vulnerability disclosed afterwards. Neither blocks day-to-day work.

- **Weekly scan** (`.github/workflows/security-weekly.yml`, Mondays 06:17 UTC, or `workflow_dispatch`): runs govulncheck on `main`, Trivy on an image built from `main`, and Trivy on the latest released image (`ghcr.io/mtanzi/spyglass:<version>`, the newest non-draft, non-pre-release release). On a finding the run fails and one open issue labelled `security-scan` is created, or updated if one is already open, so there is never one issue per week. Fix the findings, then close the issue; the next failing scan opens a new one. Findings also appear in the run summary.
- **Pull request scans** (`.github/workflows/security-scans.yml`): runs govulncheck and the Trivy image scan on every PR and shows the result. They are not required checks on `main`: a vulnerability in an unrelated dependency would otherwise block every PR, including Tier A and Dependabot auto-merges, until it is fixed. Do not add them to the branch rules. A red result on a PR that does not touch dependencies usually means a new disclosure; the weekly issue tracks it.

## Version

`make build` sets `main.version` with `-ldflags "-X main.version=<version>"`. Override with `make build VERSION=v1.2.3`.
