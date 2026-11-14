# Personal local setup

One person routes their own Claude Code sessions, signed in with their own Claude login, through a proxy on their own machine. The proxy logs each call (who, model, tokens, cost, timings, tool calls) and never stores prompts or responses. The reasons and the rules are in [upstream-connection.md](architecture/upstream-connection.md#claude-code-with-a-claude-subscription); this page is the procedure.

> **The admin plane has no sign-in.** The UI and the `/admin/*` API show every call to anyone who can reach port 9090. They listen on `127.0.0.1` only, and `serve` refuses to start on any other address. Do not forward, tunnel or proxy that port to anyone else.

For a shared or always-on deployment, or for heavy use, do not use this mode. Use `proxy_key` with an API key and follow the [runbook](runbook.md).

## Quick start

You need Docker, Go and Node 22 (to build the UI), `curl` and `openssl`. Run the commands from the repository root. Nothing here uses a real credential until you sign in with `/login` in step 6.

1. **Start Postgres.** The compose override publishes it on `127.0.0.1:5432` only.

   ```sh
   chmod 700 secrets && openssl rand -hex 24 > secrets/pg_password && chmod 644 secrets/pg_password
   docker compose -f docker-compose.yml -f docker-compose.personal.yml up -d --wait postgres
   ```

2. **Build the proxy.**

   ```sh
   make ui-build build        # writes bin/spyglass; without the UI, use `make build` alone
   ```

3. **Create the config and a key.** Copy the loopback config, then generate a key.

   ```sh
   cp config.personal.example.yaml config.yaml
   bin/spyglass keygen --name me-laptop
   ```

   `keygen` prints the plaintext key once (`lp_…`) and a `keys:` entry. Replace the placeholder entry under `keys:` in `config.yaml` with that entry. Copy the plaintext key now; the proxy cannot show it again.

4. **Run the proxy.** It applies its migrations at start, then listens on `127.0.0.1:8080` (calls) and `127.0.0.1:9090` (UI and admin API).

   ```sh
   export SPYGLASS_DATABASE_URL="postgres://llmproxy:$(cat secrets/pg_password)@127.0.0.1:5432/llmproxy?sslmode=disable"
   bin/spyglass serve --config config.yaml
   ```

   If your `config.yaml` comes from release 0.1.0, it may say `url: { env: LLM_PROXY_DATABASE_URL }`. Change it to `url: { env: SPYGLASS_DATABASE_URL }` before you use this export: the old name in `config.yaml` is not mapped to the new one.

   Leave it running. In another terminal, `curl -s http://127.0.0.1:8080/readyz` prints `ok`.

5. **Point Claude Code at it.** Add this to `~/.claude/settings.json`, with your key. Use the user file, not a project's committed `.claude/settings.json`: the key is a credential.

   ```json
   {
     "env": {
       "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
       "ANTHROPIC_CUSTOM_HEADERS": "x-spyglass-key: lp_…",
       "CLAUDE_CODE_GATEWAY_HINT_HEADERS": "1"
     }
   }
   ```

   - `ANTHROPIC_BASE_URL` sends Claude Code's requests to the proxy.
   - `ANTHROPIC_CUSTOM_HEADERS` adds the virtual key as the `x-spyglass-key` header. In this mode your own login occupies `Authorization`, so the key travels in its own header. One `Name: Value` pair per line. (Release 0.1.0 named this header `x-llm-proxy-key`; that name still works until 0.4.0, with a warning in the proxy log.)
   - `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1` makes Claude Code send the hint headers that the session tree and tool durations need. They are off by default behind a custom base URL.

   **Do not set `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY` or `apiKeyHelper`.** Any of them replaces your saved login, and the proxy would reject an `lp_` value there with `401`.

6. **Sign in and run.** Start `claude`, run `/login` and complete the sign-in through Anthropic's own flow. The proxy takes no part in it and stores no token. Then send a prompt.

7. **Confirm the call was recorded.** The newest call is in the admin API:

   ```sh
   curl -s 'http://127.0.0.1:9090/admin/calls?limit=1'
   ```

   `count` should be at least 1 and the call's `key` should be `me-laptop`. Open `http://127.0.0.1:9090/ui/` to see the same in the UI. In this mode the cost is an API-equivalent figure, labelled so in the UI; your plan bills by subscription, not per token.

If a call fails with `401`, the key header is missing or wrong (`ANTHROPIC_CUSTOM_HEADERS` not picked up, or the hash in `config.yaml` is not the one `keygen` printed for this key) or an `lp_` value is set in a credential variable. Restart Claude Code after editing `settings.json`. After editing `config.yaml`, send `SIGHUP` to the `serve` process (`kill -HUP <pid>`) instead of restarting it.

To stop: `Ctrl-C` in the `serve` terminal, then `docker compose -f docker-compose.yml -f docker-compose.personal.yml down` (add `-v` to delete the call history).

## What the proxy must and must not do

The proxy, in this mode:

- forwards your `Authorization` header and the full `anthropic-beta` header unchanged, and sends traffic only to `api.anthropic.com`;
- forwards the `anthropic-ratelimit-unified-*` response headers, so Claude Code can show your plan usage;
- records `auth_mode = client_passthrough` on each call and nothing else about the credential.

The proxy does not store the OAuth token (not in the database, the logs or as a hash), hold or inject a token of its own, offer or capture a sign-in, or fall back to a proxy-held key. A request without a valid `x-spyglass-key` gets `401`, and so does one that carries an `lp_` value in `Authorization` or `x-api-key`. The key never reaches Anthropic.

You must keep to the setup's own rules:

- one person, one login, one machine. Do not share the proxy, the key or the login with anyone, and do not present it as a team service;
- keep the data plane on `127.0.0.1`. The example config binds it there;
- keep the admin plane on loopback (see the warning at the top).

## Terms

Anthropic's terms allow signing in to the unmodified Claude Code binary with your own subscription, and they do not allow routing other people's requests through Free, Pro or Max credentials, or collecting, storing or intermediating Claude.ai credentials. They assume ordinary, individual use, and Anthropic may enforce this without notice. This setup is designed to stay inside that reading; it is not legal advice. The quoted terms are in [upstream-connection.md](architecture/upstream-connection.md#terms-that-apply).

Technical limits: your plan's usage limits apply, and a fleet of parallel sessions on one login uses them fast. Remote Control is disabled behind a non-Anthropic base URL, and gateway sessions are not eligible for the HIPAA configuration. Some calls never pass through the proxy (the fast-mode check, the WebFetch domain check and telemetry go to Anthropic directly), so the log does not show them.
