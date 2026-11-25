<script lang="ts">
	// /settings opens the first tab this account may see (the layout shows the rest).
	import { goto } from '$app/navigation';
	import { session } from '#lib/session.svelte.ts';
	import { t } from '#lib/i18n.svelte.ts';
	import { settingsTabs } from '#lib/settings.svelte.ts';

	const first = $derived(settingsTabs()[0]?.href);
	$effect(() => {
		const href = first;
		if (href) goto(href, { replaceState: true });
	});
</script>

{#if !first}
	<p class="card text-center">{t('admin.only')}
		{#if !session.account}<a class="underline" href="/login">{t('nav.login')}</a>{/if}</p>
{/if}
