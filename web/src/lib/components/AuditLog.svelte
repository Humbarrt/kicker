<script lang="ts">
	// The audit log (log.view); IP addresses only come with log.ip.
	import { api, type AuditEntry } from '#lib/api/client.ts';
	import { i18n, t, tErr } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';

	let entries = $state<AuditEntry[]>([]);
	let loaded = $state(false);
	let error = $state('');

	$effect(() => {
		api.audit()
			.then((e) => (entries = e))
			.catch((e) => (error = tErr(e)))
			.finally(() => (loaded = true));
	});
</script>

{#if error}<p class="error">{error}</p>{/if}
<section class="card overflow-x-auto p-0">
	<table class="w-full text-sm">
		<thead class="bg-stone-50 text-left text-xs text-stone-500 uppercase">
			<tr>
				<th class="px-4 py-2">{t('audit.when')}</th><th class="px-4">{t('audit.who')}</th><th class="px-4">{t('audit.action')}</th>
				<th class="px-4">{t('audit.target')}</th><th class="px-4">{t('audit.details')}</th>
			</tr>
		</thead>
		<tbody>
			{#each entries as e, i (i)}
				<tr class="border-t border-stone-100 align-top">
					<td class="px-4 py-2 whitespace-nowrap text-stone-500">{dateTime(e.at, i18n.lang)}</td>
					<td class="px-4 py-2">{e.account_id ? e.account || '#' + e.account_id : t('audit.console')}
						{#if e.ip}<span class="block text-xs text-stone-400">{e.ip}</span>{/if}</td>
					<td class="px-4 py-2 font-mono text-xs">{e.action}</td>
					<td class="px-4 py-2">{e.target}</td>
					<td class="px-4 py-2 text-stone-600">{e.details}</td>
				</tr>
			{:else}
				{#if loaded}<tr><td colspan="5" class="px-4 py-6 muted">{t('audit.empty')}</td></tr>{/if}
			{/each}
		</tbody>
	</table>
</section>
