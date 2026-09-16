# AGENTS.md

NERVER NERVER TOUNCH PORT 1765

Go LLM proxy/router. Accepts OpenAI-style requests (`/v1/chat/completions`, `/v1/responses`, `/v1/models`) and routes them across multiple upstream providers with multi-key failover and Anthropic<->OpenAI translation.

## Quick start

1. Copy `routerllm.yaml.example` to `routerllm.yaml`
2. Edit `routerllm.yaml` — set your API keys
3. Run `routerllm` (or `go run ./cmd/routerllm`)

## Commands

```bash
go build -o routerllm.exe ./cmd/routerllm  # build single binary
go vet ./...                                # static checks (no linter configured)
go build ./...                              # compile-check all packages
docker compose up --build                    # container run, exposes :1765
```

### Admin console frontend

```bash
pnpm -C web install                        # once
pnpm -C web dev                            # Vite dev server on :5173, proxies /admin/api + /v1
pnpm -C web build                          # emits internal/admin/dist (embedded by go:embed)
pnpm -C web lint                           # oxlint
```

Rebuild the frontend before `go build` whenever `web/` changes — the Go binary serves the committed `internal/admin/dist`, not the live source.

### Runtime

```bash
routerllm   # starts API server (reads routerllm.yaml, listens on :1765)
```

With no arguments it just runs the server. The only flags are `--cline-login` and `--alysis-login` — one-time account setup for the `cline`/`alysis` provider styles (see Gotchas).

## Configuration

Config is read from `routerllm.yaml` by default (override with `ROUTERLLM_CONFIG_FILE` env var).

All provider fields (style, base_url, headers, auth_mode, share, query) must be specified in YAML — there are no hardcoded defaults.

**Full example:** see `routerllm.yaml.example`

## Architecture

- Entry: `cmd/routerllm/main.go` — loads config, builds registry, starts server.
- `internal/config/config.go` — reads `routerllm.yaml` via YAML loader. `ProviderConfig` defines each upstream.
- `internal/config/yaml.go` — YAML config loader. Supports `${VAR}` env var substitution with comma-split for multi-key.
- `internal/model/model.go` — shared types: Rule/Spec/RequestDefaults for routing, Model/Choice/Message for chat API.
- `internal/provider/provider.go` — `NewRegistry(configs, rules, cooldown)` builds the live route table from model rules. A route only activates if its provider is configured (has keys).
- `internal/services/proxy.go` — `Forward` selects routes, applies defaults, translates for anthropic-style and google-style providers, iterates keys with retry/backoff.
- `internal/adapter/anthropic.go` — translates OpenAI chat bodies to Anthropic `/v1/messages` and converts Anthropic SSE back to OpenAI shape.
- `internal/adapter/google.go` — translates OpenAI chat bodies to Gemini `generateContent` (`:streamGenerateContent?alt=sse`) and converts Gemini SSE back to OpenAI shape (or straight to Anthropic SSE via the OpenAI-chunk pipe for `/v1/messages` and `force_stream`).
- `internal/keys/manager.go` — round-robin key selection with per-key cooldown.
- `internal/util/dotenv.go` — minimal `.env` parser. **Only sets a var if not already in env** (does not override real env vars).

## Gotchas

