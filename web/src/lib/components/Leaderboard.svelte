<script lang="ts">
	// Leaderboard rows: rank, player, rating, change in the last days, record, form.
	import type { LeaderRow } from '#lib/api/client.ts';
	import { players } from '#lib/players.svelte.ts';
	import { t, tPlayer } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import FormDots from '#lib/components/FormDots.svelte';

	let { rows, compact = false, windowDays = 30 }: { rows: LeaderRow[]; compact?: boolean; windowDays?: number } = $props();

	const pct = (r: LeaderRow) => (r.record.games ? Math.round((100 * r.record.wins) / r.record.games) : 0);
	const change = (v: number) => (Math.round(v) > 0 ? `▲ ${Math.round(v)}` : Math.round(v) < 0 ? `▼ ${-Math.round(v)}` : '');
</script>

<ol class="divide-y divide-stone-100">
	{#each rows as r (r.player_id)}
		{@const p = players.get(r.player_id)}
		<li>
			<a href="/players/{r.player_id}" class="flex items-center gap-3 rounded-lg px-2 py-2 hover:bg-stone-50" data-testid="leader-row">
				<span class="w-6 shrink-0 text-right font-semibold tabular-nums {r.rank ? '' : 'text-stone-400'}">{r.rank || '–'}</span>
				{#if p}<PlayerAvatar player={p} size={compact ? 28 : 32} />{/if}
				<span class="min-w-0 flex-1">
					<span class="block truncate font-medium">{p ? tPlayer(p) : '…'}</span>
					{#if !compact}
						<span class="block text-xs text-stone-500 tabular-nums">
							{t('stats.wld', { w: r.record.wins, l: r.record.losses, d: r.record.draws })} · {pct(r)} %
						</span>
					{/if}
				</span>
				{#if !compact}<span class="hidden sm:block"><FormDots form={r.form} size="sm" /></span>{/if}
				<span class="w-12 text-right text-xs text-stone-500 tabular-nums" title={t('stats.change_hint', { n: windowDays })}>{change(r.change)}</span>
				<span class="w-12 text-right font-score text-lg font-bold tabular-nums">{Math.round(r.rating)}</span>
			</a>
		</li>
	{/each}
</ol>
