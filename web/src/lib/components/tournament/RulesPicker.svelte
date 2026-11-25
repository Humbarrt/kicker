<script lang="ts">
	// A tournament's game rules, like "New game": one of the presets (default: the built-in
	// "Official") or custom rules. The rules are copied into the tournament, so a later preset
	// edit doesn't change it; presetId only remembers the choice.
	import { onMount } from 'svelte';
	import { api, type Preset, type TournamentRules } from '#lib/api/client.ts';
	import { t, tErr } from '#lib/i18n.svelte.ts';
	import { describeRules } from '#lib/rules.ts';

	let {
		initial = null,
		initialPreset,
		rules = $bindable(),
		presetId = $bindable()
	}: { initial?: TournamentRules | null; initialPreset?: number; rules?: TournamentRules; presetId?: number } = $props();

	let presets = $state<Preset[]>([]);
	let choice = $state(''); // a preset id or 'custom'
	let error = $state('');
	const start = (() => initial)(); // the rules to show first
	let custom = $state({
		win_score: start?.win_score ?? 10,
		win_by: start?.win_by ?? 2,
		max_score: (start?.max_score ?? '') as string | number,
		minutes: (start?.time_limit_s ? start.time_limit_s / 60 : '') as string | number
	});

	onMount(() => {
		api.presets().then((p) => {
			presets = p;
			const saved = initialPreset;
			const official = p.find((x) => x.builtin && x.label === 'Official') ?? p[0];
			if (saved != null && p.some((x) => x.id === saved)) choice = String(saved);
			else if (start) choice = String(p.find((x) => sameRules(fromPreset(x), start))?.id ?? 'custom');
			else choice = official ? String(official.id) : 'custom';
		}).catch((e) => (error = tErr(e)));
	});

	function fromPreset(p: Preset): TournamentRules {
		return { win_score: p.rules.win_score, win_by: p.rules.win_by, max_score: p.rules.max_score ?? undefined, time_limit_s: p.rules.time_limit_s ?? undefined };
	}
	function sameRules(a: TournamentRules, b: TournamentRules): boolean {
		return a.win_score === b.win_score && a.win_by === b.win_by && (a.max_score ?? 0) === (b.max_score ?? 0) && (a.time_limit_s ?? 0) === (b.time_limit_s ?? 0);
	}
	const num = (x: string | number) => (x === '' || x === null ? undefined : Number(x));

	$effect(() => {
		if (choice === 'custom') {
			const m = num(custom.minutes);
			rules = { win_score: Number(custom.win_score), win_by: Number(custom.win_by), max_score: num(custom.max_score), time_limit_s: m ? Math.round(m * 60) : undefined };
			presetId = undefined;
			return;
		}
		const p = presets.find((x) => String(x.id) === choice);
		rules = p ? fromPreset(p) : undefined;
		presetId = p?.id;
	});
</script>

<fieldset class="space-y-2">
	<legend class="label">{t('tourn.rules')}</legend>
	{#each presets as p (p.id)}
		<label class="flex cursor-pointer items-center gap-3 rounded-xl border px-4 py-3 {choice === String(p.id) ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200 hover:bg-stone-50'}">
			<input type="radio" name="t-rules" value={String(p.id)} bind:group={choice} />
			<span class="flex-1">
				<span class="block font-medium">{p.label}</span>
				<span class="muted">{describeRules(p.rules)}</span>
			</span>
		</label>
	{/each}
	<label class="flex cursor-pointer items-center gap-3 rounded-xl border px-4 py-3 {choice === 'custom' ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200 hover:bg-stone-50'}">
		<input type="radio" name="t-rules" value="custom" bind:group={choice} />
		<span class="flex-1">
			<span class="block font-medium">{t('board.custom')}</span>
			<span class="muted">{t('tourn.custom_hint')}</span>
		</span>
	</label>
	{#if choice === 'custom'}
		<div class="grid gap-4 rounded-xl bg-stone-50 p-4 sm:grid-cols-2">
			<label><span class="label">{t('rules.win_score')}</span>
				<input class="input" type="number" min="1" max="99" required bind:value={custom.win_score} /></label>
			<label><span class="label">{t('rules.win_by')}</span>
				<input class="input" type="number" min="1" max="10" required bind:value={custom.win_by} /></label>
			<label><span class="label">{t('rules.max_score')}</span>
				<input class="input" type="number" min="1" max="99" bind:value={custom.max_score} placeholder={t('board.no_cap')} /></label>
			<label><span class="label">{t('rules.time_limit')}</span>
				<input class="input" type="number" min="1" max="1440" bind:value={custom.minutes} placeholder={t('board.no_limit')} /></label>
		</div>
	{/if}
	<p class="muted text-sm">{t('tourn.rules_note')}</p>
	{#if error}<p class="error">{error}</p>{/if}
</fieldset>
