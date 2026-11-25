import { expect, test } from '@playwright/test';
import { admin, login, newPlayer, otherBrowser, register, wake } from './helpers';

test.beforeEach(async ({ page }) => login(page, admin));

// A whole game through the scoreboard, watched live from a second browser (SSE), then found in
// the history, on the profiles and the leaderboard.
test('play a game', async ({ page, browser }) => {
	await newPlayer(page, 'Mia');
	await newPlayer(page, 'Tom');

	await page.goto('/');
	await wake(page);
	await register(page, 'home', 'Mia');
	await register(page, 'away', 'Tom');
	await page.getByRole('button', { name: 'Start now' }).click();
	await expect(page.getByTestId('phase')).toHaveText('Playing', { timeout: 15_000 }); // after the kickoff countdown

	const watcher = await otherBrowser(browser);
	await login(watcher, admin);
	await watcher.goto('/');
	await expect(watcher.getByTestId('score-home')).toHaveText('0');

	await page.getByRole('button', { name: 'Away +1' }).click();
	await expect(watcher.getByTestId('score-away')).toHaveText('1');
	for (let i = 1; i <= 10 && !(await page.getByTestId('phase').textContent())?.includes('Finished'); i++) {
		await page.getByRole('button', { name: 'Home +1' }).click();
		await expect(page.getByTestId('score-home')).toHaveText(String(i));
	}
	await expect(page.getByTestId('phase')).toHaveText('Finished');
	await expect(page.getByText('Home wins')).toBeVisible();
	await expect(watcher.getByText('Home wins')).toBeVisible();
	await watcher.close();

	// History
	await page.goto('/games');
	const row = page.getByTestId('game-row');
	await expect(row).toHaveCount(1);
	await expect(row).toContainText('Mia');
	await expect(row).toContainText('Tom');

	// Profile: one win, a rating above the start (the projector rates the game asynchronously).
	await page.getByRole('link', { name: 'Players' }).first().click();
	await page.getByRole('link', { name: 'Mia' }).click();
	await expect(page.getByTestId('record')).toContainText('1');
	await expect(page.getByTestId('ratings')).toContainText('1016');
});

// Voiding the game in its edit tools takes its rating back.
test('void the game', async ({ page }) => {
	await page.goto('/games');
	await page.getByTestId('game-row').first().click();
	page.once('dialog', (d) => d.accept());
	await page.getByTestId('edit-tools').getByRole('button', { name: 'Void game' }).click();
	await expect(page.getByText('This game is void')).toBeVisible();

	await page.goto('/games');
	await expect(page.getByTestId('game-row').first()).toContainText('void');
	await page.goto('/players');
	await page.getByRole('link', { name: 'Mia' }).click();
	await expect(page.getByRole('heading', { name: 'Mia' })).toBeVisible();
	await expect(page.getByTestId('ratings')).toContainText('No rated games yet.');
	await expect(page.getByTestId('record')).toContainText('0 %');
});
