<script lang="ts">
	// Sign-up (spec 03 §3): username, password and "who are you?" – an existing player without an
	// account (approval mode; an admin confirms) or a new player. Open sign-ups always get a new one.
	import { goto } from '$app/navigation';
	import { api, type Player } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { t, tErr, tPlayer } from '#lib/i18n.svelte.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	let username = $state('');
	let password = $state('');
	let repeat = $state('');
	let who = $state<'new' | 'existing'>('new');
	let playerName = $state('');
	let playerId = $state(0);
	let free = $state<Player[]>([]);
	let error = $state('');
	let busy = $state(false);

	const mode = $derived(session.info?.registration ?? 'closed');
	$effect(() => {
		if (mode === 'approval')
			api.players().then((l) => (free = l.filter((p) => !p.guest && !p.has_account && !p.archived))).catch(() => {});
	});
	$effect(() => {
		if (session.account) goto('/account', { replaceState: true });
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		if (password !== repeat) {
			error = t('account.mismatch');
			return;
		}
		if (who === 'existing' && !playerId) {
			error = t('signup.pick');
			return;
		}
		busy = true;
		try {
			await api.signup(who === 'existing' && mode === 'approval'
				? { username, password, player_id: playerId }
				: { username, password, player_name: playerName.trim() || username });
			await session.refresh();
			live.reconnect();
			goto('/account', { replaceState: true });
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<main class="mx-auto max-w-md px-4 py-12">
	{#if mode === 'closed'}
		<div class="card space-y-3 text-center">
			<h1 class="h1">{t('signup.title')}</h1>
			<p class="muted">{t('signup.closed')}</p>
			<a class="btn" href="/login">{t('nav.login')}</a>
		</div>
	{:else}
		<form class="card space-y-4" onsubmit={submit}>
			<h1 class="h1">{t('signup.title')}</h1>
			<p class="muted text-sm">{mode === 'open' ? t('signup.open_hint') : t('signup.approval_hint')}</p>
			<label class="block"><span class="label">{t('login.username')}</span>
				<input class="input" autocomplete="username" autocapitalize="none" bind:value={username} required minlength="3" maxlength="32" /></label>
			<label class="block"><span class="label">{t('login.password')}</span>
				<input class="input" type="password" autocomplete="new-password" minlength="10" bind:value={password} required /></label>
			<label class="block"><span class="label">{t('account.repeat')}</span>
				<input class="input" type="password" autocomplete="new-password" bind:value={repeat} required /></label>
			<p class="muted text-sm">{t('setup.password_hint')}</p>

			<fieldset class="space-y-2">
				<legend class="label">{t('signup.who')}</legend>
				{#if mode === 'approval' && free.length}
					<label class="flex items-center gap-2 text-sm"><input type="radio" bind:group={who} value="new" /> {t('signup.new_player')}</label>
				{/if}
				{#if who === 'new' || mode !== 'approval' || !free.length}
					<input class="input" placeholder={username || t('players.name')} maxlength="40" bind:value={playerName} aria-label={t('players.name')} />
					<p class="muted text-xs">{t('signup.new_hint')}</p>
				{/if}
				{#if mode === 'approval' && free.length}
					<label class="flex items-center gap-2 text-sm"><input type="radio" bind:group={who} value="existing" /> {t('signup.existing_player')}</label>
					{#if who === 'existing'}
						<div class="grid max-h-64 gap-1 overflow-y-auto rounded-lg border border-stone-200 p-1">
							{#each free as p (p.id)}
								<label class="flex cursor-pointer items-center gap-3 rounded-md px-2 py-1.5 {playerId === p.id ? 'bg-stone-100' : 'hover:bg-stone-50'}">
									<input type="radio" class="sr-only" bind:group={playerId} value={p.id} />
									<PlayerAvatar player={p} size={28} />
									<span class="flex-1">{tPlayer(p)}</span>
									{#if playerId === p.id}<span class="text-emerald-700">✓</span>{/if}
								</label>
							{/each}
						</div>
						<p class="muted text-xs">{t('signup.existing_hint')}</p>
					{/if}
				{/if}
			</fieldset>

			{#if error}<p class="error">{error}</p>{/if}
			<button class="btn btn-primary w-full" disabled={busy}>{t('signup.submit')}</button>
			<p class="text-center text-sm"><a class="text-stone-600 hover:underline" href="/login">{t('signup.have_account')}</a></p>
		</form>
	{/if}
</main>
