<script lang="ts">
	// Who plays on one side (spec 02 §4): the registrations by slot – stale players dimmed, unknown
	// coins to assign – and coins that don't count. After a game, its roster until somebody
	// registers again.
	import { api, type Registration, type Side, type TableView, type Via } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr, tPlayer, type Key } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import AssignCoinDialog from '#lib/components/AssignCoinDialog.svelte';
	import AddPlayerDialog from '#lib/components/AddPlayerDialog.svelte';

	let { view, side, changes = {} }: { view: TableView; side: Side; changes?: Record<number, number> } = $props();

	const regs = $derived(view.registrations?.[side] ?? []);
	const finished = $derived(view.game?.status === 'finished' ? view.game : null);
	const seats = $derived(regs.length === 0 && finished ? (finished.roster?.[side] ?? []) : []);
	// Coins in this side's acceptor that no registration counts (side full, or the player moved).
	const loose = $derived.by(() => {
		const counted = new Set<string>();
		for (const s of ['home', 'away'] as const)
			for (const r of view.registrations?.[s] ?? [])
				for (const src of r.sources) if (src.via === 'coin' && src.ref) counted.add(src.ref);
		return (view.coins?.[side] ?? []).filter((c) => !counted.has(c.uid));
	});

	const canEdit = $derived(session.can('table.players'));
	const canAdd = $derived(canEdit && (regs.length < 2 || regs.some((r) => r.stale)));

	let assigning = $state('');
	let assignOpen = $state(false);
	let addOpen = $state(false);
	let error = $state('');

	const viaText = (r: Registration) => r.sources.map((s) => t(('reg.via.' + s.via) as Key)).join(', ');
	const seatVia = (v: Via) => t(('reg.via.' + v) as Key);

	async function remove(slot: number) {
		error = '';
		try {
			live.setTable(await api.unregister(side, slot));
		} catch (e) {
			error = tErr(e);
		}
	}
</script>

<div class="mt-3 flex flex-wrap justify-center gap-2" data-testid="roster-{side}">
	{#each regs as r (r.slot)}
		{@const p = players.get(r.player_id)}
		<span class="inline-flex items-center gap-1.5 rounded-full py-0.5 pl-0.5 text-sm {r.player_id == null ? 'bg-amber-100 pl-3 text-amber-900' : 'bg-stone-100'} {r.stale ? 'opacity-60' : ''} {canEdit ? 'pr-1' : 'pr-3'}"
			title={viaText(r)} data-testid="reg">
			{#if p}
				<PlayerAvatar player={p} size={24} />{tPlayer(p)}
			{:else if r.player_id == null}
				{t('coins.unknown')}
				{#if session.can('coins.manage') && r.coin_uid}
					<button class="font-semibold underline" onclick={() => ((assigning = r.coin_uid!), (assignOpen = true))}>{t('coins.assign')}</button>
				{/if}
			{:else}
				<span class="pl-2">…</span>
			{/if}
			{#if r.stale}
				<span class="rounded-full bg-stone-200 px-1.5 text-xs text-stone-600" title={t('reg.left_hint')}>{t('reg.left')}</span>
			{/if}
			{#if canEdit}
				<button class="grid size-6 place-items-center rounded-full text-stone-500 hover:bg-stone-200 hover:text-stone-900"
					aria-label={t('reg.remove', { name: p ? tPlayer(p) : t('coins.unknown') })} onclick={() => remove(r.slot)}>×</button>
			{/if}
		</span>
	{/each}

	{#each seats as s (s.slot)}
		{@const p = players.get(s.player_id)}
		<span class="inline-flex items-center gap-1.5 rounded-full bg-stone-50 py-0.5 pr-3 pl-0.5 text-sm text-stone-500" title={seatVia(s.via)}>
			{#if p}<PlayerAvatar player={p} size={24} />{tPlayer(p)}{:else}<span class="pl-2">{t('reg.unknown_guest')}</span>{/if}
			{#if s.player_id != null && changes[s.player_id] != null}
				{@const d = Math.round(changes[s.player_id])}
				<span class="ml-0.5 font-semibold tabular-nums {d > 0 ? 'text-emerald-700' : d < 0 ? 'text-red-700' : ''}"
					title={t('rating.title')} data-testid="rating-change">{d > 0 ? '+' : ''}{d}</span>
			{/if}
		</span>
	{/each}

	{#each loose as c (c.uid)}
		{@const p = players.get(c.player_id)}
		<span class="inline-flex items-center gap-1 rounded-full border border-dashed border-stone-300 px-3 py-0.5 text-sm text-stone-500"
			title={t('reg.not_counted_hint')}>
			{t('reg.not_counted')}{#if p}: {tPlayer(p)}{/if}
		</span>
	{/each}

	{#if canAdd}
		<button class="rounded-full border border-dashed border-stone-300 px-3 py-0.5 text-sm text-stone-600 hover:border-stone-500 hover:text-stone-900"
			onclick={() => (addOpen = true)}>+ {t('reg.add')}</button>
	{/if}
</div>
{#if error}<p class="error mt-2 text-sm">{error}</p>{/if}

<AssignCoinDialog bind:open={assignOpen} uid={assigning} />
<AddPlayerDialog bind:open={addOpen} {side} />
