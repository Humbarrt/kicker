<script lang="ts">
	// The scoreboard in tournament mode (spec 09 §8): the tournament, the pairing on the table,
	// the next one and the other ready pairings with "Play"; or "Continue tournament?" when one is
	// paused.
	import { api } from '#lib/api/client.ts';
	import { activeTournament } from '#lib/tournament.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr } from '#lib/i18n.svelte.ts';
	import { pairingByKey, pairingLabel, stagesName } from '#lib/tournaments.ts';
	import Pairing from './Pairing.svelte';

	let busy = $state(false);
	let error = $state('');
	let dismissed = $state(0);

	$effect(() => activeTournament.track());

	const tour = $derived(activeTournament.running);
	const staged = $derived(tour?.staged ? pairingByKey(tour.stages, tour.staged) : undefined);
	const ready = $derived(tour ? tour.ready.map((k) => pairingByKey(tour.stages, k)!).filter((p) => p && p.key !== tour.staged) : []);
	const paused = $derived(activeTournament.paused);

	async function act(f: () => Promise<unknown>) {
		busy = true;
		error = '';
		try {
			await f();
			await activeTournament.load();
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}
</script>

{#if tour}
	<section class="card space-y-3 border-emerald-200" data-testid="tournament-panel">
		<div class="flex flex-wrap items-baseline gap-2">
			<span class="chip bg-emerald-100 text-emerald-800">{t('tourn.state.running')}</span>
			<a href="/tournaments/{tour.id}" class="h2 hover:underline">{tour.name}</a>
			<span class="muted">{stagesName(tour.settings.stages)}</span>
		</div>
		{#if staged}
			<div>
				<p class="mb-1 text-xs font-semibold tracking-wide text-emerald-700 uppercase">{t('tourn.on_table')}</p>
				<Pairing pairing={staged} label={pairingLabel(tour.stages, staged)} here={tour.checked_in} highlight="border-emerald-300 bg-emerald-50" />
			</div>
		{/if}
		{#if ready.length}
			<div class="space-y-2">
				<p class="text-xs font-semibold tracking-wide text-stone-500 uppercase">{staged ? t('tourn.ready') : t('tourn.next')}</p>
				{#each ready as p, i (p.key)}
					<Pairing pairing={p} label={pairingLabel(tour.stages, p)} here={tour.checked_in} {busy}
						highlight={!staged && i === 0 ? 'border-stone-300 bg-stone-50' : ''}
						onplay={tour.can.play && !staged ? () => act(() => api.playPairing(tour.id, p.key)) : undefined} />
				{/each}
				<p class="muted">{t('tourn.ready_hint')}</p>
			</div>
		{:else if !staged}
			<p class="muted">{t('tourn.waiting_round')}</p>
		{/if}
		{#if error}<p class="error">{error}</p>{/if}
	</section>
{:else if paused && paused.id !== dismissed && session.can('view.tournaments')}
	<section class="notice flex flex-wrap items-center gap-3" data-testid="tournament-continue">
		<span class="flex-1">{t('tourn.continue_prompt', { name: paused.name })}</span>
		<a class="btn btn-sm" href="/tournaments/{paused.id}">{t('tourn.title')}</a>
		<button class="btn btn-sm" onclick={() => (dismissed = paused.id)}>{t('tourn.not_now')}</button>
		<button class="btn btn-sm btn-primary" disabled={busy} onclick={() => act(() => api.tournamentAction(paused.id, 'continue'))}>{t('tourn.continue')}</button>
	</section>
	{#if error}<p class="error">{error}</p>{/if}
{/if}
