<script lang="ts">
	// Setup wizard steps 5–9 (spec 10 §6), after the admin account and the emergency kit. Every step
	// reuses the settings component for it, so the wizard and the settings never differ; 5–8 can be
	// skipped. The step survives a reload (sessionStorage) – onboarding is already finished here.
	import { goto } from '$app/navigation';
	import { api, type BackupsView, type UpdatesView } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr, type Key } from '#lib/i18n.svelte.ts';
	import TableSettingsForm from '#lib/components/TableSettingsForm.svelte';
	import ModulesPanel from '#lib/components/ModulesPanel.svelte';
	import BackupConfigForm from '#lib/components/BackupConfigForm.svelte';
	import UpdateSettings from '#lib/components/UpdateSettings.svelte';

	const KEY = 'kicker.setup.step';
	const FIRST = 5;
	const LAST = 9;
	function stored(): number {
		try {
			const n = Number(sessionStorage.getItem(KEY));
			return n >= FIRST && n <= LAST ? n : FIRST;
		} catch {
			return FIRST;
		}
	}
	let step = $state(stored());
	function go(n: number) {
		step = n;
		try {
			sessionStorage.setItem(KEY, String(n));
		} catch {
			/* private mode: the step just won't survive a reload */
		}
		scrollTo({ top: 0 });
	}
	const next = () => go(step + 1);

	function done() {
		try {
			sessionStorage.removeItem(KEY);
		} catch {
			/* nothing stored */
		}
		goto('/', { replaceState: true });
	}

	// Only an admin can change these settings; anyone else goes straight to the scoreboard.
	$effect(() => {
		if (session.loaded && !session.admin) done();
	});

	let backups = $state<BackupsView | null>(null);
	let updates = $state<UpdatesView | null>(null);
	let error = $state('');
	$effect(() => {
		if (step === 8 && !backups)
			api.backups()
				.then((v) => (backups = v))
				.catch((e) => (error = tErr(e)));
		if (step === 8 && !updates)
			api.updates()
				.then((v) => (updates = v))
				.catch(() => {});
	});

	const titles: Record<number, Key> = {
		5: 'setup.table',
		6: 'setup.game',
		7: 'setup.modules',
		8: 'setup.backups',
		9: 'setup.done'
	};
	const hints: Record<number, Key> = {
		5: 'setup.table_hint',
		6: 'setup.game_hint',
		7: 'setup.modules_hint',
		8: 'setup.backups_hint',
		9: 'setup.done_hint'
	};
</script>

<svelte:head><title>{t('setup.title')}</title></svelte:head>

<div class="mx-auto max-w-2xl space-y-4" data-testid="setup-more" data-step={step}>
	<div>
		<p class="muted text-sm">{t('setup.step', { n: step, of: LAST })}</p>
		<div class="mt-2 flex gap-1" aria-hidden="true">
			{#each Array(LAST) as _, i (i)}
				<span class="h-1.5 flex-1 rounded-full {i < step ? 'bg-emerald-600' : 'bg-stone-200'}"></span>
			{/each}
		</div>
		<h1 class="h1 mt-4">{t(titles[step])}</h1>
		<p class="muted">{t(hints[step])}</p>
	</div>

	{#snippet nav()}
		<button type="button" class="btn mr-auto" disabled={step === FIRST} onclick={() => go(step - 1)}>{t('setup.back')}</button>
		<button type="button" class="btn" onclick={next} data-testid="setup-skip">{t('setup.skip')}</button>
	{/snippet}

	{#if step === 5}
		<TableSettingsForm sections={['sides']} submitLabel={t('setup.continue')} onsaved={next} actions={nav} />
	{:else if step === 6}
		<TableSettingsForm sections={['game', 'idle']} submitLabel={t('setup.continue')} onsaved={next} actions={nav} />
	{:else if step === 7}
		<ModulesPanel />
	{:else if step === 8}
		{#if backups}
			<BackupConfigForm bind:view={backups} />
		{:else if error}
			<p class="error">{error}</p>
		{/if}
		{#if updates?.enabled}
			<UpdateSettings bind:view={updates} />
		{/if}
	{:else}
		<section class="card space-y-4" data-testid="setup-done">
			<div>
				<div class="label">{t('setup.address')}</div>
				<p class="font-mono text-lg break-all" data-testid="setup-address">{location.origin}</p>
				<p class="muted text-sm">{t('setup.address_hint')}</p>
			</div>
			<div>
				<a class="underline underline-offset-2" href="/trust" target="_blank" rel="noopener" data-sveltekit-reload>{t('setup.trust')}</a>
				<p class="muted text-sm">{t('setup.trust_hint')}</p>
			</div>
			<button class="btn btn-primary w-full" onclick={done} data-testid="setup-to-board">{t('setup.to_board')}</button>
		</section>
	{/if}

	{#if step === 7 || step === 8}
		<div class="flex items-center justify-end gap-3">
			{@render nav()}
			<button class="btn btn-primary" onclick={next} data-testid="setup-next">{t('setup.continue')}</button>
		</div>
	{/if}
</div>
