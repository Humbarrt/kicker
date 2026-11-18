// API client. In the browser it uses fetch; the Tauri app will swap in Rust-side calls
// with the pinned CA (spec 03 §10) behind the same functions.

export type Info = {
	name: string;
	version: string;
	sim: boolean;
	onboarding_done: boolean;
};

async function getJSON<T>(path: string): Promise<T> {
	const res = await fetch(path, { headers: { Accept: 'application/json' } });
	if (!res.ok) throw new Error(`${path}: ${res.status} ${res.statusText}`);
	return (await res.json()) as T;
}

export const api = {
	info: () => getJSON<Info>('/api/info')
};
