import { defineConfig } from '@playwright/test';

// End-to-end tests (spec 04 §4): the real server binary with virtual modules (`kicker serve --sim`)
// in a fresh data folder, driven through the browser. Build it first (`make build`); KICKER_BIN
// points elsewhere. The specs share one server and run in order: setup first.
const port = Number(process.env.E2E_PORT ?? 18321);
const bin = process.env.KICKER_BIN ?? '../bin/kicker';

export default defineConfig({
	testDir: 'e2e',
	fullyParallel: false,
	workers: 1,
	retries: process.env.CI ? 1 : 0,
	timeout: 30_000,
	expect: { timeout: 10_000 },
	reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
	use: {
		baseURL: `http://127.0.0.1:${port}`,
		locale: 'en-US',
		timezoneId: 'Europe/Berlin',
		trace: 'retain-on-failure',
		// A local Chrome instead of Playwright's download, e.g. PW_CHROME=/usr/bin/google-chrome
		launchOptions: process.env.PW_CHROME ? { executablePath: process.env.PW_CHROME, args: ['--no-sandbox'] } : {}
	},
	webServer: {
		command: `sh -c 'd=$(mktemp -d) && exec "${bin}" serve --sim --no-serial --data "$d" --addr 127.0.0.1:${port}'`,
		url: `http://127.0.0.1:${port}/api/info`,
		reuseExistingServer: false,
		stdout: 'ignore',
		stderr: 'pipe',
		timeout: 60_000
	}
});
