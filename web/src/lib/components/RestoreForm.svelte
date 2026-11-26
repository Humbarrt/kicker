<script lang="ts">
	// Restoring a backup (spec 10 §2): file + passphrase. The server checks it before anything is
	// replaced, then restarts and swaps it in; this waits for it to come back and reloads.
	import { api, type BackupManifest } from '#lib/api/client.ts';
	import { i18n, t, tErr } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';

	let { onboarding = false }: { onboarding?: boolean } = $props();

	let file = $state<File | null>(null);
	let passphrase = $state('');
	let busy = $state(false);
	let error = $state('');
	let done = $state<BackupManifest | null>(null);
	let manual = $state(false); // the server can't restart itself (development)

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!file) return;
		if (!onboarding && !confirm(t('backup.confirm_restore'))) return;
		busy = true;
		error = '';
		try {
			const r = await api.restoreBackup(file, passphrase, onboarding);
			done = r.manifest;
			manual = !r.restarting;
			if (r.restarting) await waitForRestart();
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}

	// The server goes away for a moment; once it answers again, start over (everybody logs in again).
	async function waitForRestart() {
		await new Promise((r) => setTimeout(r, 1500));
		for (let i = 0; i < 120; i++) {
			try {
				const res = await fetch('/api/info', { cache: 'no-store' });
				if (res.ok) {
					location.href = '/';
					return;
				}
			} catch {
				/* not back yet */
			}
			await new Promise((r) => setTimeout(r, 1000));
		}
		error = t('backup.not_back');
	}
</script>

{#if done}
	<div class="space-y-2" data-testid="restore-done">
		<p class="font-medium">{t('backup.restoring', { name: done.name, when: dateTime(done.created, i18n.lang) })}</p>
		<p class="muted text-sm">{manual ? t('backup.restart_by_hand') : t('backup.restarting')}</p>
		{#if error}<p class="error">{error}</p>{/if}
	</div>
{:else}
	<form class="space-y-3" onsubmit={submit} data-testid="restore-form">
		<label class="block"><span class="label">{t('backup.file')}</span>
			<input class="input" type="file" accept=".age" required data-testid="restore-file"
				onchange={(e) => (file = (e.currentTarget as HTMLInputElement).files?.[0] ?? null)} /></label>
		<label class="block"><span class="label">{t('backup.passphrase')}</span>
			<input class="input font-mono" bind:value={passphrase} required autocomplete="off" spellcheck="false" data-testid="restore-passphrase" /></label>
		<p class="muted text-xs">{t('backup.passphrase_hint')}</p>
		{#if error}<p class="error">{error}</p>{/if}
		<button class="btn btn-primary" disabled={busy || !file || !passphrase.trim()} data-testid="restore-submit">
			{busy ? t('backup.checking') : t('backup.restore_button')}</button>
	</form>
{/if}
