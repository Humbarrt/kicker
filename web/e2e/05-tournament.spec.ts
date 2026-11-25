import { expect, test } from '@playwright/test';
import { admin, login, newPlayer, wake } from './helpers';

test.beforeEach(async ({ page }) => login(page, admin));

// A small tournament from the setup page to the podium (spec 09): four players 1 against 1, the
// format and rules chosen by clicking, every pairing put on the table with "Play" from the
// scoreboard and won with a goal.
test('a knockout from setup to podium', async ({ page }) => {
	test.setTimeout(120_000); // four kickoff countdowns
	await newPlayer(page, 'Ann');
	await newPlayer(page, 'Ben');

	// The game an earlier spec left on the table ends first: "Play" waits for a free table.
	await page.goto('/');
	await wake(page);
	for (let i = 0; i < 20 && (await page.getByTestId('phase').textContent())?.includes('Playing'); i++) {
		await page.getByRole('button', { name: 'Home +1' }).click();
		await page.waitForTimeout(100);
	}
	await expect(page.getByTestId('phase')).not.toHaveText('Playing');

	await page.goto('/tournaments/new');
	await page.getByLabel('Name').first().fill('Friday cup');
	await page.getByTestId('add-singles').click();
	const search = page.locator('dialog[open]').getByTestId('pick-search');
	for (const name of ['Mia', 'Tom', 'Ann', 'Ben']) {
		await search.fill(name);
		await search.press('Enter'); // ticks the only match
	}
	await page.getByTestId('pick-add').click();
	await expect(page.getByTestId('setup-entries')).toContainText('Ben');

	// The formats: clicking one selects it (round robin, then back to knockout).
	const formats = page.getByTestId('format-options');
	await formats.locator('label[data-option=round_robin]').click();
	await expect(formats.locator('label[data-option=round_robin]')).toHaveClass(/border-emerald-600/);
	await formats.locator('label[data-option=knockout]').click();
	await expect(formats.locator('label[data-option=knockout]')).toHaveClass(/border-emerald-600/);
	await expect(formats.locator('label[data-option=round_robin]')).not.toHaveClass(/border-emerald-600/);

	// Custom rules: first to 1.
	await page.locator('input[name=t-rules][value=custom]').check();
	await page.getByLabel('Goals to win').fill('1');
	await page.getByLabel('Lead needed').fill('1');
	await page.getByTestId('save-tournament').click();
	await expect(page).toHaveURL(/\/tournaments\/\d+$/);
	await expect(page.getByTestId('tournament-state')).toHaveText('Setup');
	await page.getByRole('button', { name: 'Start' }).click();
	await expect(page.getByTestId('tournament-state')).toHaveText('Running');

	// Semi-finals, 3rd place, final: each from the scoreboard's tournament panel.
	await page.goto('/');
	for (let i = 0; i < 4; i++) {
		const play = page.getByTestId('play').first();
		await expect(play).toBeEnabled({ timeout: 20_000 });
		await play.click();
		await expect(page.getByTestId('phase')).toHaveText('Playing', { timeout: 15_000 }); // after the countdown
		await page.getByRole('button', { name: i % 2 ? 'Away +1' : 'Home +1' }).click();
		await expect(page.getByTestId('phase')).toHaveText('Finished');
	}

	await page.getByRole('link', { name: 'Tournaments' }).first().click();
	await page.getByRole('link', { name: 'Friday cup' }).click();
	await expect(page.getByTestId('tournament-state')).toHaveText('Finished');
	const podium = page.getByTestId('podium');
	await expect(podium.locator('li')).toHaveCount(3);
	// The winner's name opens their profile.
	await podium.locator('li').first().getByRole('link').click();
	await expect(page).toHaveURL(/\/players\/\d+$/);
	await expect(page.getByTestId('player-tournaments')).toContainText('Friday cup');
});
