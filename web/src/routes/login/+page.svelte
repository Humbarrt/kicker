<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { t, tErr } from '#lib/i18n.svelte.ts';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let busy = $state(false);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			const { account } = await api.login(username, password);
			await session.refresh();
			live.reconnect();
			goto(account.must_change_password || account.status !== 'approved' ? '/account' : '/');
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<main class="mx-auto max-w-sm px-4 py-16">
	<form class="card space-y-4" onsubmit={submit}>
		<h1 class="h1">{t('login.title')}</h1>
		<label class="block">
			<span class="label">{t('login.username')}</span>
			<input class="input" autocomplete="username" autocapitalize="none" bind:value={username} required />
		</label>
		<label class="block">
			<span class="label">{t('login.password')}</span>
			<input class="input" type="password" autocomplete="current-password" bind:value={password} required />
		</label>
		{#if error}<p class="error">{error}</p>{/if}
		<button class="btn btn-primary w-full" disabled={busy}>{t('login.submit')}</button>
		<div class="flex flex-wrap justify-between gap-2 text-sm">
			<a class="text-stone-600 underline-offset-2 hover:underline" href="/reset">{t('login.forgot')}</a>
			{#if session.info?.registration && session.info.registration !== 'closed'}
				<a class="text-stone-600 underline-offset-2 hover:underline" href="/signup">{t('login.signup')}</a>
			{/if}
		</div>
	</form>
</main>
