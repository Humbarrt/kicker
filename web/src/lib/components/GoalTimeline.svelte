<script lang="ts">
	// Goals as a match report: home goals on the left, away goals on the right, newest first,
	// with the score after each goal. Long lists are folded.
	import type { Goal } from '#lib/api/client.ts';
	import { t, tSide, type Key } from '#lib/i18n.svelte.ts';
	import { clock } from '#lib/format.ts';

	let { goals }: { goals: Goal[] } = $props();
	const FOLD = 6;
	let all = $state(false);

	// Running score after each goal that counts (chronological), shown newest first.
	const rows = $derived.by(() => {
		let h = 0;
		let a = 0;
		const out = goals.map((g) => {
			if (!g.voided) g.side === 'home' ? h++ : a++;
			return { g, h, a };
		});
		return out.reverse();
	});
	const shown = $derived(all ? rows : rows.slice(0, FOLD));
</script>

{#if rows.length === 0}
	<p class="muted py-2 text-center">{t('board.no_goals')}</p>
{:else}
	<ol class="space-y-1">
		{#each shown as { g, h, a } (g.seq)}
			<li class="grid grid-cols-[minmax(0,1fr)_8rem_minmax(0,1fr)] items-center gap-2 sm:gap-3 text-sm {g.voided ? 'text-stone-400' : ''}">
				{#each ['home', 'away'] as const as side, i (side)}
					<div class="flex min-w-0 items-center gap-2 {i === 0 ? 'order-1 justify-end text-right' : 'order-3'}">
						{#if g.side === side}
							{#if i === 1}<span class="size-2.5 shrink-0 rounded-full {g.voided ? 'bg-stone-300' : 'bg-away'}"></span>{/if}
							<span class="truncate font-medium {g.voided ? 'line-through' : ''}">{tSide(side)}</span>
							<span class="chip hidden sm:inline-flex">{g.voided ? t('board.voided') : t(('board.source.' + g.source) as Key)}</span>
							{#if i === 0}<span class="size-2.5 shrink-0 rounded-full {g.voided ? 'bg-stone-300' : 'bg-home'}"></span>{/if}
						{/if}
					</div>
				{/each}
				<div class="order-2 rounded-lg bg-stone-50 py-1 text-center font-mono whitespace-nowrap tabular-nums">
					<!-- Fixed widths (monospace), so the colons of time and score form straight lines: the
					     time is right-aligned (seconds always have 2 digits), the score is split at its colon. -->
					<span class="inline-block w-[5ch] text-right text-stone-500 {g.voided ? 'line-through' : ''}">{clock(g.play_time_ms)}</span>
					<span class="ml-1 inline-block w-[5ch] font-semibold text-stone-800">{#if !g.voided}<span
							class="inline-block w-[2ch] text-right">{h}</span>:<span class="inline-block w-[2ch] text-left">{a}</span>{/if}</span>
				</div>
			</li>
		{/each}
	</ol>
	{#if rows.length > FOLD}
		<div class="mt-3 text-center">
			<button class="btn btn-sm" onclick={() => (all = !all)}>{all ? t('board.show_less') : t('board.show_all', { n: rows.length })}</button>
		</div>
	{/if}
{/if}
