import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

// The web UI is a static SPA embedded into the Go binary (D1) and reused by the Tauri app (D4).
export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// SPA mode: every unknown route falls back to index.html, the Go server does the same.
			adapter: adapter({ fallback: 'index.html' })
		})
	],
	server: {
		// `make dev`: the Go server runs on :8080 with --sim, Vite proxies API and SSE calls to it.
		proxy: {
			// changeOrigin must stay false: the server refuses cookie requests whose Origin doesn't
			// match the Host (CSRF, spec 03 §13). The string shorthand would rewrite Host to :8080.
			'/api': { target: 'http://localhost:8080', changeOrigin: false }
		}
	}
});
