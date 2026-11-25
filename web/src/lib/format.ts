// Small formatting helpers.

/** mm:ss from milliseconds (play time, countdown). */
export function clock(ms: number): string {
	const s = Math.max(0, Math.floor(ms / 1000));
	return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

/** Date and time in the browser's locale. */
export function dateTime(ms: number | string | null | undefined, lang: string): string {
	if (ms === null || ms === undefined) return '';
	return new Date(ms).toLocaleString(lang, { dateStyle: 'medium', timeStyle: 'short' });
}

/** "5 minutes ago" in the browser's locale. */
export function ago(ms: number, now: number, lang: string): string {
	const s = Math.min(0, Math.round((ms - now) / 1000)); // never "in 3 seconds" from clock skew
	const rtf = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' });
	const abs = Math.abs(s);
	if (abs < 60) return rtf.format(s, 'second');
	if (abs < 3600) return rtf.format(Math.round(s / 60), 'minute');
	if (abs < 86400) return rtf.format(Math.round(s / 3600), 'hour');
	return rtf.format(Math.round(s / 86400), 'day');
}

export function time(ms: number | string, lang: string): string {
	return new Date(ms).toLocaleTimeString(lang, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}
