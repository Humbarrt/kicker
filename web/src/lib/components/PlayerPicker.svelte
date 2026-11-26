<script lang="ts">
	// Picking players: a search field and a grid of tiles with picture or color. "Played
	// recently" is two rows: the guest (always, as the first tile), then the players with the
	// latest games; everyone else, and recent players that don't fit, follow A–Z. With max 1 a tap picks and closes; otherwise players are ticked
	// and added together ("Select all" without a max). Taken players stay visible, greyed out
	// with a hint; the guest can always be picked. A dialog on wide screens, a bottom sheet on
	// phones.
	import X from '@lucide/svelte/icons/x';
	import Check from '@lucide/svelte/icons/check';
	import { players } from '#lib/players.svelte.ts';
	import { t, tPlayer } from '#lib/i18n.svelte.ts';
	import type { Player } from '#lib/api/client.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	let {
		open = $bindable(false),
		title,
		hint = '',
		max = 0,
		taken = new Map(),
		addLabel,
		onpick
	}: {
		open: boolean;
		title: string;
		hint?: string;
		/** 0 = no limit; 1 = pick one with a tap. */
		max?: number;
		/** Players that can't be picked, with why (e.g. "on Away", "Team 3"). */
		taken?: Map<number, string>;
		/** The add button's text for n selected players. */
		addLabel?: (n: number) => string;
		onpick: (ids: number[]) => void | Promise<void>;
	} = $props();

	const RECENT_DAYS = 30;

	let dialog: HTMLDialogElement | undefined = $state();
	let search: HTMLInputElement | undefined = $state();
	let query = $state('');
	let selected = $state<number[]>([]);
	let busy = $state(false);

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			query = '';
			selected = [];
			players.reload();
			dialog.showModal();
			search?.focus();
		} else if (!open && dialog.open) dialog.close();
	});

	const fold = (s: string) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
	const cutoff = $derived(Date.now() - RECENT_DAYS * 864e5);
	const isRecent = (p: Player) => !p.guest && (p.last_played ?? 0) >= cutoff;
	const byName = (a: Player, b: Player) => tPlayer(a).localeCompare(tPlayer(b));

	// Two rows of "Played recently": 3 tiles per row on phones, 4 from sm on. The guest takes the
	// first tile, so 5 (phones) or 7 recent players fit; the ones between are shown in "Played
	// recently" from sm on and in "All players" below it on phones.
	const PHONE = 2 * 3 - 1;
	const WIDE = 2 * 4 - 1;
	type Entry = { p: Player; cls?: string };
	const matches = $derived.by(() => {
		const q = fold(query.trim());
		const list = players.active.filter((p) => !q || fold(tPlayer(p)).includes(q));
		const guest = list.filter((p) => p.guest);
		const recent = list.filter(isRecent).sort((a, b) => (b.last_played ?? 0) - (a.last_played ?? 0));
		const others = list.filter((p) => !p.guest && !isRecent(p));
		const top: Entry[] = [
			...guest.map((p) => ({ p })),
			...recent.slice(0, WIDE).map((p, i) => ({ p, cls: i >= PHONE ? 'max-sm:hidden' : '' }))
		];
		const rest: Entry[] = [
			...recent.slice(PHONE, WIDE).map((p) => ({ p, cls: 'sm:hidden' })),
			...recent.slice(WIDE).map((p) => ({ p })),
			...others.map((p) => ({ p }))
		].sort((a, b) => byName(a.p, b.p));
		return { top, rest, recent: recent.length, all: [...guest, ...recent, ...others.sort(byName)] };
	});
	const pickable = (p: Player) => p.guest || !taken.has(p.id);
	const full = $derived(max > 0 && selected.length >= max);

	function tap(p: Player) {
		if (!pickable(p)) return;
		if (max === 1) return void finish([p.id]);
		if (selected.includes(p.id)) selected = selected.filter((x) => x !== p.id);
		else if (!full) selected = [...selected, p.id];
	}
	function selectAll() {
		const ids = matches.all.filter((p) => !p.guest && pickable(p)).map((p) => p.id);
		selected = [...new Set([...selected, ...ids])];
	}
	async function finish(ids: number[]) {
		if (!ids.length || busy) return;
		busy = true;
		try {
			await onpick(ids);
			open = false;
		} finally {
			busy = false;
		}
	}
	// Enter with one player left: pick it (or tick it and start the next search).
	function onkey(e: KeyboardEvent) {
		if (e.key !== 'Enter') return;
		e.preventDefault();
		const left = matches.all.filter(pickable);
		if (left.length === 1) {
			tap(left[0]);
			query = '';
		} else if (!query && max !== 1) finish(selected);
	}
