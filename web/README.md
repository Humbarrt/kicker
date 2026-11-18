# kicker-web

SvelteKit (Svelte 5, SvelteKit 3) with `adapter-static`, built as a single-page app.

- The build is embedded into the Go binary (`make web` copies `build/` into `server/internal/webui/dist`).
- The same build is the frontend of the Tauri app (`app/`, from milestone M4).
- Talk to the server only through `src/lib/api/` so the app can swap in its pinned-TLS client.

```sh
pnpm install
pnpm dev      # or `make dev` in the repo root: Go server with --sim + Vite with API proxy
pnpm check
pnpm build
```
