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
</div>
