// The Settings menu: one tab per page, each shown to whoever may use it. Admins see all of them;
// coins (coins.manage), edits (games.revert) and the log (log.view) can be granted to others.
import { session } from '#lib/session.svelte.ts';
import { t } from '#lib/i18n.svelte.ts';

export type SettingsTab = { href: string; label: string; badge?: number | string };

/** What needs an admin's attention: sign-ups, a missing emergency kit, failing backups (header badge). */
export function settingsAttention(): number {
	return (session.me?.pending_accounts ?? 0) + (session.me?.needs_kit ? 1 : 0) + (session.me?.backup_problem ? 1 : 0);
}

export function settingsTabs(): SettingsTab[] {
	const admin = session.admin;
	const pending = session.me?.pending_accounts ?? 0;
	return [
		{ href: '/settings/table', label: t('admin.table'), show: admin },
		{ href: '/settings/modules', label: t('admin.modules'), show: admin },
		{ href: '/settings/coins', label: t('admin.coins'), show: session.can('coins.manage') },
		{ href: '/settings/groups', label: t('admin.groups'), show: admin },
		{ href: '/settings/accounts', label: t('admin.accounts'), show: admin, badge: pending || undefined },
		{ href: '/settings/security', label: t('admin.security'), show: admin, badge: session.me?.needs_kit ? '!' : undefined },
		{ href: '/settings/backups', label: t('admin.backups'), show: admin, badge: session.me?.backup_problem ? '!' : undefined },
		{ href: '/settings/edits', label: t('admin.edits'), show: session.can('games.revert') },
		{ href: '/settings/audit', label: t('admin.audit'), show: session.can('log.view') }
	]
		.filter((tab) => tab.show)
		.map(({ href, label, badge }) => ({ href, label, badge }));
}
