import { expect, test } from '@playwright/test';
import { admin, login, register, wake } from './helpers';

test.beforeEach(async ({ page }) => login(page, admin));

// The virtual goal sensor (`--sim`) comes adopted, its instances in the goals; a ball into the
// home goal is a point for away, like with a real sensor (M1).
test('score with the simulated goal sensor', async ({ page }) => {
	await page.goto('/settings/modules');
	const card = page.locator('section.card').filter({ hasText: 'sim-goal-sensor' });
	await expect(card.getByText('adopted', { exact: true })).toBeVisible();
	await expect(card.locator('select').nth(0)).toHaveValue('home_goal');
	await expect(card.locator('select').nth(1)).toHaveValue('away_goal');

	await page.goto('/');
	await wake(page);
	await register(page, 'home', 'Mia');
	await register(page, 'away', 'Kim');
	await page.getByRole('button', { name: 'Start now' }).click();
	await expect(page.getByTestId('phase')).toHaveText('Playing', { timeout: 15_000 });

	await page.getByRole('button', { name: 'Ball into home goal' }).click();
	await expect(page.getByTestId('score-away')).toHaveText('1');
	await page.getByRole('button', { name: 'Ball into away goal' }).click();
	await expect(page.getByTestId('score-home')).toHaveText('1');
	await page.getByRole('button', { name: 'Undo' }).click();
	await expect(page.getByTestId('score-home')).toHaveText('0');
});
