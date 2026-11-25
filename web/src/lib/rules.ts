import type { Rules } from '#lib/api/client.ts';
import { t } from '#lib/i18n.svelte.ts';

/** A one-line description of win conditions, e.g. "First to 5, win by 2, max 8, 5 min". */
export function describeRules(r: Rules): string {
	const parts = [t('board.first_to', { n: r.win_score })];
	if (r.win_by > 1) parts.push(t('board.win_by', { n: r.win_by }));
	if (r.max_score) parts.push(t('board.max', { n: r.max_score }));
	if (r.time_limit_s) parts.push(`${Math.round(r.time_limit_s / 60)} ${t('board.minutes')}`);
	return parts.join(' · ');
}
