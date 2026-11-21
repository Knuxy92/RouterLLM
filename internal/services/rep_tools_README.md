# Representative-tool mode (proxy-side Code Mode)

`OPENCODE_REP_TOOLS=1` collapses a client's whole tool surface — every MCP
server it runs — into a text catalog in the system prompt plus ONE
representative `execute` tool, and maps the model's calls back to the client's
native tool names on the way out. Any OpenAI-compatible client then passes the
opencode gateway's structural gate without knowing anything about Code Mode.

## Why it exists

The gateway's structural gate rejects free-tier requests whose `tools[]` does
not look like the real CLI's, regardless of client fingerprint:

- the `tools[]` array must carry a CLI-like set (~13 names; lab probe: the
  5-tool pack passes, a lone `execute` tool flags, 24+ tools flag);
- the system prompt must contain the harness `<env>` block with
  `Working directory:` (see `injectOpenCodeSystemPrompt`).

Failure is a soft 429 whose body reads `FreeUsageLimitError "Free models work
in OpenCode…"`; real quota exhaustion answers `…Connect a provider or
subscribe… switch models` — same status, different body, so the body style is
the pass/fail probe. This is a different, request-level gate from the
transport-level client scoring the TLS relay handles (see
`internal/relay/README.md`); both are needed from datacenter IPs.

## Request transform — `collapseRepTools`

Runs in `translateRoute`'s chat branch after the harness prompt injection:

1. The client's declarations become a text catalog (`## Tool catalog` — one
   line per tool: name, parameter types, description capped at 160 chars)
   appended to the leading system message, so the `<env>` block stays intact.
2. History `assistant.tool_calls` are re-wrapped each request: native name +
   arguments become `{"tool": "<native>", "arguments": <args>}` under the
   name `execute`. `role:"tool"` results pass through untouched.
3. `tools[]` shrinks to the representative definition; `injectMissingTools`
   pads the pack with the required `edit`/`read`/`shell`/`write` — five tools
   total, the lab-verified passing shape.

Catalog names are the client's native names verbatim, which is what makes the
whole transform stateless — no mapping tables to keep alive between turns.

Wire shape upstream (chat dialect):

```json
"tools": [{"function": {"name": "execute", "parameters": {"properties": {
  "tool": {"type": "string"}, "arguments": {"type": "object"}}}}},
 {"function": {"name": "edit"}}, {"function": {"name": "read"}},
 {"function": {"name": "shell"}}, {"function": {"name": "write"}}]
```

The model calls `execute` once per tool and returns several calls in one
response to run tools in parallel.

## Response transform — `repRestoreBuffered` / `repStreamSSE`

- Buffered clients: `execute` calls in the assembled `message.tool_calls` are
  unwrapped — `arguments.tool` becomes the function name, the inner object
  becomes the arguments string.
- Streaming clients: `repStreamSSE` forwards content and reasoning deltas
  live, holds `execute` fragments per index until the finish frame, then
  emits each call complete, in index order, **before** the finish frame. The
  gateway folds the last call fragment and `finish_reason` into one frame —
  that frame is re-emitted with its buffered parts stripped and its
  finish_reason intact; dropping it would stall every client loop after the
  first tool round.

## Safety rails

- A client that already speaks Code Mode (its `tools[]` carries its own
  `execute`) skips the collapse entirely.
- The response side is content-sniffed, not stateful: only arguments that
  parse as `{"tool": ..., "arguments": ...}` are rewritten, so a CLI
  `execute` call carrying `{"code": ...}` passes byte-identical while the
  mode is on.
- `tool_choice: "none"`, no tools, or no messages → no collapse.
- Names over 64 chars travel in the sanitized form and are restored by the
  sanitize rename map chained after the unwrap.
- With the env var off, every path is byte-identical to the legacy behavior.

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `OPENCODE_REP_TOOLS` | off | `1` enables the mode (chat dialect only; messages/responses legs are untouched) |

## Troubleshooting

- 429 `FreeUsageLimitError "Free models work in OpenCode…"` — structural gate
  failed: mode off for a large-tool client, or the pack was altered. 429
  `…Connect a provider or subscribe…` — real quota exhaustion, unrelated.
- Client stalls after the first tool round — old binary without the
  finish-frame fix (`b64f242`); the finish frame must always reach the client.
- Parallel calls: several native `tool_calls` arrive in one response, index
  order preserved, ids kept.
- Catalog size: 188 real-scale tools ≈ 5–6K prompt tokens; per-tool
  descriptions are capped, parameters render as `name: type` pairs.

## Evidence

- Lab probe: rep + required four passes the gate from any IP; solo `execute`
  flags; 13-tool pack also passes (bigger prompt, no other benefit).
- E2E (local and server, through the TLS relay): 188 fake MCP tools,
  4 consecutive tool-call rounds with parallel round 1, zero `execute` leaks,
  final text answer correct. Harness: `opencode-lab/lab_e2e_rep.py`
  (gitignored lab copy on the server: `/home/admin/opencode-lab/`).
- Unit/integration coverage: `go test ./internal/services -run
  'RepTools|RepRestore'` — collapse shape, skip rules, sanitized names,
  upstream pack, stream restore, buffered restore, CLI passthrough, mixed
  content frame, folded finish_reason.
