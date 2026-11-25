<script lang="ts">
	// Simulator panel (kicker serve --sim): virtual goal sensors and coin acceptors.
	import { api, type Coin, type SimView } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { t, tErr, tPlayer, tSide, type Key } from '#lib/i18n.svelte.ts';

	let sim = $state<SimView>({ modules: [], acceptors: [] });
	let coins = $state<Coin[]>([]);
	let pick = $state<Record<string, string>>({});
	let error = $state('');

	async function load() {
		try {
			[sim, coins] = await Promise.all([api.sim(), api.simCoins()]);
		} catch (e) {
			error = tErr(e);
		}
	}
	$effect(() => {
		load();
	});
	// coins assigned elsewhere show up in the picker
	$effect(() => {
		live.table?.coins;
		api.simCoins().then((c) => (coins = c)).catch(() => {});
	});

	function goalLabel(i: SimView['modules'][number]['instances'][number]) {
		const target = i.role ? t(('sim.role.' + i.role) as Key) : t('sim.instance', { i: i.instance });
		return t('sim.goal', { target });
	}
	function coinLabel(c: Coin) {
		const p = players.get(c.player_id);
		return t('sim.coin_of', { name: p ? tPlayer(p) : '#' + c.player_id }) + (c.label ? ` (${c.label})` : '') + (c.active ? '' : ' – ' + t('coins.inactive'));
	}
	function randomUid() {
		const b = crypto.getRandomValues(new Uint8Array(7));
		b[0] = 0x04; // NXP, like real NTAG coins
		return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('').toUpperCase();
	}

	async function run(fn: () => Promise<void>) {
		error = '';
		try {
			await fn();
		} catch (e) {
			error = tErr(e);
		}
	}
	const inside = (role: string) => (live.table?.coins as Record<string, { uid: string }[]> | undefined)?.[role] ?? [];
</script>

<section class="card border-dashed border-amber-300 bg-amber-50/50">
	<h2 class="h2">{t('sim.title')}</h2>
	<p class="muted mb-4">{t('sim.hint')}</p>
	<div class="flex flex-wrap gap-2">
		{#each sim.modules as m (m.uid)}
			{#each m.instances as i (i.instance)}
				{#each i.events as ev (ev)}
					<button class="btn" onclick={() => run(() => api.simEmit(m.uid, i.instance, ev))}>
						{ev === 'goal' ? goalLabel(i) : `${i.instance}: ${ev}`}
					</button>
				{/each}
			{/each}
		{/each}
	</div>

	{#if sim.acceptors.length}
		<div class="mt-5 grid gap-4 md:grid-cols-2">
			{#each [...sim.acceptors].sort((x, y) => (x.role === 'home' ? -1 : y.role === 'home' ? 1 : 0)) as a (a.uid)}
				{@const full = inside(a.role).length >= a.cap}
				<div class="rounded-xl border border-amber-200 bg-white/70 p-3">
					<div class="mb-2 flex items-center justify-between text-sm font-medium">
						{t('sim.coins', { side: a.role ? tSide(a.role) : a.uid })}
						<span class="chip">{inside(a.role).length}/{a.cap}{full ? ' · ' + t('sim.full') : ''}</span>
					</div>
					<div class="flex flex-wrap gap-2">
						<select class="input min-w-0 flex-1 py-1" bind:value={pick[a.uid]}>
							<option value="">{t('sim.unknown_coin')}</option>
							{#each coins as c (c.uid)}<option value={c.uid}>{coinLabel(c)}</option>{/each}
						</select>
						<button class="btn" disabled={full} onclick={() => run(() => api.simInsert(a.uid, pick[a.uid] || randomUid()))}>{t('sim.insert')}</button>
						<button class="btn" onclick={() => run(() => api.simButton(a.uid))}>{t('sim.button')}</button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
	{#if error}<p class="error mt-3">{error}</p>{/if}
</section>
