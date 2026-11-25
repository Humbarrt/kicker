<script lang="ts">
	// Coins (D2): which NFC coin belongs to whom. coins.manage.
	import { api, type Coin } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, tPlayer } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	let list = $state<Coin[]>([]);
	let loaded = $state(false);
	let error = $state('');
	let form = $state({ uid: '', player: '', label: '' });

	async function load() {
		try {
			list = await api.coins();
			await players.reload();
		} catch (e) {
			error = tErr(e);
		} finally {
			loaded = true;
		}
	}
	$effect(() => {
		live.table?.coins; // last-inserted times change when coins go in
		load();
	});

	async function run(fn: () => Promise<unknown>) {
		error = '';
		try {
			await fn();
		} catch (e) {
			error = tErr(e);
		}
		await load();
	}

	async function add(e: SubmitEvent) {
		e.preventDefault();
		await run(async () => {
			await api.assignCoin(form.uid, Number(form.player), form.label);
			form = { uid: '', player: form.player, label: '' };
		});
	}
</script>

<div class="space-y-6">
	<p class="muted">{t('coins.hint')}</p>
	{#if !session.can('coins.manage')}
		<p class="card">{t('err.forbidden')}</p>
	{:else}
		{#if error}<p class="error">{error}</p>{/if}
		<section class="card overflow-x-auto p-0">
			<table class="w-full text-sm">
				<thead class="bg-stone-50 text-left text-xs text-stone-500 uppercase">
					<tr>
						<th class="px-4 py-2">{t('coins.uid')}</th><th class="px-4">{t('coins.player')}</th><th class="px-4">{t('coins.label')}</th>
						<th class="px-4">{t('coins.active')}</th><th class="px-4">{t('coins.last_seen')}</th><th class="px-4"></th>
					</tr>
				</thead>
				<tbody>
					{#each list as c (c.uid)}
						{@const p = players.get(c.player_id)}
						<tr class="border-t border-stone-100 {c.active ? '' : 'text-stone-400'}">
							<td class="px-4 py-2 font-mono">{c.uid}</td>
							<td class="px-4">
								<div class="flex items-center gap-2">
									{#if p}<PlayerAvatar player={p} size={24} />{/if}
									<select class="input w-auto py-1" value={String(c.player_id)} onchange={(e) => run(() => api.assignCoin(c.uid, Number(e.currentTarget.value), c.label))}>
										{#each players.list.filter((x) => !x.archived || x.id === c.player_id) as x (x.id)}<option value={String(x.id)}>{tPlayer(x)}</option>{/each}
									</select>
								</div>
							</td>
							<td class="px-4">
								<input class="input w-40 py-1" value={c.label} maxlength="40" placeholder={t('coins.label_hint')}
									onchange={(e) => run(() => api.assignCoin(c.uid, c.player_id, e.currentTarget.value))} />
							</td>
							<td class="px-4"><input type="checkbox" checked={c.active} onchange={(e) => run(() => api.setCoinActive(c.uid, e.currentTarget.checked))} /></td>
							<td class="px-4 whitespace-nowrap text-stone-500">{c.last_seen_at ? dateTime(c.last_seen_at, i18n.lang) : t('coins.never')}</td>
							<td class="px-4 text-right">
								<button class="btn btn-sm btn-danger" onclick={() => confirm(t('coins.delete_confirm')) && run(() => api.deleteCoin(c.uid))}>{t('app.delete')}</button>
							</td>
						</tr>
					{:else}
						{#if loaded}<tr><td colspan="6" class="muted px-4 py-6">{t('coins.empty')}</td></tr>{/if}
					{/each}
				</tbody>
			</table>
		</section>

		<form class="card grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto] sm:items-end" onsubmit={add}>
			<h2 class="h2 sm:col-span-4">{t('coins.add')}</h2>
			<label><span class="label">{t('coins.uid')}</span>
				<input class="input font-mono" bind:value={form.uid} required placeholder="04A1B2C3D4E5F6" /></label>
			<label><span class="label">{t('coins.player')}</span>
				<select class="input" bind:value={form.player} required>
					<option value="" disabled>–</option>
					{#each players.active as x (x.id)}<option value={String(x.id)}>{tPlayer(x)}</option>{/each}
				</select></label>
			<label><span class="label">{t('coins.label')}</span>
				<input class="input" bind:value={form.label} maxlength="40" placeholder={t('coins.label_hint')} /></label>
			<button class="btn btn-primary">{t('coins.add')}</button>
			<p class="muted sm:col-span-4">{t('coins.uid_hint')}</p>
		</form>
	{/if}
</div>
