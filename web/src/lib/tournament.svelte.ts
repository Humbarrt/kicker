// The running tournament (spec 09 §8) for the scoreboard: loaded when the page shows and again on
// every "tournament" message. Also the most recently paused one, for "Continue tournament?".
import { api, type TournamentDetail, type TournamentSummary } from '#lib/api/client.ts';
import { live } from '#lib/live.svelte.ts';
import { session } from '#lib/session.svelte.ts';

class ActiveTournament {
	running = $state<TournamentDetail | null>(null);
	paused = $state<TournamentSummary | null>(null);
	#loadedRev = -1;

	/** Call from an $effect: reloads when a tournament changed. */
	track() {
		const rev = live.tournamentRev;
		if (!session.can('view.tournaments') || rev === this.#loadedRev) return;
		this.#loadedRev = rev;
		this.load();
	}

	async load() {
		try {
			const list = await api.tournaments();
			const run = list.find((x) => x.state === 'running');
			this.paused = list.find((x) => x.state === 'paused') ?? null;
			this.running = run ? await api.tournament(run.id) : null;
		} catch {
			/* the scoreboard works without */
		}
	}
}

export const activeTournament = new ActiveTournament();
