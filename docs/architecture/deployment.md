# Deployment

Part of the [architecture set](README.md). The components named here are described in [backend.md](backend.md). Provider credentials and client setup are in [upstream-connection.md](upstream-connection.md).

There are two deployment tiers: one container plus Postgres for a home lab or small team, and stateless replicas behind a load balancer for a team or production. v1 builds the first tier (gap review R14). The second is described under [Later: multi-replica tier](#later-multi-replica-tier) so that v1 choices do not block it, but v1 builds none of its mechanisms.

## What runs

| Piece | Form | State |
|---|---|---|
| `spyglass serve` | One static Go binary in a distroless container. Applies its embedded migrations at start | None on disk; the call queue and the key map are in memory. Keys, providers and pricing come from the config file; call records are in the store (the `store/postgres` adapter) |
| Postgres 16 or later | Separate container or managed service | Call tables |
| Optional: reverse proxy (Caddy, a cloud load balancer) or Tailscale | In front of the listeners | TLS certificates |
| Optional: Prometheus, Grafana | Existing observability stack | Their own |

The binary reaches storage through the store ports ([backend.md](backend.md#storage-ports-and-adapters)). v1 ships one production adapter, `store/postgres`, so Postgres is the only supported store. A single-machine SQLite adapter is a possible later addition, not v1.

There is no message broker, no Redis and no separate migration container. Bodies are never stored, so there is no journal and no body volume. The only buffer is the in-memory call queue, which drops and counts records when full (gap review B5; [README](README.md#resolved-decisions), D2).

## Topology: single container (home or small team)

```mermaid
flowchart LR
    subgraph clients[Clients on the private network or tailnet]
        cc[Claude Code, scripts]
        br[Browser]
    end
    subgraph host[One host]
        rp[Optional TLS front:<br/>Caddy or Tailscale serve]
        subgraph app[spyglass container]
            dp[":8080 data plane"]
            ap[":9090 admin plane and UI"]
        end
        pg[(Postgres container<br/>volume: pgdata)]
    end
    up[Provider APIs]

    cc --> rp --> dp
    br --> rp --> ap
    dp --> up
    app --> pg
```

- One `spyglass` container runs everything: data plane, admin plane, recorder and the retention job.
- Postgres runs beside it with its own volume.
- The data plane binds to a private address. The admin plane has no sign-in, so it binds to loopback, and `serve` refuses to start if `listen.admin` is not a loopback address. Remote access to the data plane goes through a tunnel such as WireGuard or Tailscale.
- Sized for about 12,000 to 50,000 calls per day (UI report 6.1 estimates); see [Sizing](#sizing).

## Later: multi-replica tier

Not built in v1. The process keeps the property that makes it possible: it is stateless on disk, and every write is an idempotent insert on the call id, so a second replica could write to the same Postgres without coordination. What is missing, and what each item needs when the tier is built:

| State | v1 (one replica) | Needed for several replicas |
|---|---|---|
| Key map | In process, built from the config snapshot on start and on `SIGHUP` | Nothing: every replica reads the same config file and reloads it on `SIGHUP` |
| Live tail | In-process fan-out in `recorder` | A `LiveFeed` port: `store/postgres` sends `NOTIFY` after each commit, relayed by every replica (see [backend.md, Later versions](backend.md#storage-and-scale)) |
| Retention job | One process runs it | Any replica; a Postgres advisory lock keeps it single-writer |
| Migrations | `serve` applies them at start under goose's session lock | A migration job (`--migrate=off` on replicas) when change control is strict |
| Tool-call pairing | At read time from append-only halves | Nothing: the halves are plain inserts, so either order and any replica is fine |
| Budgets, rate limits, circuit breakers | Deferred ([backend.md](backend.md#later-versions)) | Shared counter row, per-replica limit shares, per-replica breakers |

A load balancer in front of replicas must not buffer responses, must keep idle connections open longer than the longest expected stream, and needs no session affinity. The admin plane would need admin sign-in first ([backend.md](backend.md#gateway-features-decision-c1)), then a separate ingress behind SSO or a VPN.

## Container image

- Multi-stage build:
  1. Node stage: `npm ci && npm run build` in `web/`.
  2. Go stage: copy the UI build into `internal/ui/dist`, then `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=…"`.
  3. Runtime stage: `gcr.io/distroless/static-debian12:nonroot`, the binary only.
- Runs as the distroless `nonroot` user (uid 65532).
- Multi-arch: `linux/amd64` and `linux/arm64`.
- Entrypoint `spyglass`; command `serve`.
- Image tags follow release versions. Deployments pin a version, never `latest`.

## Configuration

One YAML file, path from `--config` or `SPYGLASS_CONFIG`. Every scalar key can be overridden by an environment variable named `SPYGLASS_<SECTION>_<KEY>` (the `LLM_PROXY_` names of release 0.1.0 are read when the `SPYGLASS_` name is unset, with a deprecation warning, until 0.4.0; [ADR 0054](../adrs/0054-rename-to-spyglass.md)); a secret reference is overridden by its parts (`SPYGLASS_DATABASE_URL_ENV` or `SPYGLASS_DATABASE_URL_FILE`), and the lists (`providers`, `keys`, `pricing`) are not overridable. Secrets are never inlined; they are referenced by environment variable or file.

```yaml
listen:
  data: ":8080"
  admin: "127.0.0.1:9090"       # must be a loopback address; serve refuses to start otherwise
  tls:                          # optional; usually terminated in front
    cert_file: ""
    key_file: ""

database:
  url: { env: SPYGLASS_DATABASE_URL }
  max_conns: 20

providers:                      # resolved decision D7; see upstream-connection.md
  - name: anthropic
    kind: anthropic
    base_url: https://api.anthropic.com
    auth_mode: proxy_key        # proxy_key | client_passthrough, one per provider
    credential: { file: /run/secrets/anthropic_api_key }
    idle_timeout: 120s          # idle-between-chunks watchdog

keys:                           # virtual keys: names and SHA-256 hashes, never plaintext
  - name: marco-laptop
    sha256: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08   # placeholder
  - name: ci
    sha256: 60303ae22b998861bce3b28f33eec1be758a213c86c93c076dbe9f558c11c752   # placeholder
    disabled: true              # revoked: requests with this key get 401

pricing:                        # versioned by effective date; placeholder prices show the shape only
  - { provider: anthropic, model: claude-opus-4-8, from: 2026-10-01,
      input: 5.00, output: 25.00, cache_read: 0.50, cache_write_5m: 6.25, cache_write_1h: 10.00 }

retention:
  calls: 400d                   # calls and tool_events

server:
  drain_timeout: 10m
  max_request_bytes: 32MiB      # the only request-size limit; larger bodies are rejected

queue_capacity: 10000           # call records; a full queue drops and counts

log:
  level: info
  format: json
```

- Providers, pricing and virtual keys are in this file and nowhere else (resolved decisions D7 and D15). The database holds call records only. A key entry has a unique `name`, the lowercase hex `sha256` of the key, and an optional `disabled: true`. A reload that finds a duplicate name, a duplicate hash or a malformed hash is rejected whole, and the proxy keeps the previous snapshot and logs the error.
- The file holds hashes, not secrets, so it can sit in version control. Anyone who can edit it can add a key and so gain access to the proxy: restrict write access as for any deploy configuration.
- Batch size and interval, the SSE event bound and the shutdown flush timeout are constants in code until measurement says otherwise. `queue_capacity` is the one writer setting.
- `SIGHUP` re-reads the file and the referenced secrets, rebuilds the key map and swaps the snapshot atomically. Requests in flight keep the snapshot they started with. This matters because a restart drains streams for up to 10 minutes. Listener addresses, TLS files, the database URL, `database.max_conns`, `queue_capacity`, `retention.calls`, `server.drain_timeout`, `log.level` and `log.format` are read at start and need a restart: a reload that changes them logs a warning and keeps the running values. Everything else reloads, including `server.max_request_bytes`, which each request reads from its snapshot.
- v1 takes exactly one provider, of kind `anthropic`. A config with no provider, two providers, `kind: openai_compatible`, `credential: none` or `headers_to_add` is rejected at load (those last three are v2 settings; see [upstream-connection.md](upstream-connection.md)).

## Secrets

| Secret | Used for | Source |
|---|---|---|
| Provider API keys | Upstream authentication in `proxy_key` mode | File (Docker or Kubernetes secret) or environment variable |
| Database URL and password | Postgres connection | Environment variable or file |

- Virtual keys are not secrets of the proxy: the config file holds only their SHA-256 hashes. They are 256-bit random values, so there is no pepper and no extra secret to back up. The plaintext key lives only with the client that uses it.
- **Generate a key** outside the proxy. With the helper, `spyglass keygen --name marco-laptop` prints the plaintext key once (`lp_` plus base62 of 32 random bytes) and the `keys` entry to paste into the config. Without it, any 256-bit random value with the `lp_` prefix works, because the proxy hashes the presented string as is:

  ```sh
  key="lp_$(openssl rand -hex 32)"
  printf '%s' "$key" | sha256sum | cut -d' ' -f1    # on macOS: shasum -a 256
  ```

  Put the hash in the config as `sha256`, hand the key to the client, and do not keep the plaintext anywhere else. The proxy cannot show a key again.
- No secret is ever returned by the API or logged. No header value is stored in Postgres; log redaction happens in one place ([backend.md](backend.md#listener-and-router)).
- Rotation: provider keys rotate by updating the secret and sending `SIGHUP`. The database URL and password are read at start, so rotating them needs a restart. Virtual keys rotate by config edit: generate a new key, add its entry, send `SIGHUP`, switch the client, then set `disabled: true` on the old entry (or delete it) and send `SIGHUP` again (gap review R8). A revoked key is rejected from the next request after the reload; a stream already in flight finishes.

### Personal local setup (subscription passthrough)

Resolved decision D6: one person routes their own Claude login through their own proxy. `proxy_key` stays the default; this is the opt-in. It is for one person's own traffic, on one machine, never shared. Heavy or shared use goes through `proxy_key` with an API key. Rules and terms notes are in [upstream-connection.md](upstream-connection.md#claude-code-with-a-claude-subscription).

Generate a key (`spyglass keygen --name me-laptop`, or the `openssl` lines under [Secrets](#secrets)) and use a config like this:

```yaml
listen:
  data: "127.0.0.1:8080"        # loopback only
  admin: "127.0.0.1:9090"

database:
  url: { env: SPYGLASS_DATABASE_URL }

providers:
  - name: anthropic
    kind: anthropic
    base_url: https://api.anthropic.com
    auth_mode: client_passthrough   # no provider credential: the client's own login is forwarded
    idle_timeout: 120s

keys:
  - name: me-laptop
    sha256: <hex sha256 printed by keygen>
```

Claude Code, in `~/.claude/settings.json` (not a committed project file):

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
    "ANTHROPIC_CUSTOM_HEADERS": "x-spyglass-key: lp_…",
    "CLAUDE_CODE_GATEWAY_HINT_HEADERS": "1"
  }
}
```

Do not set `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY` or `apiKeyHelper`; sign in with `/login`. The proxy stores no token, and the call record carries `auth_mode = client_passthrough` only. A call without a valid `x-spyglass-key` gets `401`, and so does a call that carries an `lp_` value in `Authorization` or `x-api-key`.

## Migrations

- Migrations belong to the store adapter. The `store/postgres` SQL migrations live in `internal/store/postgres/migrations/` and are embedded in the binary.
- `serve` applies them with goose at start, under goose's session lock, before it opens the listeners. There is no separate migrate command or container.
- Every migration is **expand then contract**: a release adds columns, tables or indexes in a backward-compatible way; a later release removes what is no longer used. This keeps one-version rollbacks safe.
- A shipped migration is never edited. A change is a new numbered migration file.
- Tables are not partitioned, so there is no partition creation at runtime and no per-partition index build.

## Storage, retention and sizing

**Decisions D1 and D2 are resolved** ([README](README.md#resolved-decisions)): bodies are never stored and call records go straight to the store. In v1 the store is the `store/postgres` adapter, and Postgres is the only store. There is no journal volume, no volume permissions to manage, no encryption-at-rest requirement for bodies.

- Retention is one daily job in `store/postgres`: `DELETE … WHERE ts < cutoff` in batches of 10,000 rows. `calls` and `tool_events` 400 days.
- Encryption of the Postgres volume follows the operator's normal policy for a database that holds usage metadata. It no longer needs to protect prompts or outputs, because none are stored.
- A normalization fix applies to new calls only. Rows already written are not recomputed, because there are no raw bytes to recompute from. `usage_raw` is kept on each call row so the fix can be applied to old rows where the raw usage is enough.
- Queue capacity bounds the data lost to a crash or a long Postgres outage (default 10,000 records, a few MB of memory). Dropped records are counted in `spyglass_calls_dropped_total`.

### Sizing

Estimates, not measurements: no live Claude Code traffic has been captured yet (UI report 2).

| Item | 12,000 calls/day | 50,000 calls/day |
|---|---|---|
| `calls` (~350 bytes/row measured in UI report 6.4, plus a few hundred bytes for the extra columns and indexes) | ~1.7 to 3 GB for 400 days | ~7 to 12 GB for 400 days |
| CPU and memory for `serve` | 1 vCPU, 512 MB | 2 vCPU, 1 GB |

At the upper end that is about 20 million `calls` rows over 400 days, 0.14 to 0.58 calls per second on average. Plain Postgres with a `(ts DESC, id DESC)` index serves a 24-hour list in milliseconds. A 30-day aggregate over about 1.5 million rows takes about 0.9 to 1.5 seconds (measured below). `tool_events` rows add to this; they are small, metadata-only rows and have not been estimated separately. Measure the real size in the phase-1 spike and update this table.

**Measured usage aggregate** ([ADR 0021](../adrs/0021-postgres-usage-aggregation.md)). `Usage` over 1,500,000 generated `calls` rows spread evenly over 30 days (8 keys, 3 models, 4,000 sessions, about 3% errors and 2% unpriced), Postgres 16 in a container on an Apple M5 Pro with the default Postgres settings and the existing indexes only. `calls` was 697 MB with indexes (355 MB heap), about 465 bytes per row. Five runs each; the range is `time.Since` around the `Usage` call, so it includes the round trip:

| `GroupBy` | `Grain` | Buckets returned | Median | Slowest |
|---|---|---|---|---|
| none | day | 30 | 0.92 s | 1.14 s |
| none | hour | 720 | 0.86 s | 0.92 s |
| key | day | 240 | 1.04 s | 1.10 s |
| model | day | 90 | 1.05 s | 1.05 s |
| session | day | 120,000 | 1.52 s | 1.94 s |

Each query reads every row of the range (one pass, one sort per group for the percentiles), so the time grows with rows in range, not with the number of buckets, apart from the output size of a session grouping. The ungrouped and per-key 30-day views sit at the 1 s usage trigger for rollup tables in [backend.md](backend.md#storage-and-scale). Re-run it with `SPYGLASS_SIZING_ROWS=1500000 go test -run TestUsageSizing -v ./internal/store/postgres`. Partitioning and rollups come back at the triggers in [backend.md](backend.md#storage-and-scale).

## Backups

| Data | Importance | Backup |
|---|---|---|
| `calls`, `tool_events` | Audit history (gap review N8) | Nightly full `pg_dump` |
| Config file and secrets | Required to restore (providers, pricing, virtual-key hashes, credentials). Lose the hashes and every client needs a new key | The config file in version control; secrets in the secret store's own backup |

Test a restore at least once per release cycle: restore Postgres, start `serve` against it, and open a recent call in the UI.

## TLS and network exposure

- The binary serves plain HTTP by default. TLS is terminated in front of it by Caddy, Tailscale (`tailscale serve`), or a cloud load balancer (gap review N2). Optional `listen.tls` serves TLS directly.
- **Data plane:** private network or tailnet. When exposed beyond that, put TLS and connection limits in front, and keep virtual-key auth on.
- **Admin plane:** loopback only, with no sign-in in v1. `serve` refuses to start if `listen.admin` is not a loopback address (`127.0.0.1`, `::1` or `localhost`), so it cannot be exposed by mistake. To use it from another machine, tunnel to the host, for example `ssh -L 9090:127.0.0.1:9090 <host>`. Exposing it beyond localhost, or sharing it, needs admin sign-in first ([backend.md, Later versions](backend.md#gateway-features-decision-c1)).
- **Load balancers and reverse proxies in front of the data plane** must stream: turn off response buffering (for nginx, `proxy_buffering off`), set idle and read timeouts above the longest stream (the drain timeout of 10 minutes is a good floor), and keep HTTP/1.1 keep-alive or HTTP/2 to the backend.
- **Web application firewalls:** Anthropic's gateway docs report that WAF body rules (for example AWS WAF `CrossSiteScripting_Body`) block real Claude Code requests with an HTML `403`, because prompts contain XML-style tags and source code. Exempt `/v1/messages` from request-body inspection.
- Outbound: the container needs egress only to the configured provider base URLs and Postgres.

## Observability

Health endpoints, on both listeners:

| Endpoint | Data plane meaning | Admin plane meaning |
|---|---|---|
| `/healthz` | Process alive | Process alive |
| `/readyz` | Config loaded. **Not** tied to Postgres, because the data plane keeps working when Postgres is down | Postgres reachable |

During shutdown both `/readyz` endpoints report not-ready first, so a load balancer stops sending new requests while streams drain.

Prometheus metrics at `/metrics` on the admin listener. This is a minimal set; each collector is declared in the package that updates it:

| Metric | Watch for |
|---|---|
| `spyglass_requests_total{family,provider,status,error_class}` | Error rates |
| `spyglass_request_duration_seconds`, `spyglass_ttft_seconds` | Latency |
| `spyglass_proxy_overhead_seconds` | Time the proxy adds before the upstream request is sent; target p99 under 5 ms (gap review R15). It runs from request arrival, before the first data-plane middleware, to the upstream request headers being written. It includes the client body upload and the upstream connection wait (DNS, dial and TLS), so a slow client or a cold connection shows here |
| `spyglass_inflight_streams` | Streaming requests being forwarded now; load and drain progress |
| `spyglass_call_queue_depth`, `spyglass_calls_dropped_total{reason}` | Lost log records (should be zero). A rate above zero on `reason="unwritable"` means the store rejected records the writer does not sanitize: a bug; the error log names each call id and the SQLSTATE |
| `spyglass_writer_errors_total` | Store writes (Postgres in v1) failing |
| `spyglass_writer_systematic_failure` | 1 while the writer is blocked on a batch of 8 or more records that the store rejects in full: a systematic bug, for example a migration the writer does not match. The queue fills until it is fixed |
| `spyglass_live_events_dropped_total` | Slow UI subscribers |
| `spyglass_config_reloads_total{result}` | `SIGHUP` reloads that failed, so the proxy kept the previous snapshot |
| `spyglass_retention_deleted_total` | Rows the retention job deleted |
| `spyglass_retention_last_run_timestamp_seconds` | Retention job not running: the timestamp stops moving (0 before the first run) |

Label sets are bounded, so no series count grows with traffic. `family` and `provider` come from the code and the config file, `status` is an HTTP status code, `error_class` is the closed set of call error classes, `reason` is `queue_full`, `shutdown` or `unwritable`, and `result` is `success` or `failure`. No metric carries a key name, session, model or call id; per-key detail comes from the call log. `spyglass_requests_total` counts forwarded calls that finished, so a request rejected before forwarding (bad virtual key, unknown path) is in the access log and not in this counter.

The Go runtime and process collectors are served beside these. `cmd/spyglass/testdata/metrics.golden` pins the names, types and label names above.

- Logs: `log/slog` JSON to stdout, one line per request with the call id. Logs never contain bodies or credentials.
- Dropped calls and writer errors are visible only in `/metrics` in v1. There is no alert feed; see [backend.md](backend.md#later-versions).
- Grafana dashboards over rollup tables, and OTLP span export, are deferred with rollups and OTel.

## Upgrades

1. Read the release notes for migrations that need attention.
2. Back up Postgres (`pg_dump`).
3. Pull the new image and restart `serve`. The new process applies its migrations at start. The old process drains in-flight streams for up to `drain_timeout` before it exits; set the container stop grace period to match. Clients that lose a connection retry: Claude Code treats a cut stream as a dropped connection and retries according to its own rules.
4. The new process starts with an empty queue. Records still queued in the old process were flushed during its drain.

### Shutdown and the stop grace period

On `SIGTERM` the process reports not-ready on both `/readyz` endpoints, stops accepting connections, waits for in-flight streams up to `server.drain_timeout`, flushes the recorder queue within 30 seconds, then stops the retention job, closes the store and exits. A second signal forces an immediate exit and logs it. Streams still open at the drain deadline are cut, and the process waits up to 2 seconds for their handlers to hand the final records to the recorder before it closes the queue. Records still queued at the flush deadline are counted in `spyglass_calls_dropped_total{reason="shutdown"}` and their number is logged.

The data listener closes right after `/readyz` flips to not-ready, with no pause between them. A load balancer or orchestrator in front of the proxy that polls `/readyz` keeps routing new connections until its next probe, and those connections are refused. Make it stop routing before the proxy gets `SIGTERM`: for example, Kubernetes removes the pod from its endpoints, and a `preStop` hook that sleeps covers the propagation delay. The single-host Compose setup has no load balancer in front and is not affected.

The supervisor must wait at least as long as the drain before it sends `SIGKILL`, or it kills the process mid-drain. Set the grace period equal to `server.drain_timeout` (default 10 minutes). A stream that runs to the drain deadline leaves no time for the flush; to keep the flush in that case, add 30 seconds.

- Docker Compose: `stop_grace_period: 10m` (see [docker-compose.yml](../../docker-compose.yml)). With `docker stop`, use `--time 600`.
- Kubernetes: `terminationGracePeriodSeconds: 600`.
- systemd: `TimeoutStopSec=10min` in the `[Service]` section. The default is 90 seconds, which cuts streams. Keep `KillSignal=SIGTERM` (the default) and `SendSIGKILL=yes`.

If you change `server.drain_timeout`, change the grace period with it. The drain timeout is read at start, so a `SIGHUP` reload does not change it.

Compatibility rules:

- Rollback: migrations are expand-only within a release, so the previous binary works against the expanded schema and rolling back one release needs no database restore.
- A config-only change (pricing, provider credentials) needs `SIGHUP`, not a restart.

## docker-compose

The tested compose file is [docker-compose.yml](../../docker-compose.yml) in the repository root, with [config.example.yaml](../../config.example.yaml) and the secret files under `secrets/*.example`. CI starts it against a fake upstream ([scripts/compose-test.sh](../../scripts/compose-test.sh)). Recurring tasks are in the [runbook](../runbook.md); the single-person setup is in the [personal local setup](../personal-local-setup.md). What the file does:

- Postgres 16 with a named volume and a `pg_isready` health check. `spyglass` waits for it to be healthy.
- The config is mounted read-only. The database URL (which carries the password), the Postgres password and the provider key reach the containers as file secrets.
- `spyglass` mounts no data volume: it writes nothing to disk. `serve` migrates the database at start, so there is no migrate service.
- The data plane is published on `127.0.0.1:8080`; front it with Tailscale or Caddy for remote clients. `stop_grace_period: 10m` matches `server.drain_timeout`.
- The admin listener is loopback-only, so inside the container it is not reachable through a published port. To open the UI at `http://127.0.0.1:9090/ui/`, run `serve` on the host (see [Personal local setup](#personal-local-setup-subscription-passthrough)), or use the host network (`network_mode: host`, Linux) with `listen.admin: 127.0.0.1:9090`. There is no sign-in.
- Before the first start, generate a key and add its entry under `keys:`. Without a `keys` entry every data-plane request gets `401`. After later key changes, run `docker compose kill -s HUP spyglass`.
- Point Claude Code at `http://127.0.0.1:8080` as described in [upstream-connection.md](upstream-connection.md#claude-code-with-a-proxy-held-api-key).
