<script lang="ts">
	// Player profile: record, ratings with their history, streaks, teammates and opponents, recent games.
	import { page } from '$app/state';
	import { api, type Coin, type GameSummary, type Player, type PlayerStats, type Relation } from '#lib/api/client.ts';
	import { players } from '#lib/players.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, tPlayer } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import PlayerEditor from '#lib/components/PlayerEditor.svelte';
	import RatingChart from '#lib/components/RatingChart.svelte';
	import FormDots from '#lib/components/FormDots.svelte';
	import GameRow from '#lib/components/GameRow.svelte';

	let player = $state<Player | null>(null);
	let error = $state('');
	let editing = $state(false);
	let coins = $state<Coin[] | null>(null);
	let stats = $state<PlayerStats | null>(null);
	let games = $state<GameSummary[]>([]);
	let chartKey = $state<'solo' | 'team' | null>(null);
	let allRelations = $state(false);

	$effect(() => {
		const id = Number(page.params.id);
		void live.statsRev; // re-read when ratings change
		if (!session.can('view.stats')) return;
		api.playerStats(id).then((s) => (stats = s)).catch(() => {});
		api.games({ player: id, limit: 5 }).then((g) => (games = g)).catch(() => {});
	});
	const chart = $derived(stats?.keys.find((k) => k.key === chartKey) ?? stats?.keys.find((k) => k.key === 'team') ?? stats?.keys[0]);
	const pct = (wins: number, games: number) => (games ? Math.round((100 * wins) / games) : 0);
	const name = (id: number) => { const p = players.get(id); return p ? tPlayer(p) : '…'; };

	$effect(() => {
		const id = Number(page.params.id);
		api.player(id).then((p) => (player = p)).catch((e) => (error = tErr(e)));
		coins = null;
		// coins are visible to coins.manage and to the player themselves
		if (session.can('coins.manage') || session.account?.player_id === id)
			api.coins(id).then((c) => (coins = c)).catch(() => {});
	});

	const mine = $derived(!!player && session.account?.player_id === player.id);
	const canEdit = $derived(!!player && !!session.account && (session.can('profile.any') || (mine && session.can('profile.own'))));
</script>

