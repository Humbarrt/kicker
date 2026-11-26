<script lang="ts">
	// A team in a tournament: its players' pictures and names. Byes and unknown sides are muted.
	import Check from '@lucide/svelte/icons/check';
	import type { TEntrant } from '#lib/api/client.ts';
	import { players } from '#lib/players.svelte.ts';
	import { t, tPlayer } from '#lib/i18n.svelte.ts';
	import { teamName } from '#lib/tournaments.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	let {
		entrant,
		winner = false,
		loser = false,
		here = [],
		size = 24,
		link = false
	}: { entrant: TEntrant | null; winner?: boolean; loser?: boolean; here?: number[]; size?: number; link?: boolean } = $props();

	const present = $derived(entrant?.players.some((id) => here.includes(id)) ?? false);
</script>

<span class="flex min-w-0 items-center gap-2 {loser ? 'text-stone-400' : ''} {winner ? 'font-semibold' : ''}">
	{#if entrant && !entrant.bye}
		<span class="flex shrink-0 -space-x-2">
			{#each entrant.players as id, i (i)}
				{@const p = players.get(id)}
				{#if p}<span class="rounded-full ring-2 ring-white {loser ? 'opacity-50' : ''}"><PlayerAvatar player={p} {size} /></span>{/if}
			{/each}
		</span>
		{#if link}
			<!-- Each player's name opens their profile. -->
			<span class="truncate">
				{#each entrant.players as id, i (i)}{#if i > 0}{' & '}{/if}<a href="/players/{id}" class="hover:underline">{players.get(id) ? tPlayer(players.get(id)!) : '…'}</a>{/each}
			</span>
		{:else}
			<span class="truncate {entrant.dropped ? 'line-through decoration-stone-400' : ''}">{teamName(entrant)}</span>
		{/if}
		{#if entrant.dropped}<span class="chip shrink-0 bg-stone-100 text-stone-500">{t('tourn.dropped')}</span>{/if}
		{#if present}<span class="chip bg-emerald-100 text-emerald-800" title={t('tourn.checked_in')}><Check class="size-3.5" aria-hidden="true" />{t('tourn.checked_in')}</span>{/if}
	{:else}
		<span class="truncate text-stone-400 italic">{teamName(entrant)}</span>
	{/if}
</span>
