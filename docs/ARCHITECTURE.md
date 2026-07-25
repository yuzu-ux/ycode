# Architecture

YCode is a single-process, dependency-free Go application. Its package
boundaries are intentionally narrow so the token policy, provider transport,
and execution safety can be audited separately.

## Package map

| Package | Responsibility |
| --- | --- |
| `cmd/ycode` | Process entry point and build-time version |
| `internal/cli` | Commands, flags, interactive loop, setup diagnostics |
| `internal/agent` | Provider/tool turn loop and cumulative statistics |
| `internal/context` | Hard input budget and deterministic old-turn capsule |
| `internal/repo` | Query-ranked, token-bounded repository map |
| `internal/provider` | OpenAI-compatible JSON and SSE transport |
| `internal/tools` | Workspace jail, atomic edits, search, and shell policy |
| `internal/session` | Private, resumable, workspace-scoped local sessions |
| `internal/token` | Estimation, clipping, hashes, and output deduplication |

Dependencies point inward toward small protocol types. The provider package does
not know about the workspace, and the tools package does not know about the
agent loop.

## Turn lifecycle

1. The user request is appended to the full local session.
2. The repository mapper ranks visible files against words in the request.
3. The context window combines:
   - a stable safety and behavior prompt,
   - the bounded dynamic repository map,
   - a deterministic capsule when older turns have aged out,
   - the newest complete user turns,
   - the two tool schemas.
4. The context window drops complete old turns until the request is within its
   configured budget. Tool calls are never detached from their results.
5. The provider streams response text and/or function calls.
6. Tools run sequentially in the workspace.
7. Tool results are content-addressed, clipped, and deduplicated before they are
   appended to model history.
8. The loop repeats until the model returns no tool call or `max_turns` is hit.
9. The complete local history is saved with mode `0600`; provider context may
   remain compact even though resumable local history is complete.

## Provider contract

The current provider uses the broadly supported `POST /v1/chat/completions`
contract:

- JSON messages
- function tool definitions
- non-streaming JSON responses
- streaming SSE `delta.content`
- streaming, fragmented `delta.tool_calls`
- optional provider usage accounting

The provider has two explicit connection modes:

- `local` requires a loopback endpoint and suppresses API keys even when a key
  exists in the environment.
- `api` supports hosted HTTPS endpoints and requires `YCODE_API_KEY` or the
  configured provider environment variable when the endpoint is not loopback.

`ycode connect local` discovers models through `GET /v1/models` on known local
runtime ports, then stores only the connection mode, endpoint, and model ID.
Discovery is on demand: it adds no background process, regular-startup work,
resident memory, or model-request tokens. `ycode connect api` switches the
saved mode without storing an API key value.

## Editing contract

The model sees only two functions:

- `workspace` — `list`, `read`, `search`, `stat`, `write`, or exact `replace`
- `shell` — one command, reason, and timeout

This is both a simplicity choice and a token optimization. The workspace tool
has no delete operation. It rejects absolute paths, `..` escapes, and symlinks
that resolve beyond the selected root.

## Sessions

Session files are stored beneath `os.UserCacheDir()`:

```text
ycode/sessions/<workspace-hash>/<session-id>.json
```

The path hash prevents accidental cross-project resume. Session IDs contain a
UTC timestamp plus cryptographic random bytes. API keys are not part of session
state.

## Deliberate non-goals before 1.0

- daemon/server architecture
- multi-agent scheduling
- semantic embeddings
- terminal rendering framework
- provider OAuth
- arbitrary plugin execution

These may arrive as isolated modules, but none should weaken the hard context
budget or turn the core binary into a large dependency graph.