<main class="mx-auto max-w-4xl space-y-6 px-4 py-6">
	{#if error}<p class="error">{error}</p>{/if}
	{#if player}
		<section class="card flex flex-wrap items-center gap-6">
			<PlayerAvatar {player} size={128} />
			<div class="min-w-0 flex-1">
				<h1 class="text-3xl font-semibold tracking-tight">{tPlayer(player)}</h1>
				<p class="muted mt-1">
					{t('players.since', { date: new Date(player.created_at).toLocaleDateString(i18n.lang, { dateStyle: 'long' }) })}
				</p>
				<div class="mt-2 flex flex-wrap gap-2">
					{#each stats?.keys.filter((k) => k.rank) ?? [] as k (k.key)}
						<a href="/players" class="chip bg-stone-900 text-white" data-testid="rank-{k.key}"
							>{t('stats.rank_chip', { key: t(k.key === 'solo' ? 'rating.solo' : 'rating.team'), n: k.rank })}</a>
					{/each}
					{#if player.archived}<span class="chip">{t('players.archived')}</span>{/if}
					{#if player.has_account}<span class="chip">{t('players.has_account')}</span>{/if}
				</div>
			</div>
			{#if canEdit}<button class="btn" onclick={() => (editing = true)}>{t('players.edit')}</button>{/if}
		</section>

		{#if stats}
			{@const rec = stats.record}
			<section class="grid grid-cols-2 gap-3 sm:grid-cols-4" data-testid="record">
				{#each [
					{ v: rec.games, l: t('stats.games') },
					{ v: `${pct(rec.wins, rec.games)} %`, l: t('stats.win_rate') },
					{ v: `${rec.goals_for}:${rec.goals_against}`, l: t('stats.goals') },
					{ v: stats.best_streak, l: t('stats.best_streak') }
				] as tile (tile.l)}
					<div class="card p-4">
						<div class="font-score text-3xl font-bold tabular-nums">{tile.v}</div>
						<div class="text-sm text-stone-500">{tile.l}</div>
					</div>
				{/each}
			</section>

			{#if rec.games}
				<section class="card flex flex-wrap items-center gap-x-6 gap-y-2">
					<span class="text-sm">{t('stats.wld_long', { w: rec.wins, l: rec.losses, d: rec.draws })}</span>
					<span class="text-sm text-stone-600">
						{stats.streak > 0 ? t('stats.streak_win', { n: stats.streak }) : stats.streak < 0 ? t('stats.streak_loss', { n: -stats.streak }) : t('stats.streak_none')}
					</span>
					<span class="flex items-center gap-2 text-sm text-stone-600">{t('stats.form')} <FormDots form={stats.form} /></span>
				</section>
			{/if}

			<section class="card" data-testid="ratings">
				<div class="mb-3 flex flex-wrap items-baseline gap-3">
					<h2 class="h2 flex-1">{t('rating.title')}</h2>
					{#if stats.keys.length > 1}
						<div class="inline-flex rounded-lg bg-stone-100 p-0.5 text-sm" role="tablist">
							{#each stats.keys as k (k.key)}
								<button role="tab" aria-selected={chart?.key === k.key} onclick={() => (chartKey = k.key)}
									class="rounded-md px-3 py-1 {chart?.key === k.key ? 'bg-white font-medium shadow-sm' : 'text-stone-600'}"
									>{t(k.key === 'solo' ? 'rating.solo' : 'rating.team')}</button>
							{/each}
						</div>
					{/if}
				</div>
				{#if stats.keys.length === 0}
					<p class="muted">{t('rating.none')}</p>
				{:else}
					<div class="mb-4 grid gap-3 sm:grid-cols-2">
						{#each stats.keys as k (k.key)}
							<div class="rounded-xl bg-stone-50 p-4">
								<div class="flex items-baseline justify-between gap-2 text-sm text-stone-500">
									<span>{t(k.key === 'solo' ? 'rating.solo' : 'rating.team')}</span>
									<span class={k.rank ? 'font-medium text-stone-900' : ''}>
										{k.rank ? t('stats.rank_of', { n: k.rank, of: k.ranked }) : k.record.games < stats.min_ranked ? t('stats.unranked', { n: stats.min_ranked - k.record.games }) : t('stats.no_rank')}
									</span>
								</div>
								<div class="font-score text-4xl font-bold tabular-nums">{Math.round(k.rating)}</div>
								<div class="text-sm text-stone-500">
									{t('rating.games', { n: k.record.games })} · {t('stats.wld', { w: k.record.wins, l: k.record.losses, d: k.record.draws })} · {t('rating.peak', { n: Math.round(k.peak) })}
								</div>
							</div>
						{/each}
					</div>
					{#if chart}
						<RatingChart history={chart.history} start={chart.start}
							label={t('rating.chart', { key: t(chart.key === 'solo' ? 'rating.solo' : 'rating.team') })} />
					{/if}
				{/if}
				<p class="muted mt-3 text-xs">{t('rating.hint')}</p>
			</section>

			{#snippet relation(title: string, hint: string, r: Relation | null, value: (r: Relation) => string)}
				<div class="card p-4">
					<div class="text-xs font-medium tracking-wide text-stone-500 uppercase">{title}</div>
					{#if r}
						{@const p = players.get(r.player_id)}
						<a href="/players/{r.player_id}" class="mt-2 flex items-center gap-3 hover:underline">
							{#if p}<PlayerAvatar player={p} size={40} />{/if}
							<span class="min-w-0">
								<span class="block truncate font-medium">{name(r.player_id)}</span>
								<span class="block text-sm text-stone-500">{value(r)}</span>
							</span>
						</a>
					{:else}
						<p class="muted mt-2 text-sm">{hint}</p>
					{/if}
				</div>
			{/snippet}
			<section class="grid gap-3 sm:grid-cols-3" data-testid="relations">
				{@render relation(t('stats.best_teammate'), t('stats.not_enough', { n: stats.min_together }), stats.best_teammate,
					(r) => t('stats.together', { w: r.record.wins, n: r.record.games }))}
				{@render relation(t('stats.worst_enemy'), t('stats.not_enough', { n: stats.min_together }), stats.worst_enemy,
					(r) => t('stats.beaten_by', { l: r.record.losses, n: r.record.games }))}
				{@render relation(t('stats.favorite_victim'), t('stats.not_enough', { n: stats.min_together }), stats.favorite_victim,
					(r) => t('stats.beat', { w: r.record.wins, n: r.record.games }))}
			</section>

			{#if stats.teammates.length || stats.opponents.length}
				<section class="card grid gap-6 sm:grid-cols-2">
					{#each [{ title: t('stats.with'), list: stats.teammates }, { title: t('stats.against'), list: stats.opponents }] as group (group.title)}
						<div>
							<h3 class="mb-2 text-xs font-medium tracking-wide text-stone-500 uppercase">{group.title}</h3>
							<ul class="divide-y divide-stone-100 text-sm">
								{#each allRelations ? group.list : group.list.slice(0, 5) as r (r.player_id)}
									{@const p = players.get(r.player_id)}
									<li class="flex items-center gap-2 py-1.5">
										{#if p}<PlayerAvatar player={p} size={24} />{/if}
										<a class="min-w-0 flex-1 truncate hover:underline" href="/players/{r.player_id}">{name(r.player_id)}</a>
										<span class="text-stone-500 tabular-nums">{t('stats.wld', { w: r.record.wins, l: r.record.losses, d: r.record.draws })}</span>
										<span class="w-12 text-right font-medium tabular-nums">{pct(r.record.wins, r.record.games)} %</span>
									</li>
								{/each}
							</ul>
						</div>
					{/each}
					{#if !allRelations && (stats.teammates.length > 5 || stats.opponents.length > 5)}
						<button class="btn btn-sm justify-self-start" onclick={() => (allRelations = true)}>{t('stats.show_all')}</button>
					{/if}
				</section>
			{/if}

			{#if games.length}
				<section class="card p-2">
					<div class="flex items-baseline justify-between px-3 pt-2 pb-1">
						<h2 class="h2">{t('overview.recent')}</h2>
						<a class="text-sm text-stone-500 hover:text-stone-900" href="/games?player={player.id}">{t('overview.all_games')}</a>
					</div>
					<div class="divide-y divide-stone-100">
						{#each games as g (g.id)}<GameRow game={g} />{/each}
					</div>
				</section>
			{/if}
		{/if}

		{#if coins && coins.length}
			<section class="card">
				<h2 class="h2 mb-3">{t('coins.yours')}</h2>
				<ul class="flex flex-wrap gap-2">
					{#each coins as c (c.uid)}
						<li class="chip font-mono {c.active ? '' : 'line-through'}">{c.uid}{c.label ? ' · ' + c.label : ''}</li>
					{/each}
				</ul>
			</section>
		{/if}
	{/if}
</main>

<PlayerEditor bind:open={editing} {player} onsaved={(p) => (p ? (player = p) : history.back())} />
