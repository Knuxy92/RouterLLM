# web/ — RouterLLM admin console

Astro (static output) + React islands + TypeScript + Tailwind 4. Go embeds the
built `internal/admin/dist` and serves it at `/admin/*`; no separate frontend
server. Vendored lucide icons live in `public/vendor/`.

```bash
pnpm install          # once
pnpm dev              # Astro on :5173, proxies /admin/api + /v1 to 127.0.0.1:17701
pnpm build            # static build → ../internal/admin/dist (embedded by go:embed)
pnpm typecheck        # astro check
pnpm lint             # oxlint
```

Rebuild the frontend before `go build` whenever `web/` changes — the Go
binary serves the committed `internal/admin/dist`, not the live source.

## Layout

- `astro.config.mjs` — `base: /admin/`, `outDir` → `internal/admin/dist`, react + tailwind plugins, dev proxy
- `pnpm-workspace.yaml` — pnpm 12 config: pins Vite 7 (Astro 6's range) and allows esbuild/sharp build scripts
- `src/layouts/AppLayout.astro` — document head, login gate, shell, sidebar, header, drawer/dialog roots
- `src/pages/` — `index` (redirect), `dashboard`, `logs`, `providers`, `models` (Phase-2 placeholder)
- `src/components/` — React islands (`Dashboard`, `RequestLogs`, `ProvidersModels`) + shared shell components
- `src/lib/` — typed API (`api.ts`), pure-JS HMAC (`hmac.ts`), adapters (`data.ts`), external store (`store.ts`), wire types (`types.ts`)
- `src/styles/global.css` — Tailwind 4 tokens + component classes
- `wireframe/index.html` — original static design mock, kept as design reference
- `scripts/port-html.mjs` / `patch-html.mjs` — one-time scripts that generated the old `index.html`

## Nav

- Platform: Dashboard (`/admin/dashboard`), Request logs (`/admin/logs`)
- Management: Providers & Models (`/admin/providers`)
- docs: Models detail (`/admin/models`) — placeholder until Phase 2

The "Models detail" page will consume the existing
`GET /admin/api/providers/{name}/models` (read-only); no API changes involved.

See `docs/plans/astro-migration.md` in the repo root for the full migration plan.
