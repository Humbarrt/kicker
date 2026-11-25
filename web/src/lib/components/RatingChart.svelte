<script lang="ts">
	// A player's rating over their rated games (one key): a 2px line with a light wash, hairline
	// grid, crosshair + tooltip on hover. x = games played (evenly spaced), y = rating.
	import type { HistoryPoint } from '#lib/api/client.ts';
	import { i18n, t } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';

	let { history, start, label }: { history: HistoryPoint[]; start: number; label: string } = $props();

	const color = '#2a78d6'; // validated series color (dataviz palette slot 1)
	const height = 200;
	const pad = { top: 12, right: 12, bottom: 22, left: 44 };
	let width = $state(600);

	// The start value is point 0, so a single game still draws a line.
	const pts = $derived([{ game_id: 0, at: 0, rating: start }, ...history]);
	const lo = $derived(Math.min(...pts.map((p) => p.rating)));
	const hi = $derived(Math.max(...pts.map((p) => p.rating)));
	const step = $derived(niceStep((hi - lo) / 4 || 10));
	const y0 = $derived(Math.floor(lo / step) * step);
	const y1 = $derived(Math.max(Math.ceil(hi / step) * step, y0 + step));
	const ticks = $derived(Array.from({ length: Math.round((y1 - y0) / step) + 1 }, (_, i) => y0 + i * step));

	function niceStep(raw: number): number {
		const p = 10 ** Math.floor(Math.log10(raw));
		for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= raw) return m * p;
		return 10 * p;
	}
	const x = (i: number) => pad.left + (pts.length > 1 ? (i / (pts.length - 1)) * (width - pad.left - pad.right) : 0);
	const y = (v: number) => pad.top + (1 - (v - y0) / (y1 - y0)) * (height - pad.top - pad.bottom);
	const line = $derived(pts.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.rating).toFixed(1)}`).join(''));
	const area = $derived(`${line}L${x(pts.length - 1).toFixed(1)},${y(y0)}L${x(0).toFixed(1)},${y(y0)}Z`);

	let hover = $state<number | null>(null);
	function move(e: PointerEvent) {
		const r = (e.currentTarget as SVGElement).getBoundingClientRect();
		const f = (e.clientX - r.left - pad.left) / (width - pad.left - pad.right);
		hover = Math.max(0, Math.min(pts.length - 1, Math.round(f * (pts.length - 1))));
	}
	const hp = $derived(hover === null ? null : pts[hover]);
</script>

<figure class="relative" bind:clientWidth={width}>
	<svg {width} {height} role="img" aria-label={label} class="block touch-none select-none"
		onpointermove={move} onpointerdown={move} onpointerleave={() => (hover = null)}>
		{#each ticks as v (v)}
			<line x1={pad.left} x2={width - pad.right} y1={y(v)} y2={y(v)} stroke="#e7e5e4" stroke-width="1" />
			<text x={pad.left - 8} y={y(v)} dy="0.32em" text-anchor="end" class="fill-stone-500 text-[11px] tabular-nums">{v}</text>
		{/each}
		<text x={pad.left} y={height - 4} class="fill-stone-500 text-[11px]">{t('chart.start')}</text>
		<text x={width - pad.right} y={height - 4} text-anchor="end" class="fill-stone-500 text-[11px]">{t('chart.games', { n: pts.length - 1 })}</text>
		<path d={area} fill={color} fill-opacity="0.1" />
		<path d={line} fill="none" stroke={color} stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
		<circle cx={x(pts.length - 1)} cy={y(pts[pts.length - 1].rating)} r="4" fill={color} stroke="white" stroke-width="2" />
		{#if hp && hover !== null}
			<line x1={x(hover)} x2={x(hover)} y1={pad.top} y2={height - pad.bottom} stroke="#a8a29e" stroke-width="1" />
			<circle cx={x(hover)} cy={y(hp.rating)} r="4" fill={color} stroke="white" stroke-width="2" />
		{/if}
	</svg>
	{#if hp && hover !== null}
		<div class="pointer-events-none absolute top-0 rounded-lg border border-stone-200 bg-white px-2.5 py-1.5 text-xs shadow-sm"
			style="left: {Math.min(Math.max(x(hover) - 90, 0), width - 180)}px; width: 180px">
			<div class="font-semibold tabular-nums">{Math.round(hp.rating)}</div>
			<div class="text-stone-500">{hover === 0 ? t('chart.start') : dateTime(hp.at, i18n.lang)}</div>
		</div>
	{/if}
</figure>
