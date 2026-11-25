<script lang="ts">
	// The overview page. Top: the live scoreboard (M1 done-when 2) with the controls the user's
	// group may use. Below it, later: past games, stats, recent tournaments (M3, M6).
	import { api, type Side, type TableView } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr, tSide, type Key } from '#lib/i18n.svelte.ts';
	import { clock } from '#lib/format.ts';
	import NewGameDialog from '#lib/components/NewGameDialog.svelte';
	import GoalTimeline from '#lib/components/GoalTimeline.svelte';
	import SimPanel from '#lib/components/SimPanel.svelte';
	import SideRoster from '#lib/components/SideRoster.svelte';
	import Overview from '#lib/components/Overview.svelte';
	import PausedGames from '#lib/components/PausedGames.svelte';
	import TournamentPanel from '#lib/components/tournament/TournamentPanel.svelte';
	import { activeTournament } from '#lib/tournament.svelte.ts';

	const view = $derived(live.table);
	const pregame = $derived(view?.phase === 'pregame');
	// During the countdown the board shows the next game, not the last one.
	const game = $derived(pregame ? null : (view?.game ?? null));
	const playing = $derived(view?.phase === 'playing' && game?.status === 'active');
	// Paused by hand: the game stays on the table (§7.4).
	const pausedOnTable = $derived(view?.phase === 'playing' && !!game?.current && game.status === 'paused');
	const sleeping = $derived(view?.phase === 'sleeping');

	// Rating changes of the finished game shown (D9), re-read when the ratings change.
	const finishedId = $derived(view?.game?.status === 'finished' ? view.game.id : 0);
	let ratingChanges = $state<Record<number, number>>({});
	$effect(() => {
		const id = finishedId;
		void live.statsRev;
		if (!id || !session.can('view.stats')) {
			ratingChanges = {};
			return;
		}
		api.gameRatings(id)
			.then((rs) => (ratingChanges = Object.fromEntries(rs.map((r) => [r.player_id, r.after - r.before]))))
			.catch(() => {});
	});
	// +1 also continues a game paused by hand, like a goal at the table.
	const canScore = $derived(!!game?.current && (playing || pausedOnTable) && session.can('table.score'));

	// The clock runs locally between updates.
	let now = $state(performance.now());
	$effect(() => {
		const id = setInterval(() => (now = performance.now()), 250);
		return () => clearInterval(id);
	});
	const playMs = $derived(
		game ? game.play_time_ms + (game.clock_running ? Math.max(0, now - live.tableAt) : 0) : 0
	);
	const limitMs = $derived((game?.rules.time_limit_s ?? 0) * 1000);
	// Timed games count down; after the time is up (golden goal) the overtime counts up.
	const overtime = $derived(!!limitMs && playMs >= limitMs);
	const clockText = $derived(!limitMs ? clock(playMs) : overtime ? '+' + clock(playMs - limitMs) : clock(limitMs - playMs));

	// Kickoff countdown, on the server's clock.
	const kickoffIn = $derived(
		view?.kickoff_at ? Math.max(0, Math.ceil((view.kickoff_at - view.server_time - (now - live.tableAt)) / 1000)) : null
	);

	let dialogOpen = $state(false);
	let error = $state('');
	let busy = $state(false);
	async function run(fn: () => Promise<TableView>) {
		busy = true;
		error = '';
		try {
			live.setTable(await fn());
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}

	const rulesLine = $derived.by(() => {
		const r = game?.rules ?? view?.next?.rules;
		if (!r) return '';
		const target = game ? game.target : Math.min(Math.max(r.win_score, r.win_by), r.max_score ?? Infinity);
		const parts = [t('board.first_to', { n: target })];
		if (r.win_by > 1) parts.push(t('board.win_by', { n: r.win_by }));
		if (r.max_score) parts.push(t('board.max', { n: r.max_score }));
		if (r.time_limit_s) parts.push(`${Math.round(r.time_limit_s / 60)} ${t('board.minutes')}`);
		return parts.join(' · ');
	});
</script>

<main class="mx-auto max-w-6xl space-y-6 px-4 py-6">
	{#if !live.connected && view}
		<p class="notice">{t('board.live_off')}</p>
	{/if}

	{#if !view && !session.can('view.live')}
		<p class="card text-center">{t('board.hidden')} <a class="underline" href="/login">{t('nav.login')}</a></p>
	{:else}
		<TournamentPanel />
		<section class="card overflow-hidden p-0">
			<div class="flex flex-wrap items-center justify-between gap-2 border-b border-stone-100 px-5 py-3 text-sm">
				<span class="chip {playing ? 'bg-emerald-100 text-emerald-800' : pausedOnTable ? 'bg-amber-100 text-amber-800' : ''}" data-testid="phase">
					{#if playing}<span class="size-2 animate-pulse rounded-full bg-emerald-600"></span>{/if}
					{pausedOnTable ? t('board.paused') : view ? t(('phase.' + view.phase) as Key) : t('app.loading')}
				</span>
				{#if game}
					<span class="text-stone-500">
						{game.current ? rulesLine : t('board.last_game')}
						{#if game.status === 'finished' && session.can('view.stats')}
							· <a class="underline underline-offset-2 hover:text-stone-900" href="/games/{game.id}">{t('games.details')}</a>
						{/if}
					</span>
				{:else if pregame}
					<span class="text-stone-500">{t('board.next_game')} · {rulesLine}</span>
				{/if}
			</div>

			{#if sleeping}
				<div class="flex flex-wrap items-center justify-between gap-3 border-b border-stone-100 bg-stone-50 px-5 py-3 text-sm text-stone-600">
					<span>{t('board.sleeping')}</span>
					{#if session.can('table.control')}
						<button class="btn btn-sm" disabled={busy} onclick={() => run(api.wake)}>{t('board.wake')}</button>
					{/if}
				</div>
			{/if}
			{#if pausedOnTable}
				<div class="border-b border-amber-100 bg-amber-50 px-5 py-2 text-center text-sm text-amber-900">{t('board.paused_hint')}</div>
			{/if}
			{#if game}
				<div class="grid grid-cols-[1fr_auto_1fr] items-center gap-2 px-4 py-8 sm:px-10 {sleeping ? 'opacity-60' : ''}">
					{#each ['home', 'away'] as const as side, i (side)}
						{@const score = side === 'home' ? game.home : game.away}
						{@const won = game.winner === side}
						<div class="text-center {i === 1 ? 'order-3' : ''}">
							<div class="text-sm font-semibold tracking-widest uppercase {side === 'home' ? 'text-home' : 'text-away'}">
								{tSide(side)}
							</div>
							<div class="font-score text-[clamp(5rem,18vw,11rem)] leading-none font-bold tabular-nums {won ? (side === 'home' ? 'text-home' : 'text-away') : ''}"
								data-testid="score-{side}">
								{score}
							</div>
							{#if canScore}
								<div class="mt-3 flex justify-center gap-2">
									<button class="btn" disabled={busy} onclick={() => run(() => api.score(side as Side, -1))} aria-label="{tSide(side)} −1">−1</button>
									<button class="btn" disabled={busy} onclick={() => run(() => api.score(side as Side, 1))} aria-label="{tSide(side)} +1">+1</button>
								</div>
							{/if}
							{#if view}<SideRoster {view} side={side as Side} changes={ratingChanges} />{/if}
						</div>
						{#if i === 0}
							<div class="order-2 text-center">
								{#if overtime && game.status === 'active'}
									<div class="text-xs font-semibold tracking-wider text-amber-700 uppercase">{t('board.overtime')}</div>
								{/if}
								<div class="font-score text-3xl tabular-nums {overtime && game.status === 'active' ? 'text-amber-700' : 'text-stone-400'}">{clockText}</div>
								{#if game.golden_goal && game.status === 'active'}
									<div class="mt-2 chip bg-amber-100 text-amber-800">{t('board.golden_goal')}</div>
								{/if}
								{#if game.current && playing && game.first_ball && !game.goals.length}
									<div class="mt-2 chip">{t('board.first_ball', { side: tSide(game.first_ball) })}</div>
								{/if}
							</div>
						{/if}
					{/each}
				</div>

				{#if game.status === 'finished'}
					<div class="border-t border-stone-100 bg-stone-50 px-5 py-3 text-center font-medium">
						{game.winner ? t('board.winner', { side: tSide(game.winner) }) : t('board.draw')}
						{#if game.end_reason}<span class="muted">· {t(('board.reason.' + game.end_reason) as Key)}</span>{/if}
					</div>
				{/if}
			{:else if view}
				<!-- No game yet, or the next game: who is registered, and the kickoff countdown. -->
				<div class="grid grid-cols-[1fr_auto_1fr] items-start gap-2 px-4 py-10 sm:px-10">
					{#each ['home', 'away'] as const as side, i (side)}
						<div class="text-center {i === 1 ? 'order-3' : ''}">
							<div class="text-lg font-semibold tracking-widest uppercase {side === 'home' ? 'text-home' : 'text-away'}">{tSide(side)}</div>
							<SideRoster {view} {side} changes={ratingChanges} />
						</div>
						{#if i === 0}
							<div class="order-2 min-w-32 text-center" data-testid="kickoff">
								{#if pregame && kickoffIn != null}
									<div class="text-xs font-semibold tracking-wider text-emerald-700 uppercase">{t('board.kickoff_in')}</div>
									<div class="font-score text-7xl leading-none font-bold text-emerald-700 tabular-nums">{kickoffIn}</div>
								{:else}
									<div class="font-medium text-stone-500">{t('board.waiting')}</div>
									<div class="mt-1 text-sm text-stone-400">{t('board.waiting_hint')}</div>
								{/if}
							</div>
						{/if}
					{/each}
				</div>
			{/if}

			{#if canScore || session.can('table.control')}
				<div class="flex flex-wrap items-center justify-end gap-2 border-t border-stone-100 px-5 py-3">
					{#if canScore}
						<button class="btn" disabled={busy || !game?.goals.some((g) => !g.voided)} onclick={() => run(api.undo)}>
							{t('board.undo')}
						</button>
					{/if}
					{#if session.can('table.control') && playing && game?.current}
						<button class="btn" disabled={busy} onclick={() => run(api.pause)}>{t('board.pause')}</button>
					{/if}
					{#if session.can('table.control') && pausedOnTable}
						<button class="btn btn-primary" disabled={busy} onclick={() => run(api.resume)}>{t('board.resume')}</button>
					{/if}
					{#if session.can('table.control') && !activeTournament.running}
						{#if pregame}
							<button class="btn" onclick={() => (dialogOpen = true)}>{t('board.start')}…</button>
							<button class="btn btn-primary" disabled={busy} onclick={() => run(() => api.start())}>{t('board.start_now')}</button>
						{:else}
							<button class="btn btn-primary" onclick={() => (dialogOpen = true)}>
								{playing || pausedOnTable ? t('board.start_new') : t('board.start')}
							</button>
						{/if}
					{/if}
				</div>
			{/if}
		</section>

		{#if error}<p class="error">{error}</p>{/if}

		{#if game}
			<section class="card">
				<h2 class="h2 mb-4">{t('board.goals')}</h2>
				<GoalTimeline goals={game.goals} />
			</section>
		{/if}

		{#if view}
			<PausedGames paused={view.paused ?? []} abandonAfterHours={view.abandon_after_hours} />
		{/if}

		{#if session.can('view.stats')}
			<Overview />
		{/if}

		{#if session.info?.sim}
			<SimPanel />
		{/if}
	{/if}
</main>

<NewGameDialog bind:open={dialogOpen} running={playing || pausedOnTable} />
