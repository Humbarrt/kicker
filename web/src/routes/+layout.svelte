<script lang="ts">
	import Menu from '@lucide/svelte/icons/menu';
	import X from '@lucide/svelte/icons/x';
	import './layout.css';
	import favicon from '#lib/assets/favicon.svg';
	import { afterNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import { session } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { t } from '#lib/i18n.svelte.ts';
	import LanguagePicker from '#lib/components/LanguagePicker.svelte';
	import { settingsAttention, settingsTabs } from '#lib/settings.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	$effect(() => {
		session.refresh();
	});

	// Side colors from the table settings (D17): the text-home / bg-away … utilities follow.
	$effect(() => {
		const sides = live.table?.sides;
		if (!sides) return;
		const st = document.documentElement.style;
		st.setProperty('--color-home', sides.home.color);
		st.setProperty('--color-away', sides.away.color);
	});

	// Setup first (spec 03 §12); a forced password change before anything else.
	$effect(() => {
		const info = session.info;
		if (!info) return;
		const path = page.url.pathname;
		if (!info.onboarding_done) {
			if (path !== '/setup') goto('/setup', { replaceState: true });
			return;
		}
		live.connect();
		if (path === '/setup') goto('/', { replaceState: true });
		else if (session.account?.must_change_password && path !== '/account') goto('/account', { replaceState: true });
	});

	// Settings: whatever tabs this account may see (admins all; coins, edits, log by permission).
	const settings = $derived(settingsTabs());
	const attention = $derived(settingsAttention());
	const nav = $derived([
		{ href: '/', label: t('nav.scoreboard'), show: true },
		{ href: '/games', label: t('nav.games'), show: session.can('view.stats'), prefix: '/games' },
		{ href: '/players', label: t('nav.players'), show: session.can('view.stats'), prefix: '/players' },
		{ href: '/tournaments', label: t('nav.tournaments'), show: session.can('view.tournaments'), prefix: '/tournaments' },
		{ href: settings[0]?.href ?? '/settings', label: t('nav.settings'), show: settings.length > 0, prefix: '/settings' }
	]);

	// Mobile: a side menu instead of the header links.
	let menu = $state(false);
	afterNavigate(() => (menu = false));
	const setup = $derived(page.url.pathname.startsWith('/setup'));
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>{session.info?.name ?? t('app.name')}</title>
</svelte:head>

{#if !session.loaded}
	<div class="grid min-h-screen place-items-center muted">{t('app.loading')}</div>
{:else if session.error && !session.info}
	<div class="grid min-h-screen place-items-center p-6">
		<div class="card max-w-sm text-center">
			<p class="mb-4">{t('app.unreachable')}</p>
			<button class="btn" onclick={() => session.refresh()}>{t('app.retry')}</button>
		</div>
	</div>
{:else}
	{#if !setup}
		<header class="sticky top-0 z-30 border-b border-stone-200 bg-white/80 backdrop-blur">
			<div class="mx-auto flex max-w-6xl items-center gap-4 px-4 py-3 md:gap-6">
				<a href="/" class="flex items-center gap-2 font-semibold tracking-tight">
					<img src={favicon} alt="" class="size-6" />
					{session.info?.name ?? t('app.name')}
				</a>
				{#if session.info?.sim}<span class="chip bg-amber-100 text-amber-800">{t('app.sim')}</span>{/if}
				<nav class="hidden flex-1 items-center gap-1 text-sm md:flex">
					{#each nav.filter((n) => n.show) as n (n.href)}
						{@const active = n.prefix ? page.url.pathname.startsWith(n.prefix) : page.url.pathname === n.href}
						<a href={n.href}
							class="rounded-lg px-3 py-1.5 {active ? 'bg-stone-900 text-white' : 'text-stone-600 hover:bg-stone-100'}">{n.label}{#if n.prefix === '/settings' && attention}<span
									class="ml-1.5 rounded-full bg-amber-500 px-1.5 py-0.5 text-[10px] font-semibold text-white" data-testid="settings-badge">{attention}</span>{/if}</a>
					{/each}
				</nav>
				<div class="hidden md:block">
					{#if session.account}
						<a href="/account" class="text-sm text-stone-600 hover:text-stone-900">{session.account.username}</a>
					{:else}
						<a href="/login" class="btn btn-sm">{t('nav.login')}</a>
					{/if}
				</div>
				<button class="btn btn-sm relative ml-auto md:hidden" aria-label={t('nav.menu')} aria-expanded={menu} onclick={() => (menu = true)}>
					<Menu class="size-5" aria-hidden="true" />{#if attention}<span class="absolute -top-1 -right-1 size-2.5 rounded-full bg-amber-500"></span>{/if}
				</button>
			</div>
		</header>

		{#if menu}
			<div class="fixed inset-0 z-40 md:hidden" role="dialog" aria-modal="true" aria-label={t('nav.menu')}>
				<button class="absolute inset-0 bg-stone-900/40" aria-label={t('app.close')} onclick={() => (menu = false)}></button>
				<aside class="absolute inset-y-0 right-0 flex w-72 max-w-[85vw] flex-col bg-white shadow-xl">
					<div class="flex items-center justify-between border-b border-stone-100 px-4 py-3">
						{#if session.account}
							<a href="/account" class="min-w-0">
								<span class="block truncate font-semibold">{session.account.username}</span>
								<span class="muted">{t('nav.account')}</span>
							</a>
						{:else}
							<a href="/login" class="btn btn-primary btn-sm">{t('nav.login')}</a>
						{/if}
						<button class="btn btn-sm" aria-label={t('app.close')} onclick={() => (menu = false)}><X class="size-4" aria-hidden="true" /></button>
					</div>
					<nav class="flex-1 space-y-1 overflow-y-auto p-3 text-sm">
						{#each nav.filter((n) => n.show && n.prefix !== '/settings') as n (n.href)}
							{@const active = n.prefix ? page.url.pathname.startsWith(n.prefix) : page.url.pathname === n.href}
							<a href={n.href} class="block rounded-lg px-3 py-2 {active ? 'bg-stone-900 text-white' : 'hover:bg-stone-100'}">{n.label}</a>
						{/each}
						{#if settings.length}
							<p class="px-3 pt-4 pb-1 text-xs font-semibold tracking-wider text-stone-400 uppercase">{t('nav.settings')}</p>
							{#each settings as n (n.href)}
								<a href={n.href} class="block rounded-lg px-3 py-2 {page.url.pathname.startsWith(n.href) ? 'bg-stone-900 text-white' : 'hover:bg-stone-100'}">{n.label}{#if n.badge}<span
										class="ml-1.5 rounded-full bg-amber-500 px-1.5 py-0.5 text-[10px] font-semibold text-white">{n.badge}</span>{/if}</a>
							{/each}
						{/if}
					</nav>
					<div class="border-t border-stone-100 p-4"><LanguagePicker /></div>
				</aside>
			</div>
		{/if}
	{/if}
	{@render children()}
{/if}
