import { expect, test, type Page } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { admin, login, newPlayer } from './helpers';

// Backup → restore on a fresh kicker (spec 10 §2, M7 task 8): moving to a new Pi. A backup made
// on the test server is restored through the setup wizard of a second, new server, which must
// come back with the same identity, accounts and players.
const passphrase = 'correct horse battery staple';
const bin = process.env.KICKER_BIN ?? '../bin/kicker';
const port = Number(process.env.E2E_PORT ?? 18321) + 1;

async function waitUp(page: Page, url: string) {
	for (let i = 0; i < 100; i++) {
		try {
			if ((await page.request.get(url, { timeout: 1000 })).ok()) return;
		} catch {
			/* not up yet */
		}
		await page.waitForTimeout(200);
	}
	throw new Error(`${url} didn't come up`);
}

test('a backup restores on a fresh kicker', async ({ page, browser }) => {
	test.setTimeout(120_000);
	const dir = mkdtempSync(join(tmpdir(), 'kicker-e2e-'));
	let second: ChildProcess | undefined;
	try {
		await login(page, admin);
		await newPlayer(page, 'Restorina');

		// A passphrase of our own, so the new kicker can be given it.
		await page.goto('/settings/security');
		const pp = page.getByTestId('passphrase');
		await pp.getByLabel('My own').check();
		await pp.getByLabel('New passphrase').fill(passphrase);
		await pp.getByLabel('Your password').fill(admin.password);
		await pp.getByRole('button', { name: 'Change passphrase' }).click();
		await expect(pp.getByTestId('new-passphrase')).toHaveText(passphrase);

		// Back up now, and download the backup from the list (newest first).
		await page.goto('/settings/backups');
		const rows = page.getByTestId('backup-row');
		await expect(page.getByTestId('backup-list')).toBeVisible();
		const before = await rows.count();
		await page.getByTestId('backup-now').click();
		await expect(rows).toHaveCount(before + 1);
		await rows.first().getByTestId('backup-menu').click();
		const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'Download' }).click()]);
		expect(download.suggestedFilename()).toMatch(/^kicker-backup-.*-manual\.tar\.gz\.age$/);
		const file = join(dir, download.suggestedFilename());
		await download.saveAs(file);
		const info = await (await page.request.get('/api/info')).json();

		// A second, new kicker.
		second = spawn(bin, ['serve', '--sim', '--no-serial', '--data', join(dir, 'data'), '--addr', `127.0.0.1:${port}`], { stdio: 'ignore' });
		const base = `http://127.0.0.1:${port}`;
		const p2 = await browser.newPage({ baseURL: base, locale: 'en-US' });
		await waitUp(p2, `${base}/api/info`);
		expect((await (await p2.request.get('/api/info')).json()).server_id).not.toBe(info.server_id);

		// Setup: language, then "Restore a backup".
		await p2.goto('/');
		await expect(p2).toHaveURL(/\/setup$/);
		await p2.getByLabel('English').check();
		await p2.getByRole('button', { name: 'Continue' }).click();
		await p2.getByTestId('setup-restore').click();
		await p2.getByTestId('restore-file').setInputFiles(file);

		// A wrong passphrase is refused before anything is replaced.
		await p2.getByTestId('restore-passphrase').fill('not the passphrase at all');
		await p2.getByTestId('restore-submit').click();
		await expect(p2.getByText('Wrong passphrase, or not a kicker backup.')).toBeVisible({ timeout: 30_000 });

		// The right one: the kicker restarts with the backup and the wizard is over.
		await p2.getByTestId('restore-passphrase').fill(passphrase);
		await p2.getByTestId('restore-submit').click();
		await expect(p2).not.toHaveURL(/\/setup/, { timeout: 60_000 });

		// The same kicker as before: identity, the admin's account, the players.
		const after = await (await p2.request.get('/api/info')).json();
		expect(after.server_id).toBe(info.server_id);
		expect(after.onboarding_done).toBe(true);
		const res = await p2.request.post('/api/auth/login', { data: admin, headers: { origin: base } });
		expect(res.ok(), await res.text()).toBeTruthy();
		await p2.goto('/players');
		await expect(p2.getByText('Restorina').first()).toBeVisible();
		await p2.close();
	} finally {
		second?.kill();
		rmSync(dir, { recursive: true, force: true });
	}
});
