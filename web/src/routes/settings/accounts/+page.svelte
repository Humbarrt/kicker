<script lang="ts">
	import { api, type Account, type Group, type Player, type RegistrationSettings } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, tGroup, tPlayer, type Key } from '#lib/i18n.svelte.ts';
	import { dateTime } from '#lib/format.ts';

	let accounts = $state<Account[]>([]);
	let groups = $state<Group[]>([]);
	let players = $state<Player[]>([]);
	let error = $state('');
	let form = $state({ username: '', password: '', group_id: 0, must_change_password: true });

	const assignable = $derived(groups.filter((g) => g.builtin !== 'anybody'));

	let reg = $state<RegistrationSettings | null>(null);
	async function load() {
		const [a, g, p, r] = await Promise.all([api.accounts(), api.groups(), api.players(true), api.registration()]);
		accounts = a;
		groups = g;
		players = p.filter((x) => !x.guest);
		reg = r;
		if (!form.group_id) form.group_id = g.find((x) => x.builtin === 'member')?.id ?? 0;
		for (const x of a.filter((x) => x.status === 'pending')) {
			approval[x.id] ??= {
				group_id: r.default_group_id,
				player: x.requested_player_id ? String(x.requested_player_id) : 'new',
				name: x.requested_player_name ?? x.username
			};
		}
	}
	$effect(() => {
		void session.me?.pending_accounts; // new sign-ups arrive over SSE
		load().catch((e) => (error = tErr(e)));
	});
	void live;

	// Registration (spec 03 §3).
	async function saveReg(change: Partial<RegistrationSettings>) {
		if (!reg) return;
		error = '';
		try {
			reg = await api.saveRegistration({ ...reg, ...change });
			await session.refresh();
		} catch (e) {
			error = tErr(e);
		}
	}

	// Pending sign-ups: group and player are decided here; linking to an existing player is the
	// confirmation that nobody takes over someone else's history.
	const pending = $derived(accounts.filter((a) => a.status === 'pending'));
	const others = $derived(accounts.filter((a) => a.status !== 'pending'));
	let approval = $state<Record<number, { group_id: number; player: string; name: string }>>({});
	async function approve(a: Account) {
		const x = approval[a.id];
		error = '';
		try {
			await api.approveAccount(a.id, {
				group_id: x.group_id,
				player_id: x.player !== 'new' && x.player !== 'none' ? Number(x.player) : undefined,
				new_player_name: x.player === 'new' ? x.name.trim() : undefined
			});
			delete approval[a.id];
			await session.refresh();
		} catch (e) {
			error = tErr(e);
		}
		await load();
	}
	async function reject(a: Account) {
		if (!confirm(t('accounts.reject_confirm', { name: a.username }))) return;
		error = '';
		try {
			await api.rejectAccount(a.id);
			await session.refresh();
		} catch (e) {
			error = tErr(e);
		}
		await load();
	}
	const freePlayers = $derived(players.filter((p) => !p.has_account && !p.archived));

	// Reset codes (spec 03 §7): shown once, passed on in person.
	let code = $state<{ user: string; code: string; expires_at: number } | null>(null);
	async function resetCode(a: Account) {
		error = '';
		try {
			const c = await api.resetCode(a.id);
			code = { user: a.username, ...c };
		} catch (e) {
			error = tErr(e);
		}
	}

	// players that can be linked to this account: its own and those without an account
	const linkable = (a: Account) => players.filter((p) => !p.has_account || p.id === a.player_id);

	async function update(a: Account, change: { group_id?: number; status?: string; player_id?: number }) {
		error = '';
		try {
			await api.updateAccount(a.id, change);
			if (a.id === session.account?.id) await session.refresh();
		} catch (e) {
			error = tErr(e);
		}
		await load();
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		try {
			await api.createAccount(form);
			form = { ...form, username: '', password: '' };
			await load();
		} catch (err) {
			error = tErr(err);
		}
	}
</script>

