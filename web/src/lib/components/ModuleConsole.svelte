<script lang="ts">
	// The live module console (D8): every line in and out, with validation results.
	import { api, type ConsoleLine } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { i18n, t } from '#lib/i18n.svelte.ts';
	import { time } from '#lib/format.ts';

	let { uid }: { uid: string } = $props();
	let lines = $state<ConsoleLine[]>([]);
	let box: HTMLDivElement | undefined = $state();

	$effect(() => {
		api.console(uid).then((l) => (lines = l)).catch(() => {});
		return live.onConsole((l) => {
			if (l.uid !== uid) return;
			lines.push(l);
			if (lines.length > 500) lines.splice(0, lines.length - 500);
		});
	});

	$effect(() => {
		lines.length;
		if (box) box.scrollTop = box.scrollHeight;
	});

	const color = (l: ConsoleLine) =>
		l.err ? 'text-red-400' : l.warnings?.length ? 'text-amber-300' : l.dir === 'out' ? 'text-sky-300' : 'text-stone-200';
</script>

<div bind:this={box} class="mt-3 max-h-72 overflow-auto rounded-lg bg-stone-900 p-3 font-mono text-xs leading-relaxed">
	{#each lines as l, i (i)}
		<div class={color(l)}>
			<span class="text-stone-500">{time(l.at, i18n.lang)}</span>
			<span class="text-stone-500">{l.dir === 'out' ? '→' : l.dir === 'in' ? '←' : '·'}</span>
			{l.text}
			{#if l.err}<span class="text-red-400"> ✗ {l.err}</span>{/if}
			{#each l.warnings ?? [] as w (w)}<span class="text-amber-300"> ⚠ {w}</span>{/each}
		</div>
	{:else}
		<p class="text-stone-500">{t('modules.console_empty')}</p>
	{/each}
</div>
