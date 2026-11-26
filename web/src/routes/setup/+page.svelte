<script lang="ts">
	// First-boot wizard (spec 03 §12, spec 10 §6), steps 1–4: language, fresh start or restore, admin
	// account, emergency kit. The kit belongs to the new admin's login, so a lost tab logs in again
	// here instead of starting over. Steps 5–9 follow on /setup/more, with the settings API.
	import Sparkles from '@lucide/svelte/icons/sparkles';
	import ArchiveRestore from '@lucide/svelte/icons/archive-arrow-up';
	import { goto } from '$app/navigation';
	import { api, type EmergencyKit as Kit, type OnboardingStatus } from '#lib/api/client.ts';
	import EmergencyKit from '#lib/components/EmergencyKit.svelte';
	import RestoreForm from '#lib/components/RestoreForm.svelte';
	import { session, setLocalLanguage } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { i18n, languages, t, tErr } from '#lib/i18n.svelte.ts';

	let status = $state<OnboardingStatus | null>(null);
	let lang = $state(i18n.lang);
	let username = $state('');
	let password = $state('');
	let repeat = $state('');
	let error = $state('');
	let busy = $state(false);
	let start = $state<'' | 'fresh' | 'restore'>(''); // step 2

	async function claim() {
		error = '';
		const now = await api.onboarding().catch(() => null);
		if (now?.step === 'kit') {
			status = now;
			return;
		}
		try {
			status = await api.onboardingStart();
			if (status.language) {
				lang = status.language;
				i18n.set(lang);
			}
		} catch (e) {
			status = await api.onboarding().catch(() => null);
			if (!status?.busy) error = tErr(e);
		}
	}
	$effect(() => {
		claim();
	});

	async function saveLanguage(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		try {
			status = await api.onboardingLanguage(lang);
			setLocalLanguage(lang);
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}

	async function createAdmin(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		if (password !== repeat) {
			error = t('account.mismatch');
			return;
		}
		busy = true;
		try {
			await api.onboardingAdmin(username, password);
			await session.refresh();
			status = await api.onboarding();
			password = repeat = '';
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}

	// Step 3: the emergency kit, made once when the step opens (each call makes a new key).
	let kit = $state<Kit | null>(null);
	let kitRequested = false; // not reactive: one request per visit
	$effect(() => {
		if (status?.step === 'kit' && session.admin && !kitRequested) {
			kitRequested = true;
			api.onboardingKit()
				.then((k) => (kit = k))
				.catch((e) => (error = tErr(e)));
		}
	});
	async function login(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			await api.login(username, password);
			await session.refresh();
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
	async function finish() {
		try {
			await api.onboardingFinish();
			await session.refresh();
			live.reconnect();
			goto('/setup/more', { replaceState: true }); // steps 5–9
		} catch (err) {
			error = tErr(err);
		}
	}

	const step = $derived(status?.step === 'kit' ? 4 : status?.step === 'admin' ? (start === 'fresh' ? 3 : 2) : 1);
</script>

<main class="mx-auto max-w-md px-4 py-16">
	<div class="mb-6 text-center">
		<h1 class="text-3xl font-semibold tracking-tight">{t('setup.title')}</h1>
		{#if status?.mine || status?.step === 'kit'}<p class="muted mt-1">{t('setup.step', { n: step, of: 9 })}</p>{/if}
	</div>

	{#if !status}
		<p class="card text-center muted">{error || t('app.loading')}</p>
	{:else if status.step === 'kit'}
		<section class="card space-y-4">
			<h2 class="h2">{t('setup.kit')}</h2>
			{#if !session.admin}
				<p class="muted">{t('setup.kit_login')}</p>
				<form class="space-y-4" onsubmit={login}>
					<label class="block"><span class="label">{t('login.username')}</span>
						<input class="input" autocomplete="username" autocapitalize="none" bind:value={username} required /></label>
					<label class="block"><span class="label">{t('login.password')}</span>
						<input class="input" type="password" autocomplete="current-password" bind:value={password} required /></label>
					{#if error}<p class="error">{error}</p>{/if}
					<button class="btn btn-primary w-full" disabled={busy}>{t('login.submit')}</button>
				</form>
			{:else if kit}
				<p class="muted">{t('setup.kit_hint')}</p>
				<EmergencyKit {kit} onconfirm={finish} confirmLabel={t('setup.finish')} />
				{#if error}<p class="error">{error}</p>{/if}
			{:else}
				<p class="muted">{error || t('app.loading')}</p>
			{/if}
		</section>
	{:else if status.busy}
		<div class="card space-y-3 text-center">
			<p>{t('setup.busy')}</p>
			<p class="muted">{t('setup.busy_hint')}</p>
			<button class="btn" onclick={claim}>{t('app.retry')}</button>
		</div>
	{:else if status.step === 'language'}
		<form class="card space-y-4" onsubmit={saveLanguage}>
			<h2 class="h2">{t('setup.language')}</h2>
			<p class="muted">{t('setup.language_hint')}</p>
			<div class="grid gap-2">
				{#each languages as l (l.code)}
					<label class="flex cursor-pointer items-center gap-3 rounded-lg border px-4 py-3 {lang === l.code ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200'}">
						<input type="radio" name="lang" value={l.code} bind:group={lang} onchange={() => i18n.set(lang)} />
						{l.name}
					</label>
				{/each}
			</div>
			{#if error}<p class="error">{error}</p>{/if}
			<button class="btn btn-primary w-full" disabled={busy}>{t('setup.continue')}</button>
		</form>
	{:else if start !== 'fresh'}
		<!-- Step 2: a new kicker, or one restored from a backup (moving to a new Pi) -->
		<section class="card space-y-4" data-testid="setup-start">
			<h2 class="h2">{t('setup.start')}</h2>
			{#if start === 'restore'}
				<p class="muted">{t('setup.restore_hint')}</p>
				<RestoreForm onboarding />
				<button class="btn w-full" onclick={() => (start = '')}>{t('setup.back')}</button>
			{:else}
				<button class="flex w-full items-start gap-3 rounded-xl border border-stone-200 p-4 text-left hover:border-emerald-600 hover:bg-emerald-50"
					onclick={() => (start = 'fresh')} data-testid="setup-fresh">
					<Sparkles class="size-6 shrink-0 text-emerald-700" aria-hidden="true" />
					<span><span class="block font-medium">{t('setup.fresh')}</span><span class="muted text-sm">{t('setup.fresh_hint')}</span></span>
				</button>
				<button class="flex w-full items-start gap-3 rounded-xl border border-stone-200 p-4 text-left hover:border-emerald-600 hover:bg-emerald-50"
					onclick={() => (start = 'restore')} data-testid="setup-restore">
					<ArchiveRestore class="size-6 shrink-0 text-emerald-700" aria-hidden="true" />
					<span><span class="block font-medium">{t('setup.restore')}</span><span class="muted text-sm">{t('setup.restore_choice_hint')}</span></span>
				</button>
			{/if}
		</section>
	{:else}
		<form class="card space-y-4" onsubmit={createAdmin}>
			<h2 class="h2">{t('setup.admin')}</h2>
			<p class="muted">{t('setup.admin_hint')}</p>
			<label class="block"><span class="label">{t('login.username')}</span>
				<input class="input" autocomplete="username" autocapitalize="none" bind:value={username} required /></label>
			<label class="block"><span class="label">{t('login.password')}</span>
				<input class="input" type="password" autocomplete="new-password" minlength="10" bind:value={password} required /></label>
			<label class="block"><span class="label">{t('account.repeat')}</span>
				<input class="input" type="password" autocomplete="new-password" bind:value={repeat} required /></label>
			<p class="muted">{t('setup.password_hint')}</p>
			{#if busy}<p class="notice">{t('setup.creating')}</p>{/if}
			{#if error}<p class="error">{error}</p>{/if}
			<button class="btn btn-primary w-full" disabled={busy}>{t('app.create')}</button>
		</form>
	{/if}
	{#if session.info?.ca_fingerprint}
		<p class="muted mt-6 text-center text-xs">
			{t('https.fingerprint')}: <span class="font-mono">{session.info.ca_fingerprint}</span>
			· <a class="underline" href="/trust" target="_blank" rel="noopener">{t('https.help')}</a>
		</p>
	{/if}
</main>
