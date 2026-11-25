import { expect, test, type Browser, type Page } from '@playwright/test';

// Shared by the specs, which run in order against one server (playwright.config.ts).
export const admin = { username: 'anna', password: 'blue tiger dances' };
export const member = { username: 'kim', password: 'green frog jumps' };

// Log in through the API: the page's request context shares its cookies with the browser.
export async function login(page: Page, who: { username: string; password: string }) {
	const res = await page.request.post('/api/auth/login', { data: who, headers: { origin: baseURL() } });
	expect(res.ok(), await res.text()).toBeTruthy();
}

// Log in through the login form.
export async function loginForm(page: Page, who: { username: string; password: string }) {
	await page.goto('/login');
	await page.getByLabel('Username').fill(who.username);
	await page.getByLabel('Password', { exact: true }).fill(who.password);
	await page.getByRole('button', { name: 'Log in' }).last().click();
	await expect(page).not.toHaveURL(/\/login/);
}

// The table may be asleep (the simulator has no coins inserted yet).
export async function wake(page: Page) {
	const button = page.getByRole('button', { name: 'Wake up' });
	if (await button.isVisible()) await button.click();
	await expect(button).toBeHidden();
}

export function baseURL(): string {
	return new URL(test.info().project.use.baseURL!).origin;
}

// A second browser (its own cookies), e.g. a phone watching the scoreboard.
export function otherBrowser(browser: Browser): Promise<Page> {
	return browser.newPage({ baseURL: baseURL(), locale: 'en-US' });
}

export async function newPlayer(page: Page, name: string) {
	await page.goto('/players');
	await page.getByRole('button', { name: 'New player' }).click();
	await page.getByLabel('Name', { exact: true }).fill(name);
	await page.getByRole('button', { name: 'Create', exact: true }).click();
	await expect(page.getByRole('dialog')).toBeHidden();
}

export async function register(page: Page, side: 'home' | 'away', name: string) {
	await page.getByTestId(`roster-${side}`).getByRole('button', { name: 'Add player' }).click();
	// The player picker: search, then Enter picks the only match.
	const search = page.locator('dialog[open]').getByTestId('pick-search');
	await search.fill(name);
	await search.press('Enter');
	await expect(page.locator('dialog[open]')).toHaveCount(0);
	await expect(page.getByTestId(`roster-${side}`).getByTestId('reg')).toContainText(name);
}
