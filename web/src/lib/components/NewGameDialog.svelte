<script lang="ts">
	// "New game": pick the rules – the current preset, another preset (table.preset) or custom
	// rules for this game only (table.rules) – and start.
	import { api, type Preset, type Rules } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr } from '#lib/i18n.svelte.ts';
	import { describeRules } from '#lib/rules.ts';

	let { open = $bindable(false), running }: { open: boolean; running: boolean } = $props();

	let dialog: HTMLDialogElement | undefined = $state();
	let presets = $state<Preset[]>([]);
	let choice = $state<string>('');
	let custom = $state({ win_score: 10, win_by: 2, max_score: '' as string | number, minutes: '' as string | number, tie: 'golden_goal' as Rules['time_tie'] });
	let busy = $state(false);
	let error = $state('');

	const canPreset = $derived(session.can('table.preset'));
	const canRules = $derived(session.can('table.rules'));

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			error = '';
			api.presets().then((p) => {
				presets = p;
				choice = String(live.table?.game?.preset_id ?? p[0]?.id ?? '');
			}).catch(() => {});
			dialog.showModal();
		} else if (!open && dialog.open) dialog.close();
	});

	async function start(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			const num = (x: string | number) => (x === '' || x === null ? null : Number(x));
			let v;
			if (choice === 'custom') {
				const minutes = num(custom.minutes);
				v = await api.start({
					rules: {
						win_score: Number(custom.win_score),
						win_by: Number(custom.win_by),
						max_score: num(custom.max_score),
						time_limit_s: minutes ? Math.round(minutes * 60) : null,
						time_tie: custom.tie
					}
				});
			} else {
				v = await api.start(canPreset && choice ? { preset_id: Number(choice) } : {});
			}
			live.setTable(v);
			open = false;
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<dialog bind:this={dialog} onclose={() => (open = false)}
	class="m-auto w-[min(36rem,calc(100vw-2rem))] rounded-2xl bg-white p-0 shadow-xl backdrop:bg-stone-900/40">
	<form class="flex max-h-[85vh] flex-col" onsubmit={start}>
		<div class="border-b border-stone-100 px-6 py-4">
			<h2 class="h2">{t('board.new_game')}</h2>
		</div>
		<div class="space-y-3 overflow-y-auto px-6 py-5">
			{#if running}<p class="notice">{t('board.running_note')}</p>{/if}

			{#if canPreset || canRules}
				<fieldset class="space-y-2">
					<legend class="label">{t('board.preset')}</legend>
					{#each presets as p (p.id)}
						{#if canPreset || String(p.id) === choice}
							<label class="flex cursor-pointer items-center gap-3 rounded-xl border px-4 py-3 {choice === String(p.id) ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200 hover:bg-stone-50'}">
								<input type="radio" name="rules" value={String(p.id)} bind:group={choice} />
								<span class="flex-1">
									<span class="block font-medium">{p.label}</span>
									<span class="muted">{describeRules(p.rules)}</span>
								</span>
							</label>
						{/if}
					{/each}
					{#if canRules}
						<label class="flex cursor-pointer items-center gap-3 rounded-xl border px-4 py-3 {choice === 'custom' ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200 hover:bg-stone-50'}">
							<input type="radio" name="rules" value="custom" bind:group={choice} />
							<span class="flex-1">
								<span class="block font-medium">{t('board.custom')}</span>
								<span class="muted">{t('board.custom_hint')}</span>
							</span>
						</label>
					{/if}
				</fieldset>
			{/if}

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
					{#if custom.minutes}
						<label class="sm:col-span-2"><span class="label">{t('rules.time_tie')}</span>
							<select class="input" bind:value={custom.tie}>
								<option value="golden_goal">{t('rules.tie.golden_goal')}</option>
								<option value="draw">{t('rules.tie.draw')}</option>
							</select></label>
					{/if}
				</div>
			{/if}
			{#if error}<p class="error">{error}</p>{/if}
		</div>
		<div class="flex justify-end gap-2 border-t border-stone-100 px-6 py-4">
			<button type="button" class="btn" onclick={() => (open = false)}>{t('app.cancel')}</button>
			<button class="btn btn-primary" disabled={busy}>{running ? t('board.start_new') : t('board.start')}</button>
		</div>
	</form>
</dialog>
