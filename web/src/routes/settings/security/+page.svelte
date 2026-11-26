<script lang="ts">
	// Security (admins): the emergency kit – recovery key and backup passphrase (spec 03 §7, §8).
	// The recovery key is only stored as a hash, so showing the kit again makes a new key.
	import { api, type EmergencyKit as Kit } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';
	import EmergencyKit from '#lib/components/EmergencyKit.svelte';

	let created = $state<number | null | undefined>(undefined);
	let fingerprint = $state('');
	let https = $state(false);
	let password = $state('');
	let kit = $state<Kit | null>(null);
	let error = $state('');
	let busy = $state(false);

	async function load() {
		const s = await api.security();
		created = s.recovery_key_created_at;
		fingerprint = s.ca_fingerprint;
		https = s.https;
	}
	$effect(() => {
		load().catch((e) => (error = tErr(e)));
	});

	// --- the backup passphrase (spec 03 §8): only new backups use a new one
	let ppPassword = $state('');
	let ppOwn = $state(false);
	let ppValue = $state('');
	let ppNew = $state('');
	let ppError = $state('');
	let ppBackedUp = $state(false);
	async function changePassphrase(e: SubmitEvent) {
		e.preventDefault();
		ppError = '';
		busy = true;
		try {
			ppNew = (await api.changePassphrase(ppPassword, ppOwn ? ppValue : '')).passphrase;
			ppPassword = ppValue = '';
			ppBackedUp = false;
		} catch (err) {
			ppError = tErr(err);
		} finally {
			busy = false;
		}
	}
	async function backUpNow() {
		busy = true;
		try {
			await api.runBackup();
			ppBackedUp = true;
		} catch (err) {
			ppError = tErr(err);
		} finally {
			busy = false;
		}
	}

	async function make(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		busy = true;
		try {
			kit = await api.emergencyKit(password);
			password = '';
			await load();
			await session.refresh();
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<div class="space-y-6">
	<section class="card space-y-3" data-testid="https">
		<h2 class="h2">{t('https.title')}</h2>
		{#if fingerprint}
			<p class="muted text-sm">{t('https.hint')}</p>
			<div>
				<div class="label">{t('https.fingerprint')}</div>
				<p class="rounded-xl bg-stone-50 p-3 font-mono text-sm break-words" data-testid="ca-fingerprint">{fingerprint}</p>
			</div>
			{#if !https}<p class="notice">{t('https.not_used')}</p>{/if}
			<div class="flex flex-wrap gap-2">
				<a class="btn" href="/ca.crt" download="kicker-ca.crt">{t('https.download_ca')}</a>
				<a class="btn" href="/trust" target="_blank" rel="noopener">{t('https.help')}</a>
			</div>
		{:else}
			<p class="notice">{t('https.off')}</p>
		{/if}
	</section>

	<section class="card space-y-4" data-testid="security">
		<h2 class="h2">{t('security.kit')}</h2>
		<p class="muted text-sm">{t('security.kit_hint')}</p>
		{#if created === null}
			<p class="notice">{t('security.no_kit')}</p>
		{:else if created}
			<p class="text-sm">{t('security.kit_made', { when: dateTime(created, i18n.lang) })}</p>
		{/if}

		{#if kit}
			<EmergencyKit {kit} onconfirm={() => (kit = null)} confirmLabel={t('app.close')} />
		{:else}
			<form class="flex flex-wrap items-end gap-3" onsubmit={make}>
				<label class="block min-w-60 flex-1"><span class="label">{t('security.password')}</span>
					<input class="input" type="password" autocomplete="current-password" bind:value={password} required /></label>
				<button class="btn btn-primary" disabled={busy}>{created ? t('security.new_kit') : t('security.make_kit')}</button>
			</form>
			{#if created}<p class="muted text-xs">{t('security.replaces')}</p>{/if}
		{/if}
		{#if error}<p class="error">{error}</p>{/if}
	</section>

	<section class="card space-y-3" data-testid="passphrase">
		<h2 class="h2">{t('passphrase.title')}</h2>
		<p class="muted text-sm">{t('passphrase.hint')}</p>
		{#if ppNew}
			<p class="rounded-xl bg-stone-50 p-3 font-mono text-sm break-words" data-testid="new-passphrase">{ppNew}</p>
			<p class="notice">{t('passphrase.old_backups')}</p>
			<div class="flex flex-wrap items-center gap-2">
				<button class="btn btn-primary" disabled={busy || ppBackedUp} onclick={backUpNow}>{t('backup.now')}</button>
				{#if ppBackedUp}<span class="text-sm text-emerald-700">{t('passphrase.backed_up')}</span>{/if}
				<button class="btn" onclick={() => (ppNew = '')}>{t('app.close')}</button>
			</div>
		{:else}
			<form class="space-y-3" onsubmit={changePassphrase}>
				<div class="flex flex-wrap gap-4 text-sm">
					<label class="flex items-center gap-2"><input type="radio" bind:group={ppOwn} value={false} />{t('passphrase.words')}</label>
					<label class="flex items-center gap-2"><input type="radio" bind:group={ppOwn} value={true} />{t('passphrase.own')}</label>
				</div>
				{#if ppOwn}
					<label class="block"><span class="label">{t('passphrase.new')}</span>
						<input class="input font-mono" bind:value={ppValue} minlength="16" required autocomplete="off" /></label>
				{/if}
				<div class="flex flex-wrap items-end gap-3">
					<label class="block min-w-60 flex-1"><span class="label">{t('security.password')}</span>
						<input class="input" type="password" autocomplete="current-password" bind:value={ppPassword} required /></label>
					<button class="btn" disabled={busy}>{t('passphrase.change')}</button>
				</div>
			</form>
		{/if}
		{#if ppError}<p class="error">{ppError}</p>{/if}
	</section>
</div>