<div class="space-y-6">
	{#if error}<p class="error">{error}</p>{/if}

	{#if code}
		<section class="card border-2 border-amber-300" data-testid="reset-code">
			<h2 class="h2">{t('accounts.code_for', { name: code.user })}</h2>
			<p class="my-3 font-mono text-3xl font-bold tracking-widest">{code.code}</p>
			<p class="muted text-sm">{t('accounts.code_hint', { until: dateTime(code.expires_at, i18n.lang) })}</p>
			<button class="btn btn-sm mt-3" onclick={() => (code = null)}>{t('app.close')}</button>
		</section>
	{/if}

	{#if pending.length}
		<section class="card space-y-3" data-testid="pending">
			<h2 class="h2">{t('accounts.pending', { n: pending.length })}</h2>
			{#each pending as a (a.id)}
				{@const x = approval[a.id]}
				{#if x}
					<div class="grid gap-3 rounded-xl border border-stone-200 p-3 sm:grid-cols-[1fr_auto] sm:items-end">
						<div class="space-y-2">
							<div class="font-medium">{a.username} <span class="text-sm font-normal text-stone-500">· {dateTime(a.created_at, i18n.lang)}</span></div>
							<div class="text-sm text-stone-600">
								{#if a.requested_player_id}
									{t('accounts.asks_player', { name: tPlayer(players.find((p) => p.id === a.requested_player_id) ?? { name: '?' }) })}
								{:else if a.requested_player_name}
									{t('accounts.asks_new', { name: a.requested_player_name })}
								{/if}
							</div>
							<div class="flex flex-wrap items-center gap-2 text-sm">
								<select class="input w-auto py-1" aria-label={t('accounts.group')} bind:value={x.group_id}>
									{#each assignable as g (g.id)}<option value={g.id}>{tGroup(g)}</option>{/each}
								</select>
								<select class="input w-auto py-1" aria-label={t('accounts.player')} bind:value={x.player}>
									<option value="new">{t('accounts.new_player')}</option>
									<option value="none">{t('players.no_player')}</option>
									{#each freePlayers as p (p.id)}<option value={String(p.id)}>{tPlayer(p)}</option>{/each}
								</select>
								{#if x.player === 'new'}
									<input class="input w-40 py-1" maxlength="40" aria-label={t('players.name')} bind:value={x.name} />
								{/if}
							</div>
						</div>
						<div class="flex gap-2">
							<button class="btn" onclick={() => reject(a)}>{t('accounts.reject')}</button>
							<button class="btn btn-primary" onclick={() => approve(a)}>{t('accounts.approve')}</button>
						</div>
					</div>
				{/if}
			{/each}
		</section>
	{/if}

	<section class="card overflow-x-auto p-0">
		<table class="w-full text-sm">
			<thead class="bg-stone-50 text-left text-xs text-stone-500 uppercase">
				<tr>
					<th class="px-4 py-2">{t('accounts.username')}</th>
					<th class="px-4">{t('accounts.group')}</th>
					<th class="px-4">{t('accounts.player')}</th>
					<th class="px-4">{t('accounts.status')}</th>
					<th class="px-4">{t('accounts.last_login')}</th>
					<th class="px-4"></th>
				</tr>
			</thead>
			<tbody>
				{#each others as a (a.id)}
					<tr class="border-t border-stone-100">
						<td class="px-4 py-2 font-medium">
							{a.username}
							{#if a.id === session.account?.id}<span class="chip ml-1">{t('accounts.you')}</span>{/if}
						</td>
						<td class="px-4">
							<select class="input w-auto py-1" value={a.group.id} onchange={(e) => update(a, { group_id: Number(e.currentTarget.value) })}>
								{#each assignable as g (g.id)}<option value={g.id}>{tGroup(g)}</option>{/each}
							</select>
						</td>
						<td class="px-4">
							<select class="input w-auto py-1" value={a.player_id ?? 0} onchange={(e) => update(a, { player_id: Number(e.currentTarget.value) })}>
								<option value={0}>{t('players.no_player')}</option>
								{#each linkable(a) as p (p.id)}<option value={p.id}>{tPlayer(p)}</option>{/each}
							</select>
						</td>
						<td class="px-4">
							<select class="input w-auto py-1" value={a.status} onchange={(e) => update(a, { status: e.currentTarget.value })}>
								{#each ['approved', 'disabled'] as s (s)}
									<option value={s}>{t(('accounts.status.' + s) as Key)}</option>
								{/each}
							</select>
						</td>
						<td class="px-4 text-stone-500">{a.last_login_at ? dateTime(a.last_login_at, i18n.lang) : t('accounts.never')}</td>
						<td class="px-4 text-right whitespace-nowrap">
							{#if a.id !== session.account?.id}
								<button class="btn btn-sm" onclick={() => resetCode(a)}>{t('accounts.reset')}</button>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</section>

	<form class="card grid gap-3 sm:grid-cols-[1fr_1fr_auto_auto] sm:items-end" autocomplete="off" onsubmit={create}>
		<h2 class="h2 sm:col-span-4">{t('accounts.new')}</h2>
		<label><span class="label">{t('accounts.username')}</span>
			<input class="input" autocomplete="off" autocapitalize="none" bind:value={form.username} required /></label>
		<label><span class="label">{t('accounts.temp_password')}</span>
			<input class="input" type="text" autocomplete="off" minlength="10" bind:value={form.password} required /></label>
		<label><span class="label">{t('accounts.group')}</span>
			<select class="input" bind:value={form.group_id}>
				{#each assignable as g (g.id)}<option value={g.id}>{tGroup(g)}</option>{/each}
			</select></label>
		<button class="btn btn-primary">{t('app.create')}</button>
		<label class="flex items-center gap-2 text-sm sm:col-span-4">
			<input type="checkbox" autocomplete="off" bind:checked={form.must_change_password} /> {t('accounts.must_change')}
		</label>
	</form>

	{#if reg}
		<section class="card space-y-3" data-testid="registration">
			<h2 class="h2">{t('reg_settings.title')}</h2>
			<div class="grid gap-2">
				{#each ['approval', 'open', 'closed'] as const as m (m)}
					<label class="flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-2 {reg.mode === m ? 'border-emerald-600 bg-emerald-50' : 'border-stone-200'}">
						<input type="radio" class="mt-1" name="regmode" checked={reg.mode === m} onchange={() => saveReg({ mode: m })} />
						<span>
							<span class="block font-medium">{t(`reg_settings.${m}` as Key)}</span>
							<span class="block text-sm text-stone-600">{t(`reg_settings.${m}_hint` as Key)}</span>
						</span>
					</label>
				{/each}
			</div>
			{#if reg.mode === 'open'}
				<label class="flex flex-wrap items-center gap-2 text-sm">{t('reg_settings.open_group')}
					<select class="input w-auto py-1" value={reg.open_group_id} onchange={(e) => saveReg({ open_group_id: Number(e.currentTarget.value) })}>
						{#each assignable.filter((g) => g.builtin !== 'admin') as g (g.id)}<option value={g.id}>{tGroup(g)}</option>{/each}
					</select></label>
			{:else if reg.mode === 'approval'}
				<label class="flex flex-wrap items-center gap-2 text-sm">{t('reg_settings.default_group')}
					<select class="input w-auto py-1" value={reg.default_group_id} onchange={(e) => saveReg({ default_group_id: Number(e.currentTarget.value) })}>
						{#each assignable as g (g.id)}<option value={g.id}>{tGroup(g)}</option>{/each}
					</select></label>
			{/if}
		</section>
	{/if}
</div>
