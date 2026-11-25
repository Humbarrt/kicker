<script lang="ts">
	// Changing a running or paused tournament (spec 09 §10–11, tournaments.edit): rename it and
	// set the rules of stages not started yet, replace a player, let a team (in Monster DYP: a
	// player) drop out.
	import { api, type TEntrant, type TournamentDetail, type TournamentRules } from '#lib/api/client.ts';
	import { players } from '#lib/players.svelte.ts';
	import { t, tErr, tPlayer } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import PlayerPicker from '#lib/components/PlayerPicker.svelte';
	import RulesPicker from './RulesPicker.svelte';

	let { d, onchange }: { d: TournamentDetail; onchange: (d: TournamentDetail) => void } = $props();

	let busy = $state(false);
	let error = $state('');
	async function act(f: () => Promise<TournamentDetail>, confirmText = '') {
		if (confirmText && !confirm(confirmText)) return;
		busy = true;
		error = '';
		try {
			onchange(await f());
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}

	const monster = $derived(d.settings.team_mode === 'monster');
	const current = $derived(d.participants.filter((p) => p.replaced_by == null));
	// The participant playing for a player of an entrant now.
	function participantOf(e: TEntrant, id: number) {
		if (monster) return current[(e.slots?.[0] ?? 0) - 1];
		return current.find((p) => p.team === e.team && p.player_id === id);
	}
	// Who a participant took the place of ("for Tom"), if anyone.
	function replacedOf(pid: number | undefined): string {
		const old = d.participants.filter((p) => p.replaced_by === pid).map((p) => players.get(p.player_id));
		return old.filter(Boolean).map((p) => tPlayer(p!)).join(', ');
	}
	const entrantNo = (e: TEntrant) => (monster ? (e.slots?.[0] ?? 0) : e.team);
	const label = (e: TEntrant) => (monster ? '' : t('tourn.team', { n: e.team }));

	// --- replacing
	let replacing = $state<number | null>(null); // participant id
	let pickerOpen = $state(false);
	const taken = $derived.by(() => {
		const m = new Map<number, string>();
		for (const p of current) m.set(p.player_id, monster ? t('pick.added') : t('tourn.team', { n: p.team ?? 0 }));
		return m;
	});
	function replace(pid: number) {
		replacing = pid;
		pickerOpen = true;
	}

	// --- name and rules
	let editing = $state(false);
	let name = $state('');
	let rules = $state<TournamentRules | undefined>();
	let presetId = $state<number | undefined>();
	const laterStages = $derived(d.stages.length < d.settings.stages.length);
	function edit() {
		name = d.name;
		editing = true;
	}
</script>

<section class="card space-y-3" data-testid="tournament-changes">
	<div class="flex flex-wrap items-center gap-2">
		<h2 class="h2 flex-1">{t('tourn.changes')}</h2>
		<button class="btn btn-sm" onclick={edit} data-testid="change-tournament">{t('tourn.change_name_rules')}</button>
	</div>

	{#if editing}
		<div class="space-y-3 rounded-xl bg-stone-50 p-4">
			<label class="block"><span class="label">{t('tourn.name')}</span>
				<input class="input" bind:value={name} maxlength="80" /></label>
			{#if laterStages}
				<p class="muted text-sm">{t('tourn.rules_later', { n: d.stages.length + 1 })}</p>
				<RulesPicker initial={d.settings.stages[d.stages.length]?.rules ?? null} initialPreset={d.settings.preset_id} bind:rules bind:presetId />
			{/if}
			<div class="flex justify-end gap-2">
				<button class="btn" onclick={() => (editing = false)}>{t('app.cancel')}</button>
				<button class="btn btn-primary" disabled={busy || !name.trim()}
					onclick={() => act(async () => {
						const x = await api.changeTournament(d.id, { name, ...(laterStages && rules ? { rules, preset_id: presetId } : {}) });
						editing = false;
						return x;
					})}>{t('tourn.save')}</button>
			</div>
		</div>
	{/if}

	<p class="muted text-sm">{t(monster ? 'tourn.changes_hint_monster' : 'tourn.changes_hint')}</p>
	<ul class="grid gap-2 md:grid-cols-2" data-testid="change-teams">
		{#each d.teams as e (entrantNo(e))}
			<li class="flex flex-wrap items-center gap-2 rounded-xl border border-stone-200 px-3 py-2 {e.dropped ? 'opacity-60' : ''}">
				{#if label(e)}<span class="w-14 shrink-0 text-xs text-stone-500">{label(e)}</span>{/if}
				<span class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
					{#each e.players as id, i (i)}
						{@const p = players.get(id)}
						{@const part = participantOf(e, id)}
						{@const was = replacedOf(part?.id)}
						<span class="chip gap-1.5 bg-stone-100 py-1 pr-1">
							{#if p}<PlayerAvatar player={p} size={20} />{tPlayer(p)}{/if}
							{#if was}<span class="text-xs whitespace-nowrap text-stone-500">{t('tourn.for', { name: was })}</span>{/if}
							{#if part && !e.dropped}
								<button class="rounded-full px-1.5 text-xs text-stone-500 hover:bg-stone-200 hover:text-stone-900" disabled={busy}
									onclick={() => replace(part.id)} data-testid="replace">{t('tourn.replace')}</button>
							{/if}
						</span>
					{/each}
				</span>
				{#if e.dropped}
					<span class="chip bg-stone-100 text-stone-500">{t('tourn.dropped')}</span>
				{:else}
					<button class="btn btn-sm" disabled={busy} data-testid="drop-out"
						onclick={() => act(() => api.dropOut(d.id, entrantNo(e)), t(monster ? 'tourn.confirm_dropout_player' : 'tourn.confirm_dropout'))}>
						{t('tourn.drop_out')}</button>
				{/if}
			</li>
		{/each}
	</ul>
	{#if error}<p class="error">{error}</p>{/if}
</section>

<PlayerPicker bind:open={pickerOpen} max={1} {taken} title={t('tourn.replace_title')} hint={t('tourn.replace_hint')}
	onpick={(ids) => act(() => api.replacePlayer(d.id, replacing!, ids[0]))} />
