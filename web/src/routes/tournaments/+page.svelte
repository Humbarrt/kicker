<script lang="ts">
	// All tournaments: running and paused first (spec 09 §12).
	import { api, type TournamentSummary } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, type Key } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';
	import { formatName } from '#lib/tournaments.ts';

	let list = $state<TournamentSummary[] | null>(null);
	let error = $state('');
	$effect(() => {
		void live.tournamentRev;
		api.tournaments().then((l) => (list = l)).catch((e) => (error = tErr(e)));
	});
	const stateClass: Record<string, string> = {
		running: 'bg-emerald-100 text-emerald-800',
		paused: 'bg-amber-100 text-amber-900',
		setup: 'bg-sky-100 text-sky-800'
	};
</script>

<main class="mx-auto max-w-4xl space-y-6 px-4 py-6">
	<div class="flex items-center gap-3">
		<h1 class="h1 flex-1">{t('tourn.title')}</h1>
		{#if session.can('tournaments.create')}<a class="btn btn-primary" href="/tournaments/new">{t('tourn.new')}</a>{/if}
	</div>
	{#if error}<p class="error">{error}</p>{/if}
	{#if list && !list.length}
		<p class="card muted">{t('tourn.empty')}</p>
	{:else if list}
		<ul class="card divide-y divide-stone-100 p-2">
			{#each list as x (x.id)}
				<li>
					<a href="/tournaments/{x.id}" class="flex items-center gap-3 rounded-lg px-3 py-3 hover:bg-stone-50" data-testid="tournament-row">
						<span class="min-w-0 flex-1">
							<span class="block truncate font-medium">{x.name}</span>
							<span class="block truncate text-xs text-stone-500">
								{x.formats.map(formatName).join(' → ')} · {t(('tourn.mode.' + x.team_mode) as Key)} ·
								{dateTime(x.started_at ?? x.created_at, i18n.lang)}
							</span>
						</span>
						<span class="chip {stateClass[x.state] ?? ''}">{t(('tourn.state.' + x.state) as Key)}</span>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</main>
