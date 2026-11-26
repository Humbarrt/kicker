import { expect, test } from '@playwright/test';
import { admin } from './helpers';

// First start: the setup wizard creates the admin, hands out the emergency kit (spec 03 §7) and
// walks through table, game defaults, modules and backups (spec 10 §6).
test('setup wizard', async ({ page }) => {
	await page.goto('/');
	await expect(page).toHaveURL(/\/setup/);

	await page.getByLabel('English').check();
	await page.getByRole('button', { name: 'Continue' }).click();

	// Step 2: fresh start or restore.
	await expect(page.getByTestId('setup-restore')).toBeVisible();
	await page.getByTestId('setup-fresh').click();

	await page.getByLabel('Username').fill(admin.username);
	await page.getByLabel('Password', { exact: true }).fill(admin.password);
	await page.getByLabel('Repeat new password').fill(admin.password);
	await page.getByRole('button', { name: 'Create' }).click();

	// The kit: the recovery key words; finishing needs the "saved" tick.
	await expect(page.getByTestId('kit-words').locator('li')).toHaveCount(12, { timeout: 30_000 });
	const finish = page.getByRole('button', { name: 'Finish setup' });
	await expect(finish).toBeDisabled();
	await page.getByTestId('kit-saved').check();
	await finish.click();

	// Steps 5–9 reuse the settings forms.
	const more = page.getByTestId('setup-more');
	await expect(more).toHaveAttribute('data-step', '5');
	await page.getByTestId('table-save').click();
	await expect(more).toHaveAttribute('data-step', '6');
	await page.getByTestId('table-save').click();
	await expect(more).toHaveAttribute('data-step', '7');
	await page.getByTestId('setup-skip').click();
	await expect(more).toHaveAttribute('data-step', '8');
	await expect(page.getByTestId('backup-cycle')).toBeVisible();
	await page.getByTestId('setup-next').click();
	await expect(page.getByTestId('setup-address')).toHaveText(/^http/);
	await page.getByTestId('setup-to-board').click();

	await expect(page).toHaveURL(/\/$/);
	await expect(page.getByRole('link', { name: 'Settings' }).first()).toBeVisible();
	await expect(page.getByTestId('phase')).toBeVisible();
});

test('setup is closed afterwards', async ({ page }) => {
	await page.goto('/setup');
	await expect(page).not.toHaveURL(/\/setup/);
});
