// A short device description ("Firefox on macOS") from a user agent, for "My devices".

export function describeAgent(ua: string): string {
	if (!ua) return '';
	const browser = /Edg\//.test(ua)
		? 'Edge'
		: /OPR\//.test(ua)
			? 'Opera'
			: /Firefox\//.test(ua)
				? 'Firefox'
				: /Chrome\//.test(ua)
					? 'Chrome'
					: /Safari\//.test(ua)
						? 'Safari'
						: '';
	const os = /iPhone|iPad|iPod/.test(ua)
		? 'iOS'
		: /Android/.test(ua)
			? 'Android'
			: /Mac OS X|Macintosh/.test(ua)
				? 'macOS'
				: /Windows/.test(ua)
					? 'Windows'
					: /CrOS/.test(ua)
						? 'ChromeOS'
						: /Linux/.test(ua)
							? 'Linux'
							: '';
	if (browser && os) return `${browser} · ${os}`;
	return browser || os || ua.slice(0, 40);
}

/** This browser's description, sent as device_name when logging in. */
export function deviceName(): string {
	return typeof navigator === 'undefined' ? '' : describeAgent(navigator.userAgent);
}
