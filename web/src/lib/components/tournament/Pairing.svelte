<script lang="ts">
	// One pairing as a row: both teams, the score, a label, and "Play" for ready pairings.
	import type { TPairing } from '#lib/api/client.ts';
	import { t } from '#lib/i18n.svelte.ts';
	import { pairingScore } from '#lib/tournaments.ts';
	import Team from './Team.svelte';

	let {
		pairing,
		label = '',
		here = [],
		onplay,
		busy = false,
		highlight = ''
	}: { pairing: TPairing; label?: string; here?: number[]; onplay?: () => void; busy?: boolean; highlight?: string } = $props();

	const done = $derived(pairing.state === 'done' || pairing.state === 'bye');
	// A finished pairing of one game opens that game; best-of pairings list their games as links.
	const only = $derived(done && pairing.games.length === 1 ? pairing.games[0] : null);
</script>

<svelte:element this={only ? 'a' : 'div'} href={only ? `/games/${only.game_id}` : undefined}
	class="flex items-center gap-3 rounded-xl border px-3 py-2 {highlight || 'border-stone-200 bg-white'} {only ? 'hover:bg-stone-50' : ''}" data-testid="pairing" data-key={pairing.key}>
	<div class="min-w-0 flex-1 space-y-1">
		{#if label}<p class="text-xs text-stone-500">{label}</p>{/if}
		<Team entrant={pairing.a} {here} winner={done && pairing.winner === 'a'} loser={done && pairing.winner === 'b'} />
		<Team entrant={pairing.b} {here} winner={done && pairing.winner === 'b'} loser={done && pairing.winner === 'a'} />
		{#if !only && pairing.games.length}
			<p class="flex flex-wrap gap-1 pt-0.5">
				{#each pairing.games as g (g.game_id)}
					<a class="chip tabular-nums hover:bg-stone-200" href="/games/{g.game_id}" data-testid="pairing-game">{g.goals_a}:{g.goals_b}</a>
				{/each}
			</p>
		{/if}
	</div>
	{#if pairing.best_of > 1 && pairing.state !== 'done'}
		<span class="chip">{t('tourn.best_of', { n: pairing.best_of })}{pairing.wins_a + pairing.wins_b ? ` · ${pairing.wins_a}:${pairing.wins_b}` : ''}</span>
	{/if}
	{#if done}<span class="font-score text-lg font-semibold tabular-nums">{pairingScore(pairing)}</span>{/if}
	{#if onplay}<button class="btn btn-sm btn-primary" disabled={busy} onclick={onplay} data-testid="play">{t('tourn.play')}</button>{/if}
</svelte:element>
