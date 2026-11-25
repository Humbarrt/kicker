<script lang="ts">
	// Forgot password (spec 03 §7): a reset code from an admin (members), or the recovery key
	// (admins). After a recovery the new emergency kit must be saved.
	import { goto } from '$app/navigation';
	import { api, type EmergencyKit as Kit } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { t, tErr } from '#lib/i18n.svelte.ts';
	import EmergencyKit from '#lib/components/EmergencyKit.svelte';

	let way = $state<'code' | 'key'>('code');
	let username = $state('');
	let secret = $state('');
	let password = $state('');
	let repeat = $state('');
	let error = $state('');
	let busy = $state(false);
	let kit = $state<Kit | null>(null);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		if (password !== repeat) {
			error = t('account.mismatch');
			return;
		}
		busy = true;
		try {
			if (way === 'code') {
				await api.resetPassword(username, secret, password);
			} else {
				kit = (await api.recover(username, secret, password)).kit;
			}
			await session.refresh();
			live.reconnect();
			if (way === 'code') goto('/', { replaceState: true });
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<main class="mx-auto max-w-md px-4 py-12">
	{#if kit}
		<section class="card space-y-4">
			<h1 class="h1">{t('reset.new_kit')}</h1>
			<p class="muted">{t('reset.new_kit_hint')}</p>
			<EmergencyKit {kit} onconfirm={() => goto('/', { replaceState: true })} />
		</section>
	{:else}
		<form class="card space-y-4" onsubmit={submit}>
			<h1 class="h1">{t('reset.title')}</h1>
			<div class="inline-flex rounded-lg bg-stone-100 p-0.5 text-sm" role="tablist">
				{#each [['code', t('reset.with_code')], ['key', t('reset.with_key')]] as [k, label] (k)}
					<button type="button" role="tab" aria-selected={way === k} onclick={() => ((way = k as 'code' | 'key'), (secret = ''), (error = ''))}
						class="rounded-md px-3 py-1 {way === k ? 'bg-white font-medium shadow-sm' : 'text-stone-600'}">{label}</button>
				{/each}
			</div>
			<p class="muted text-sm">{way === 'code' ? t('reset.code_hint') : t('reset.key_hint')}</p>
			<label class="block"><span class="label">{t('login.username')}</span>
				<input class="input" autocomplete="username" autocapitalize="none" bind:value={username} required /></label>
			{#if way === 'code'}
				<label class="block"><span class="label">{t('reset.code')}</span>
					<input class="input font-mono uppercase" autocomplete="one-time-code" autocapitalize="characters" placeholder="ABCD-EFGH"
						bind:value={secret} required /></label>
			{:else}
				<label class="block"><span class="label">{t('kit.recovery_key')}</span>
					<textarea class="input font-mono" rows="3" autocapitalize="none" spellcheck="false" placeholder={t('reset.key_placeholder')}
						bind:value={secret} required></textarea></label>
			{/if}
			<label class="block"><span class="label">{t('account.new')}</span>
				<input class="input" type="password" autocomplete="new-password" minlength="10" bind:value={password} required /></label>
			<label class="block"><span class="label">{t('account.repeat')}</span>
				<input class="input" type="password" autocomplete="new-password" bind:value={repeat} required /></label>
			{#if error}<p class="error">{error}</p>{/if}
			<button class="btn btn-primary w-full" disabled={busy}>{t('reset.submit')}</button>
			<p class="text-center text-sm"><a class="text-stone-600 hover:underline" href="/login">{t('reset.back')}</a></p>
		</form>
	{/if}
</main>
