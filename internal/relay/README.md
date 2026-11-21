# OpenCode TLS relay

Routes opencode providers through a **local Bun child process** so the gateway
sees Bun's TLS fingerprint instead of Go's.

## Why

The opencode free-tier gateway scores clients at the **transport level** and
only Bun-family clients pass its structural gate — from datacenter IPs,
curl/python/Go and even Chrome-impersonated TLS all fail with a fake
`FreeUsageLimitError` (verified 2026-11-21: byte-identical replay via curl
fails while the real CLI passes; a Bun relay passes with the same bytes).

## How it works

```
client ──► RouterLLM (:1765)
              └─ opencode leg ──► relay child 127.0.0.1:<port> (bun)
                                     └─► https://<provider origin>
```

- `config.Load()` (see `applyOpencodeRelay` in `internal/config`) finds
  providers with `style: opencode`, calls `Manager.Ensure(origin)`, and
  rewrites `base_url` to `http://127.0.0.1:<port>`. Hot reload reuses the same
  child (Ensure is idempotent; a base_url already pointing at a supervised
  relay is returned unchanged — never double-wrapped).
- The relay script (`bun-relay.ts`, embedded via `go:embed`, materialized in
  `os.TempDir()/routerllm-relay/`) forwards path, query, headers and the
  streaming body verbatim; only the transport and origin change.
- The manager supervises the child: exit → respawn with backoff (1s→30s).
  `Manager.Stop` (wired via `defer` in `main`) kills it on shutdown.

## Env vars

| var | default | |
|---|---|---|
| `OPENCODE_TLS_RELAY` | off | `1` = enable for every active `style: opencode` provider |
| `OPENCODE_RELAY_PORT` | auto | fixed port; unset/`0` = OS-assigned per upstream |
| `OPENCODE_RELAY_BIN` | `bun` | bun binary path |
| `OPENCODE_RELAY_UPSTREAM` | provider origin | force the upstream origin |

## Failure behavior

Bun missing, spawn failure or health-check timeout → the provider keeps its
**original base_url** (direct connection) with a single warning log. Requests
never block on the relay.

## Diagnosing

The gateway answers structural-gate violations with
`FreeUsageLimitError: "Free models work in OpenCode…"` and real quota
exhaustion with `…Connect a provider or subscribe to OpenCode Go… switch
models`. Same 429 status, different body: the body style tells you whether
the request was flagged (transport/shape) or the account's free quota is gone.

## Known limits

- Bun version updates can change the TLS fingerprint the gateway expects.
- The relay binds `127.0.0.1` only.
- `log.Fatalf` startup paths skip defers — a relay child can survive a fatal
  startup error; the next Ensure spawns a fresh child on a new port.
