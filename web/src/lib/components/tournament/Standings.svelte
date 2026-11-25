<script lang="ts">
	// A ranking table: round robin, a group, Swiss, Monster DYP (by player).
	import type { TStanding } from '#lib/api/client.ts';
	import { t } from '#lib/i18n.svelte.ts';
	import Team from './Team.svelte';

	let { rows, byPlayer = false, perGame = false, buchholz = false, qualify = 0 }:
		{ rows: TStanding[]; byPlayer?: boolean; perGame?: boolean; buchholz?: boolean; qualify?: number } = $props();
</script>

<div class="overflow-x-auto">
	<table class="w-full text-sm" data-testid="standings">
		<thead class="text-xs text-stone-500">
			<tr>
				<th class="w-6 py-1 text-right font-medium">#</th>
				<th class="px-2 py-1 text-left font-medium">{t(byPlayer ? 'tourn.col.player' : 'tourn.col.team')}</th>
				<th class="px-1 py-1 text-right font-medium">{t('tourn.col.played')}</th>
				<th class="px-1 py-1 text-right font-medium">{t('tourn.col.won')}</th>
				<th class="px-1 py-1 text-right font-medium">{t('tourn.col.lost')}</th>
				<th class="px-1 py-1 text-right font-medium">{t('tourn.col.diff')}</th>
				{#if buchholz}<th class="px-1 py-1 text-right font-medium">{t('tourn.col.buchholz')}</th>{/if}
				<th class="px-1 py-1 text-right font-medium">{t('tourn.col.points')}</th>
			</tr>
		</thead>
		<tbody class="divide-y divide-stone-100">
			{#each rows as r, i (r.id)}
				<tr class={qualify && i < qualify ? 'bg-emerald-50/60' : ''}>
					<td class="py-1.5 text-right font-semibold tabular-nums">{i + 1}</td>
					<td class="min-w-28 px-2 py-1.5"><Team entrant={{ team: byPlayer ? 0 : r.id, players: byPlayer ? [r.id] : r.players }} size={20} /></td>
					<td class="px-1 text-right tabular-nums">{r.played}</td>
					<td class="px-1 text-right tabular-nums">{r.won}</td>
					<td class="px-1 text-right tabular-nums">{r.lost}</td>
					<td class="px-1 text-right whitespace-nowrap tabular-nums">{r.goals_for}:{r.goals_against}</td>
					{#if buchholz}<td class="px-1 text-right tabular-nums">{r.buchholz}</td>{/if}
					<td class="px-1 text-right font-semibold tabular-nums">{perGame ? r.per_game.toFixed(2) : r.points}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
