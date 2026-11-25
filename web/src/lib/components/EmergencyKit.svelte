<script lang="ts">
	// The emergency kit (spec 03 §8): the recovery key and the backup passphrase, shown once, as a
	// text file to download. With onconfirm, "Continue" needs the "I have saved it" tick.
	import type { EmergencyKit } from '#lib/api/client.ts';
	import { t } from '#lib/i18n.svelte.ts';

	let { kit, onconfirm, confirmLabel }: { kit: EmergencyKit; onconfirm?: () => void; confirmLabel?: string } = $props();

	let saved = $state(false);
	let downloaded = $state(false);
	let copied = $state(false);
	const words = $derived(kit.recovery_key.split(' '));

	function download() {
		const url = URL.createObjectURL(new Blob([kit.text], { type: 'text/plain;charset=utf-8' }));
		const a = document.createElement('a');
		a.href = url;
		a.download = kit.filename;
		a.click();
		setTimeout(() => URL.revokeObjectURL(url), 1000);
		downloaded = true;
	}
	async function copy() {
		try {
			await navigator.clipboard.writeText(kit.text);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			/* no clipboard (plain HTTP): the download still works */
		}
	}
</script>

<div class="space-y-5" data-testid="emergency-kit">
	<p class="notice">{t('kit.warning')}</p>

	<div>
		<h3 class="mb-1 font-medium">{t('kit.recovery_key')}</h3>
		<p class="muted mb-3 text-sm">{t('kit.recovery_hint')}</p>
		<ol class="grid grid-cols-2 gap-x-4 gap-y-1.5 rounded-xl bg-stone-50 p-4 font-mono text-sm sm:grid-cols-3" data-testid="kit-words">
			{#each words as w, i (i)}
				<li class="flex gap-2"><span class="w-5 text-right text-stone-400 tabular-nums">{i + 1}.</span><span class="font-semibold">{w}</span></li>
			{/each}
		</ol>
	</div>

	<div>
		<h3 class="mb-1 font-medium">{t('kit.passphrase')}</h3>
		<p class="muted mb-3 text-sm">{t('kit.passphrase_hint')}</p>
		<p class="rounded-xl bg-stone-50 p-4 font-mono text-sm font-semibold break-words" data-testid="kit-passphrase">{kit.backup_passphrase}</p>
	</div>

	{#if kit.ca_fingerprint}
		<div>
			<h3 class="mb-1 font-medium">{t('https.fingerprint')}</h3>
			<p class="muted mb-2 text-sm">{t('https.fingerprint_hint')}</p>
			<p class="rounded-xl bg-stone-50 p-3 font-mono text-xs break-words">{kit.ca_fingerprint}</p>
		</div>
	{/if}

	<div class="flex flex-wrap gap-2">
		<button type="button" class="btn btn-primary" onclick={download}>{t('kit.download')}</button>
		<button type="button" class="btn" onclick={copy}>{copied ? t('kit.copied') : t('kit.copy')}</button>
		{#if downloaded}<span class="self-center text-sm text-emerald-700">✓ {kit.filename}</span>{/if}
	</div>

	{#if onconfirm}
		<label class="flex items-start gap-2 text-sm">
			<input type="checkbox" class="mt-0.5" bind:checked={saved} data-testid="kit-saved" />
			{t('kit.saved')}
		</label>
		<button type="button" class="btn btn-primary w-full" disabled={!saved} onclick={onconfirm}>{confirmLabel ?? t('setup.continue')}</button>
	{/if}
</div>
