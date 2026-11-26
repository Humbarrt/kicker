<script lang="ts">
	// Behaviour settings of one capability instance (spec 01 §4). Wiring stays in the firmware
	// (spec 04 §10); only known capabilities get fields for now.
	import Check from '@lucide/svelte/icons/check';
	import { api, ApiError, type CapView } from '#lib/api/client.ts';
	import { t, tErr, type Key } from '#lib/i18n.svelte.ts';

	let { uid, cap, onsaved }: { uid: string; cap: CapView; onsaved: () => void } = $props();

	const fields: Record<string, { key: string; def: number; min: number; max: number }[]> = {
		goal_sensor: [
			{ key: 'lockout_ms', def: 1000, min: 0, max: 10000 },
			{ key: 'min_block_us', def: 300, min: 0, max: 100000 }
		]
	};
	const list = $derived(fields[cap.capability.split('@')[0]] ?? []);
	let values = $state<Record<string, string>>({});
	let error = $state('');
	let saved = $state(false);

	$effect(() => {
		values = Object.fromEntries(list.map((f) => [f.key, cap.config?.[f.key] !== undefined ? String(cap.config[f.key]) : '']));
	});

	async function apply(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		saved = false;
		const partial: Record<string, number> = {};
		for (const f of list) partial[f.key] = values[f.key] === '' ? f.def : Number(values[f.key]);
		try {
			await api.setConfig(uid, cap.instance, partial);
			saved = true;
			onsaved();
		} catch (err) {
			error = err instanceof ApiError && err.code === 'invalid_config' ? `${tErr(err)} ${err.message}` : tErr(err);
		}
	}
</script>

{#if list.length}
	<form class="mt-2 grid gap-3 rounded-xl bg-stone-50 p-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end" onsubmit={apply}>
		{#each list as f (f.key)}
			<label class="block">
				<span class="label">{t(('modules.cfg.' + f.key) as Key)}</span>
				<input class="input" type="number" min={f.min} max={f.max} placeholder={t('modules.default', { v: f.def })} bind:value={values[f.key]} />
				<span class="mt-1 block text-xs text-stone-500">{t(('modules.cfg.' + f.key + '.hint') as Key)}</span>
			</label>
		{/each}
		<div class="flex items-center gap-2 sm:pb-5">
			<button class="btn">{t('modules.apply')}</button>
			{#if saved}<Check class="size-4 text-emerald-700" aria-hidden="true" />{/if}
		</div>
		{#if error}<p class="error sm:col-span-3">{error}</p>{/if}
	</form>
{/if}
