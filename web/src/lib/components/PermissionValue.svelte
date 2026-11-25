<script lang="ts">
	// One permission value: a switch (on/off), off/own/all (scope) or a number of days.
	import type { PermissionKind } from '#lib/api/client.ts';
	import { t } from '#lib/i18n.svelte.ts';

	let { kind, value = $bindable(), disabled = false }: { kind: PermissionKind; value: number; disabled?: boolean } = $props();

	const dayOptions = [0, 1, 3, 7, 14, 30, 60, 90, 180, 365, -1];
	const days = $derived(dayOptions.includes(value) ? dayOptions : [...dayOptions, value].sort((a, b) => (a === -1 ? 1 : b === -1 ? -1 : a - b)));
	const dayLabel = (d: number) => (d === 0 ? t('kind.off') : d === -1 ? t('kind.unlimited') : `${d} ${t('kind.days')}`);
</script>

{#if kind === 'on_off'}
	<label class="relative inline-flex cursor-pointer items-center {disabled ? 'cursor-not-allowed opacity-60' : ''}">
		<input type="checkbox" class="peer sr-only" checked={value === 1} {disabled} onchange={(e) => (value = e.currentTarget.checked ? 1 : 0)} />
		<span class="h-6 w-11 rounded-full bg-stone-300 transition peer-checked:bg-emerald-600 peer-focus-visible:outline-2 peer-focus-visible:outline-emerald-600"></span>
		<span class="absolute left-0.5 size-5 rounded-full bg-white shadow transition peer-checked:translate-x-5"></span>
	</label>
{:else if kind === 'scope'}
	<div class="inline-flex overflow-hidden rounded-lg border border-stone-300 text-xs {disabled ? 'opacity-60' : ''}">
		{#each [0, 1, 2] as v (v)}
			<button type="button" {disabled}
				class="px-2.5 py-1 {value === v ? 'bg-stone-900 text-white' : 'bg-white text-stone-600 hover:bg-stone-50'} {disabled ? 'cursor-not-allowed' : ''}"
				onclick={() => (value = v)}>{t(v === 0 ? 'kind.off' : v === 1 ? 'kind.own' : 'kind.all')}</button>
		{/each}
	</div>
{:else}
	<select class="input w-auto py-1 text-xs" {disabled} value={value} onchange={(e) => (value = Number(e.currentTarget.value))}>
		{#each days as d (d)}<option value={d}>{dayLabel(d)}</option>{/each}
	</select>
{/if}
