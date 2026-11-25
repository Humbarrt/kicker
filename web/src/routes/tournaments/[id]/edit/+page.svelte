<script lang="ts">
	import { page } from '$app/state';
	import { api, type TournamentDetail } from '#lib/api/client.ts';
	import { t, tErr } from '#lib/i18n.svelte.ts';
	import SetupEditor from '#lib/components/tournament/SetupEditor.svelte';

	let detail = $state<TournamentDetail | null>(null);
	let error = $state('');
	$effect(() => {
		api.tournament(+page.params.id!).then((d) => (detail = d)).catch((e) => (error = tErr(e)));
	});
</script>

<main class="mx-auto max-w-4xl space-y-6 px-4 py-6">
	<h1 class="h1">{t('tourn.edit')}</h1>
	{#if error}<p class="error">{error}</p>{/if}
	{#if detail}<SetupEditor initial={detail} />{/if}
</main>
