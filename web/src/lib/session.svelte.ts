// Who is using the UI and what they may do (GET /api/info + /api/auth/me).
import { api, type Info, type Me } from '#lib/api/client.ts';
import { i18n } from '#lib/i18n.svelte.ts';

class Session {
	info = $state<Info | null>(null);
	me = $state<Me | null>(null);
	error = $state<unknown>(null);
	loaded = $state(false);

	get account() {
		return this.me?.account ?? null;
	}
	get admin() {
		return this.me?.admin === true;
	}

	/** can reports whether the user has a permission (any value but "off"). */
	can(key: string): boolean {
		return (this.me?.permissions[key] ?? 0) !== 0;
	}

	async refresh() {
		try {
			const info = await api.info();
			this.info = info;
			this.me = await api.me(); // during setup too: step 4 belongs to the new admin's login
			const lang = localLanguage() ?? info.language;
			if (lang) i18n.set(lang);
			this.error = null;
		} catch (e) {
			this.error = e;
		} finally {
			this.loaded = true;
		}
	}
}

/** A language chosen in this browser overrides the kicker's (stored per browser). */
export function localLanguage(): string | null {
	try {
		return localStorage.getItem('kicker.language');
	} catch {
		return null;
	}
}

export function setLocalLanguage(code: string) {
	try {
		localStorage.setItem('kicker.language', code);
	} catch {
		/* private mode: only for this visit */
	}
	i18n.set(code);
}

export const session = new Session();
