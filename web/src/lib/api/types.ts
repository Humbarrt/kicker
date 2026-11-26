// API types. Hand-written for now; `tygo` will generate them from the Go DTOs (spec 04 §2).

export type Side = 'home' | 'away';

export type Info = {
	server_id: string;
	name: string;
	version: string;
	sim: boolean;
	onboarding_done: boolean;
	language: string;
	registration: RegistrationMode;
	ca_fingerprint: string; // "" = no HTTPS
};

export type RegistrationMode = 'approval' | 'open' | 'closed';

/** Registration settings (spec 03 §3). */
export type RegistrationSettings = { mode: RegistrationMode; open_group_id: number; default_group_id: number };

/** The emergency kit: recovery key + backup passphrase (spec 03 §8). Shown once. */
export type EmergencyKit = {
	name: string;
	server_id: string;
	ca_fingerprint: string;
	recovery_key: string;
	backup_passphrase: string;
	created_at: number;
	filename: string;
	text: string;
};

/** One of my sessions ("My devices"). */
export type DeviceSession = {
	id: number;
	kind: 'web' | 'app';
	device_name: string;
	user_agent: string;
	created_at: number;
	last_seen_at: number;
	current: boolean;
};

export type GroupRef = { id: number; name: string; builtin?: 'admin' | 'member' | 'anybody' };

export type Account = {
	id: number;
	username: string;
	group: GroupRef;
	status: 'pending' | 'approved' | 'disabled';
	player_id: number | null;
	must_change_password: boolean;
	created_at: number;
	last_login_at: number | null;
	requested_player_id?: number; // sign-up request of a pending account
	requested_player_name?: string;
};

export type Me = {
	account: Account | null;
	admin: boolean;
	permissions: Record<string, number>;
	language: string;
	pending_accounts?: number; // admins: sign-ups waiting
	needs_kit?: boolean; // admins: no emergency kit made yet
	backup_problem?: boolean; // admins: a backup target failed, or nothing younger than 48 h
};

export type PermissionKind = 'on_off' | 'scope' | 'days';

export type Permission = {
	key: string;
	area: string;
	kind: PermissionKind;
	login_required: boolean;
	default_member: number;
	default_anybody: number;
};

export type Group = {
	id: number;
	name: string;
	builtin?: 'admin' | 'member' | 'anybody';
	members: number;
	permissions: Record<string, number>;
	effective: Record<string, number>;
};

export type Rules = {
	win_score: number;
	win_by: number;
	max_score: number | null;
	time_limit_s: number | null;
	time_tie: 'golden_goal' | 'draw';
};

export type Preset = { id: number; label: string; rules: Rules; builtin: boolean };

export type Goal = {
	seq: number;
	side: Side;
	source: 'sensor' | 'table' | 'manual' | 'system';
	at: number;
	play_time_ms: number;
	voided: boolean;
};

export type GameView = {
	id: number;
	current: boolean;
	preset_id: number | null;
	rules: Rules;
	status: 'active' | 'paused' | 'finished' | 'abandoned';
	home: number;
	away: number;
	target: number;
	winner: Side | null;
	end_reason?: 'score' | 'time' | 'golden_goal';
	started_at: number;
	ended_at: number | null;
	play_time_ms: number;
	clock_running: boolean;
	clock_expired: boolean;
	golden_goal: boolean;
	next_ball: Side | null;
	first_ball: Side | null;
	goals: Goal[];
	roster: { home: Seat[]; away: Seat[] };
	/** A tournament game: its pairing row, and where team A plays (spec 09). */
	pairing_id?: number;
	pairing_a_side?: Side;
	/** The game counts for nothing (game_voided). */
	voided: boolean;
	/** Corrections after the game in force. */
	edits: number;
};

export type Via = 'coin' | 'corner' | 'manual';

/** A slot of a game's roster. player_id null = an unknown coin (counts as guest). */
export type Seat = { slot: 1 | 2; player_id: number | null; coin_uid: string | null; via: Via };

/** A player – or an unknown coin (player_id null) – registered in a slot (spec 02 §4). */
export type Registration = {
	slot: 1 | 2;
	player_id: number | null;
	coin_uid: string | null;
	stale: boolean;
	sources: { via: Via; ref?: string; stale: boolean }[];
};

export type CoinInside = { uid: string; player_id: number | null };

