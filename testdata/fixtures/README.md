# Test fixtures

Recorded Anthropic exchanges. Three kinds of test read them:

- the extractor goldens (`internal/family/anthropic`, `make goldens`),
- the fake upstream (`internal/fakeupstream`), which replays them with
  controllable timing,
- the end-to-end tests (`cmd/spyglass/e2e_test.go`, `make e2e`).

All text in them is fake. Prompt, response and tool text starts with
`FAKEPROMPT-`, so a test can tell it from real data and the no-bodies checks can
search for it.

## Format

One directory per exchange:

| File | Content |
|---|---|
| `meta.json` | `endpoint`, `status`, `streamed`, `ttfb_ms`, `ttft_ms`, `duration_ms`, optional `error_class` |
| `request.json` | the request body |
| `response.sse` | the response body of a streamed exchange |
| `response.json` | the response body of any other exchange |
| `replay.json` | optional: `headers` the upstream sends, and `chunks` (`bytes`, `delay_ms`) |
| `record.golden.json` | the call record the extractor builds (`make goldens`) |

Without `replay.json` the fake upstream sends one chunk per SSE event, or the
whole body for a non-streamed exchange. A test can change the timing per call
with `fakeupstream.Reply`: `Split` (fixed-size chunks that cut events), `Delay`,
`Pings`, `AbortAfter`, `Status` and `Headers`.

`claude_code_project` is a hand-written exchange, not a capture: its request has a
system prompt in text blocks, the second one shaped like the environment block of
Claude Code (`Primary working directory:`, `Is a git repository:`, `Platform:`),
which the extractor reads to name the project (ADR 0059). That block lists no git
remote. The capture tool scrubs system prompts, so a refresh cannot carry it;
change this fixture by hand.

`claude-code-headers.json` holds two header sets of a Claude Code client, one
without and one with the hint headers (ADR 0010). The values are placeholders
until a real capture replaces them.

## Refresh from real traffic

The capture tool is `cmd/fixturecap`, with two subcommands, `record` and
`scrub`. A refresh takes three steps.

1. Record. Start the pass-through recorder with a directory outside this
   repository (the tool refuses one inside a checkout):

   ```sh
   go run ./cmd/fixturecap record -out ~/captures/claude-code
   ```

   Point the client at it, with its own credentials, so the recorder only
   forwards them:

   ```sh
   ANTHROPIC_BASE_URL=http://127.0.0.1:8081 claude
   ```

   Each exchange lands in `~/captures/claude-code/NNNN`. Credential headers are
   replaced by `[redacted]` before anything is written, but the bodies are real
   prompts and responses: do not share or commit the capture. To record the
   hint headers, also set `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1` (ADR 0042); record
   once with it and once without to get both header sets.

2. Scrub one exchange into a fixture:

   ```sh
   go run ./cmd/fixturecap scrub -in ~/captures/claude-code/0003 -out testdata/fixtures/NAME
   ```

   Scrub replaces every string of the request and the response in place with
   `FAKEPROMPT-` text of the same encoded and decoded byte length, keeping the
   JSON layout, so recorded argument and result sizes survive. Streamed tool
   arguments are scrubbed as one JSON text and cut again at the original
   fragment lengths. It keeps only structural fields (types, roles, model, tool names,
   ids), drops credentials, replaces session, agent and prompt ids with stable
   fakes, and refuses to write anything that still looks like a key, a bearer
   token, an email address or a home path.

3. Review and generate the golden. The scrub is mechanical, so read every file
   of the new directory, especially tool names and ids. Then run `make goldens`,
   read `record.golden.json`, and add a case to `TestFixtureFacts` if the
   exchange shows a rule no fixture shows yet. For a header set, copy the
   scrubbed `request.headers.json` into `claude-code-headers.json`.

The capture of a first real set waits for a Claude Code session that can be
routed through the proxy (ADR 0040).
