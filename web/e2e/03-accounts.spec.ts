import { expect, test } from '@playwright/test';
import { admin, login, loginForm, member, otherBrowser } from './helpers';

// Sign-up waits for an admin (spec 03 §5); after the approval the member logs in normally and
// sees only what members may.
test('sign up and approve', async ({ page, browser }) => {
	await page.goto('/signup');
	await page.getByLabel('Username').fill(member.username);
	await page.getByLabel('Password', { exact: true }).fill(member.password);
	await page.getByLabel('Repeat new password').fill(member.password);
	await page.getByLabel('Name', { exact: true }).fill('Kim');
	await page.getByRole('button', { name: 'Create account' }).click();
	await expect(page.locator('p.notice').first()).toBeVisible();

	const adminPage = await otherBrowser(browser);
	await login(adminPage, admin);
	await adminPage.goto('/');
	await expect(adminPage.getByTestId('settings-badge')).toBeVisible();
	await adminPage.goto('/settings/accounts');
	await adminPage.getByTestId('pending').getByRole('button', { name: 'Approve' }).click();
	await expect(adminPage.getByTestId('pending')).toBeHidden();
	await adminPage.close();

	const fresh = await otherBrowser(browser);
	await loginForm(fresh, member);
	await fresh.goto('/settings');
	await expect(fresh.getByRole('link', { name: 'Accounts' })).toHaveCount(0);
	await fresh.close();
});

test('wrong password', async ({ page }) => {
	await page.goto('/login');
	await page.getByLabel('Username').fill(admin.username);
	await page.getByLabel('Password', { exact: true }).fill('not the password');
	await page.getByRole('button', { name: 'Log in' }).last().click();
	await expect(page.locator('.error')).toBeVisible();
	await expect(page).toHaveURL(/\/login/);
});
