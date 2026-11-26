<script lang="ts">
	// A game: result, players, rating changes, the event log – and corrections of finished games
	// (D2): each one is a new event; nothing is deleted.
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';
	import ArrowLeftRight from '@lucide/svelte/icons/arrow-left-right';
	import { page } from '$app/state';
	import { api, type EditRequest, type GameDetail, type RatingChange, type Side } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, tPlayer, tSide, type Key } from '#lib/i18n.svelte.ts';
	import { clock } from '#lib/format.ts';
	import { eventText, playerName, seatAt } from '#lib/gamelog.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	const id = $derived(Number(page.params.id));
	let d = $state<GameDetail | null>(null);
	let ratings = $state<RatingChange[]>([]);
	let error = $state('');
	let busy = $state(false);

	$effect(() => {
		const gid = id;
		void live.statsRev; // edited elsewhere, or ratings recalculated
		api.game(gid).then((x) => (d = x)).catch((e) => (error = tErr(e)));
		if (session.can('view.stats')) api.gameRatings(gid).then((r) => (ratings = r)).catch(() => {});
	});

	const g = $derived(d?.game);
	const finished = $derived(g?.status === 'finished');
	const canEdit = $derived(!!d?.can_edit && finished);
	const change = $derived(Object.fromEntries(ratings.map((r) => [r.player_id, r.after - r.before])));
	const voidedGoals = $derived(new Set(g?.goals.filter((x) => x.voided).map((x) => x.seq)));
	const when = $derived(g ? new Date(g.started_at).toLocaleString(i18n.lang, { dateStyle: 'full', timeStyle: 'short' }) : '');

	async function edit(req: EditRequest, confirmText?: string) {
		if (confirmText && !confirm(confirmText)) return;
		busy = true;
		error = '';
		try {
			d = await api.editGame(id, req);
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}

	function setSlot(side: Side, slot: number, value: string) {
		if (value === '') edit({ kind: 'remove_player', side, slot });
		else edit({ kind: 'set_player', side, slot, player_id: Number(value) });
	}
</script>

<main class="mx-auto max-w-4xl space-y-6 px-4 py-6">
	<a href="/games" class="inline-flex items-center gap-1 text-sm text-stone-500 hover:text-stone-900"><ArrowLeft class="size-4" aria-hidden="true" />{t('games.title')}</a>
	{#if error}<p class="error">{error}</p>{/if}

	{#if d && g}
		<section class="card space-y-5">
			<div class="flex flex-wrap items-baseline justify-between gap-2">
				<h1 class="h1">{t('game.title', { id: g.id })}</h1>
				<span class="text-sm text-stone-500">{when} · {t('game.duration', { time: clock(g.play_time_ms) })}</span>
			</div>
			{#if g.voided}<p class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-800">{t('game.voided_banner')}</p>{/if}

			<div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-start gap-4">
				{#each ['home', 'away'] as const as side, i (side)}
					<div class="{i === 1 ? 'order-3' : 'text-right'} min-w-0">
						<div class="text-sm font-semibold tracking-widest uppercase {side === 'home' ? 'text-home' : 'text-away'}">{tSide(side)}</div>
						<ul class="mt-2 space-y-1.5" data-testid="seats-{side}">
							{#each [1, 2] as slot (slot)}
								{@const seat = seatAt(g.roster[side], slot)}
								<li class="flex items-center gap-2 {i === 0 ? 'flex-row-reverse' : ''}">
									{#if seat}
										{@const p = players.get(seat.player_id)}
										{#if p}
											<a href="/players/{p.id}" class="flex min-w-0 items-center gap-2 hover:underline {i === 0 ? 'flex-row-reverse' : ''}" data-testid="seat-player">
												<PlayerAvatar player={p} size={28} /><span class="truncate">{playerName(seat.player_id)}</span>
											</a>
										{:else}
											<span class="grid size-7 place-items-center rounded-full bg-stone-200 text-xs">?</span>
											<span class="truncate">{playerName(seat.player_id)}</span>
										{/if}
										{#if seat.player_id != null && change[seat.player_id] != null}
											{@const c = Math.round(change[seat.player_id])}
											<span class="text-sm font-semibold tabular-nums {c > 0 ? 'text-emerald-700' : c < 0 ? 'text-red-700' : 'text-stone-500'}">{c > 0 ? '+' : ''}{c}</span>
										{/if}
									{/if}
								</li>
							{/each}
						</ul>
					</div>
					{#if i === 0}
						<div class="order-2 text-center">
							<div class="font-score text-6xl font-bold tabular-nums {g.voided ? 'text-stone-400 line-through' : ''}" data-testid="score">
								{g.home}<span class="text-stone-300">:</span>{g.away}
							</div>
							<div class="mt-1 text-sm text-stone-500">
								{#if g.status === 'finished'}
									{g.winner ? t('board.winner', { side: tSide(g.winner) }) : t('board.draw')}
								{:else}
									{g.status === 'abandoned' ? t('games.abandoned') : g.status === 'paused' ? t('board.paused') : t('phase.playing')}
								{/if}
							</div>
							{#if g.edits}<span class="chip mt-1">{t('games.edited')}</span>{/if}
						</div>
					{/if}
				{/each}
			</div>
		</section>

		{#if finished && (canEdit || d.can_void)}
			<section class="card space-y-3" data-testid="edit-tools">
				<div>
					<h2 class="h2">{t('game.edit')}</h2>
					<p class="muted text-sm">{t('game.edit_hint')}</p>
				</div>
				{#if canEdit}
					<div class="grid gap-3 sm:grid-cols-2" data-testid="player-edit">
						{#each ['home', 'away'] as const as side (side)}
							<div>
								<span class="label {side === 'home' ? 'text-home' : 'text-away'}">{t('game.set_player')} · {tSide(side)}</span>
								<div class="space-y-1.5">
									{#each [1, 2] as slot (slot)}
										{@const seat = seatAt(g.roster[side], slot)}
										<select class="input" disabled={busy} aria-label="{tSide(side)} {slot}"
											value={seat?.player_id != null ? String(seat.player_id) : ''}
											onchange={(e) => setSlot(side, slot, (e.currentTarget as HTMLSelectElement).value)}>
											<option value="">{seat ? (seat.player_id == null ? t('reg.unknown_guest') : t('game.remove_player')) : t('game.empty_slot')}</option>
											{#each players.list as p (p.id)}<option value={String(p.id)}>{tPlayer(p)}</option>{/each}
										</select>
									{/each}
								</div>
							</div>
						{/each}
					</div>
				{/if}
				<div class="flex flex-wrap gap-2">
					{#if canEdit}
						<button class="btn" disabled={busy} onclick={() => edit({ kind: 'add_goal', side: 'home' })}>+ {t('game.add_goal', { side: tSide('home') })}</button>
						<button class="btn" disabled={busy} onclick={() => edit({ kind: 'add_goal', side: 'away' })}>+ {t('game.add_goal', { side: tSide('away') })}</button>
						<button class="btn" disabled={busy} onclick={() => edit({ kind: 'swap_sides' })}><ArrowLeftRight class="size-4" aria-hidden="true" />{t('game.swap')}</button>
					{/if}
					{#if d.can_void && !g.voided}
						<button class="btn text-red-700" disabled={busy} onclick={() => edit({ kind: 'void_game' }, t('game.void_confirm'))}>{t('game.void')}</button>
					{/if}
				</div>
			</section>
		{/if}

		<section class="card">
			<h2 class="h2">{t('game.log')}</h2>
			<p class="muted mb-3 text-sm">{t('game.log_hint')}</p>
			<ol class="divide-y divide-stone-100" data-testid="log">
				{#each d.events as e (e.seq)}
					{@const struck = e.reverted || (e.type === 'goal' && voidedGoals.has(e.seq))}
					<li class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm {struck ? 'text-stone-400' : ''}">
						<span class="w-8 shrink-0 font-mono text-xs text-stone-400">#{e.seq}</span>
						<span class="w-12 shrink-0 font-mono text-xs text-stone-500 tabular-nums">{e.play_time_ms != null ? clock(e.play_time_ms) : ''}</span>
						<span class="min-w-0 flex-1 {struck ? 'line-through' : ''}">
							{eventText(e)}

						</span>
						{#if e.edit}<span class="chip bg-amber-100 text-amber-800">{t('ev.correction')}</span>{:else}<span class="chip">{t(('ev.source.' + e.source) as Key)}</span>{/if}
						{#if e.author}<span class="text-xs text-stone-500">{t('ev.by', { name: e.author })}</span>{/if}
						{#if canEdit && e.type === 'goal' && !e.reverted && !voidedGoals.has(e.seq)}
							<button class="btn btn-sm" disabled={busy} onclick={() => edit({ kind: 'void_goal', target_seq: e.seq })}>{t('game.void_goal')}</button>
						{/if}
						{#if d.can_revert && e.edit && !e.reverted}
							<button class="btn btn-sm" disabled={busy} onclick={() => edit({ kind: 'revert', target_seq: e.seq })}>{t('game.revert')}</button>
						{/if}
					</li>
				{/each}
			</ol>
		</section>
	{/if}
</main>
