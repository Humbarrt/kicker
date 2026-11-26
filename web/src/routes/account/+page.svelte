<script lang="ts">
	import Smartphone from '@lucide/svelte/icons/smartphone';
	import Monitor from '@lucide/svelte/icons/monitor';
	import ArrowRight from '@lucide/svelte/icons/arrow-right';
	import { goto } from '$app/navigation';
	import { api } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { t, tErr, tGroup } from '#lib/i18n.svelte.ts';
	import LanguagePicker from '#lib/components/LanguagePicker.svelte';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';
	import PlayerEditor from '#lib/components/PlayerEditor.svelte';
	import { tPlayer } from '#lib/i18n.svelte.ts';
	import type { DeviceSession, Player } from '#lib/api/client.ts';
	import { describeAgent } from '#lib/device.ts';
	import { ago } from '#lib/format.ts';
	import { i18n } from '#lib/i18n.svelte.ts';

	// My devices (spec 03 §5): every session of this account; others can be ended.
	let devices = $state<DeviceSession[]>([]);
	let deviceError = $state('');
	async function loadDevices() {
		devices = await api.sessions().catch(() => []);
	}
	$effect(() => {
		if (session.account) loadDevices();
	});
	async function endDevice(id?: number) {
		deviceError = '';
		try {
			await (id ? api.endSession(id) : api.endOtherSessions());
		} catch (e) {
			deviceError = tErr(e);
		}
		await loadDevices();
	}

	let myPlayer = $state<Player | null>(null);
	let creating = $state(false);
	$effect(() => {
		const id = session.account?.player_id;
		if (id) api.player(id).then((p) => (myPlayer = p)).catch(() => {});
		else myPlayer = null;
	});

	let current = $state('');
	let next = $state('');
	let repeat = $state('');
	let error = $state('');
	let done = $state('');
	let busy = $state(false);

	$effect(() => {
		if (session.loaded && !session.account) goto('/login', { replaceState: true });
	});

	async function change(e: SubmitEvent) {
		e.preventDefault();
		error = done = '';
		if (next !== repeat) {
			error = t('account.mismatch');
			return;
		}
		busy = true;
		try {
			await api.changePassword(current, next);
			current = next = repeat = '';
			done = t('account.changed');
			await session.refresh();
			live.reconnect();
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}

	async function logout() {
		await api.logout().catch(() => {});
		await session.refresh();
		live.reconnect();
		goto('/');
	}
</script>

{#if session.account}
	{@const a = session.account}
	<main class="mx-auto max-w-lg space-y-6 px-4 py-8">
		<section class="card">
			<div class="flex items-start justify-between gap-4">
				<div>
					<h1 class="h1">{a.username}</h1>
					<p class="muted">{t('account.group')}: {tGroup(a.group)}</p>
				</div>
				<button class="btn" onclick={logout}>{t('account.logout')}</button>
			</div>
			{#if a.status === 'pending'}<p class="notice mt-4">{t('account.pending')}</p>{/if}
			<div class="mt-4"><LanguagePicker /></div>
		</section>

		{#if a.status === 'approved' && !a.must_change_password}
			<section class="card">
				<h2 class="h2 mb-3">{t('players.your_player')}</h2>
				{#if myPlayer}
					<a href="/players/{myPlayer.id}" class="flex items-center gap-3 rounded-xl p-2 hover:bg-stone-50">
						<PlayerAvatar player={myPlayer} size={48} />
						<span class="flex-1 font-medium">{tPlayer(myPlayer)}</span>
						<span class="muted inline-flex items-center gap-1">{t('players.open')} <ArrowRight class="size-4" aria-hidden="true" /></span>
					</a>
				{:else if session.can('profile.own')}
					<p class="muted mb-3">{t('players.create_mine_hint')}</p>
					<button class="btn btn-primary" onclick={() => (creating = true)}>{t('players.create_mine')}</button>
				{/if}
			</section>
		{/if}

		<form class="card space-y-4" onsubmit={change}>
			<h2 class="h2">{t('account.password')}</h2>
			{#if a.must_change_password}<p class="notice">{t('account.must_change')}</p>{/if}
			<label class="block"><span class="label">{t('account.current')}</span>
				<input class="input" type="password" autocomplete="current-password" bind:value={current} required /></label>
			<label class="block"><span class="label">{t('account.new')}</span>
				<input class="input" type="password" autocomplete="new-password" minlength="10" bind:value={next} required /></label>
			<label class="block"><span class="label">{t('account.repeat')}</span>
				<input class="input" type="password" autocomplete="new-password" bind:value={repeat} required /></label>
			<p class="muted">{t('setup.password_hint')}</p>
			{#if error}<p class="error">{error}</p>{/if}
			{#if done}<p class="rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800">{done}</p>{/if}
			<button class="btn btn-primary" disabled={busy}>{t('account.password')}</button>
		</form>

		<section class="card" data-testid="devices">
			<div class="mb-2 flex items-baseline justify-between gap-2">
				<h2 class="h2">{t('devices.title')}</h2>
				{#if devices.length > 1}
					<button class="text-sm text-stone-500 hover:text-stone-900" onclick={() => endDevice()}>{t('devices.end_others')}</button>
				{/if}
			</div>
			<p class="muted mb-3 text-sm">{t('devices.hint')}</p>
			<ul class="divide-y divide-stone-100">
				{#each devices as d (d.id)}
					<li class="flex items-center gap-3 py-2.5">
						<span class="grid size-9 shrink-0 place-items-center rounded-full bg-stone-100 text-stone-500" aria-hidden="true">
							{#if d.kind === 'app'}
								<Smartphone class="size-4" />
							{:else}
								<Monitor class="size-4" />
							{/if}
						</span>
						<span class="min-w-0 flex-1">
							<span class="block truncate text-sm font-medium">
								{d.device_name || describeAgent(d.user_agent) || t(d.kind === 'app' ? 'devices.app' : 'devices.browser')}
								{#if d.current}<span class="chip ml-1">{t('devices.this')}</span>{/if}
							</span>
							<span class="block text-xs text-stone-500">{t('devices.seen', { when: ago(d.last_seen_at, Date.now(), i18n.lang) })}</span>
						</span>
						{#if !d.current}
							<button class="btn btn-sm" onclick={() => endDevice(d.id)}>{t('devices.end')}</button>
						{/if}
					</li>
				{/each}
			</ul>
			{#if deviceError}<p class="error mt-2">{deviceError}</p>{/if}
		</section>
	</main>
{/if}

<PlayerEditor bind:open={creating} mine onsaved={() => session.refresh()} />