- **Port**: code reads `ROUTERLLM_PORT` (default `1765`). **Never run/kill the production server on 1765 casually; it is the live port.**
- **Routing config**: all routes must be defined in `routerllm.yaml` under `routes:` — there are no built-in defaults.
- **Base URL**: trailing `/v1` is trimmed from base_url in YAML. Routes append `/v1/...` paths themselves.
- **Streaming is forced**: `stream=true` on every `/v1/chat/completions` outbound request regardless of client. Non-stream clients get the SSE buffered into a single JSON response.
- **`/v1/responses`**: served by openai-style legs (native passthrough) and by any leg with `stylecall: responses`; other legs are filtered out for that path, and if none remain the model 404s for it (unchanged error). `/v1/files` filtering is unchanged (openai-style only).
- **Provider styles**: `openai` (passthrough OpenAI shape), `anthropic` (request translated to `/v1/messages`, response translated back), `cline` (Cline account auth via `routerllm --cline-login`, refresh tokens in `cline-accounts.json`, access tokens in memory only), `alysis` (Alysis gateway account auth via `routerllm --alysis-login`, long-lived `slk_` gateway keys stored in `alysis-accounts.json` and used directly as Bearer — no token refresh; plain OpenAI passthrough, so DeepSeek `reasoning_content` and tool calls flow through unchanged; each login adds one key and keys rotate via the normal key manager; path override env `ALYSIS_ACCOUNTS_FILE`; the gateway caps request tools at 128 (OpenAI/DeepSeek limit) — larger requests are rejected locally with an actionable 400 (`errTooManyTools` in `internal/services/proxy.go`) instead of the gateway's generic invalid-request error), `google` (request translated to Gemini `:streamGenerateContent?alt=sse` in `internal/adapter/google.go`, Gemini SSE translated back to OpenAI shape; auth always `x-goog-api-key` per key — `auth_mode`/`query` are ignored; tool calls, inline images and `reasoning_effort`→`thinkingConfig` are translated both ways). The example yaml ships the cline provider **commented out** — enabling a cline provider without a logged-in account is a hard startup error (`no cline accounts found`), so uncomment the provider and its route only after `--cline-login` has created the accounts file. In Docker, uncomment the `cline-accounts.json` mount in `docker-compose.yml` too, and only after the host file exists (a missing bind-mount host path is auto-created as a directory, which breaks login and the account loader). Same for alysis: the example yaml ships that provider commented out too, and an enabled alysis provider without a logged-in account is a hard startup error (`no alysis accounts found`) — uncomment the provider and its route only after `--alysis-login` has created the accounts file; in Docker, uncomment the `alysis-accounts.json` mount too, and only after the host file exists.
- **Reasoning normalization**: reasoning settings arrive in several dialects — OpenAI `reasoning_effort` (`none|minimal|low|medium|high|xhigh|max`; `ultra` is normalized to `max`), OpenRouter `reasoning` map (`{enabled, effort, max_tokens, exclude}`), Qwen/DashScope top-level `enable_thinking` + `thinking_budget`, Anthropic `thinking: {type: enabled, budget_tokens}`, Gemini `thinkingConfig`, Cline gateway `reasoning_effort` — and are all folded (client-sent and route defaults) into two canonical keys and re-emitted in whichever dialect the target provider speaks. Precedence: client `reasoning` map > client `reasoning_effort`/`enable_thinking` > route defaults, which remain `reasoning_effort`, `enable_thinking` (`false` → effort `none`) and `thinking_budget`. New per-provider yaml field on openai-style providers: `reasoning_style: openai | openrouter | qwen | raw` (default `openai`) — `openai` sends only `reasoning_effort`, `openrouter` sends the `reasoning` map, `qwen` sends `enable_thinking` + `thinking_budget`, `raw` is the legacy escape hatch (defaults injected verbatim, client dialect keys untouched). anthropic/google/cline styles have fixed dialects and ignore `reasoning_style`. Client dialect keys are consumed and stripped — no longer forwarded raw to openai-style upstreams (`raw` restores the old behavior). `/v1/responses` passthrough is unchanged, and the admin UI/editor effort whitelist is now `none|minimal|low|medium|high|xhigh|max` (ultra removed from the dropdown).
- **`stylecall` (per-leg call style)**: per-route-leg YAML field (sibling of `provider`/`model`/`disabled`/`defaults`) that selects the upstream API dialect for that one leg, so a single provider can mix models needing different endpoints. Values: `chat` = `/v1/chat/completions` (OpenAI chat), `responses` = `/v1/responses` (OpenAI Responses), `messages` = `/v1/messages` (Anthropic Messages); empty/absent means the provider style decides (fully backward compatible). Allowed only on providers with style `openai`, `anthropic`, or `alysis` — `google` and `cline` reject it at config load (their transports are dialect-bound). Effective dialect = `stylecall` when set, else the provider style (`openai`→openai, `anthropic`→messages, `google`→google, `alysis`→alysis, `cline`→cline). Inbound: `/v1/chat/completions` and `/v1/messages` clients can reach `stylecall: responses` legs through the new translator in `internal/adapter/responses.go` (chat→Responses on the way out, Responses SSE→OpenAI chat chunks back — streaming, tool calls, reasoning, usage converted); `stylecall: messages` legs reuse the existing Anthropic translation both ways; `force_stream` and `/v1/messages` responses for `stylecall: responses` legs are converted Responses→chat→Anthropic via the same pipe trick google uses. `/v1/responses` clients are served only by legs whose effective dialect is `openai` or `responses` (see the `/v1/responses` bullet above). Admin console: the add-leg dialog and provider test panel have a "Call style" dropdown, `/admin/api/status` legs carry an optional `stylecall`, and the add/test endpoints accept it. Config hot-reload picks it up automatically — no restart, no new env vars.
- **Tool hygiene toggles (`sanitize_tool_names`, `dedupe_tools`)**: per-route-leg YAML bools (siblings of `stylecall`, default `false`) applied to the outbound body before dialect translation, and only on the leg that sets them. `dedupe_tools` drops tool definitions whose function name repeats an earlier one (exact, case-sensitive, first wins, dropped count logged as `route <model>/<provider>: dropped N duplicate tool definition(s)`); `sanitize_tool_names` rewrites names longer than the upstream's 64-char cap to `name[:56] + "-" + sha256(name)[:7]` (always 64 chars, deterministic, collisions resolved by a `sha256(name:i)` fallback loop). Both toggles also rewrite the names inside `messages[].tool_calls[].function.name` history and a named `tool_choice`, and never touch ids/call_ids. The proxy keeps the sanitized→original map and hands the client back the names it sent, on every response shape it writes: chat chunks (`tool_calls[].function.name`), Anthropic frames (`content_block.name` on `tool_use`), and buffered JSON — so a `sanitize_tool_names` leg is transparent to the client. When a leg sanitizes nothing, responses stay byte-identical. `messages`-dialect legs drop tools entirely (unchanged), so both toggles are a no-op there. Config hot-reload picks them up automatically; `/admin/api/status` legs carry both flags, the admin console has no editors for them yet (set them in YAML).
- **AuthMode**: `bearer` (default, `Authorization: Bearer <key>`), `x-api-key`, `both` (sends both headers).
- **Shared keys**: use `share:` in YAML to share a key manager across providers. A key marked dead in one is dead for all.
- **Dead vs transient**: HTTP 401/402/403 marks the key dead (cooldown, skipped until revived). 408/429/5xx triggers up to 3 retries with exponential backoff on the _same_ key before moving on.
- **Hot-reload**: the config file (path from `ROUTERLLM_CONFIG_FILE`, default `routerllm.yaml`) is polled every 3s and compared by SHA-256 of its contents — contents, not mtime, because mtime propagation is unreliable through Docker Desktop bind mounts on Windows. On change the new config is validated and the routing table is swapped atomically; requests already in flight finish on the old config, so worst-case apply latency is ~3-4s after saving. Invalid YAML or failed validation (e.g. a mid-edit save) is rejected: the previous working config keeps serving and the server logs `config reload rejected: <error>`. It never crashes and never serves an empty routing table. Reload also applies `force_stream`, `forward_client_headers` and `allow_client_headers` via `Proxy.ApplySettings` — previously they were silently ignored until a restart. Still requires a restart: `port`, the HTTP client transport settings, and `ROUTERLLM_DEBUG` / `ROUTERLLM_DEBUG_ADVANCED` / `ROUTERLLM_LOG_FILE`. **Docker Desktop caveat**: a single-file bind mount does not propagate host-side edits into the container at all — hand-editing `routerllm.yaml` on the host goes unnoticed (no reload, no rejection). Inside-the-container writes (admin console/API) reload normally and sync back. So under Docker Desktop: edit via the admin console, or `docker compose restart`. Plain host installs hot-reload hand edits as documented.
- **`disabled`**: `providers[].disabled: true` skips the provider entirely at startup and on reload, and relaxes its validation — no `api_key` required, and a disabled `style: cline`/`style: alysis` provider needs no logged-in accounts. This is the intended way to park a provider whose key died instead of deleting the block (deleting it breaks startup while routes still reference it). `routes[].routes[].disabled: true` skips that one upstream entry; the remaining entries for the same `model_id` still serve it, but if every entry is disabled the model disappears from `/v1/models` and requests for it return 404. A route entry referencing a provider that exists but is disabled is silently dropped from the routing table (logged as a notice); a route referencing a provider that does not exist at all is still a hard startup error.
- **Admin console**: set `ROUTERLLM_ADMIN_TOKEN` in `.env` to enable the embedded vanilla-JS console at `/admin/` and its API at `/admin/api`. The Docker Compose config mounts `routerllm.yaml` read-write because provider/route toggles and leg edits are persisted surgically to the file without expanding `${ENV_VAR}` placeholders. Without the token, admin endpoints return 403. Every write also drops a `.bak` next to the file (gitignored), and the image chowns `/app` (plus pre-creates `/app/data`) to UID 65532 so the non-root user can create both the backup and the atomic temp file — without the chown the telemetry store falls back to memory-only and history is lost on restart. Docker caveat: because `docker-compose.yml` mounts the yaml as a **single file**, the `.bak` is created in the container layer, not on the host — it vanishes when the container is recreated. On host installs the `.bak` sits next to the config as described.
- **`/v1` Bearer auth**: every non-GET/HEAD/OPTIONS request under `/v1` (chat/completions, responses, messages, file uploads) requires `Authorization: Bearer <token>` where the accepted tokens come from env **`AUTHTOKEN`** (comma-separated list, compared constant-time via SHA-256 digests). GETs (`/v1/models`, file downloads) and `/health` stay open. `AUTHTOKEN` unset (or the value empty after comma-splitting) disables the gate entirely — startup logs a notice. Restart-only: edits need a process restart.
- **Admin console TLS (optional)**: set `ROUTERLLM_ADMIN_TLS_PORT` (e.g. `1766`) to serve the console over HTTPS on a dedicated listener; the main port then stops serving `/admin` entirely (a plain-HTTP console would defeat the TLS). Certificate comes from `ROUTERLLM_ADMIN_TLS_CERT`/`ROUTERLLM_ADMIN_TLS_KEY`, auto-generating a self-signed ECDSA pair (SANs: localhost, loopback, hostname, LAN IPs; 825-day validity; `admin-tls.crt`/`admin-tls.key` beside the config, gitignored) when missing — browsers show the usual self-signed warning once, and `curl --cacert admin-tls.crt` verifies. Unset = console on the main port over plain HTTP, as before.
- **Admin auth is challenge–response, not bearer**: the secret never crosses the wire. `POST /admin/api/auth/challenge` returns a single-use 60s nonce; the client answers `POST /admin/api/auth/verify` with `proof = hex(HMAC-SHA256(secret, nonce))` and receives an opaque session id (≤100 live sessions, 12h TTL, in-memory only — a restart logs everyone out). `requireSession` accepts only that session id as `Bearer`; sending the raw secret is a 401. The frontend computes the HMAC with a pure-JS implementation (`web/src/js/hmac.js`, pinned by RFC 4231 vectors) because `crypto.subtle` is unavailable on plain `http://<lan-ip>`. Posture is still trusted-LAN without TLS: an on-path observer sees nonces and session ids, not the secret.
- **Add route leg**: `POST /admin/api/routes/{model}/add {provider, model, reasoning_effort?, disabled?}` appends a fallback leg to an existing model and reloads — the UI `+` button opens a native `<dialog>` with custom dropdowns for Provider / Upstream model / Reasoning Effort (empty = no `defaults` node) / Disabled (default off). The provider must already be configured; an unknown one is rejected before the file is touched. **Remove route leg**: `POST /admin/api/routes/{model}/remove {index}` deletes a leg (UI `✕` next to the ↑↓ buttons); removing the last leg of a model is rejected — disable it or delete the model instead.
- **One reload owner**: `config.Reloader` holds the content hash. The 3s poll loop and the admin API's immediate reload both call `Reloader.Reload()`, which no-ops when the hash is unchanged — so an admin write applies exactly once instead of twice (API now, watcher on the next tick).
- **Cline state is pruned on reload**: `Proxy.clineManagers` is keyed by provider base URL and lives on `Proxy`, which outlives every registry generation, so `Apply` drops managers whose base URL is no longer configured (or is now disabled). `cline.Manager.tokens` is keyed by refresh token; those rotate and each reload re-reads the account file, so a refresh sweeps expired entries. Without both, each reload leaked one manager and one token entry for the process lifetime.
- **`ROUTERLLM_DEBUG` vs `ROUTERLLM_DEBUG_ADVANCED`**: `debug` gates the per-request routing trace (`/v1/chat/completions model=… routes=…` and `serving … via provider=… dialect=… reasoning=[effort=… budget=…]` — the reasoning settings actually carried on the outbound body). Failure lines — dead keys, retries, exhausted providers, reload rejections — are logged unconditionally, since those are what you read when a provider breaks. `advancedDebug` adds response bodies and, together with `ROUTERLLM_LOG_FILE`, enables the full request/response audit middleware in `internal/routers/router.go` (which buffers up to 10 MiB per direction per request — leave it off in normal operation).
- **`active` route leg**: `/admin/api/status` marks the first leg that is neither route-disabled nor provider-disabled as `active`. That is the *primary eligible* upstream in configured order, not a record of which provider served the last real request — the UI labels it `primary` for that reason.
- **Frontend workspace**: `web/` is Vite + **vanilla JS** + Tailwind 4 (no React, no shadcn, no TypeScript) managed with **pnpm** (`pnpm-lock.yaml`; Docker installs with `--frozen-lockfile`). One `index.html` holds all markup; logic lives in small ES modules under `web/src/js/` (`api.js` fetch wrapper, `state.js` server-backed state with a 3s poll + optimistic toggles, `data.js` adapters, `render.js` innerHTML rendering, `hmac.js` pure-JS HMAC). `pnpm -C web build` writes directly to `internal/admin/dist`, which is committed and embedded with `go:embed`, so ordinary `go build ./...` remains Node-free. Docker rebuilds the frontend in a `node:22-alpine` stage before compiling Go. `web/wireframe/` and `web/scripts/` are the static design mock + one-time generator scripts, kept as reference.
- **Telemetry (request events + metrics)**: every proxied request records one event into `internal/telemetry` — metadata (masked key, provider/upstream, TTFT, duration, tokens_out, per-route attempts) plus the truncated upstream error body (2 KB cap per attempt) on failed requests; request bodies and successful response bodies are never stored. Events land in a 2000-entry ring and append to `routerllm-telemetry.jsonl` beside the config (`ROUTERLLM_TELEMETRY_FILE` overrides; memory-only when empty), replayed on start so dashboards survive restarts; rotates at ~10 MB, 7-day retention, hourly prune. Console endpoints: `GET /admin/api/requests?since=<seq>` (log table + trace drawer) and `GET /admin/api/metrics` (24h summaries + hourly/weekly windows per provider/leg/model; the dashboard's 24h/7d toggle also swaps the KPI row between `global` and `global_weekly` — a `SummarySince` rollup over the trailing 7 days, because p50 and tok/s cannot be rebuilt client-side from the windows; the traffic chart uses 1-hour buckets for 24h and midnight-anchored daily buckets for 7d — `Metrics.Windows` anchors to clock boundaries in local time). Docker persistence: `docker-compose.yml` sets `ROUTERLLM_TELEMETRY_FILE=/app/data/routerllm-telemetry.jsonl` on a named volume (`routerllm-data`, survives `up --build`; dropped by `down -v`) — the file must live on a directory mount, not beside the single-file-mounted yaml, because the store rotates by `os.Rename`. Bandwidth profile: the console polls `GET /admin/api/pulse` every 3s — a ~100-byte heartbeat carrying change signatures for /status and /metrics plus the telemetry cursor (`internal/admin/pulse.go`) — and re-fetches a full payload only when its signature moved; new request events arrive as `?since=` deltas into a client-side ring, so an idle system's steady-state poll is one pulse (~100 B, gzip on). Both the admin API and the static UI are served gzip-compressed (`chi/middleware.Compress`). tok/s comes from a passive body watcher sniffing `completion_tokens`/`output_tokens`/`candidatesTokenCount`; for openai/cline styles `stream_options.include_usage` is injected on `/v1/chat/completions` so streams carry a usage chunk — `ROUTERLLM_TELEMETRY_USAGE=off` disables the injection (some strict relays reject the field).
- **Provider test**: `POST /admin/api/providers/{name}/test` runs a one-shot chat request pinned to a single provider (bypasses route chains / model_id lookup) using the provider's real key pool and its style's request translation. Body: `{model, prompt, max_tokens, effort, timeout_seconds}` — server clamps timeout to 5–120s (default 20) and max_tokens to 1–8192 (default 256), effort must be in the reasoning whitelist, prompt capped at 4000 chars. The deadline covers the whole key/retry loop. Every run records a telemetry event → shows in the /logs feed (success info, failures warn/error with truncated upstream body). Response: `{provider, upstream_model, style, status, ttft_ms, duration_ms, tokens, content, error}`.
- **Model toggle vs key toggle**: `POST /admin/api/routes/{model} {"disabled":bool}` persists `disabled: true` on the route rule (model vanishes from `/v1/models`, requests 404, yaml block intact — same semantics as disabling every leg). `POST /admin/api/providers/{name}/keys/{index} {"disabled":bool}` is **runtime-only**: a manual disable in the key manager that `MarkDead` cannot override and that survives hot-reload via Snapshot/Restore, but a process restart brings the key back — keys come from `${ENV}` placeholders, nothing to persist; retire a key for good by removing it from the env var.
- **`routerllm.exe` binary in root is gitignored build artifact** — delete freely.
- `memory-bank/` is gitignored (Cline memory bank, not part of the app).

## Code style

### Router
Uses `go-chi/chi/v5` — not `http.ServeMux`. Route through `r.Group`, `r.Route`, `r.Use`. Middleware chaining via `r.Use(chimw.Logger, chimw.Recoverer, ...)`.

### Blank lines
- **Between declarations of different types**: separate with a blank line
  ```go
  finish := make(map[int]string)

  var usage json.RawMessage
  var resultID, modelName string
  ```
- **Before `return`**: if preceded by a multi-line block, insert a blank line
  ```go
  	}
  
  	return
  ```
- **After `if` block ending with `return`**: insert a blank line before the next statement
  ```go
  if err != nil {
  	http.Error(w, "...", http.StatusBadRequest)
  	return
  }

  body, clientStream, err := ...
  ```

### Extract reusable code
Patterns appearing ≥2 times → extract to a helper function (e.g. `writeStreamHeaders`, `copyHeaders`).

### if/else
Short single-line branches can stay compact. Multi-line branches get a blank line after the closing `}` from the preceding branch.

