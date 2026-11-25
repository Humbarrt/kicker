<script lang="ts">
	// Below the scoreboard: the last days in numbers, highlights, the top of the leaderboards and
	// the latest games. Re-read whenever the stats change.
	import { api, type GameSummary, type Highlight, type Overview } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { t, tPlayer, type Key } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import Leaderboard from '#lib/components/Leaderboard.svelte';
	import GameRow from '#lib/components/GameRow.svelte';

	let ov = $state<Overview | null>(null);
	let games = $state<GameSummary[]>([]);

	$effect(() => {
		void live.statsRev;
		api.overview().then((o) => (ov = o)).catch(() => {});
		api.games({ limit: 5 }).then((g) => (games = g)).catch(() => {});
	});

	const names = (h: Highlight) => h.player_ids.map((id) => { const p = players.get(id); return p ? tPlayer(p) : '…'; }).join(' & ');
</script>

{#if ov}
	<section class="grid gap-6 lg:grid-cols-2" data-testid="overview">
		<div class="card space-y-4">
			<h2 class="h2">{t('overview.title', { n: ov.window_days })}</h2>
			<div class="grid grid-cols-2 gap-3">
				<div class="rounded-xl bg-stone-50 p-4">
					<div class="font-score text-3xl font-bold tabular-nums">{ov.games}</div>
					<div class="text-sm text-stone-500">{t('overview.games')}</div>
				</div>
				<div class="rounded-xl bg-stone-50 p-4">
					<div class="font-score text-3xl font-bold tabular-nums">{ov.goals}</div>
					<div class="text-sm text-stone-500">{t('overview.goals')}</div>
				</div>
			</div>
			{#if ov.highlights.length}
				<ul class="space-y-2">
					{#each ov.highlights as h (h.kind)}
						{@const first = players.get(h.player_ids[0])}
						<li class="flex items-center gap-3 rounded-xl border border-stone-100 px-3 py-2" data-testid="highlight">
							{#if first}<PlayerAvatar player={first} size={32} />{/if}
							<span class="min-w-0 flex-1 text-sm">
								<span class="block text-xs font-medium tracking-wide text-stone-500 uppercase">{t(('highlight.' + h.kind) as Key)}</span>
								{#if h.kind === 'biggest_win' && h.game_id}
									<a class="hover:underline" href="/games/{h.game_id}">{t('highlight.biggest_win.text', { who: names(h) || t('player.guest'), home: Math.max(h.home ?? 0, h.away ?? 0), away: Math.min(h.home ?? 0, h.away ?? 0) })}</a>
								{:else}
									<a class="hover:underline" href="/players/{h.player_ids[0]}">{t(('highlight.' + h.kind + '.text') as Key, { who: names(h), n: h.value })}</a>
								{/if}
							</span>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="muted text-sm">{t('overview.quiet')}</p>
			{/if}
		</div>

		<div class="card space-y-4">
			<div class="flex items-baseline justify-between">
				<h2 class="h2">{t('overview.top')}</h2>
				<a class="text-sm text-stone-500 hover:text-stone-900" href="/players">{t('overview.all_rankings')}</a>
			</div>
			{#each ov.top as top (top.key)}
				<div>
					<h3 class="mb-1 text-xs font-medium tracking-wide text-stone-500 uppercase">{t(top.key === 'solo' ? 'rating.solo' : 'rating.team')}</h3>
					<Leaderboard rows={top.rows} compact windowDays={ov.window_days} />
				</div>
			{:else}
				<p class="muted text-sm">{t('overview.no_ranking')}</p>
			{/each}
		</div>
	</section>

	{#if games.length}
		<section class="card p-2">
			<div class="flex items-baseline justify-between px-3 pt-2 pb-1">
				<h2 class="h2">{t('overview.recent')}</h2>
				<a class="text-sm text-stone-500 hover:text-stone-900" href="/games">{t('overview.all_games')}</a>
			</div>
			<div class="divide-y divide-stone-100">
				{#each games as g (g.id)}<GameRow game={g} />{/each}
			</div>
		</section>
	{/if}
{/if}
