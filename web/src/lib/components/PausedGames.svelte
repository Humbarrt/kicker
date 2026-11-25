<script lang="ts">
	// The paused games list (D16): resume one, and the abandoned games to reinstate.
	import { api, type GameView, type PausedGame, type Seat, type Side } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, tPlayer, tSide, type Key } from '#lib/i18n.svelte.ts';
	import { ago } from '#lib/format.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import { pauseReason } from '#lib/gamelog.ts';

	let { paused, abandonAfterHours = 24 }: { paused: PausedGame[]; abandonAfterHours?: number } = $props();

	let now = $state(Date.now());
	$effect(() => {
		const id = setInterval(() => (now = Date.now()), 15_000);
		return () => clearInterval(id);
	});
	$effect.pre(() => {
		void paused; // a new list: "x minutes ago" from now
		now = Date.now();
	});

	let error = $state('');
	let busy = $state(0);
	let showAbandoned = $state(false);
	let abandoned = $state<GameView[] | null>(null);

	async function resume(id: number) {
		busy = id;
		error = '';
		try {
			live.setTable(await api.resumeGame(id));
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = 0;
		}
	}
	async function loadAbandoned() {
		try {
			abandoned = await api.abandonedGames();
		} catch (e) {
			error = tErr(e);
		}
	}
	async function reinstate(id: number) {
		busy = id;
		error = '';
		try {
			live.setTable(await api.reinstateGame(id));
			await loadAbandoned();
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = 0;
		}
	}
	$effect(() => {
		if (showAbandoned && !abandoned) loadAbandoned();
	});
</script>

{#snippet seats(list: Seat[], side: Side)}
	<span class="flex min-w-0 items-center gap-1 {side === 'home' ? 'justify-end' : ''}">
		{#each list as s (s.slot)}
			{@const p = players.get(s.player_id)}
			{#if p}<span title={tPlayer(p)}><PlayerAvatar player={p} size={22} /></span>{:else}<span
					class="grid size-[22px] place-items-center rounded-full bg-stone-200 text-xs text-stone-500" title={t('reg.unknown_guest')}>?</span>{/if}
		{/each}
	</span>
{/snippet}

{#snippet row(g: GameView, extra: () => any)}
	<li class="space-y-2 py-3">
		<div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-2">
			<span class="flex min-w-0 items-center justify-end gap-2">
				<span class="truncate text-sm font-medium text-home">{tSide('home')}</span>{@render seats(g.roster.home, 'home')}
			</span>
			<span class="shrink-0 rounded-lg bg-stone-50 px-2 py-0.5 font-mono font-semibold whitespace-nowrap tabular-nums"
				><span class="inline-block w-[2ch] text-right">{g.home}</span>:<span class="inline-block w-[2ch]">{g.away}</span></span>
			<span class="flex min-w-0 items-center gap-2">
				{@render seats(g.roster.away, 'away')}<span class="truncate text-sm font-medium text-away">{tSide('away')}</span>
			</span>
		</div>
		<div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">{@render extra()}</div>
	</li>
{/snippet}

{#if paused.length}
	<section class="card" data-testid="paused-games">
		<h2 class="h2">{t('paused.title')}</h2>
		<p class="muted mb-2 text-sm">{t('paused.hint')}</p>
		<ul class="divide-y divide-stone-100">
			{#each paused as g (g.id)}
				{#snippet extra()}
					<span class="text-sm text-stone-500">
						{pauseReason(g.reason)} · {ago(g.paused_at, now, i18n.lang)}
					</span>
					{#if session.can('table.control')}
						<button class="btn btn-sm ml-auto" disabled={busy === g.id} onclick={() => resume(g.id)}>{t('board.resume')}</button>
					{/if}
				{/snippet}
				{@render row(g, extra)}
			{/each}
		</ul>
	</section>
{/if}

{#if error}<p class="error">{error}</p>{/if}

{#if session.can('games.edit')}
	<details class="card" bind:open={showAbandoned}>
		<summary class="cursor-pointer font-medium">{t('paused.abandoned')}</summary>
		<p class="muted mt-2 text-sm">{t('paused.abandoned_hint', { n: abandonAfterHours })}</p>
		{#if abandoned && abandoned.length === 0}
			<p class="muted py-3 text-sm">{t('paused.abandoned_none')}</p>
		{:else if abandoned}
			<ul class="divide-y divide-stone-100">
				{#each abandoned as g (g.id)}
					{#snippet extra()}
						<span class="text-sm text-stone-500">{t('paused.started', { when: ago(g.started_at, now, i18n.lang) })}</span>
						<button class="btn btn-sm ml-auto" disabled={busy === g.id} onclick={() => reinstate(g.id)}>{t('paused.reinstate')}</button>
					{/snippet}
					{@render row(g, extra)}
				{/each}
			</ul>
		{/if}
	</details>
{/if}
