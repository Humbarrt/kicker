import { expect, test } from '@playwright/test';
import { admin } from './helpers';

// First start: the setup wizard creates the admin and hands out the emergency kit (spec 03 §7).
test('setup wizard', async ({ page }) => {
	await page.goto('/');
	await expect(page).toHaveURL(/\/setup/);

	await page.getByLabel('English').check();
	await page.getByRole('button', { name: 'Continue' }).click();

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

	await expect(page).toHaveURL(/\/$/);
	await expect(page.getByRole('link', { name: 'Settings' }).first()).toBeVisible();
	await expect(page.getByTestId('phase')).toBeVisible();
});

test('setup is closed afterwards', async ({ page }) => {
	await page.goto('/setup');
	await expect(page).not.toHaveURL(/\/setup/);
});