export type TableView = {
	rev: number;
	phase: 'sleeping' | 'idle' | 'pregame' | 'playing' | 'finished';
	server_time: number;
	game: GameView | null;
	coins: { home: CoinInside[]; away: CoinInside[] };
	registrations: { home: Registration[]; away: Registration[] };
	/** Display names ('' = translated default) and colors of the sides (D17). */
	sides: { home: SideSettings; away: SideSettings };
	/** End of the kickoff countdown (Unix ms), in pregame only. */
	kickoff_at: number | null;
	/** Rules of the next game; null = no preset exists. */
	next: { preset_id: number | null; rules: Rules } | null;
	/** The paused games list (D16), newest pause first. */
	paused: PausedGame[];
	abandon_after_hours: number;
};

export type PausedGame = GameView & {
	paused_at: number;
	reason: 'manual' | 'timeout' | 'new_game' | 'switched';
};

export type SideSettings = { name: string; color: string };

/** A row of the game history. */
export type GameSummary = {
	id: number;
	state: 'finished' | 'abandoned';
	home: number;
	away: number;
	winner: Side | null;
	voided: boolean;
	edits: number;
	preset_id: number | null;
	started_at: number;
	ended_at: number | null;
	roster: { home: Seat[]; away: Seat[] };
};

/** A line of a game's event log (spec 02 §11). */
export type GameEvent = {
	seq: number;
	type: string;
	side: Side | null;
	slot: number | null;
	player_id: number | null;
	coin_uid: string | null;
	via: string | null;
	target_seq: number | null;
	rule: string | null;
	rule_value: string | null;
	source: 'sensor' | 'table' | 'manual' | 'system';
	author_id: number | null;
	author: string | null;
	reason: string | null;
	at: number;
	play_time_ms: number | null;
	/** Taken back by a `reverted` event in force. */
	reverted: boolean;
	/** A correction after the game. */
	edit: boolean;
};

/** A game page: the game, its event log, and what the viewer may do. */
export type GameDetail = {
	game: GameView;
	events: GameEvent[];
	can_edit: boolean;
	can_void: boolean;
	can_revert: boolean;
};

export type EditKind = 'add_goal' | 'void_goal' | 'set_player' | 'remove_player' | 'swap_sides' | 'void_game' | 'revert';

export type EditRequest = {
	kind: EditKind;
	side?: Side;
	slot?: number;
	player_id?: number;
	target_seq?: number;
	play_time_ms?: number;
};

/** An edit in the feed of recent edits. */
export type EditFeedEntry = {
	game_id: number;
	seq: number;
	type: string;
	side: Side | null;
	slot: number | null;
	player_id: number | null;
	target_seq: number | null;
	author: string | null;
	at: number;
	reverted: boolean;
};

export type RatingKey = 'solo' | 'team';

/** A current Elo rating (D9). */
export type Rating = { player_id: number; key: RatingKey; rating: number; games: number };

/** Filters of GET /api/games (all optional; see the API). */
export type GameFlag = 'voided' | 'edited' | 'unassigned' | 'unknown' | 'shutout' | 'close';
export type GameQuery = {
	before?: number;
	limit?: number;
	player?: number | number[]; // all of them played (at most 4)
	relation?: '' | 'together' | 'against'; // of the first two players
	result?: '' | 'win' | 'loss' | 'draw'; // for the first player
	kind?: '' | 'solo' | 'team';
	status?: '' | 'finished' | 'abandoned';
	flags?: GameFlag[];
	from?: number; // Unix ms, started at or after
	to?: number; // Unix ms, started before
};

// --- stats (M3 task 8) ---

export type Result = 'win' | 'loss' | 'draw';

/** A win/loss record (rated games only). */
export type StatRecord = { games: number; wins: number; losses: number; draws: number; goals_for: number; goals_against: number };

/** A player's rating after a game. */
export type HistoryPoint = { game_id: number; at: number; rating: number };

/** A player's rating and record in one key. */
export type KeyStats = {
	key: RatingKey;
	rating: number;
	peak: number;
	start: number;
	record: StatRecord;
	history: HistoryPoint[];
	rank: number; // 0 = not ranked (yet)
	ranked: number; // players with a rank in this key
};

/** A player's record with or against another player. */
export type Relation = { player_id: number; record: StatRecord };

/** GET /api/players/:id/stats */
export type PlayerStats = {
	player_id: number;
	record: StatRecord;
	keys: KeyStats[];
	streak: number; // > 0 wins in a row, < 0 losses, 0 after a draw
	best_streak: number;
	form: Result[]; // newest first
	last_played: number | null;
	best_teammate: Relation | null;
	worst_enemy: Relation | null;
	favorite_victim: Relation | null;
	teammates: Relation[];
	opponents: Relation[];
	min_together: number;
	min_ranked: number;
};

export type LeaderRow = { rank: number; player_id: number; rating: number; record: StatRecord; form: Result[]; change: number };

