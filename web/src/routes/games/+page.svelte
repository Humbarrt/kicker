<script lang="ts">
	// The game history, newest first, with filters. The filters live in the URL (?player=1,2&kind=team…),
	// so a filtered list can be linked to – the profile's "All games" does that.
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import X from '@lucide/svelte/icons/x';
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type GameFlag, type GameQuery, type GameSummary } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { t, tErr, tPlayer, type Key } from '#lib/i18n.svelte.ts';
	import GameRow from '#lib/components/GameRow.svelte';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	const pageSize = 30;
	const allFlags: GameFlag[] = ['voided', 'edited', 'unassigned', 'unknown', 'shutout', 'close'];
	let games = $state<GameSummary[] | null>(null);
	let more = $state(false);
	let error = $state('');
	let busy = $state(false);

	// --- the filter, read from and written to the URL ---
	const params = $derived(page.url.searchParams);
	const list = (k: string) => (params.get(k) ?? '').split(',').filter(Boolean);
	const sel = $derived({
		player: list('player').map(Number).filter((n) => n > 0).slice(0, 4),
		relation: (params.get('relation') ?? '') as GameQuery['relation'],
		result: (params.get('result') ?? '') as GameQuery['result'],
		kind: (params.get('kind') ?? '') as GameQuery['kind'],
		status: (params.get('status') ?? '') as GameQuery['status'],
		flags: list('flags').filter((f): f is GameFlag => allFlags.includes(f as GameFlag)),
		from: params.get('from') ?? '', // YYYY-MM-DD
		to: params.get('to') ?? ''
	});
	type Sel = typeof sel;

	function update(change: Partial<Sel>) {
		const next = { ...sel, ...change };
		if (next.player.length < 2) next.relation = '';
		if (!next.player.length && (next.result === 'win' || next.result === 'loss')) next.result = '';
		const p = new URLSearchParams();
		for (const [k, v] of Object.entries(next)) {
			const val = Array.isArray(v) ? v.join(',') : v;
			if (val) p.set(k, String(val));
		}
		goto(p.size ? `?${p}` : '/games', { replace: true, reset: false });
	}
	const active = $derived(
		sel.player.length + sel.flags.length + [sel.relation, sel.result, sel.kind, sel.status, sel.from, sel.to].filter(Boolean).length
	);
	let filtersOpen = $state(untrack(() => active > 0)); // open when the page is opened with filters

	// A day in the browser's time zone: from its start; "to" includes the whole day.
	const day = (d: string, next = false) => {
		if (!d) return undefined;
		const [y, m, dd] = d.split('-').map(Number);
		return new Date(y, m - 1, dd + (next ? 1 : 0)).getTime();
	};
	const query = $derived<GameQuery>({
		player: sel.player,
		relation: sel.relation,
		result: sel.result,
		kind: sel.kind,
		status: sel.status,
		flags: sel.flags,
		from: day(sel.from),
		to: day(sel.to, true)
	});

	$effect(() => {
		void live.statsRev; // a finished or edited game
		const q = query;
		api.games({ ...q, limit: pageSize })
			.then((l) => ((games = l), (more = l.length === pageSize), (error = '')))
			.catch((e) => (error = tErr(e)));
	});

	async function loadMore() {
		if (!games?.length) return;
		busy = true;
		try {
			const l = await api.games({ ...query, before: games[games.length - 1].id, limit: pageSize });
			games = [...games, ...l];
			more = l.length === pageSize;
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}

	const pickable = $derived(players.active.filter((p) => !p.guest && !sel.player.includes(p.id)));
	const first = $derived(players.get(sel.player[0]));
	const pname = (id: number) => { const p = players.get(id); return p ? tPlayer(p) : '…'; };
	$effect(() => {
		if (!players.list.length) players.reload();
	});
</script>

{#snippet segmented<T extends string>(value: T | undefined, options: { v: T; label: string; disabled?: boolean }[], set: (v: T) => void, label: string)}
	<div class="inline-flex flex-wrap justify-self-start rounded-lg bg-stone-100 p-0.5 text-sm" role="radiogroup" aria-label={label}>
		{#each options as o (o.v)}
			<button role="radio" aria-checked={(value ?? '') === o.v} disabled={o.disabled} onclick={() => set(o.v)}
				class="rounded-md px-2.5 py-1 disabled:opacity-40 {(value ?? '') === o.v ? 'bg-white font-medium shadow-sm' : 'text-stone-600'}"
				>{o.label}</button>
		{/each}
	</div>
{/snippet}

{#snippet filterHeader()}
	{t('filter.title')}
	{#if active}<span class="chip bg-stone-900 text-white">{active}</span>{/if}
	{#if active}
		<button class="ml-auto text-sm font-normal text-stone-500 hover:text-stone-900"
			onclick={(e) => (e.preventDefault(), goto('/games', { replace: true, reset: false }))}
			>{t('filter.reset')}</button>
	{/if}
{/snippet}

{#snippet filterFields()}
	<div class="grid gap-1.5">
		<span class="label mt-3 mb-0 first:mt-0">{t('filter.players')}</span>
		<div class="flex flex-wrap items-center gap-2">
			{#each sel.player as id (id)}
				{@const p = players.get(id)}
				<span class="chip gap-1.5 py-1 pr-1">
					{#if p}<PlayerAvatar player={p} size={20} />{/if}
					{p ? tPlayer(p) : '…'}
					<button class="rounded px-1 text-stone-500 hover:bg-stone-200 hover:text-stone-900" aria-label={t('filter.remove')}
						onclick={() => update({ player: sel.player.filter((x) => x !== id) })}><X class="size-3.5" aria-hidden="true" /></button>
				</span>
			{/each}
			{#if sel.player.length < 4}
				<select class="input w-auto py-1 text-sm" aria-label={t('filter.add_player')} value=""
					onchange={(e) => { const v = Number(e.currentTarget.value); e.currentTarget.value = ''; if (v) update({ player: [...sel.player, v] }); }}>
					<option value="">{t('filter.add_player')}</option>
					{#each pickable as p (p.id)}<option value={p.id}>{tPlayer(p)}</option>{/each}
				</select>
			{/if}
		</div>

		{#if sel.player.length >= 2}
			<span class="label mt-3 mb-0 first:mt-0">{t('filter.relation', { a: pname(sel.player[0]), b: pname(sel.player[1]) })}</span>
			{@render segmented(sel.relation, [
				{ v: '', label: t('filter.any') },
				{ v: 'together', label: t('filter.together') },
				{ v: 'against', label: t('filter.against') }
			], (v) => update({ relation: v }), t('filter.relation_label'))}
		{/if}

		<span class="label mt-3 mb-0 first:mt-0">{first ? t('filter.result_for', { name: tPlayer(first) }) : t('filter.result')}</span>
		{@render segmented(sel.result, [
			{ v: '', label: t('filter.any') },
			{ v: 'win', label: t('filter.won'), disabled: !first },
			{ v: 'loss', label: t('filter.lost'), disabled: !first },
			{ v: 'draw', label: t('filter.draw') }
		], (v) => update({ result: v }), t('filter.result'))}

		<span class="label mt-3 mb-0 first:mt-0">{t('filter.kind')}</span>
		{@render segmented(sel.kind, [
			{ v: '', label: t('filter.any') },
			{ v: 'solo', label: t('rating.solo') },
			{ v: 'team', label: t('rating.team') }
		], (v) => update({ kind: v }), t('filter.kind'))}

		<span class="label mt-3 mb-0 first:mt-0">{t('filter.status')}</span>
		{@render segmented(sel.status, [
			{ v: '', label: t('filter.any') },
			{ v: 'finished', label: t('filter.finished') },
			{ v: 'abandoned', label: t('filter.abandoned') }
		], (v) => update({ status: v }), t('filter.status'))}

		<span class="label mt-3 mb-0 first:mt-0">{t('filter.only')}</span>
		<div class="flex flex-wrap gap-1.5">
			{#each allFlags as f (f)}
				{@const on = sel.flags.includes(f)}
				<button aria-pressed={on} title={t(`filter.flag.${f}.hint` as Key)}
					onclick={() => update({ flags: on ? sel.flags.filter((x) => x !== f) : [...sel.flags, f] })}
					class="rounded-full border px-3 py-1 text-sm {on ? 'border-stone-900 bg-stone-900 text-white' : 'border-stone-200 text-stone-600 hover:border-stone-400'}"
					>{t(`filter.flag.${f}` as Key)}</button>
			{/each}
		</div>

		<span class="label mt-3 mb-0 first:mt-0">{t('filter.period')}</span>
		<div class="grid grid-cols-[1fr_auto_1fr] items-center gap-2 text-sm lg:grid-cols-1">
			<input class="input min-w-0 py-1" type="date" aria-label={t('filter.from')} value={sel.from} max={sel.to || undefined}
				onchange={(e) => update({ from: e.currentTarget.value })} />
			<span class="text-stone-500 lg:hidden">–</span>
			<input class="input min-w-0 py-1" type="date" aria-label={t('filter.to')} value={sel.to} min={sel.from || undefined}
				onchange={(e) => update({ to: e.currentTarget.value })} />
		</div>
	</div>
{/snippet}

<main class="mx-auto max-w-6xl px-4 py-6">
	<h1 class="h1">{t('games.title')}</h1>
	<div class="mt-4 grid gap-4 lg:grid-cols-[17rem_minmax(0,1fr)] lg:items-start lg:gap-6">
		<!-- Wide screens: the filters beside the list, always open. -->
		<aside class="card sticky top-20 hidden lg:block" data-testid="game-filters">
			<div class="mb-4 flex items-center gap-2 font-medium">{@render filterHeader()}</div>
			{@render filterFields()}
		</aside>
		<!-- Phones: above the list, folded. -->
		<details class="card group lg:hidden" bind:open={filtersOpen} data-testid="game-filters-mobile">
			<summary class="flex cursor-pointer list-none items-center gap-2 font-medium [&::-webkit-details-marker]:hidden">
				<ChevronRight class="size-4 shrink-0 text-stone-500 transition-transform group-open:rotate-90" aria-hidden="true" />
				{@render filterHeader()}
			</summary>
			<div class="mt-4">{@render filterFields()}</div>
		</details>

		<div class="min-w-0 space-y-4">
			{#if error}<p class="error">{error}</p>{/if}
			{#if games}
				<section class="card divide-y divide-stone-100 p-2" data-testid="game-list">
					{#each games as g (g.id)}
						<GameRow game={g} />
					{:else}
						<p class="muted p-4">{active ? t('filter.none') : t('games.none')}</p>
					{/each}
				</section>
				{#if more}
					<div class="text-center"><button class="btn" disabled={busy} onclick={loadMore}>{t('games.more')}</button></div>
				{/if}
			{/if}
		</div>
	</div>
</main>
