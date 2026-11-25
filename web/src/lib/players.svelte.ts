// A cached list of players (incl. archived) for names and pictures on the scoreboard, the coin
// pages and the simulator. Reloaded when an unknown id shows up or after a change.
import { api, type Player } from '#lib/api/client.ts';

class PlayerCache {
	list = $state<Player[]>([]);
	#byId = $derived(new Map(this.list.map((p) => [p.id, p])));
	#loading: Promise<void> | null = null;
	#missing = new Set<number>();

	get(id: number | null | undefined): Player | undefined {
		if (id == null) return undefined;
		const p = this.#byId.get(id);
		if (!p && !this.#missing.has(id)) {
			this.#missing.add(id);
			this.reload();
		}
		return p;
	}

	/** Players that can be picked (not archived), guest first. */
	get active(): Player[] {
		return this.list.filter((p) => !p.archived);
	}

	reload(): Promise<void> {
		this.#loading ??= api
			.players(true)
			.then((l) => {
				this.list = l;
				this.#missing.clear();
			})
			.catch(() => {})
			.finally(() => (this.#loading = null));
		return this.#loading;
	}
}

export const players = new PlayerCache();
