<script lang="ts">
	// A game in the history: players, score, badges, link to the game page.
	import type { GameSummary, Seat, Side } from '#lib/api/client.ts';
	import { players } from '#lib/players.svelte.ts';
	import { i18n, t, tPlayer, tSide } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	let { game: g }: { game: GameSummary } = $props();

	const when = $derived(new Date(g.ended_at ?? g.started_at).toLocaleString(i18n.lang, { dateStyle: 'medium', timeStyle: 'short' }));
</script>

{#snippet side(seats: Seat[], s: Side)}
	<span class="flex min-w-0 items-center gap-1.5 {s === 'home' ? 'flex-row-reverse text-right' : ''}">
		<span class="flex shrink-0 -space-x-1.5 {s === 'home' ? 'flex-row-reverse space-x-reverse' : ''}">
			{#each seats as seat (seat.slot)}
				{@const p = players.get(seat.player_id)}
				{#if p}<span class="rounded-full ring-2 ring-white"><PlayerAvatar player={p} size={26} /></span>{:else}<span
						class="grid size-[26px] place-items-center rounded-full bg-stone-200 text-xs text-stone-500 ring-2 ring-white">?</span>{/if}
			{/each}
		</span>
		<span class="hidden min-w-0 truncate text-sm sm:inline {g.winner === s ? 'font-semibold' : 'text-stone-600'}">
			{seats.map((x) => { const p = players.get(x.player_id); return p ? tPlayer(p) : t('reg.unknown_guest'); }).join(' & ') || tSide(s)}
		</span>
	</span>
{/snippet}

<a href="/games/{g.id}" class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 rounded-xl px-3 py-2.5 hover:bg-stone-50 {g.voided ? 'opacity-60' : ''}"
	data-testid="game-row">
	{@render side(g.roster.home, 'home')}
	<span class="text-center">
		<span class="rounded-lg bg-stone-100 px-2 py-0.5 font-mono font-semibold tabular-nums {g.voided ? 'line-through' : ''}"
			><span class="inline-block w-[2ch] text-right">{g.home}</span>:<span class="inline-block w-[2ch]">{g.away}</span></span>
		<span class="mt-0.5 block text-xs text-stone-500">{when}</span>
		<span class="flex justify-center gap-1">
			{#if g.state === 'abandoned'}<span class="chip">{t('games.abandoned')}</span>{/if}
			{#if g.voided}<span class="chip bg-red-50 text-red-700">{t('games.voided')}</span>{/if}
			{#if g.edits && !g.voided}<span class="chip">{t('games.edited')}</span>{/if}
		</span>
	</span>
	{@render side(g.roster.away, 'away')}
</a>
