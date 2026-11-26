// The SSE stream (GET /api/events): the live table for everyone, module changes and the module
// console for admins. EventSource reconnects by itself; after a login or logout call reconnect()
// so the server picks the topics for the new session.
import type { ConsoleLine, TableView } from '#lib/api/client.ts';
import { i18n } from '#lib/i18n.svelte.ts';
import { session } from '#lib/session.svelte.ts';

type ConsoleListener = (l: ConsoleLine) => void;

class Live {
	table = $state<TableView | null>(null);
	/** performance.now() when `table` arrived, to run the game clock locally. */
	tableAt = $state(0);
	connected = $state(false);
	/** Increases on every "modules" event: pages re-read /api/modules. */
	modulesRev = $state(0);
	/** Counts "stats" messages: ratings and stats changed, re-read them. */
	statsRev = $state(0);
	/** Counts "tournament" messages: a tournament changed (state, results, the pairing on the table). */
	tournamentRev = $state(0);
	backupRev = $state(0);
	updateRev = $state(0);
	firmwareRev = $state(0);

	#es: EventSource | null = null;
	#console = new Set<ConsoleListener>();

	connect() {
		if (this.#es) return;
		const es = new EventSource('/api/events');
		this.#es = es;
		es.onopen = () => (this.connected = true);
		es.onerror = () => {
			this.connected = false;
			// 401/403/503: the browser gives up; try again later (e.g. after setup or login).
			if (es.readyState === EventSource.CLOSED) {
				this.#es = null;
				setTimeout(() => this.connect(), 5000);
			}
		};
		es.addEventListener('table', (e) => this.setTable(JSON.parse((e as MessageEvent).data)));
		es.addEventListener('modules', () => this.modulesRev++);
		es.addEventListener('stats', () => this.statsRev++);
		es.addEventListener('tournament', () => this.tournamentRev++);
		es.addEventListener('firmware', () => {
			this.firmwareRev++;
			void session.refresh(); // admins: the firmware badge
		});
		es.addEventListener('update', () => {
			this.updateRev++;
			void session.refresh(); // admins: the update badge
		});
		es.addEventListener('backup', () => {
			this.backupRev++;
			void session.refresh(); // admins: the backup badge
		});
		es.addEventListener('accounts', () => session.refresh()); // admins: sign-ups (badge)
		es.addEventListener('console', (e) => {
			const line = JSON.parse((e as MessageEvent).data) as ConsoleLine;
			for (const l of this.#console) l(line);
		});
	}

	reconnect() {
		this.#es?.close();
		this.#es = null;
		this.connect();
	}

	/** setTable takes a newer view (from the stream or a command's answer). */
	setTable(v: TableView) {
		if (this.table && v.rev < this.table.rev && v.server_time <= this.table.server_time) return;
		this.table = v;
		this.tableAt = performance.now();
		if (v.sides) i18n.sides = { home: v.sides.home.name, away: v.sides.away.name };
	}

	onConsole(fn: ConsoleListener): () => void {
		this.#console.add(fn);
		return () => this.#console.delete(fn);
	}
}

export const live = new Live();
