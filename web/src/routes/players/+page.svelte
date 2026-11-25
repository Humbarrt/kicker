<script lang="ts">
	import { api, type Leaderboard as Board, type Player, type RatingKey } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr, tPlayer } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import PlayerEditor from '#lib/components/PlayerEditor.svelte';
	import Leaderboard from '#lib/components/Leaderboard.svelte';

	let list = $state<Player[]>([]);
	let loaded = $state(false);
	let error = $state('');
	let query = $state('');
	let archived = $state(false);
	let editing = $state(false);

	async function load() {
		try {
			list = await api.players(archived);
		} catch (e) {
			error = tErr(e);
		} finally {
			loaded = true;
		}
	}
	$effect(() => {
		archived;
		load();
	});

	// The leaderboard (M3 task 8): ranked players first, those with too few games below.
	let key = $state<RatingKey>('team');
	let board = $state<Board | null>(null);
	$effect(() => {
		void live.statsRev;
		if (session.can('view.stats')) api.leaderboard(key).then((b) => (board = b)).catch((e) => (error = tErr(e)));
	});
	const ranked = $derived(board?.rows.filter((r) => r.rank > 0) ?? []);
	const unranked = $derived(board?.rows.filter((r) => r.rank === 0) ?? []);

	const shown = $derived(list.filter((p) => tPlayer(p).toLowerCase().includes(query.trim().toLowerCase())));
</script>

<main class="mx-auto max-w-6xl space-y-6 px-4 py-6">
	{#if board}
		<section class="card" data-testid="leaderboard">
			<div class="mb-2 flex flex-wrap items-center gap-3">
				<h1 class="h1 flex-1">{t('leaderboard.title')}</h1>
				<div class="inline-flex rounded-lg bg-stone-100 p-0.5 text-sm" role="tablist">
					{#each ['team', 'solo'] as const as k (k)}
						<button role="tab" aria-selected={key === k} onclick={() => (key = k)}
							class="rounded-md px-3 py-1 {key === k ? 'bg-white font-medium shadow-sm' : 'text-stone-600'}"
							>{t(k === 'solo' ? 'rating.solo' : 'rating.team')}</button>
					{/each}
				</div>
			</div>
			{#if ranked.length}
				<Leaderboard rows={ranked} windowDays={board.window_days} />
			{:else}
				<p class="muted py-3">{t('leaderboard.empty', { n: board.min_ranked })}</p>
			{/if}
			{#if unranked.length}
				<details class="mt-3">
					<summary class="cursor-pointer text-sm text-stone-600">{t('leaderboard.unranked', { n: board.min_ranked, count: unranked.length })}</summary>
					<Leaderboard rows={unranked} windowDays={board.window_days} />
				</details>
			{/if}
			<p class="muted mt-3 text-xs">{t('rating.hint')}</p>
		</section>
	{/if}

	<div class="flex flex-wrap items-center gap-3">
		<h2 class="h1 flex-1">{t('players.title')}</h2>
		<input class="input w-56" type="search" placeholder={t('players.search')} bind:value={query} />
		{#if session.can('profile.any')}
			<label class="flex items-center gap-2 text-sm text-stone-600"><input type="checkbox" bind:checked={archived} /> {t('players.show_archived')}</label>
		{/if}
		{#if session.can('players.create')}
			<button class="btn btn-primary" onclick={() => (editing = true)}>{t('players.new')}</button>
		{/if}
	</div>

	{#if error}<p class="error">{error}</p>{/if}
	{#if loaded && shown.length === 0}<p class="muted">{t('players.empty')}</p>{/if}

	<ul class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
		{#each shown as p (p.id)}
			<li>
				<a href="/players/{p.id}" class="card flex flex-col items-center gap-3 p-4 text-center transition hover:shadow-md {p.archived ? 'opacity-60' : ''}">
					<PlayerAvatar player={p} size={72} />
					<span class="font-medium">{tPlayer(p)}</span>
					{#if p.archived}<span class="chip">{t('players.archived')}</span>{/if}
				</a>
			</li>
		{/each}
	</ul>
</main>

<PlayerEditor bind:open={editing} onsaved={() => load()} />
