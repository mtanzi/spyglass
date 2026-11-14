# Runbook

Recurring tasks for the single-container deployment: `spyglass` and Postgres from [docker-compose.yml](../docker-compose.yml). The design behind each step is in [architecture/deployment.md](architecture/deployment.md). For one person routing their own Claude login through a local proxy, use the [personal local setup](personal-local-setup.md) instead.

> **The admin plane has no sign-in.** It serves the call log, the metrics and the UI to anyone who can reach it. It listens on loopback only, and `serve` refuses to start on any other address. Never publish its port, proxy it, or share a tunnel to it.

Commands assume the repository root and `docker compose` v2.

## First start

1. Create the config and the three secret files. Each `.example` file shows the shape:

   ```sh
   chmod 700 secrets
   cp config.example.yaml config.yaml
   pw=$(openssl rand -hex 24)
   printf '%s\n' "$pw" > secrets/pg_password
   printf 'postgres://llmproxy:%s@postgres:5432/llmproxy?sslmode=disable\n' "$pw" > secrets/database_url
   printf '%s\n' 'sk-ant-…' > secrets/anthropic_api_key      # your Console API key
   chmod 644 config.yaml secrets/*
   ```

   The containers run as uid 65532 (`spyglass`) and uid 999 (Postgres), so the mounted files are mode 644, and the `secrets` directory is mode 700 so no other local user can reach them. Keep it 700. `config.yaml` and `secrets/*` are git-ignored.
