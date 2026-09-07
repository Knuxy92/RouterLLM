# web/ — RouterLLM admin console

Vanilla JS + Tailwind 4 + Vite. No React, no shadcn — one `index.html`,
small ES modules in `src/js/`, vendored lucide icons in `public/vendor/`.

```bash
pnpm install          # once
pnpm dev              # Vite on :5173, proxies /admin/api + /v1 to 127.0.0.1:17701
pnpm build            # emits ../internal/admin/dist (embedded by go:embed)
pnpm lint             # oxlint
```

Rebuild the frontend before `go build` whenever `web/` changes — the Go
binary serves the committed `internal/admin/dist`, not the live source.

## Layout

- `index.html` — full markup: login gate, dashboard / logs / providers pages, drawers, add-leg dialog
- `src/main.js` — entrypoint; wires all modules
- `src/js/auth.js` — challenge–response login against `/admin/api/auth/*`
- `src/js/api.js` — fetch wrapper + session token storage
- `src/js/state.js` — server-backed state (3s poll, optimistic toggles)
- `src/js/data.js` — adapters: backend shapes → render shapes
- `src/js/render.js` — innerHTML rendering for providers/models/logs/drawers
- `src/js/router.js` — hash routing (`#/dashboard`, `#/logs`, `#/providers`)
- `src/js/search.js` — cmd+k palette
- `src/js/hmac.js` — pure-JS HMAC-SHA256 (used on plain `http://<lan-ip>` where `crypto.subtle` is unavailable), pinned by RFC 4231 vectors
- `wireframe/index.html` — the original static design mock, kept as design reference
- `scripts/port-html.mjs` / `patch-html.mjs` — one-time scripts that generated `index.html` from the wireframe
