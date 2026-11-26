<script lang="ts">
	// Settings → Updates (admins, spec 10 §3): the running version, "Check now", the available
	// release with its notes and "Update to X", the last result, and the switches. Installing
	// backs up, downloads, checks and restarts; the page waits for the new version and reloads.
	import { api, type UpdatesView } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, type Key } from '#lib/i18n.svelte.ts';
	import { ago, dateTime } from '#lib/format.ts';
	import { markdown } from '#lib/markdown.ts';
	import UpdateSettings from '#lib/components/UpdateSettings.svelte';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';
	import CircleCheck from '@lucide/svelte/icons/circle-check';
	import CircleX from '@lucide/svelte/icons/circle-x';

	let view = $state<UpdatesView | null>(null);
	let error = $state('');
	let checking = $state(false);
	let restarting = $state(false);
	let now = $state(Date.now());

	$effect(() => {
		void live.updateRev; // a check, a step of an installation, a result
		api.updates()
			.then((v) => ((view = v), (now = Date.now())))
			.catch((e) => (error = tErr(e)));
	});

	async function check() {
		checking = true;
		error = '';
		try {
			view = await api.checkUpdates();
			now = Date.now();
			await session.refresh();
		} catch (e) {
			error = tErr(e);
		} finally {
			checking = false;
		}
	}

	async function install() {
		error = '';
		try {
			const old = view?.version ?? '';
			view = await api.installUpdate();
			void waitForNewVersion(old);
		} catch (e) {
			error = tErr(e);
		}
	}

	// The server restarts into the new version (or back into this one after a rollback): wait
	// until it answers with another version, then reload so the new web UI is loaded too.
	async function waitForNewVersion(old: string) {
		for (let i = 0; i < 600; i++) {
			await new Promise((r) => setTimeout(r, 1500));
			try {
				const r = await fetch('/api/info', { cache: 'no-store' });
				if (!r.ok) {
					restarting = true;
					continue;
				}
				const info = await r.json();
				if (info.version !== old) return location.reload();
				if (restarting) return location.reload(); // back, same version: it failed or rolled back
				if (view?.version === old && !view.status.step) return; // failed before the restart: see the result
			} catch {
				restarting = true; // not reachable while it restarts
			}
		}
	}

	const step = $derived(view?.status.step ?? '');
	const steps = ['backup', 'download', 'check', 'restart', 'verify'] as const;
</script>

<div class="space-y-6">
	{#if view}
		<!-- The running version -->
		<section class="card flex flex-wrap items-center gap-4" data-testid="update-current">
			<div class="min-w-0 flex-1">
				<h2 class="h2">{t('upd.title')}</h2>
				<p class="text-sm">{t('upd.running', { version: view.version })}</p>
				{#if view.enabled}
					<p class="text-sm {view.status.check_error ? 'text-red-700' : 'muted'}" data-testid="update-last-check">
						{#if view.status.check_error}
							{t('upd.check_failed', { error: view.status.check_error })}
						{:else if view.status.last_check}
							{t('upd.last_check', { when: ago(Date.parse(view.status.last_check), now, i18n.lang) })}
						{:else}
							{t('upd.never_checked')}
						{/if}
					</p>
				{/if}
			</div>
			{#if view.enabled}
				<button class="btn" disabled={checking || !!step} onclick={check} data-testid="update-check">
					<RefreshCw class="size-4 {checking ? 'animate-spin' : ''}" aria-hidden="true" />{t('upd.check_now')}
				</button>
			{/if}
		</section>

		{#if !view.enabled}
			<p class="notice" data-testid="update-disabled">{t('upd.disabled')}</p>
		{/if}

		<!-- The last installation -->
		{#if view.status.last && !step}
			{@const r = view.status.last}
			<div class="flex items-start gap-3 rounded-xl border p-4 text-sm {r.ok ? 'border-emerald-200 bg-emerald-50 text-emerald-900' : 'border-red-200 bg-red-50 text-red-900'}" data-testid="update-result">
				{#if r.ok}<CircleCheck class="size-5 shrink-0" aria-hidden="true" />{:else}<CircleX class="size-5 shrink-0" aria-hidden="true" />{/if}
				<div>
					<div class="font-medium">
						{r.ok ? t('upd.done', { from: r.from, to: r.to }) : t('upd.failed', { to: r.to })}
						<span class="font-normal opacity-75">· {dateTime(r.at, i18n.lang)}</span>
					</div>
					{#if r.error}<div class="mt-1">{r.error}</div>{/if}
				</div>
			</div>
		{/if}

		{#if step === 'verify'}
			<section class="card space-y-2" data-testid="update-verify">
				<ol class="flex flex-wrap gap-2 text-sm">
					{#each steps as s (s)}
						<li class="chip {s === 'verify' ? 'bg-stone-800 text-white' : 'bg-emerald-100 text-emerald-800'}">{t(('upd.step.' + s) as Key)}</li>
					{/each}
				</ol>
				<p class="muted text-sm">{t('upd.verifying')}</p>
			</section>
		{/if}

		<!-- The available release -->
		{#if view.status.available}
			{@const a = view.status.available}
			<section class="card space-y-4" data-testid="update-available">
				<div class="flex flex-wrap items-center gap-4">
					<div class="min-w-0 flex-1">
						<h2 class="h2">{t('upd.available', { version: a.version })}</h2>
						<p class="muted text-sm">
							{dateTime(a.date, i18n.lang)}
							{#if a.url} · <a class="underline underline-offset-2" href={a.url} target="_blank" rel="noopener noreferrer">{t('upd.on_github')}</a>{/if}
						</p>
					</div>
					{#if !step}
						<button class="btn btn-primary" disabled={!!view.blocked} onclick={install} data-testid="update-install">
							{t('upd.install', { version: a.version })}
						</button>
					{/if}
				</div>
				{#if step || restarting}
					<ol class="flex flex-wrap gap-2 text-sm" data-testid="update-progress">
						{#each steps as s, i (s)}
							{@const done = restarting || steps.indexOf(step as (typeof steps)[number]) > i}
							{@const current = !restarting && step === s}
							<li class="chip {done ? 'bg-emerald-100 text-emerald-800' : current ? 'bg-stone-800 text-white' : 'bg-stone-100 text-stone-500'}">
								{t(('upd.step.' + s) as Key)}
							</li>
						{/each}
					</ol>
					<p class="muted text-sm">{t(step === 'verify' ? 'upd.verifying' : restarting || step === 'restart' ? 'upd.restarting' : 'upd.installing')}</p>
				{:else if view.blocked}
					<p class="notice" data-testid="update-blocked">{t(('upd.blocked.' + view.blocked) as Key)}</p>
				{:else}
					<p class="muted text-sm">{t('upd.install_hint')}</p>
				{/if}
				{#if a.notes}
					<div class="space-y-2 border-t border-stone-100 pt-4 text-sm" data-testid="update-notes">
						<!-- eslint-disable-next-line svelte/no-at-html-tags -- escaped by markdown() -->
						{@html markdown(a.notes)}
					</div>
				{/if}
			</section>
		{:else if view.enabled && !step && view.status.last_check && !view.status.check_error}
			<p class="muted text-sm" data-testid="update-none">{t('upd.up_to_date')}</p>
		{/if}

		{#if error}<p class="error">{error}</p>{/if}

		{#if view.enabled}
			<UpdateSettings bind:view={view!} />
		{/if}
	{:else if error}
		<p class="error">{error}</p>
	{/if}
</div>
