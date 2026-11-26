// API client. In the browser it uses fetch with the session cookie; the Tauri app will swap in
// Rust-side calls with the pinned CA (spec 03 §10) behind the same functions.
import type {
	Account,
	AuditEntry,
	ConsoleLine,
	Group,
	Info,
	Me,
	ModuleInfo,
	OnboardingStatus,
	Palette,
	Player,
	Permission,
	PortView,
	Preset,
	Rules,
	Side,
	SimView,
	Coin,
	EditFeedEntry,
	EditRequest,
	GameDetail,
	GameQuery,
	GameSummary,
	GameView,
	Rating,
	RegistrationSettings,
	EmergencyKit,
	DeviceSession,
	RatingKey,
	PlayerStats,
	Leaderboard,
	Overview,
	RatingChange,
	TableSettings,
	TableView,
	TournamentSummary,
	TournamentDetail,
	TournamentOption,
	TournamentSetupInput,
	TournamentRules,
	BackupsView,
	BackupConfig,
	RestoreView,
	PlayerTournamentResult
} from './types.ts';

export type * from './types.ts';
import { deviceName } from '#lib/device.ts';

/** An error answer of the API: `code` is stable and used for translations. */
export class ApiError extends Error {
	constructor(
		readonly status: number,
		readonly code: string,
		message: string,
		/** The whole error body, e.g. `field` of invalid_setting. */
		readonly data: Record<string, unknown> = {}
	) {
		super(message);
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	let res: Response;
	try {
		const form = body instanceof FormData; // uploads: the browser sets the multipart type
		res = await fetch(path, {
			method,
			headers: body === undefined || form ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' },
			body: body === undefined ? undefined : form ? body : JSON.stringify(body)
		});
	} catch {
		throw new ApiError(0, 'unreachable', 'server not reachable');
	}
	if (res.status === 204 || res.status === 202) return undefined as T;
	const text = await res.text();
	let data: unknown = null;
	try {
		data = text ? JSON.parse(text) : null;
	} catch {
		/* not JSON */
	}
	if (!res.ok) {
		const e = (data ?? {}) as { code?: string; error?: string };
		throw new ApiError(res.status, e.code ?? 'http_' + res.status, e.error ?? res.statusText, (data ?? {}) as Record<string, unknown>);
	}
	return data as T;
}

const get = <T>(p: string) => request<T>('GET', p);
const post = <T>(p: string, b: unknown = {}) => request<T>('POST', p, b);
const put = <T>(p: string, b: unknown) => request<T>('PUT', p, b);
const patch = <T>(p: string, b: unknown) => request<T>('PATCH', p, b);
const del = <T>(p: string) => request<T>('DELETE', p);
const enc = encodeURIComponent;

export const api = {
	// backups (spec 10 §2, admins)
	backups: () => get<BackupsView>('/api/backups'),
	setBackupConfig: (c: BackupConfig) => put<BackupsView>('/api/backups/config', c),
	runBackup: () => post<BackupsView>('/api/backups/run'),
	backupFileUrl: (file: string, location = '') =>
		`/api/backups/files/${enc(file)}${location ? '?location=' + enc(location) : ''}`,
	deleteBackup: (file: string, location = '') =>
		del<BackupsView>(`/api/backups/files/${enc(file)}${location ? '?location=' + enc(location) : ''}`),
	restoreStored: (file: string, passphrase = '') => post<RestoreView>(`/api/backups/files/${enc(file)}/restore`, { passphrase }),
	uploadBackup: (file: File) => {
		const f = new FormData();
		f.append('file', file);
		return post<{ file: string }>('/api/backups/upload', f);
	},
	restoreBackup: (file: File, passphrase: string, onboarding = false) => {
		const f = new FormData();
		f.append('file', file);
		f.append('passphrase', passphrase);
		return post<RestoreView>(onboarding ? '/api/onboarding/restore' : '/api/backups/restore', f);
	},
	changePassphrase: (password: string, passphrase: string) => post<{ passphrase: string }>('/api/backups/passphrase', { password, passphrase }),

	// tournaments (spec 09)
	tournaments: () => get<TournamentSummary[]>('/api/tournaments'),
	tournament: (id: number) => get<TournamentDetail>(`/api/tournaments/${id}`),
	tournamentOptions: (teams: number, monster: boolean, rules?: TournamentRules) => {
		const q = new URLSearchParams({ teams: String(teams) });
		if (monster) q.set('monster', '1');
		if (rules) for (const [k, v] of Object.entries(rules)) if (v != null) q.set(k, String(v));
		return get<TournamentOption[]>(`/api/tournaments/options?${q}`);
	},
	createTournament: (s: TournamentSetupInput) => post<TournamentDetail>('/api/tournaments', s),
	updateTournament: (id: number, s: TournamentSetupInput) => put<TournamentDetail>(`/api/tournaments/${id}`, s),
	tournamentAction: (id: number, action: 'start' | 'pause' | 'continue' | 'abandon' | 'reinstate') =>
		post<TournamentDetail>(`/api/tournaments/${id}/${action}`),
	playPairing: (id: number, pairing: string) => post<TournamentDetail>(`/api/tournaments/${id}/play`, { pairing }),
	deleteTournament: (id: number) => del<void>(`/api/tournaments/${id}`),
	changeTournament: (id: number, c: { name: string; rules?: TournamentRules; preset_id?: number }) =>
		patch<TournamentDetail>(`/api/tournaments/${id}`, c),
	replacePlayer: (id: number, participant: number, player: number) =>
		post<TournamentDetail>(`/api/tournaments/${id}/replace`, { participant, player }),
	dropOut: (id: number, entrant: number) => post<TournamentDetail>(`/api/tournaments/${id}/dropout`, { entrant }),
	rebuildTournament: (id: number) => post<TournamentDetail>(`/api/tournaments/${id}/rebuild`),
	playerTournaments: (id: number) => get<PlayerTournamentResult[]>(`/api/players/${id}/tournaments`),

	info: () => get<Info>('/api/info'),

	// setup wizard (spec 03 §12)
	onboarding: () => get<OnboardingStatus>('/api/onboarding'),
	onboardingStart: () => post<OnboardingStatus>('/api/onboarding/start'),
	onboardingLanguage: (language: string) => put<OnboardingStatus>('/api/onboarding/language', { language }),
	onboardingAdmin: (username: string, password: string) =>
		post<{ account: Account }>('/api/onboarding/admin', { username, password }),
	onboardingKit: () => post<EmergencyKit>('/api/onboarding/kit'),
	onboardingFinish: () => post<OnboardingStatus>('/api/onboarding/finish'),

	// session
	me: () => get<Me>('/api/auth/me'),
	login: (username: string, password: string) =>
		post<{ account: Account }>('/api/auth/login', { username, password, device_name: deviceName() }),
	signup: (s: { username: string; password: string; player_id?: number; player_name?: string }) =>
		post<{ account: Account }>('/api/auth/signup', { ...s, device_name: deviceName() }),
	resetPassword: (username: string, code: string, password: string) =>
		post<{ account: Account }>('/api/auth/reset', { username, code, password, device_name: deviceName() }),
	recover: (username: string, recovery_key: string, password: string) =>
		post<{ account: Account; kit: EmergencyKit }>('/api/auth/recover', { username, recovery_key, password, device_name: deviceName() }),
	sessions: () => get<DeviceSession[]>('/api/auth/sessions'),
	endSession: (id: number) => del<void>(`/api/auth/sessions/${id}`),
	endOtherSessions: () => del<void>('/api/auth/sessions'),
	logout: () => post<void>('/api/auth/logout'),
	changePassword: (current: string, next: string) => post<void>('/api/auth/password', { current, new: next }),

	// players
	players: (all = false) => get<Player[]>('/api/players' + (all ? '?all=1' : '')),
	player: (id: number) => get<Player>(`/api/players/${id}`),
	palette: () => get<Palette>('/api/players/palette'),
	createPlayer: (name: string, color = '') => post<Player>('/api/players', { name, color }),
	createMyPlayer: (name: string, color = '') => post<Player>('/api/players/me', { name, color }),
	updatePlayer: (id: number, change: { name?: string; color?: string; archived?: boolean }) =>
		patch<Player>(`/api/players/${id}`, change),
	uploadAvatar: async (id: number, jpeg: Blob): Promise<Player> => {
		const res = await fetch(`/api/players/${id}/avatar`, { method: 'PUT', body: jpeg, headers: { 'Content-Type': 'image/jpeg' } });
		const data = await res.json().catch(() => ({}));
		if (!res.ok) throw new ApiError(res.status, data.code ?? 'http_' + res.status, data.error ?? res.statusText);
		return data as Player;
	},
	removeAvatar: (id: number) => del<Player>(`/api/players/${id}/avatar`),
	deletePlayer: (id: number) => del<void>(`/api/players/${id}`),

	// coins
	coins: (playerId = 0) => get<Coin[]>('/api/coins' + (playerId ? `?player_id=${playerId}` : '')),
	assignCoin: (uid: string, player_id: number, label = '') => put<Coin>(`/api/coins/${enc(uid)}`, { player_id, label }),
	setCoinActive: (uid: string, active: boolean) => patch<Coin>(`/api/coins/${enc(uid)}`, { active }),
	deleteCoin: (uid: string) => del<void>(`/api/coins/${enc(uid)}`),

	// table
	table: () => get<TableView>('/api/table'),
	presets: () => get<Preset[]>('/api/presets'),
	start: (opts: { preset_id?: number; rules?: Rules } = {}) => post<TableView>('/api/table/start', opts),
	score: (side: Side, delta: 1 | -1) => post<TableView>('/api/table/score', { side, delta }),
	undo: () => post<TableView>('/api/table/undo'),
	pause: () => post<TableView>('/api/table/pause'),
	resume: () => post<TableView>('/api/table/resume'),
	wake: () => post<TableView>('/api/table/wake'),
	resumeGame: (id: number) => post<TableView>(`/api/games/${id}/resume`),
	reinstateGame: (id: number) => post<TableView>(`/api/games/${id}/reinstate`),
	abandonedGames: () => get<GameView[]>('/api/games/abandoned'),
	register: (side: Side, player_id: number) => post<TableView>('/api/table/registrations', { side, player_id }),
	unregister: (side: Side, slot: number) => del<TableView>(`/api/table/registrations/${side}/${slot}`),

	// game history and edits (D2)
	games: (q: GameQuery = {}) => {
		const p = new URLSearchParams();
		for (const [k, v] of Object.entries(q)) {
			const val = Array.isArray(v) ? v.join(',') : v;
			if (val) p.set(k, String(val));
		}
		return get<GameSummary[]>('/api/games' + (p.size ? '?' + p : ''));
	},
	game: (id: number) => get<GameDetail>(`/api/games/${id}`),
	editGame: (id: number, e: EditRequest) => post<GameDetail>(`/api/games/${id}/edits`, e),
	edits: (limit = 50) => get<EditFeedEntry[]>(`/api/edits?limit=${limit}`),

	// ratings (D9)
	playerRatings: (id: number) => get<Rating[]>(`/api/players/${id}/ratings`),
	playerStats: (id: number) => get<PlayerStats>(`/api/players/${id}/stats`),
	leaderboard: (key: RatingKey) => get<Leaderboard>(`/api/leaderboard?key=${key}`),
	overview: () => get<Overview>('/api/overview'),
	gameRatings: (id: number) => get<RatingChange[]>(`/api/games/${id}/ratings`),

	// settings (admin)
	tableSettings: () => get<TableSettings>('/api/settings/table'),
	saveTableSettings: (s: Partial<TableSettings>) => put<TableSettings>('/api/settings/table', s),

	// simulator (--sim only)
	sim: () => get<SimView>('/api/sim'),
	simCoins: () => get<Coin[]>('/api/sim/coins'),
	simInsert: (uid: string, coin: string) => post<void>(`/api/sim/${enc(uid)}/insert`, { coin }),
	simButton: (uid: string) => post<void>(`/api/sim/${enc(uid)}/button`),
	simEmit: (uid: string, instance: string, event: string) =>
		post<void>(`/api/sim/${enc(uid)}/${enc(instance)}/${enc(event)}`),

	// modules (admin)
	modules: () => get<ModuleInfo[]>('/api/modules'),
	console: (uid: string) => get<ConsoleLine[]>(`/api/modules/${enc(uid)}/console`),
	adopt: (uid: string, name: string, roles: Record<string, string>) =>
		post<void>(`/api/modules/${enc(uid)}/adopt`, { name, roles }),
	release: (uid: string) => post<void>(`/api/modules/${enc(uid)}/release`),
	identify: (uid: string) => post<void>(`/api/modules/${enc(uid)}/identify`),
	setConfig: (uid: string, instance: string, partial: Record<string, unknown>) =>
		patch<void>(`/api/modules/${enc(uid)}/config/${enc(instance)}`, partial),
	ports: () => get<PortView[]>('/api/modules/ports'),
	probe: (port: string) => post<void>('/api/modules/ports/probe', { port }),

	// groups, accounts, audit (admin)
	permissions: () => get<Permission[]>('/api/permissions'),
	groups: () => get<Group[]>('/api/groups'),
	createGroup: (name: string, copy_from: number) => post<Group>('/api/groups', { name, copy_from }),
	updateGroup: (id: number, change: { name?: string; permissions?: Record<string, number> }) =>
		patch<Group>(`/api/groups/${id}`, change),
	deleteGroup: (id: number, moveTo: number) => del<void>(`/api/groups/${id}?move_to=${moveTo}`),
	accounts: () => get<Account[]>('/api/accounts'),
	createAccount: (a: { username: string; password: string; group_id: number; must_change_password: boolean }) =>
		post<Account>('/api/accounts', a),
	updateAccount: (id: number, change: { group_id?: number; status?: string; player_id?: number }) =>
		patch<Account>(`/api/accounts/${id}`, change),
	approveAccount: (id: number, a: { group_id: number; player_id?: number; new_player_name?: string }) =>
		post<Account>(`/api/accounts/${id}/approve`, a),
	rejectAccount: (id: number) => post<void>(`/api/accounts/${id}/reject`),
	resetCode: (id: number) => post<{ code: string; expires_at: number }>(`/api/accounts/${id}/reset-code`),
	registration: () => get<RegistrationSettings>('/api/settings/registration'),
	saveRegistration: (r: RegistrationSettings) => put<RegistrationSettings>('/api/settings/registration', r),
	security: () => get<{ recovery_key_created_at: number | null; ca_fingerprint: string; https: boolean }>('/api/settings/security'),
	emergencyKit: (password: string) => post<EmergencyKit>('/api/settings/emergency-kit', { password }),
	audit: (limit = 200) => get<AuditEntry[]>(`/api/audit?limit=${limit}`)
};

/** URL of a profile picture (128 for lists, 512 for profiles). */
export function avatarUrl(hash: string, size: 128 | 512 = 128): string {
	return `/api/media/avatars/${hash}-${size}.jpg`;
}
