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
- **Provider styles**: `openai` (passthrough OpenAI shape), `anthropic` (request translated to `/v1/messages`, response translated back), `cline` (Cline account auth via `routerllm --cline-login`, refresh tokens in `cline-accounts.json`, access tokens in memory only).
- **AuthMode**: `bearer` (default, `Authorization: Bearer <key>`), `x-api-key`, `both` (sends both headers).
- **Shared keys**: use `share:` in YAML to share a key manager across providers. A key marked dead in one is dead for all.
- **Dead vs transient**: HTTP 401/402/403 marks the key dead (cooldown, skipped until revived). 408/429/5xx triggers up to 3 retries with exponential backoff on the _same_ key before moving on.
- **No hot-reload yet**: config is read at startup. Restart the server to apply changes.
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