2. [Generate a key](#generate-a-key) and put its entry under `keys:` in `config.yaml`. Without an entry every data-plane request gets `401`.
3. Start the stack. The first run builds the image from this directory. To run a released image, set `SPYGLASS_IMAGE=ghcr.io/mtanzi/spyglass:<version>`, in the shell or an `.env` file.

   ```sh
   docker compose up -d
   curl -s http://127.0.0.1:8080/readyz
   ```

`serve` applies its database migrations at start, so there is no migrate step. The data plane is published on `127.0.0.1:8080` (change the host port with `SPYGLASS_PORT`). The admin plane is not published; see [Reach the admin UI](#reach-the-admin-ui).

## Keys

A virtual key is a name and the SHA-256 of a secret value. The config holds only hashes. The proxy cannot show a key again, so hand the plaintext to its client and keep it nowhere else.

### Generate a key

With the binary, from the image (a locally built one is `spyglass:local`):

```sh
docker run --rm "${SPYGLASS_IMAGE:-spyglass:local}" keygen --name marco-laptop
```

It prints the plaintext once and a `keys:` entry to paste into the config. Names must be unique and contain no whitespace; history is grouped by name, so keep them stable.

Without the binary, any `lp_` value with 256 bits of randomness works, because the proxy hashes the presented string as is:

```sh
key="lp_$(openssl rand -hex 32)"
printf '%s' "$key" | shasum -a 256 | cut -d' ' -f1      # Linux: sha256sum
```

Put the hash in the config as `sha256`.

### Add, revoke and rotate

Edit `config.yaml`, then reload without a restart. A restart drains streams for up to 10 minutes; a reload does not touch them.

```sh
docker compose kill -s HUP spyglass
```

- **Add:** append the entry from `keygen`, reload.
- **Revoke:** set `disabled: true` on the entry (or delete it), reload. The key gets `401` from the next request; a stream already in flight finishes.
- **Rotate:** run `keygen` with a new name, add the entry, reload, switch the client to the new key, then revoke the old entry and reload again.

A reload that finds a duplicate name, a duplicate hash or a malformed hash is rejected whole, and the proxy keeps the previous keys. Check `spyglass_config_reloads_total{result="failure"}` or the log after every reload. Listener addresses, TLS files, the database URL, `database.max_conns`, `queue_capacity`, `retention.calls`, `server.drain_timeout` and `log.*` need a restart; a reload that changes them logs a warning and keeps the running values.

## Rotate secrets

- **Provider key:** write the new key to `secrets/anthropic_api_key`, then reload with `SIGHUP`. A Docker file secret is a bind mount of the host file, so the container sees the new content. If the editor replaced the file by a new inode and the container still sees the old content, run `docker compose up -d --force-recreate spyglass`.
- **Database password:** it is read at start, so it needs a restart, and the two places that hold it must change together.

  ```sh
  pw=$(openssl rand -hex 24)
  docker compose exec postgres psql -U llmproxy -d llmproxy -c "ALTER USER llmproxy PASSWORD '$pw'"
  printf '%s\n' "$pw" > secrets/pg_password
  printf 'postgres://llmproxy:%s@postgres:5432/llmproxy?sslmode=disable\n' "$pw" > secrets/database_url
  chmod 644 secrets/pg_password secrets/database_url
  docker compose up -d --force-recreate spyglass
  ```

  Postgres reads `pg_password` only when it first creates the data directory, so the `ALTER USER` is what changes the live password; the file keeps the two in agreement for a later rebuild.

## Upgrade and roll back

1. Read the release notes for migrations that need attention.
2. [Back up](#backups).
3. Pin the new version, pull and restart. In the shell or the `.env` file, set `SPYGLASS_IMAGE` to the new release, for example `SPYGLASS_IMAGE=ghcr.io/mtanzi/spyglass:0.3.0` (the version has no `v`; there is no `latest` tag). Then:

   ```sh
   docker compose pull spyglass
   docker compose up -d spyglass
   ```

   (For a locally built image, leave `SPYGLASS_IMAGE` unset and use `docker compose build spyglass` in place of `pull`.) The old container gets `SIGTERM` and drains in-flight streams for up to `server.drain_timeout`; the file sets `stop_grace_period: 10m` to match. Keep the two equal, and add 30 seconds if you want the final flush to survive a stream that runs to the deadline. A client that loses a connection retries by its own rules.
4. Check `curl -s http://127.0.0.1:8080/readyz`, and run `docker compose exec spyglass spyglass version` to confirm the new version.

The new process applies its migrations at start. Migrations are expand-only within a release, so the previous version still works against the expanded schema.

**Roll back one version:** set `SPYGLASS_IMAGE` to the previous version and run `docker compose up -d spyglass`. No database restore is needed. Rolling back two or more versions is not supported; restore the pre-upgrade backup instead.

A config-only change (pricing, provider key, keys) needs `SIGHUP`, not an upgrade.

### Upgrading from llm-proxy 0.1.0

Release 0.1.0 was named `llm-proxy`. Later releases are named `spyglass` ([ADR 0054](adrs/0054-rename-to-spyglass.md)). Your data stays where it is: the compose project is still named `llm-proxy`, so the Postgres volume `llm-proxy_pgdata`, the database `llmproxy` and its user keep their names. Follow [Upgrade and roll back](#upgrade-and-roll-back) with these changes:

1. **Environment.** Rename the variables in your shell or `.env` file: `LLM_PROXY_IMAGE` to `SPYGLASS_IMAGE`, and the same for `_CONFIG_FILE`, `_SECRETS_DIR` and `_PORT`. Rename any `LLM_PROXY_*` override the binary reads, for example `LLM_PROXY_DATABASE_URL` to `SPYGLASS_DATABASE_URL`. In the same step, update every secret reference in `config.yaml` that names a renamed variable, for example `url: { env: LLM_PROXY_DATABASE_URL }` to `url: { env: SPYGLASS_DATABASE_URL }`. The fallback only maps a `SPYGLASS_` name to its `LLM_PROXY_` name, so a config that still names `LLM_PROXY_DATABASE_URL` fails at start after you rename the export. The old names still work when the new name is unset, and `serve` logs a warning that names each old variable it read. They stop working in 0.4.0.
2. **Image.** The image is now `ghcr.io/mtanzi/spyglass`: set `SPYGLASS_IMAGE=ghcr.io/mtanzi/spyglass:<version>`.
3. **Service name.** The compose service is now `spyglass`, so the 0.1.0 container (`llm-proxy-llm-proxy-1`) becomes an orphan that still holds port 8080. Stop it with the full drain time, then start the new service and remove the orphan:

   ```sh
   docker compose pull spyglass
   docker stop --time 600 llm-proxy-llm-proxy-1
   docker compose up -d --remove-orphans spyglass
   ```

4. **Clients.** Send the virtual key in `x-spyglass-key`. In `~/.claude/settings.json` change `"ANTHROPIC_CUSTOM_HEADERS": "x-llm-proxy-key: lp_…"` to `"x-spyglass-key: lp_…"`. The old header still works until 0.4.0, with the same checks; the proxy logs one warning per process the first time a client sends it, without the key. When a request carries both headers, `x-spyglass-key` is used.
5. **Metrics.** Every metric is renamed from `llm_proxy_*` to `spyglass_*`, with no alias: update Prometheus queries, recording rules, alerts and dashboards. The old series stop at the upgrade.
6. **Binary.** A release binary is now `spyglass-v<version>-<os>-<arch>`, and the command is `spyglass serve`.

**Roll back to 0.1.0:** set `SPYGLASS_IMAGE=ghcr.io/mtanzi/llm-proxy:0.1.0` (the compose file also sets `LLM_PROXY_CONFIG`, so the 0.1.0 binary finds the config) and run `docker compose up -d spyglass`. Clients must send `x-llm-proxy-key` again, and the metrics go back to `llm_proxy_*`.

## Backups

| Data | Backup |
|---|---|
| `calls`, `tool_events` | Nightly `pg_dump` |
| `config.yaml`, `secrets/` | Version control for the config (it holds hashes, not secrets). The secret store's own backup for `secrets/`. Without the key hashes every client needs a new key |

Dump and restore:

```sh
docker compose exec -T postgres pg_dump -U llmproxy -Fc llmproxy > llmproxy-$(date +%F).dump
# restore into an empty database:
docker compose exec -T postgres pg_restore -U llmproxy -d llmproxy --no-owner < llmproxy-<date>.dump
```

**Test a restore at least once per release cycle.** [scripts/restore-test.sh](../scripts/restore-test.sh) does the whole drill against a throwaway stack: record a call, dump, restore into a fresh Postgres, start `serve`, and open the call by id and through the UI. CI runs it on every change. Run it by hand against a built image before a release, and do the same by hand against a real dump once: restore into a scratch Postgres, start `serve` against it, and open a recent call in the UI.

```sh
docker build -t spyglass:local . && scripts/restore-test.sh spyglass:local
```

## TLS and network exposure

The binary serves plain HTTP unless `listen.tls` is set. Terminate TLS in front of the data plane, or keep it on a private network or tailnet. Keep virtual-key auth on in every case.

- **Tailscale:** `tailscale serve --bg 8080` publishes `127.0.0.1:8080` to the tailnet with a certificate. Clients use the tailnet name as `ANTHROPIC_BASE_URL`.
- **Caddy:** the minimum is one block. Caddy streams by default and does not buffer responses.

  ```
  spyglass.example.com {
      reverse_proxy 127.0.0.1:8080 {
          flush_interval -1
          transport http {
              read_timeout 15m
              response_header_timeout 15m
          }
      }
  }
  ```

- **Any reverse proxy or load balancer:** turn off response buffering (nginx: `proxy_buffering off`), set idle and read timeouts above 10 minutes (the longest stream the drain allows), and keep HTTP/1.1 keep-alive or HTTP/2 to the backend.
- **Web application firewall:** exempt `/v1/messages` from request-body inspection. Rules such as AWS WAF `CrossSiteScripting_Body` block real Claude Code requests with an HTML `403`, because prompts hold XML-style tags and source code.
- **Egress:** the proxy needs to reach the provider's `base_url` and Postgres, nothing else.

Do not put the admin plane behind any of these.

## Reach the admin UI

The admin plane (`/ui/`, `/admin/*`, `/metrics`) listens on `127.0.0.1:9090` inside the container, which no published port reaches. Three ways in:

- **Run `serve` on the host.** Run Postgres from compose with [docker-compose.personal.yml](../docker-compose.personal.yml), which publishes it on `127.0.0.1:5432`, and start `spyglass serve` on the host, as in the [personal local setup](personal-local-setup.md#quick-start). The UI is then at `http://127.0.0.1:9090/ui/`.
- **Host network (Linux only).** Give the `spyglass` service `network_mode: host` and drop its `ports:`. The container then shares the host's loopback, so also set `listen.data: "127.0.0.1:8080"` (otherwise the data plane listens on every interface), and point `database.url` at a Postgres published on the host loopback. Anyone with a shell on the host can reach the UI.
- **From a container beside it, for one-off checks:**

  ```sh
  docker run --rm --network container:$(docker compose ps -q spyglass) curlimages/curl -s http://127.0.0.1:9090/admin/calls?limit=1
  ```

From another machine, tunnel to the host and open the UI through the tunnel. The tunnel reaches the host loopback, so it works only with the first two options (`serve` on the host, or host network):

```sh
ssh -L 9090:127.0.0.1:9090 <host>      # then open http://127.0.0.1:9090/ui/
```

Exposing the admin plane beyond localhost, or sharing it, needs admin sign-in first, which v1 does not have.

## Metrics

Prometheus metrics are at `/metrics` on the admin listener. Scrape from the same host or through the tunnel; do not publish the admin port. Label sets are bounded, and no metric carries a key name, session, model or call id.

| Metric | Watch for |
|---|---|
| `spyglass_requests_total{family,provider,status,error_class}` | Error rates |
| `spyglass_request_duration_seconds`, `spyglass_ttft_seconds` | Latency |
| `spyglass_proxy_overhead_seconds` | Time the proxy adds before the upstream request; target p99 under 5 ms |
| `spyglass_inflight_streams` | Load and drain progress |
| `spyglass_call_queue_depth`, `spyglass_calls_dropped_total{reason}` | Lost call records; should stay zero. `reason="unwritable"` above zero is a bug: the error log names each call id and SQLSTATE |
| `spyglass_writer_errors_total` | Postgres writes failing |
| `spyglass_writer_systematic_failure` | 1 while the writer is blocked on a batch Postgres rejects in full; the queue fills until the cause is fixed. See [Observability](architecture/deployment.md#observability) |
| `spyglass_live_events_dropped_total` | Slow UI subscribers |
| `spyglass_config_reloads_total{result}` | A `SIGHUP` that failed, so the old config is still running |
| `spyglass_retention_deleted_total` | Rows the retention job deleted |
| `spyglass_retention_last_run_timestamp_seconds` | Retention not running: the timestamp stops moving |

`/healthz` (process alive) and `/readyz` are on both listeners. The data plane's `/readyz` does not depend on Postgres, because the data plane keeps working when Postgres is down; the admin plane's does. Dropped calls and writer errors show up only in `/metrics` in v1.

## Sizing

Provisional. These are the design estimates, not measurements; [ADR 0039](adrs/0039-performance-targets-and-load-verification.md) replaces them with measured values.

| Calls per day | `calls` over 400 days | `serve` |
|---|---|---|
| 12,000 | about 1.7 to 3 GB | 1 vCPU, 512 MB |
| 50,000 | about 7 to 12 GB | 2 vCPU, 1 GB |

Retention deletes `calls` and `tool_events` older than `retention.calls` (400 days) in a daily job. Size the Postgres volume for the row estimate above plus the dump you keep.

## Not covered

Multi-replica deployment is a later tier, not built; see [deployment.md](architecture/deployment.md#later-multi-replica-tier).
