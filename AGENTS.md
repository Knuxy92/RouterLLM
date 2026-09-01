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

No flags, no subcommands — just run it.

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
- `internal/services/proxy.go` — `Forward` selects routes, applies defaults, translates for anthropic-style providers, iterates keys with retry/backoff.
- `internal/adapter/anthropic.go` — translates OpenAI chat bodies to Anthropic `/v1/messages` and converts Anthropic SSE back to OpenAI shape.
- `internal/keys/manager.go` — round-robin key selection with per-key cooldown.
- `internal/util/dotenv.go` — minimal `.env` parser. **Only sets a var if not already in env** (does not override real env vars).

## Gotchas

- **Port**: code reads `ROUTERLLM_PORT` (default `1765`). **Never run/kill the production server on 1765 casually; it is the live port.**
- **Routing config**: all routes must be defined in `routerllm.yaml` under `routes:` — there are no built-in defaults.
- **Base URL**: trailing `/v1` is trimmed from base_url in YAML. Routes append `/v1/...` paths themselves.
- **Streaming is forced**: `stream=true` on every `/v1/chat/completions` outbound request regardless of client. Non-stream clients get the SSE buffered into a single JSON response.
- **`/v1/responses` is OpenAI-only**: anthropic-style providers are filtered out for that path; only `openai`-style providers serve it.
- **Provider styles**: `openai` (passthrough OpenAI shape), `anthropic` (request translated to `/v1/messages`, response translated back), `cline` (Cline account auth via `routerllm --cline-login`, refresh tokens in `cline-accounts.json`, access tokens in memory only). The example yaml ships the cline provider **commented out** — enabling a cline provider without a logged-in account is a hard startup error (`no cline accounts found`), so uncomment the provider and its route only after `--cline-login` has created the accounts file. In Docker, uncomment the `cline-accounts.json` mount in `docker-compose.yml` too, and only after the host file exists (a missing bind-mount host path is auto-created as a directory, which breaks login and the account loader).
- **AuthMode**: `bearer` (default, `Authorization: Bearer <key>`), `x-api-key`, `both` (sends both headers).
- **Shared keys**: use `share:` in YAML to share a key manager across providers. A key marked dead in one is dead for all.
- **Dead vs transient**: HTTP 401/402/403 marks the key dead (cooldown, skipped until revived). 408/429/5xx triggers up to 3 retries with exponential backoff on the _same_ key before moving on.
- **Hot-reload**: the config file (path from `ROUTERLLM_CONFIG_FILE`, default `routerllm.yaml`) is polled every 3s and compared by SHA-256 of its contents — contents, not mtime, because mtime propagation is unreliable through Docker Desktop bind mounts on Windows. On change the new config is validated and the routing table is swapped atomically; requests already in flight finish on the old config, so worst-case apply latency is ~3-4s after saving. Invalid YAML or failed validation (e.g. a mid-edit save) is rejected: the previous working config keeps serving and the server logs `config reload rejected: <error>`. It never crashes and never serves an empty routing table. still requires a restart: `port`, the HTTP client transport settings, and `ROUTERLLM_DEBUG` / `ROUTERLLM_DEBUG_ADVANCED` / `ROUTERLLM_LOG_FILE`. **Docker Desktop caveat**: a single-file bind mount does not propagate host-side edits into the container at all — hand-editing `routerllm.yaml` on the host goes unnoticed (no reload, no rejection). Inside-the-container writes (admin console/API) reload normally and sync back. So under Docker Desktop: edit via the admin console, or `docker compose restart`. Plain host installs hot-reload hand edits as documented.
- **`disabled`**: `providers[].disabled: true` skips the provider entirely at startup and on reload, and relaxes its validation — no `api_key` required, and a disabled `style: cline` provider needs no logged-in cline accounts. This is the intended way to park a provider whose key died instead of deleting the block (deleting it breaks startup while routes still reference it). `routes[].routes[].disabled: true` skips that one upstream entry; the remaining entries for the same `model_id` still serve it, but if every entry is disabled the model disappears from `/v1/models` and requests for it return 404. A route entry referencing a provider that exists but is disabled is silently dropped from the routing table (logged as a notice); a route referencing a provider that does not exist at all is still a hard startup error.
- **Admin console**: set `ROUTERLLM_ADMIN_TOKEN` in `.env` to enable the embedded React console at `/admin/` and its API at `/admin/api`. The Docker Compose config mounts `routerllm.yaml` read-write because provider/route toggles and leg edits are persisted surgically to the file without expanding `${ENV_VAR}` placeholders. Without the token, admin endpoints return 403. Every write also drops a `.bak` next to the file (gitignored), and the image chowns `/app` to UID 65532 so the non-root user can create both the backup and the atomic temp file. Docker caveat: because `docker-compose.yml` mounts the yaml as a **single file**, the `.bak` is created in the container layer, not on the host — it vanishes when the container is recreated. On host installs the `.bak` sits next to the config as described.
- **`/v1` Bearer auth**: every non-GET/HEAD/OPTIONS request under `/v1` (chat/completions, responses, messages, file uploads) requires `Authorization: Bearer <token>` where the accepted tokens come from env **`AUTHTOKEN`** (comma-separated list, compared constant-time via SHA-256 digests). GETs (`/v1/models`, file downloads) and `/health` stay open. `AUTHTOKEN` unset (or the value empty after comma-splitting) disables the gate entirely — startup logs a notice. Restart-only: edits need a process restart.
- **Admin console TLS (optional)**: set `ROUTERLLM_ADMIN_TLS_PORT` (e.g. `1766`) to serve the console over HTTPS on a dedicated listener; the main port then stops serving `/admin` entirely (a plain-HTTP console would defeat the TLS). Certificate comes from `ROUTERLLM_ADMIN_TLS_CERT`/`ROUTERLLM_ADMIN_TLS_KEY`, auto-generating a self-signed ECDSA pair (SANs: localhost, loopback, hostname, LAN IPs; 825-day validity; `admin-tls.crt`/`admin-tls.key` beside the config, gitignored) when missing — browsers show the usual self-signed warning once, and `curl --cacert admin-tls.crt` verifies. Unset = console on the main port over plain HTTP, as before.
- **Admin auth is challenge–response, not bearer**: the secret never crosses the wire. `POST /admin/api/auth/challenge` returns a single-use 60s nonce; the client answers `POST /admin/api/auth/verify` with `proof = hex(HMAC-SHA256(secret, nonce))` and receives an opaque session id (≤100 live sessions, 12h TTL, in-memory only — a restart logs everyone out). `requireSession` accepts only that session id as `Bearer`; sending the raw secret is a 401. The frontend computes the HMAC with a pure-TS implementation (`web/src/lib/hmac.ts`, pinned by RFC 4231 vectors) because `crypto.subtle` is unavailable on plain `http://<lan-ip>`. Posture is still trusted-LAN without TLS: an on-path observer sees nonces and session ids, not the secret.
- **Add route leg**: `POST /admin/api/routes/{model}/add {provider, model, reasoning_effort?, disabled?}` appends a fallback leg to an existing model and reloads — the UI `+` button opens a shadcn Dialog (Radix custom selects, not native `<select>`) with Provider / Upstream model / Reasoning Effort (empty = no `defaults` node) / Disabled (default off). The provider must already be configured; an unknown one is rejected before the file is touched. **Remove route leg**: `POST /admin/api/routes/{model}/remove {index}` deletes a leg (UI `✕` next to the ↑↓ buttons); removing the last leg of a model is rejected — disable it or delete the model instead.
- **One reload owner**: `config.Reloader` holds the content hash. The 3s poll loop and the admin API's immediate reload both call `Reloader.Reload()`, which no-ops when the hash is unchanged — so an admin write applies exactly once instead of twice (API now, watcher on the next tick).
- **Cline state is pruned on reload**: `Proxy.clineManagers` is keyed by provider base URL and lives on `Proxy`, which outlives every registry generation, so `Apply` drops managers whose base URL is no longer configured (or is now disabled). `cline.Manager.tokens` is keyed by refresh token; those rotate and each reload re-reads the account file, so a refresh sweeps expired entries. Without both, each reload leaked one manager and one token entry for the process lifetime.
- **`ROUTERLLM_DEBUG` vs `ROUTERLLM_DEBUG_ADVANCED`**: `debug` gates the per-request routing trace (`/v1/chat/completions model=… routes=…` and `serving … via provider=…`). Failure lines — dead keys, retries, exhausted providers, reload rejections — are logged unconditionally, since those are what you read when a provider breaks. `advancedDebug` adds response bodies and, together with `ROUTERLLM_LOG_FILE`, enables the full request/response audit middleware in `internal/routers/router.go` (which buffers up to 10 MiB per direction per request — leave it off in normal operation).
- **`active` route leg**: `/admin/api/status` marks the first leg that is neither route-disabled nor provider-disabled as `active`. That is the *primary eligible* upstream in configured order, not a record of which provider served the last real request — the UI labels it `primary` for that reason.
- **Frontend workspace**: `web/` is Vite + React + TypeScript + Tailwind 4, managed with **pnpm** (`pnpm-lock.yaml`; Docker installs with `--frozen-lockfile`), with shadcn wired through `web/components.json` (`pnpm dlx shadcn@latest add <component>` lands in `web/src/components/ui`) and Magic UI registered as `@magicui`. `pnpm -C web build` writes directly to `internal/admin/dist`, which is committed and embedded with `go:embed`, so ordinary `go build ./...` remains Node-free. Docker rebuilds the frontend in a `node:22-alpine` stage before compiling Go.
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

