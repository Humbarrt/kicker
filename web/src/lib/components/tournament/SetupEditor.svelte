<script lang="ts">
	// Creating or editing a tournament (spec 09 §4): name, players and teams, the format – chosen
	// from the options sorted by how long they take – its options, and the game rules.
	import X from '@lucide/svelte/icons/x';
	import ArrowUp from '@lucide/svelte/icons/arrow-up';
	import ArrowDown from '@lucide/svelte/icons/arrow-down';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type TournamentRules, type TeamMode, type TournamentDetail, type TournamentOption, type TournamentStage } from '#lib/api/client.ts';
	import { players } from '#lib/players.svelte.ts';
	import { i18n, t, tErr, tPlayer, type Key } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';
	import { estimateText, formatName, optionName } from '#lib/tournaments.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import PlayerPicker from '#lib/components/PlayerPicker.svelte';
	import RulesPicker from './RulesPicker.svelte';

	let { initial = null }: { initial?: TournamentDetail | null } = $props();
	onMount(() => void players.reload()); // the list to pick from

	// --- state, from the tournament being edited or empty
	const init = (() => {
		if (!initial) return { name: t('tourn.name_default', { date: dateTime(Date.now(), i18n.lang) }), mode: 'fixed' as TeamMode, entries: [] as number[][], stages: [] as TournamentStage[], manual: false };
		const s = initial.settings;
		let entries: number[][] = [];
		if (s.team_mode === 'fixed') {
			const byTeam = new Map<number, number[]>();
			for (const p of initial.participants) byTeam.set(p.team ?? 0, [...(byTeam.get(p.team ?? 0) ?? []), p.player_id]);
			entries = [...byTeam.entries()].sort((a, b) => a[0] - b[0]).map((e) => e[1]);
		} else entries = initial.participants.map((p) => [p.player_id]);
		return { name: initial.name, mode: s.team_mode, entries, stages: s.stages, manual: !!s.manual_seeds };
	})();
	let name = $state(init.name);
	let mode = $state<TeamMode>(init.mode);
	let entries = $state<number[][]>(init.entries); // fixed: teams; DYP / Monster DYP: one player each
	let stages = $state<TournamentStage[]>(init.stages);
	let manualSeeds = $state(init.manual);

	// --- game rules (RulesPicker): a preset, copied into the tournament, or custom rules.
	let rules = $state<TournamentRules | undefined>();
	let presetId = $state<number | undefined>();
	let error = $state('');
	let busy = $state(false);

	const chosen = $derived(entries.flat());
	const count = $derived(mode === 'fixed' ? entries.length : mode === 'dyp' ? Math.ceil(chosen.length / 2) : chosen.length);
	const minimum = $derived(mode === 'monster' ? 4 : 2);

	function setMode(m: TeamMode) {
		if (m === mode) return;
		entries = entries.flat().map((id) => [id]); // teams are made again
		if (m === 'monster' || mode === 'monster') stages = [];
		mode = m;
	}
	// The picker: a team of up to two (fixed teams), or any number of players – one entry each
	// (DYP, Monster DYP, or 1-player teams).
	let picking = $state<'' | 'team' | 'singles'>('');
	let pickerOpen = $state(false);
	$effect(() => {
		if (!pickerOpen) picking = '';
	});
	function openPicker(kind: 'team' | 'singles') {
		picking = kind;
		pickerOpen = true;
	}
	const taken = $derived.by(() => {
		const m = new Map<number, string>();
		entries.forEach((team, i) => team.forEach((id) => m.set(id, mode === 'fixed' ? t('tourn.team', { n: i + 1 }) : t('pick.added'))));
		return m;
	});
	function picked(ids: number[]) {
		entries = picking === 'team' ? [...entries, ids] : [...entries, ...ids.map((id) => [id])];
	}
	const compact = $derived(!manualSeeds && entries.length > 0 && entries.every((e) => e.length === 1));
	function remove(ti: number, id: number) {
		const team = entries[ti].filter((x) => x !== id);
		entries = team.length ? entries.with(ti, team) : entries.filter((_, i) => i !== ti);
	}
	function move(ti: number, d: number) {
		const j = ti + d;
		if (j < 0 || j >= entries.length) return;
		const next = [...entries];
		[next[ti], next[j]] = [next[j], next[ti]];
		entries = next;
	}

	// --- format options for the current number of teams, sorted by duration
	let options = $state<TournamentOption[]>([]);
	let optionsFor = init.stages.length ? 'saved' : ''; // the team count the stages were last fitted to ('saved': as stored)
	$effect(() => {
		const n = count,
			monster = mode === 'monster',
			r = rules; // the estimates follow the rules
		if (n < minimum || !r) {
			options = [];
			return;
		}
		api.tournamentOptions(n, monster, $state.snapshot(r)).then((o) => {
			options = o;
			const key = `${n}/${monster}`;
			const keep = key === optionsFor || optionsFor === 'saved'; // only the rules changed, or the stored setup
			optionsFor = key;
			if (keep && stages.length) return;
			const match = o.find((x) => same(x.stages));
			if (match) {
				// The same format for the new number of teams: its defaults (e.g. a 3rd place game only
				// from 4 teams on), but the games per pairing chosen so far.
				const old = stages;
				stages = structuredClone(match.stages).map((st, i) => ({ ...st, best_of: old[i]?.best_of ?? st.best_of, rounds: old[i]?.rounds }));
			} else if (!stages.length && o.length) stages = structuredClone(o[0].stages);
		}).catch((e) => (error = tErr(e)));
	});
	function same(s: TournamentStage[]): boolean {
		return s.length === stages.length && s.every((x, i) => x.format === stages[i].format && (x.groups ?? 0) === (stages[i].groups ?? 0));
	}
	function choose(o: TournamentOption) {
		stages = $state.snapshot(o.stages); // options is reactive state: structuredClone can't copy its proxies
	}

	async function save() {
		busy = true;
		error = '';
		const input = {
			name,
			settings: { team_mode: mode, manual_seeds: manualSeeds, preset_id: presetId, stages: stages.map((s) => ({ ...s, rules })) },
			participants:
				mode === 'fixed'
					? entries.flatMap((team, i) => team.map((id) => ({ player_id: id, team: i + 1 })))
					: chosen.map((id) => ({ player_id: id }))
		};
		try {
			const d = initial ? await api.updateTournament(initial.id, input) : await api.createTournament(input);
			await goto(`/tournaments/${d.id}`);
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}
</script>

<div class="space-y-6">
	<section class="card space-y-4">
		<div>
			<label class="label" for="t-name">{t('tourn.name')}</label>
			<input id="t-name" class="input" bind:value={name} maxlength="80" />
		</div>
		<fieldset>
			<legend class="label">{t('tourn.mode')}</legend>
			<div class="grid gap-2 sm:grid-cols-3">
				{#each ['fixed', 'dyp', 'monster'] as const as m (m)}
					<label class="cursor-pointer rounded-xl border p-3 {mode === m ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200'}">
						<input type="radio" class="sr-only" name="mode" checked={mode === m} onchange={() => setMode(m)} />
						<span class="block font-medium">{t(('tourn.mode.' + m) as Key)}</span>
						<span class="block text-xs text-stone-500">{t(('tourn.mode.' + m + '_hint') as Key)}</span>
					</label>
				{/each}
			</div>
		</fieldset>
	</section>

	<section class="card space-y-3">
		<div class="flex flex-wrap items-center gap-3">
			<h2 class="h2 flex-1">{t('tourn.participants')}</h2>
			<span class="muted">{mode === 'fixed' ? t('tourn.teams_count', { n: entries.length }) : t('tourn.players_count', { n: chosen.length })}</span>
		</div>
		<!-- Single players as a wrapping list of chips; teams as rows (one column when the order is the seeding). -->
		<ul class={compact ? 'flex flex-wrap gap-2' : manualSeeds ? 'space-y-2' : 'grid gap-2 sm:grid-cols-2'} data-testid="setup-entries">
			{#each entries as team, ti (team.join('-') + '/' + ti)}
				<li class={compact ? 'contents' : 'flex flex-wrap items-center gap-2 rounded-xl border border-stone-200 px-3 py-2'}>
					{#if mode === 'fixed' && !compact}<span class="w-14 shrink-0 text-xs text-stone-500">{t('tourn.team', { n: ti + 1 })}</span>{/if}
					{#each team as id, pi (pi)}
						{@const p = players.get(id)}
						<span class="chip gap-1.5 bg-stone-100 py-1 pr-1">
							{#if p}<PlayerAvatar player={p} size={20} />{tPlayer(p)}{/if}
							<button class="rounded-full px-1 text-stone-400 hover:text-red-600" aria-label={t('app.remove')} onclick={() => remove(ti, id)}><X class="size-3.5" aria-hidden="true" /></button>
						</span>
					{/each}
					{#if !compact}<span class="flex-1"></span>{/if}
					{#if manualSeeds}
						<button class="btn btn-sm" aria-label={t('app.move_up')} disabled={ti === 0} onclick={() => move(ti, -1)}><ArrowUp class="size-4" aria-hidden="true" /></button>
						<button class="btn btn-sm" aria-label={t('app.move_down')} disabled={ti === entries.length - 1} onclick={() => move(ti, 1)}><ArrowDown class="size-4" aria-hidden="true" /></button>
					{/if}
				</li>
			{/each}
		</ul>
		<div class="flex flex-wrap items-center gap-2">
			{#if mode === 'fixed'}
				<button class="btn btn-primary" onclick={() => openPicker('team')} data-testid="add-team">+ {t('tourn.add_team')}</button>
				<button class="btn" onclick={() => openPicker('singles')} data-testid="add-singles">+ {t('tourn.add_singles')}</button>
			{:else}
				<button class="btn btn-primary" onclick={() => openPicker('singles')} data-testid="add-players">+ {t('tourn.add_players')}</button>
			{/if}
			{#if entries.length}
				<span class="flex-1"></span>
				<button class="btn btn-sm" onclick={() => (entries = [])} data-testid="remove-all">{t('tourn.remove_all')}</button>
			{/if}
		</div>
		{#if mode === 'fixed'}<p class="muted">{t('tourn.mode.fixed_hint')}</p>{/if}
		<PlayerPicker bind:open={pickerOpen} max={picking === 'team' ? 2 : 0} {taken} onpick={picked}
			title={picking === 'team' ? t('tourn.add_team') : mode === 'fixed' ? t('tourn.add_singles') : t('tourn.add_players')}
			hint={picking === 'team' ? t('tourn.add_team_hint') : mode === 'fixed' ? t('tourn.add_singles_hint') : ''}
			addLabel={(n) => (picking === 'team' ? t('tourn.add_team') : t('tourn.add_n', { n }))} />
		{#if mode !== 'dyp'}
			<label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={manualSeeds} /> {t('tourn.manual_seeds')}</label>
		{/if}
	</section>

	<section class="card space-y-3">
		<div>
			<h2 class="h2">{t('tourn.formats')}</h2>
			<p class="muted">{count < minimum ? t('tourn.need_more', { n: minimum }) : t('tourn.formats_hint')}</p>
		</div>
		{#if options.length}
			<div class="grid gap-2 sm:grid-cols-2" data-testid="format-options">
				{#each options as o, i (i)}
					<label data-option={o.name} class="cursor-pointer rounded-xl border p-3 {same(o.stages) ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200'}">
						<input type="radio" class="sr-only" name="format" checked={same(o.stages)} onchange={() => choose(o)} />
						<span class="block font-medium">{optionName(o)}</span>
						<span class="block text-xs text-stone-500">{estimateText(o.estimate)} · {t('tourn.min_games', { n: o.estimate.min_games_per_team })}</span>
					</label>
				{/each}
			</div>
		{/if}

		{#each stages as s, i (i)}
			<div class="rounded-xl bg-stone-50 p-3">
				<p class="mb-2 text-sm font-medium">{stages.length > 1 ? t('tourn.stage', { n: i + 1 }) + ': ' : ''}{formatName(s.format)}{s.groups ? ` (${s.groups})` : ''}</p>
				<div class="flex flex-wrap items-end gap-4 text-sm">
					{#if s.format !== 'groups'}
						<label>
							<span class="label">{t('tourn.games_per_pairing')}</span>
							<select class="input w-auto" bind:value={s.best_of}>
								{#each [1, 3, 5] as n (n)}<option value={n}>{n === 1 ? '1' : t('tourn.best_of', { n })}</option>{/each}
							</select>
						</label>
					{/if}
					{#if s.format === 'knockout'}
						<label class="flex items-center gap-2"><input type="checkbox" bind:checked={s.third_place} /> {t('tourn.third_place')}</label>
					{/if}
					{#if s.format === 'groups'}
						<label>
							<span class="label">{t('tourn.advance')}</span>
							<select class="input w-auto" bind:value={s.advance}>{#each [1, 2, 3, 4] as n (n)}<option value={n}>{n}</option>{/each}</select>
						</label>
						<label>
							<span class="label">&nbsp;</span>
							<select class="input w-auto" bind:value={s.seeding}>
								<option value="crossed">{t('tourn.seeding.crossed')}</option>
								<option value="reseed">{t('tourn.seeding.reseed')}</option>
							</select>
						</label>
						<label class="flex items-center gap-2"><input type="checkbox" bind:checked={s.fill_thirds} /> {t('tourn.fill_thirds')}</label>
					{/if}
					{#if s.format === 'swiss' || s.format === 'monster_dyp'}
						<label>
							<span class="label">{t('tourn.rounds')}</span>
							<input class="input w-20" type="number" min="1" max="30" bind:value={s.rounds} placeholder="auto" />
						</label>
					{/if}
				</div>
			</div>
		{/each}

		<RulesPicker initial={init.stages[0]?.rules ?? null} initialPreset={initial?.settings.preset_id} bind:rules bind:presetId />
	</section>

	{#if error}<p class="error">{error}</p>{/if}
	<div class="flex justify-end gap-2">
		<a class="btn" href={initial ? `/tournaments/${initial.id}` : '/tournaments'}>{t('app.cancel')}</a>
		<button class="btn btn-primary" disabled={busy || !name.trim() || count < minimum || !stages.length} onclick={save} data-testid="save-tournament">
			{initial ? t('tourn.save') : t('tourn.create')}
		</button>
	</div>
</div>