/** GET /api/leaderboard?key= – ranked rows first (rank > 0), then the unranked ones. */
export type Leaderboard = { key: RatingKey; min_ranked: number; window_days: number; rows: LeaderRow[] };

export type Highlight = {
	kind: 'streak' | 'most_games' | 'climber' | 'biggest_win';
	player_ids: number[];
	value: number;
	game_id?: number;
	home?: number;
	away?: number;
};

/** GET /api/overview */
export type Overview = {
	window_days: number;
	games: number;
	goals: number;
	top: { key: RatingKey; rows: LeaderRow[] }[];
	highlights: Highlight[];
};

/** The rating change of one player in one game. */
export type RatingChange = { player_id: number; key: RatingKey; before: number; after: number };

/** The table settings (spec 02 §13, D17) and the current preset. */
export type TableSettings = {
	home: SideSettings;
	away: SideSettings;
	kickoff_requires: 'both_sides' | 'any_player';
	kickoff_delay_s: number;
	first_ball: 'random' | 'lower_rating' | 'loser_previous' | 'alternate' | 'home' | 'away';
	after_goal_ball: 'conceding' | 'scoring' | 'alternate';
	finished_hold_s: number;
	pause_after_minutes: number;
	sleep_after_minutes: number;
	on_wake: 'smart' | 'resume' | 'new_game';
	abandon_after_hours: number;
	corner_claim_timeout_s: number;
	current_preset_id: number;
};

export type CapView = {
	instance: string;
	capability: string;
	supported: boolean;
	can_wake: boolean;
	role: string;
	roles: string[] | null;
	status: string;
	status_msg: string;
	config?: Record<string, unknown>;
};

export type ModuleInfo = {
	uid: string;
	name: string;
	port: string;
	online: boolean;
	adopted: boolean;
	virtual: boolean;
	manifest: string;
	firmware: string;
	fw_version: string;
	hardware: string;
	connected_at: string;
	caps: CapView[] | null;
};

export type ConsoleLine = {
	uid: string;
	at: string;
	port: string;
	dir: string;
	kind: string;
	text: string;
	err?: string;
	warnings?: string[];
};

export type PortView = { name: string; vid: string; pid: string; product: string };

export type SimAcceptor = { uid: string; instance: string; role: string; cap: number; coins: string[] };

export type SimView = { modules: SimModule[]; acceptors: SimAcceptor[] };

export type Coin = {
	uid: string;
	player_id: number;
	label: string;
	active: boolean;
	created_at: number;
	last_seen_at: number | null;
};

export type SimModule = {
	uid: string;
	manifest: string;
	instances: { instance: string; capability: string; role: string; events: string[] }[];
};

export type AuditEntry = {
	at: number;
	account_id: number | null;
	account: string; // "" = console / system
	ip?: string; // only with log.ip
	action: string;
	target: string;
	details: string;
};

export type OnboardingStatus = {
	done: boolean;
	mine: boolean;
	busy: boolean;
	step: 'language' | 'admin' | 'kit' | 'done';
	language: string;
	languages: string[];
};

export type Player = {
	id: number;
	name: string;
	color: string;
	avatar?: string;
	guest: boolean;
	archived: boolean;
	has_account: boolean;
	created_at: number;
	/** End of the last rated game (player lists only). */
	last_played?: number;
};

export type Palette = { colors: string[]; suggested: string };

// --- tournaments (spec 09) ---------------------------------------------------------------------

export type TournamentState = 'setup' | 'running' | 'paused' | 'finished' | 'abandoned';
export type TeamMode = 'fixed' | 'dyp' | 'monster';
export type TournamentFormat = 'round_robin' | 'groups' | 'knockout' | 'double_knockout' | 'swiss' | 'monster_dyp';

export interface TournamentStage {
	format: TournamentFormat;
	best_of?: number;
	rounds?: number;
	double?: boolean;
	third_place?: boolean;
	groups?: number;
	advance?: number;
	fill_thirds?: boolean;
	seeding?: 'crossed' | 'reseed';
	rules?: TournamentRules | null;
}

export interface TournamentRules {
	win_score: number;
	win_by: number;
	max_score?: number;
	time_limit_s?: number;
}

export interface TournamentSettings {
	team_mode: TeamMode;
	stages: TournamentStage[];
	tie_breaks?: string[];
	seed?: number;
	manual_seeds?: boolean;
	/** The preset the stages' rules were copied from; absent = custom rules. */
	preset_id?: number;
}

export interface TournamentSetupInput {
	name: string;
	settings: TournamentSettings;
	participants: { player_id: number; team?: number }[];
}

