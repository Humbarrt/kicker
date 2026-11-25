<script lang="ts">
	// Table settings (spec 02 §13, D17): side names and colors, game defaults, inactivity. Admins only.
	import { api, type Preset, type TableSettings } from '#lib/api/client.ts';
	import { t, tErr, tSide, type Key } from '#lib/i18n.svelte.ts';

	let s = $state<TableSettings | null>(null);
	let presets = $state<Preset[]>([]);
	let error = $state('');
	let field = $state('');
	let saved = $state(false);
	let busy = $state(false);

	$effect(() => {
		Promise.all([api.tableSettings(), api.presets()])
			.then(([settings, ps]) => ((s = settings), (presets = ps)))
			.catch((e) => (error = tErr(e)));
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!s) return;
		busy = true;
		error = field = '';
		saved = false;
		try {
			s = await api.saveTableSettings(s);
			saved = true;
			setTimeout(() => (saved = false), 2500);
		} catch (err) {
			error = tErr(err);
			field = (err as { data?: { field?: string } }).data?.field ?? '';
		} finally {
			busy = false;
		}
	}

	const bad = (f: string) => (field === f ? 'border-red-500 ring-1 ring-red-500' : '');
	const firstBallOptions = ['random', 'loser_previous', 'alternate', 'lower_rating', 'home', 'away'] as const;
</script>

<h1 class="h1 mb-4">{t('set.title')}</h1>

{#if !s}
	{#if error}<p class="error">{error}</p>{:else}<p class="muted">{t('app.loading')}</p>{/if}
{:else}
	<form class="space-y-6" onsubmit={save}>
		<section class="card space-y-4">
			<div>
				<h2 class="h2">{t('set.sides')}</h2>
				<p class="muted text-sm">{t('set.sides_hint')}</p>
			</div>
			<div class="grid gap-4 sm:grid-cols-2">
				{#each ['home', 'away'] as const as side (side)}
					<div class="rounded-xl border border-stone-200 p-4" style="border-top: 4px solid {s[side].color}">
						<label class="block"><span class="label">{t('set.name')}</span>
							<input class="input {bad(side + '.name')}" bind:value={s[side].name} maxlength="24"
								placeholder={t(('side.' + side) as Key)} data-testid="name-{side}" /></label>
						<label class="mt-3 flex items-center gap-3"><span class="label mb-0">{t('set.color')}</span>
							<input type="color" class="h-9 w-14 cursor-pointer rounded border border-stone-300 {bad(side + '.color')}"
								bind:value={s[side].color} data-testid="color-{side}" />
							<span class="font-mono text-sm text-stone-500">{s[side].color}</span></label>
					</div>
				{/each}
			</div>
		</section>

		<section class="card space-y-4">
			<h2 class="h2">{t('set.game')}</h2>
			<div class="grid gap-4 sm:grid-cols-2">
				<label class="block"><span class="label">{t('set.preset')}</span>
					<select class="input {bad('current_preset_id')}" bind:value={s.current_preset_id}>
						{#each presets as p (p.id)}<option value={p.id}>{p.label}</option>{/each}
					</select>
					<span class="muted text-xs">{t('set.preset_hint')}</span></label>
				<label class="block"><span class="label">{t('set.kickoff_requires')}</span>
					<select class="input" bind:value={s.kickoff_requires}>
						<option value="both_sides">{t('set.kickoff.both_sides')}</option>
						<option value="any_player">{t('set.kickoff.any_player')}</option>
					</select></label>
				<label class="block"><span class="label">{t('set.kickoff_delay')}</span>
					<span class="flex items-center gap-2"><input class="input w-24 {bad('kickoff_delay_s')}" type="number" min="0" max="60"
						bind:value={s.kickoff_delay_s} data-testid="kickoff-delay" />{t('set.seconds')}</span>
					<span class="muted text-xs">{t('set.kickoff_delay_hint')}</span></label>
				<label class="block"><span class="label">{t('set.first_ball')}</span>
					<select class="input" bind:value={s.first_ball}>
						{#each firstBallOptions as o (o)}
							<option value={o}>{t(('set.first.' + o) as Key, { side: tSide(o) })}</option>
						{/each}
					</select>
					{#if s.first_ball === 'lower_rating'}<span class="muted text-xs">{t('set.first_ball_hint')}</span>{/if}</label>
				<label class="block"><span class="label">{t('set.after_goal')}</span>
					<select class="input" bind:value={s.after_goal_ball}>
						<option value="conceding">{t('set.after.conceding')}</option>
						<option value="scoring">{t('set.after.scoring')}</option>
						<option value="alternate">{t('set.after.alternate')}</option>
					</select>
					<span class="muted text-xs">{t('set.after_goal_hint')}</span></label>
				<label class="block"><span class="label">{t('set.hold')}</span>
					<span class="flex items-center gap-2"><input class="input w-24 {bad('finished_hold_s')}" type="number" min="3" max="120"
						bind:value={s.finished_hold_s} />{t('set.seconds')}</span></label>
			</div>
		</section>

		<section class="card space-y-4">
			<h2 class="h2">{t('set.idle')}</h2>
			<div class="grid gap-4 sm:grid-cols-2">
				<label class="block"><span class="label">{t('set.pause_after')}</span>
					<span class="flex items-center gap-2"><input class="input w-24 {bad('pause_after_minutes')}" type="number" min="1" max="60"
						bind:value={s.pause_after_minutes} />{t('set.minutes')}</span></label>
				<label class="block"><span class="label">{t('set.sleep_after')}</span>
					<span class="flex items-center gap-2"><input class="input w-24 {bad('sleep_after_minutes')}" type="number" min="1" max="60"
						bind:value={s.sleep_after_minutes} />{t('set.minutes')}</span></label>
				<label class="block"><span class="label">{t('set.on_wake')}</span>
					<select class="input" bind:value={s.on_wake}>
						<option value="smart">{t('set.wake.smart')}</option>
						<option value="resume">{t('set.wake.resume')}</option>
						<option value="new_game">{t('set.wake.new_game')}</option>
					</select></label>
				<label class="block"><span class="label">{t('set.abandon_after')}</span>
					<span class="flex items-center gap-2"><input class="input w-24 {bad('abandon_after_hours')}" type="number" min="1" max="720"
						bind:value={s.abandon_after_hours} />{t('set.hours')}</span></label>
			</div>
		</section>

		<div class="flex items-center justify-end gap-3">
			{#if error}<p class="error">{error}</p>{/if}
			{#if saved}<p class="text-sm text-emerald-700">{t('set.saved')}</p>{/if}
			<button class="btn btn-primary" disabled={busy}>{t('set.save')}</button>
		</div>
	</form>
{/if}
