<script lang="ts">
	// A knockout bracket: one column per round, the pairings in bracket order. Double knockouts
	// show the winners and the losers bracket, then the grand final (spec 09 §6.2, §6.3).
	import type { TStage, TPairing } from '#lib/api/client.ts';
	import { t, type Key } from '#lib/i18n.svelte.ts';
	import { roundLabel, pairingScore } from '#lib/tournaments.ts';
	import Team from './Team.svelte';

	let { stage, staged = '', here = [], conflicts = [] }: { stage: TStage; staged?: string; here?: number[]; conflicts?: string[] } = $props();

	function columns(bracket: string): { round: number; pairings: TPairing[] }[] {
		const ps = stage.pairings.filter((p) => p.bracket === bracket);
		const rounds = [...new Set(ps.map((p) => p.round))].sort((a, b) => a - b);
		return rounds.map((r) => ({ round: r, pairings: ps.filter((p) => p.round === r).sort((a, b) => a.pos - b.pos) }));
	}
	const brackets = $derived(
		stage.format === 'double_knockout'
			? [
					{ name: 'W', title: t('tourn.bracket.W') },
					{ name: 'L', title: t('tourn.bracket.L') }
				]
			: [{ name: 'W', title: '' }]
	);
	const finals = $derived(stage.pairings.filter((p) => (p.bracket === 'GF' || p.bracket === '3rd') && p.state !== 'skipped'));
</script>

{#snippet match(p: TPairing)}
	{@const done = p.state === 'done' || p.state === 'bye'}
	{@const only = done && p.games.length === 1 ? p.games[0] : null}
	<svelte:element this={only ? 'a' : 'div'} href={only ? `/games/${only.game_id}` : undefined}
		class="block w-56 rounded-xl border bg-white px-3 py-2 text-sm shadow-xs {only ? 'hover:bg-stone-50' : ''} {p.key === staged ? 'border-emerald-500 ring-2 ring-emerald-200' : conflicts.includes(p.key) ? 'border-amber-400 ring-2 ring-amber-200' : 'border-stone-200'} {p.state === 'bye' ? 'opacity-60' : ''}"
		data-testid="bracket-match" data-key={p.key}>
		<div class="flex items-center gap-2"><span class="min-w-0 flex-1"><Team entrant={p.a} {here} size={20} winner={done && p.winner === 'a'} loser={done && p.winner === 'b'} /></span>
			{#if p.state === 'done'}<span class="tabular-nums {p.winner === 'a' ? 'font-semibold' : 'text-stone-400'}">{p.best_of > 1 ? p.wins_a : (p.games.at(-1)?.goals_a ?? '')}</span>{/if}</div>
		<div class="mt-1 flex items-center gap-2"><span class="min-w-0 flex-1"><Team entrant={p.b} {here} size={20} winner={done && p.winner === 'b'} loser={done && p.winner === 'a'} /></span>
			{#if p.state === 'done'}<span class="tabular-nums {p.winner === 'b' ? 'font-semibold' : 'text-stone-400'}">{p.best_of > 1 ? p.wins_b : (p.games.at(-1)?.goals_b ?? '')}</span>{/if}</div>
		{#if p.walkover}<p class="mt-1 text-xs text-stone-500">{pairingScore(p)}</p>{/if}
		{#if !only && p.games.length}
			<p class="mt-1 flex flex-wrap gap-1">
				{#each p.games as g (g.game_id)}
					<a class="chip tabular-nums hover:bg-stone-200" href="/games/{g.game_id}" data-testid="pairing-game">{g.goals_a}:{g.goals_b}</a>
				{/each}
			</p>
		{/if}
	</svelte:element>
{/snippet}

<div class="space-y-6">
	{#each brackets as b (b.name)}
		{@const cols = columns(b.name)}
		{#if cols.length}
			<div>
				{#if b.title}<h3 class="mb-2 text-sm font-semibold text-stone-600">{b.title}</h3>{/if}
				<div class="flex gap-4 overflow-x-auto pb-2">
					{#each cols as col (col.round)}
						<div class="flex shrink-0 flex-col">
							<p class="mb-2 text-xs font-medium text-stone-500">{roundLabel(stage, b.name, col.round)}</p>
							<div class="flex flex-1 flex-col justify-around gap-3">
								{#each col.pairings as p (p.key)}{@render match(p)}{/each}
							</div>
						</div>
					{/each}
				</div>
			</div>
		{/if}
	{/each}
	{#if finals.length}
		<div class="flex flex-wrap gap-4">
			{#each finals as p (p.key)}
				<div>
					<p class="mb-2 text-xs font-medium text-stone-500">{roundLabel(stage, p.bracket, p.round)}</p>
					{@render match(p)}
				</div>
			{/each}
		</div>
	{/if}
</div>