export interface TournamentSummary {
	id: number;
	name: string;
	state: TournamentState;
	team_mode: TeamMode;
	formats: TournamentFormat[];
	owner_account_id: number | null;
	created_at: number;
	started_at: number | null;
	ended_at: number | null;
}

export interface TEntrant {
	team: number;
	players: number[];
	bye?: boolean;
	/** Monster DYP: the players' places (from 1). */
	slots?: number[];
	/** Dropped out (spec 09 §10). */
	dropped?: boolean;
}

export interface TPairing {
	key: string;
	stage: number;
	round: number;
	pos: number;
	bracket: string;
	state: 'waiting' | 'ready' | 'done' | 'bye' | 'skipped';
	a: TEntrant | null;
	b: TEntrant | null;
	wins_a: number;
	wins_b: number;
	goals_a: number;
	goals_b: number;
	winner: 'a' | 'b' | '';
	walkover: boolean;
	best_of: number;
	games: { game_id: number; a_side: 'home' | 'away'; winner: 'a' | 'b' | ''; goals_a: number; goals_b: number }[];
}

export interface TStanding {
	id: number;
	players: number[];
	seed: number;
	played: number;
	won: number;
	lost: number;
	points: number;
	per_game: number;
	games_won: number;
	games_lost: number;
	goals_for: number;
	goals_against: number;
	buchholz: number;
	dropped?: boolean;
}

export interface TStage {
	index: number;
	format: TournamentFormat;
	best_of: number;
	done: boolean;
	groups: { name: string; teams: TEntrant[]; standings: TStanding[] }[];
	pairings: TPairing[];
	standings: TStanding[];
	sat_out?: number[][];
}

export interface TEstimate {
	pairings: number;
	up_to: boolean;
	games: number;
	minutes: number;
	min_games_per_team: number;
}

export interface TournamentDetail extends TournamentSummary {
	settings: TournamentSettings;
	participants: { id: number; player_id: number; team: number | null; seed: number; dropped: boolean; replaced_by: number | null }[];
	teams: TEntrant[];
	stages: TStage[];
	placements: { place: number; team: number; players: number[] }[];
	ready: string[];
	suggested: string;
	staged: string;
	checked_in: number[];
	finished: boolean;
	estimate: TEstimate | null;
	events: { at: number; account_id: number | null; kind: string; details: Record<string, unknown> }[];
	can: { edit: boolean; run: boolean; play: boolean; manage: boolean; change: boolean };
	preview: boolean;
	/** Pairings played by other teams than the bracket has now, after an edit (spec 09 §11). */
	conflicts: string[];
}

export interface TournamentOption {
	name: string;
	stages: TournamentStage[];
	estimate: TEstimate;
}

export interface PlayerTournamentResult {
	id: number;
	name: string;
	state: TournamentState;
	ended_at: number | null;
	place: number;
	replaced?: boolean;
	dropped?: boolean;
}

// --- backups (spec 10 §2) ---
export type LocationKind = 'local' | 'usb' | 'folder';
export interface BackupLocation {
	id: string;
	name: string;
	kind: LocationKind;
	/** usb: the drive's name; folder: an absolute path; local: "". */
	path: string;
	enabled: boolean;
}
export interface BackupConfig {
	schedule: 'daily' | 'days' | 'manual';
	/** 0 = Sunday … 6 = Saturday */
	days?: number[];
	/** HH:MM, or "" for the automatic time */
	time: string;
	/** Retention of automatic backups: the newest `copies`, or with keep_days the ones younger than that. */
	copies: number;
	keep_days?: number;
	locations: BackupLocation[];
}
export interface BackupResult {
	at: string;
	file?: string;
	error?: string;
	last_ok?: string;
}
export type BackupKindName = 'auto' | 'manual' | 'pre-update' | 'pre-migration' | 'uploaded';
export interface BackupEntry {
	file: string;
	kicker: string;
	own: boolean;
	created: string;
	kind: BackupKindName;
	size: number;
	locations: string[];
}
export interface BackupManifest {
	format: number;
	name: string;
	server_id: string;
	version: string;
	schema: number;
	created: string;
	files: { path: string; size: number }[];
}
export interface BackupsView {
	config: BackupConfig;
	status: { locations: Record<string, BackupResult>; last_run?: string };
	backups: BackupEntry[];
	auto_time: string;
	next_run: string | null;
	drives: string[];
	local: string;
	pending: BackupManifest | null;
	problem: boolean;
}
export interface RestoreView {
	manifest: BackupManifest;
	restarting: boolean;
}
