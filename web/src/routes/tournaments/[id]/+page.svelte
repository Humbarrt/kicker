<script lang="ts">
	// A tournament (spec 09 §12): its state and actions, what's on the table and ready to play,
	// the podium, every stage – bracket, groups, standings, pairings – and its history.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, type TournamentDetail, type TPairing, type TStage } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { i18n, t, tErr, tPlayer, type Key } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';
	import { estimateText, formatName, pairingByKey, pairingLabel, stagesName } from '#lib/tournaments.ts';
	import Bracket from '#lib/components/tournament/Bracket.svelte';
	import Standings from '#lib/components/tournament/Standings.svelte';
	import Pairing from '#lib/components/tournament/Pairing.svelte';
	import Team from '#lib/components/tournament/Team.svelte';
	import TournamentChanges from '#lib/components/tournament/TournamentChanges.svelte';

	const id = $derived(Number(page.params.id));
	let d = $state<TournamentDetail | null>(null);
	let error = $state('');
	let busy = $state(false);

	$effect(() => {
		void live.tournamentRev;
		api.tournament(id).then((x) => (d = x)).catch((e) => (error = tErr(e)));
	});

	async function act(f: () => Promise<TournamentDetail | void>, confirmText = '') {
		if (confirmText && !confirm(confirmText)) return;
		busy = true;
		error = '';
		try {
			const x = await f();
			if (x) d = x;
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}

	const staged = $derived(d?.staged ? pairingByKey(d.stages, d.staged) : undefined);
	const ready = $derived(d ? d.ready.map((k) => pairingByKey(d!.stages, k)!).filter((p) => p && p.key !== d!.staged) : []);
	const stateClass: Record<string, string> = {
		running: 'bg-emerald-100 text-emerald-800',
		paused: 'bg-amber-100 text-amber-900',
		setup: 'bg-sky-100 text-sky-800'
	};
	const medal = ['', '🥇', '🥈', '🥉'];

	/** Pairings of a round-based stage by round, newest round first. */
	function rounds(stage: TStage, group = ''): { round: number; pairings: TPairing[] }[] {
		const ps = stage.pairings.filter((p) => !group || p.bracket === group);
		const rs = [...new Set(ps.map((p) => p.round))].sort((a, b) => b - a);
		return rs.map((r) => ({ round: r, pairings: ps.filter((p) => p.round === r && p.state !== 'bye') }));
	}
	function playerNames(ids: number[]): string {
		return ids.map((i) => (players.get(i) ? tPlayer(players.get(i)!) : '…')).join(', ');
	}
	// A history entry with what it changed (spec 09 §11).
	function eventText(e: TournamentDetail['events'][number]): string {
		const x = e.details as Record<string, unknown>;
		const base = t(('tourn.event.' + e.kind) as Key);
		const name = (id: unknown) => (players.get(id as number) ? tPlayer(players.get(id as number)!) : '…');
		switch (e.kind) {
			case 'paused':
				return x.reason === 'sleep' ? `${base} · ${t('tourn.paused_by_sleep')}` : base;
			case 'player_replaced':
				return `${base}: ${name(x.old_player)} → ${name(x.new_player)}`;
			case 'dropout':
				return `${base}: ${playerNames((x.players as number[]) ?? [])}${x.annul ? ' · ' + t('tourn.annulled') : ''}`;
			case 'rebuilt':
				return `${base}: ${t('tourn.replayed', { n: ((x.pairings as string[]) ?? []).length })}`;
			case 'edited':
				return x.name ? `${t('tourn.event.renamed')}: ${x.name}` : x.rules_from_stage ? t('tourn.event.rules_changed') : base;
		}
		return base;
	}
	function advanceOf(stageIndex: number): number {
		const s = d?.settings.stages[stageIndex];
		return s?.format === 'groups' ? (s.advance ?? 2) : 0;
	}
</script>

<main class="mx-auto max-w-6xl space-y-6 px-4 py-6">
	{#if error}<p class="error">{error}</p>{/if}
	{#if d}
		<section class="card space-y-3">
			<div class="flex flex-wrap items-center gap-3">
				<h1 class="h1 min-w-0 flex-1 truncate">{d.name}</h1>
				<span class="chip {stateClass[d.state] ?? ''}" data-testid="tournament-state">{t(('tourn.state.' + d.state) as Key)}</span>
			</div>
			<p class="muted">
				{stagesName(d.settings.stages)} · {t(('tourn.mode.' + d.settings.team_mode) as Key)}
				{#if d.estimate} · {estimateText(d.estimate)}{/if}
			</p>
			<div class="flex flex-wrap gap-2">
				{#if d.state === 'setup' && d.can.edit}<a class="btn" href="/tournaments/{d.id}/edit">{t('tourn.edit')}</a>{/if}
				{#if d.state === 'setup' && d.can.run}
					<button class="btn btn-primary" disabled={busy} onclick={() => act(() => api.tournamentAction(d!.id, 'start'))}>{t('tourn.start')}</button>
				{/if}
				{#if d.state === 'running' && d.can.run}
					<button class="btn" disabled={busy} onclick={() => act(() => api.tournamentAction(d!.id, 'pause'))}>{t('tourn.pause')}</button>
				{/if}
				{#if d.state === 'paused' && d.can.run}
					<button class="btn btn-primary" disabled={busy} onclick={() => act(() => api.tournamentAction(d!.id, 'continue'))}>{t('tourn.continue')}</button>
				{/if}
				{#if (d.state === 'running' || d.state === 'paused') && d.can.manage}
					<button class="btn btn-danger" disabled={busy} onclick={() => act(() => api.tournamentAction(d!.id, 'abandon'), t('tourn.confirm_abandon'))}>{t('tourn.abandon')}</button>
				{/if}
				{#if d.state === 'abandoned' && d.can.manage}
					<button class="btn" disabled={busy} onclick={() => act(() => api.tournamentAction(d!.id, 'reinstate'))}>{t('tourn.reinstate')}</button>
				{/if}
				{#if (d.state === 'setup' || d.state === 'abandoned') && d.can.manage}
					<button class="btn btn-danger" disabled={busy}
						onclick={() => act(async () => { await api.deleteTournament(d!.id); await goto('/tournaments'); }, t('tourn.confirm_delete'))}>{t('tourn.delete')}</button>
				{/if}
			</div>
		</section>

		{#if d.conflicts.length}
			<section class="notice space-y-2" data-testid="conflicts">
				<p class="font-medium">{t('tourn.conflicts', { n: d.conflicts.length })}</p>
				<p class="text-sm">{t('tourn.conflicts_hint')}</p>
				{#if d.can.manage}
					<button class="btn btn-sm btn-primary" disabled={busy || !!d.staged} data-testid="rebuild"
						onclick={() => act(() => api.rebuildTournament(d!.id), t('tourn.confirm_rebuild'))}>{t('tourn.rebuild')}</button>
				{/if}
			</section>
		{/if}

		{#if d.placements.length}
			<section class="card" data-testid="podium">
				<h2 class="h2 mb-3">{t('tourn.placements')}</h2>
				<ol class="grid gap-2 sm:grid-cols-3">
					{#each d.placements as p (p.place + '-' + p.players.join())}
						<li class="flex items-center gap-3 rounded-xl bg-stone-50 px-3 py-2">
							<span class="text-2xl">{medal[p.place]}</span>
							<Team entrant={{ team: p.team, players: p.players }} size={28} winner={p.place === 1} link />
						</li>
					{/each}
				</ol>
			</section>
		{/if}

		{#if d.state === 'running' && (staged || ready.length)}
			<section class="card space-y-3" data-testid="ready">
				{#if staged}
					<p class="text-xs font-semibold tracking-wide text-emerald-700 uppercase">{t('tourn.on_table')}</p>
					<Pairing pairing={staged} label={pairingLabel(d.stages, staged)} here={d.checked_in} highlight="border-emerald-300 bg-emerald-50" />
				{/if}
				{#if ready.length}
					<p class="text-xs font-semibold tracking-wide text-stone-500 uppercase">{t('tourn.ready')}</p>
					<div class="grid gap-2 md:grid-cols-2">
						{#each ready as p (p.key)}
							<Pairing pairing={p} label={(p.key === d.suggested ? t('tourn.next') + ' · ' : '') + pairingLabel(d.stages, p)}
								here={d.checked_in} {busy} highlight={p.key === d.suggested ? 'border-stone-300 bg-stone-50' : ''}
								onplay={d.can.play && !staged ? () => act(() => api.playPairing(d!.id, p.key)) : undefined} />
						{/each}
					</div>
					<p class="muted">{t('tourn.ready_hint')}</p>
				{/if}
			</section>
		{/if}

		{#if d.state === 'setup' && !d.stages.length}
			<section class="card space-y-2">
				<h2 class="h2">{t('tourn.participants')}</h2>
				<p class="muted">{playerNames(d.participants.map((p) => p.player_id))}</p>
				{#if d.settings.team_mode !== 'fixed'}<p class="muted">{t('tourn.drawn_at_start')}</p>{/if}
			</section>
		{/if}
		{#if d.preview}<p class="notice">{t('tourn.preview')}</p>{/if}

		{#each d.stages as stage (stage.index)}
			<section class="card space-y-4" data-testid="stage">
				<h2 class="h2">
					{d.stages.length > 1 ? t('tourn.stage', { n: stage.index + 1 }) + ': ' : ''}{formatName(stage.format)}
					{#if stage.best_of > 1}<span class="chip ml-2 align-middle">{t('tourn.best_of', { n: stage.best_of })}</span>{/if}
				</h2>
				{#if stage.format === 'knockout' || stage.format === 'double_knockout'}
					<Bracket {stage} staged={d.staged} here={d.checked_in} conflicts={d.conflicts} />
				{:else if stage.format === 'groups'}
					<div class="grid gap-4 lg:grid-cols-2">
						{#each stage.groups as g (g.name)}
							<div class="space-y-2 rounded-xl border border-stone-200 p-3">
								<h3 class="font-semibold">{t('tourn.group', { g: g.name })}</h3>
								<Standings rows={g.standings} perGame qualify={advanceOf(stage.index)} />
								<details>
									<summary class="cursor-pointer text-sm text-stone-600">{t('tourn.pairings')}</summary>
									<div class="mt-2 space-y-2">
										{#each rounds(stage, g.name) as r (r.round)}
											{#each r.pairings as p (p.key)}<Pairing pairing={p} label={t('tourn.round', { n: r.round })} here={d.checked_in} />{/each}
										{/each}
									</div>
								</details>
							</div>
						{/each}
					</div>
				{:else}
					<Standings rows={stage.standings} byPlayer={stage.format === 'monster_dyp'} buchholz={stage.format !== 'round_robin'} />
					<div class="space-y-3">
						{#each rounds(stage) as r (r.round)}
							<div>
								<p class="mb-1 text-xs font-medium text-stone-500">{t('tourn.round', { n: r.round })}</p>
								<div class="grid gap-2 md:grid-cols-2">
									{#each r.pairings as p (p.key)}<Pairing pairing={p} here={d.checked_in} />{/each}
								</div>
								{#if stage.sat_out?.[r.round - 1]?.length}
									<p class="mt-1 text-xs text-stone-500">{t('tourn.sat_out', { names: playerNames(stage.sat_out[r.round - 1]) })}</p>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			</section>
		{/each}

		{#if d.can.change}
			<TournamentChanges {d} onchange={(x) => (d = x)} />
		{/if}

		<section class="card">
			<h2 class="h2 mb-2">{t('tourn.history')}</h2>
			<ul class="space-y-1 text-sm">
				{#each [...d.events].reverse() as e, i (i)}
					<li class="flex gap-3">
						<span class="w-44 shrink-0 whitespace-nowrap text-stone-500 tabular-nums">{dateTime(e.at, i18n.lang)}</span>
						<span>{eventText(e)}</span>
					</li>
				{/each}
			</ul>
		</section>
	{/if}
</main>
