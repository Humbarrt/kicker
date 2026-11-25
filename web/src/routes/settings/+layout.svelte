<script lang="ts">
	import { page } from '$app/state';
	import { session } from '#lib/session.svelte.ts';
	import { t } from '#lib/i18n.svelte.ts';
	import { settingsTabs } from '#lib/settings.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	const tabs = $derived(settingsTabs());
	const index = $derived(page.url.pathname.replace(/\/$/, '') === '/settings'); // forwards to the first tab
	const allowed = $derived(index || tabs.some((tab) => page.url.pathname.startsWith(tab.href)));
</script>

<main class="mx-auto max-w-6xl px-4 py-6">
	{#if tabs.length}
		<nav class="mb-6 flex gap-1 overflow-x-auto border-b border-stone-200 text-sm">
			{#each tabs as tab (tab.href)}
				<a href={tab.href}
					class="-mb-px border-b-2 px-3 py-2 whitespace-nowrap {page.url.pathname.startsWith(tab.href) ? 'border-stone-900 font-medium' : 'border-transparent text-stone-500 hover:text-stone-800'}"
					>{tab.label}{#if tab.badge}<span class="ml-1.5 rounded-full bg-amber-500 px-1.5 py-0.5 text-[10px] font-semibold text-white">{tab.badge}</span>{/if}</a>
			{/each}
		</nav>
	{/if}
	{#if allowed}
		{@render children()}
	{:else}
		<p class="card text-center">{t('admin.only')}
			{#if !session.account}<a class="underline" href="/login">{t('nav.login')}</a>{/if}</p>
	{/if}
</main>
