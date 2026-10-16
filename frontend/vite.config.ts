import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) => (filename.split(/[/\\]/).includes('node_modules') ? undefined : true)
			},
			// The SPA is embedded into the Go binaries (desktop and web) from internal/ui/dist.
			adapter: adapter({
				pages: '../internal/ui/dist',
				assets: '../internal/ui/dist',
				strict: false
			})
		})
	],
	server: {
		// `pnpm dev` alone talks to a running `tablef-web` for the API.
		proxy: { '/api': 'http://127.0.0.1:7070' }
	}
});
