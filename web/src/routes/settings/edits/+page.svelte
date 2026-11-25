<script lang="ts">
	// Recent corrections of finished games (D2): what changed, who, why – and taking one back.
	import { api, type EditFeedEntry } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { i18n, t, tErr, type Key } from '#lib/i18n.svelte.ts';
	import { eventText } from '#lib/gamelog.ts';

	let entries = $state<EditFeedEntry[] | null>(null);
	let error = $state('');
	let busy = $state('');

	function load() {
		api.edits(100).then((e) => (entries = e)).catch((e) => (error = tErr(e)));
	}
	$effect(() => {
		void live.statsRev;
		load();
	});

	async function revert(e: EditFeedEntry) {
		busy = `${e.game_id}/${e.seq}`;
		error = '';
		try {
			await api.editGame(e.game_id, { kind: 'revert', target_seq: e.seq });
			load();
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = '';
		}
	}
	const asEvent = (e: EditFeedEntry) =>
		({ ...e, coin_uid: null, via: null, rule: null, rule_value: null, reason: null, source: 'manual', author_id: null, play_time_ms: null, edit: true }) as const;
</script>

<h1 class="h1 mb-1">{t('edits.title')}</h1>
<p class="muted mb-4 text-sm">{t('edits.hint')}</p>
{#if error}<p class="error mb-3">{error}</p>{/if}
{#if entries}
	<section class="card divide-y divide-stone-100 p-2" data-testid="edits">
		{#each entries as e (`${e.game_id}/${e.seq}`)}
			<div class="flex flex-wrap items-center gap-x-3 gap-y-1 px-2 py-2.5 text-sm {e.reverted ? 'text-stone-400' : ''}">
				<a class="font-medium text-stone-900 underline-offset-2 hover:underline" href="/games/{e.game_id}">{t('game.title', { id: e.game_id })}</a>
				<span class="min-w-0 flex-1 {e.reverted ? 'line-through' : ''}">{eventText(asEvent(e))}</span>
				<span class="text-xs text-stone-500">
					{e.author ? t('ev.by', { name: e.author }) : ''} · {new Date(e.at).toLocaleString(i18n.lang, { dateStyle: 'short', timeStyle: 'short' })}
				</span>
				{#if !e.reverted}
					<button class="btn btn-sm" disabled={busy === `${e.game_id}/${e.seq}`} onclick={() => revert(e)}>{t('game.revert')}</button>
				{/if}
			</div>
		{:else}
			<p class="muted p-4">{t('edits.none')}</p>
		{/each}
	</section>
{/if}