</script>

{#snippet tile(p: Player, cls = '')}
	{@const on = selected.includes(p.id)}
	{@const why = p.guest ? '' : taken.get(p.id)}
	<button type="button" onclick={() => tap(p)} disabled={!!why || (full && !on)} data-testid="pick-{p.id}" aria-pressed={max === 1 ? undefined : on}
		class="relative flex min-w-0 flex-col items-center gap-1 rounded-xl border px-1 py-2 text-center transition
			{on ? 'border-emerald-600 bg-emerald-50' : 'border-transparent hover:bg-stone-100'}
			{why ? 'opacity-40' : ''} {cls} disabled:cursor-not-allowed">
		<PlayerAvatar player={p} size={44} />
		<span class="w-full truncate text-sm">{tPlayer(p)}</span>
		{#if why}<span class="w-full truncate text-xs text-stone-500">{why}</span>{/if}
		{#if on}<span class="absolute top-1 right-2 grid size-5 place-items-center rounded-full bg-emerald-600 text-xs text-white"><Check class="size-3.5" strokeWidth={3} aria-hidden="true" /></span>{/if}
	</button>
{/snippet}

{#snippet section(title: string, list: Entry[], testid = '')}
	{#if list.length}
		{#if title}<h3 class="px-1 pt-2 text-xs font-semibold tracking-wide text-stone-500 uppercase">{title}</h3>{/if}
		<div class="grid grid-cols-3 gap-1 sm:grid-cols-4" data-testid={testid || undefined}>
			{#each list as e (e.p.id)}{@render tile(e.p, e.cls)}{/each}
		</div>
	{/if}
{/snippet}

<dialog bind:this={dialog} onclose={() => (open = false)} data-testid="player-picker"
	class="m-auto flex max-h-[85vh] text-left w-[min(34rem,calc(100vw-2rem))] flex-col rounded-2xl bg-white p-0 shadow-xl backdrop:bg-stone-900/40
		max-sm:mb-0 max-sm:w-full max-sm:max-w-full max-sm:rounded-b-none [&:not([open])]:hidden">
	<div class="space-y-3 border-b border-stone-100 px-5 pt-4 pb-3">
		<div class="flex items-start gap-3">
			<div class="min-w-0 flex-1">
				<h2 class="h2">{title}</h2>
				{#if hint}<p class="muted text-sm">{hint}</p>{/if}
			</div>
			<button type="button" class="grid size-8 place-items-center rounded-full text-stone-500 hover:bg-stone-100" aria-label={t('app.cancel')} onclick={() => (open = false)}><X class="size-4" aria-hidden="true" /></button>
		</div>
		<div class="flex items-center gap-2">
			<input bind:this={search} bind:value={query} onkeydown={onkey} type="search" class="input flex-1" placeholder={t('pick.search')}
				aria-label={t('pick.search')} autocomplete="off" data-testid="pick-search" />
			{#if max === 0}
				<button type="button" class="btn btn-sm whitespace-nowrap" onclick={selectAll} data-testid="pick-all">{t('pick.select_all')}</button>
			{/if}
		</div>
	</div>

	<div class="min-h-40 flex-1 overflow-y-auto px-4 pb-3">
		{#if !matches.all.length}
			<p class="muted py-8 text-center">{t('pick.none')}</p>
		{:else if query.trim() || !matches.recent}
			{@render section('', matches.all.map((p) => ({ p })))}
		{:else}
			{@render section(t('pick.recent'), matches.top, 'pick-recent')}
			{@render section(t('pick.all'), matches.rest, 'pick-rest')}
		{/if}
	</div>

	{#if max !== 1}
		<div class="flex items-center gap-2 border-t border-stone-100 px-5 py-3">
			<span class="muted flex-1 text-sm">{t('pick.selected', { n: selected.length })}{max ? ` / ${max}` : ''}</span>
			{#if selected.length}<button type="button" class="btn btn-sm" onclick={() => (selected = [])}>{t('pick.clear')}</button>{/if}
			<button type="button" class="btn btn-primary" disabled={!selected.length || busy} onclick={() => finish(selected)} data-testid="pick-add">
				{addLabel && selected.length ? addLabel(selected.length) : t('pick.add')}
			</button>
		</div>
	{/if}
</dialog>
