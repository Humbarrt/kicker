<script lang="ts">
	// Recent results as small letter chips, newest first (the letter carries the meaning, not only the color).
	import type { Result } from '#lib/api/client.ts';
	import { t } from '#lib/i18n.svelte.ts';

	let { form, size = 'md' }: { form: Result[]; size?: 'sm' | 'md' } = $props();

	const style: Record<Result, string> = {
		win: 'bg-emerald-100 text-emerald-800',
		loss: 'bg-red-100 text-red-800',
		draw: 'bg-stone-200 text-stone-700'
	};
</script>

<span class="inline-flex gap-0.5" aria-label={t('stats.form')}>
	{#each form as r, i (i)}
		<span title={t(`result.${r}`)}
			class="grid place-items-center rounded font-semibold {style[r]} {size === 'sm' ? 'size-4 text-[10px]' : 'size-5 text-xs'}"
			>{t(`result.${r}.short`)}</span>
	{/each}
</span>
