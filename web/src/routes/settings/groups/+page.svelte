<script lang="ts">
	// Groups & permissions (spec 03 §4): one group per account, Anybody's permissions apply to all.
	import { api, type Group, type Permission } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr, tGroup, tPerm, type Key } from '#lib/i18n.svelte.ts';
	import PermissionValue from '#lib/components/PermissionValue.svelte';

	let catalog = $state<Permission[]>([]);
	let groups = $state<Group[]>([]);
	let selected = $state<number | null>(null);
	let draft = $state<Record<string, number>>({});
	let name = $state('');
	let error = $state('');
	let saved = $state(false);
	let busy = $state(false);

	let newName = $state('');
	let copyFrom = $state(0);
	let moveTo = $state(0);

	const group = $derived(groups.find((g) => g.id === selected) ?? null);
	const anybody = $derived(groups.find((g) => g.builtin === 'anybody') ?? null);
	const areas = $derived([...new Set(catalog.map((p) => p.area))]);
	const dirty = $derived(
		!!group && group.builtin !== 'admin' &&
			(name.trim() !== tGroup(group) || catalog.some((p) => (draft[p.key] ?? 0) !== (group.permissions[p.key] ?? 0)))
	);

	async function load(keep?: number) {
		const [c, g] = await Promise.all([api.permissions(), api.groups()]);
		catalog = c;
		groups = g;
		select(keep ?? selected ?? g.find((x) => x.builtin === 'member')?.id ?? g[0]?.id);
	}
	$effect(() => {
		load().catch((e) => (error = tErr(e)));
	});

	function select(id: number | undefined) {
		const g = groups.find((x) => x.id === id);
		if (!g) return;
		selected = g.id;
		draft = { ...g.permissions };
		name = tGroup(g);
		error = '';
		saved = false;
		copyFrom = g.id;
		moveTo = groups.find((x) => x.builtin === 'member')?.id ?? 0;
	}

	async function save() {
		if (!group) return;
		busy = true;
		error = '';
		try {
			const changed: Record<string, number> = {};
			for (const p of catalog) if ((draft[p.key] ?? 0) !== (group.permissions[p.key] ?? 0)) changed[p.key] = draft[p.key];
			await api.updateGroup(group.id, {
				...(name.trim() !== tGroup(group) ? { name: name.trim() } : {}),
				...(group.builtin !== 'admin' && Object.keys(changed).length ? { permissions: changed } : {})
			});
			await load(group.id);
			await session.refresh(); // own permissions may have changed
			saved = true;
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		try {
			const g = await api.createGroup(newName, copyFrom);
			newName = '';
			await load(g.id);
		} catch (err) {
			error = tErr(err);
		}
	}

	async function remove() {
		if (!group || !confirm(`${t('groups.delete')}: ${tGroup(group)}?`)) return;
		try {
			await api.deleteGroup(group.id, moveTo);
			selected = null;
			await load();
		} catch (e) {
			error = tErr(e);
		}
	}

	/** The value a group gets from Anybody, if it is more than its own. */
	function fromAnybody(p: Permission): boolean {
		if (!group || !anybody || group.builtin === 'anybody' || group.builtin === 'admin') return false;
		const a = anybody.permissions[p.key] ?? 0;
		const own = draft[p.key] ?? 0;
		if (p.kind === 'days') return a === -1 ? own !== -1 : own !== -1 && a > own;
		return a > own;
	}

	const disabled = (p: Permission) => group?.builtin === 'admin' || (group?.builtin === 'anybody' && p.login_required);
	const shownValue = (p: Permission) => (group?.builtin === 'admin' ? group.permissions[p.key] : draft[p.key] ?? 0);
</script>

<p class="muted mb-4">{t('groups.hint')}</p>
<div class="grid gap-6 lg:grid-cols-[16rem_1fr]">
	<aside class="space-y-4">
		<ul class="card space-y-1 p-2">
			{#each groups as g (g.id)}
				<li>
					<button class="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm {g.id === selected ? 'bg-stone-900 text-white' : 'hover:bg-stone-100'}"
						onclick={() => select(g.id)}>
						<span class="font-medium">{tGroup(g)}</span>
						<span class="text-xs opacity-70">{g.builtin === 'anybody' ? '∗' : t('groups.members', { n: g.members })}</span>
					</button>
				</li>
			{/each}
		</ul>

		<form class="card space-y-3" onsubmit={create}>
			<h2 class="font-semibold">{t('groups.new')}</h2>
			<input class="input" placeholder={t('groups.name')} bind:value={newName} maxlength="40" required />
			<label class="block"><span class="label">{t('groups.copy_from')}</span>
				<select class="input" bind:value={copyFrom}>
					{#each groups as g (g.id)}<option value={g.id}>{tGroup(g)}</option>{/each}
				</select></label>
			<button class="btn w-full">{t('app.create')}</button>
		</form>
	</aside>

	{#if group}
		<section class="card">
			<div class="flex flex-wrap items-end gap-3">
				<label class="min-w-48 flex-1">
					<span class="label">{t('groups.name')}</span>
					<input class="input text-lg font-semibold" bind:value={name} maxlength="40" />
				</label>
				<button class="btn btn-primary" disabled={(!dirty && name.trim() === tGroup(group)) || busy} onclick={save}>{t('app.save')}</button>
			</div>
			{#if group.builtin === 'admin'}<p class="notice mt-4">{t('groups.admin_fixed')}</p>{/if}
			{#if group.builtin === 'anybody'}<p class="notice mt-4">{t('groups.anybody_hint')}</p>{/if}
			{#if error}<p class="error mt-4">{error}</p>{/if}
			{#if saved && !dirty}<p class="mt-4 text-sm text-emerald-700">{t('app.saved')}</p>{/if}
			{#if dirty}<p class="mt-4 text-sm text-amber-700">{t('groups.unsaved')}</p>{/if}

			{#each areas as area (area)}
				<h3 class="mt-6 mb-2 text-xs font-semibold tracking-wider text-stone-500 uppercase">{t(('area.' + area) as Key)}</h3>
				<ul class="divide-y divide-stone-100 rounded-xl border border-stone-100">
					{#each catalog.filter((p) => p.area === area) as p (p.key)}
						<li class="flex flex-wrap items-center gap-3 px-4 py-2.5">
							<div class="min-w-0 flex-1">
								<span class="text-sm">{tPerm(p.key)}</span>
								{#if p.login_required && group.builtin === 'anybody'}<span class="chip ml-2">{t('groups.login_required')}</span>{/if}
								{#if fromAnybody(p)}<span class="chip ml-2 bg-sky-50 text-sky-700">{t('groups.from_anybody')}</span>{/if}
							</div>
							{#if group.builtin === 'admin'}
								<PermissionValue kind={p.kind} value={shownValue(p)} disabled />
							{:else}
								<PermissionValue kind={p.kind} bind:value={draft[p.key]} disabled={disabled(p)} />
							{/if}
						</li>
					{/each}
				</ul>
			{/each}
			<p class="muted mt-6">{t('groups.system')}</p>

			{#if !group.builtin}
				<div class="mt-6 flex flex-wrap items-end gap-3 border-t border-stone-100 pt-6">
					<label><span class="label">{t('groups.move_to')}</span>
						<select class="input" bind:value={moveTo}>
							{#each groups.filter((g) => g.id !== group.id && g.builtin !== 'anybody') as g (g.id)}<option value={g.id}>{tGroup(g)}</option>{/each}
						</select></label>
					<button class="btn btn-danger" onclick={remove}>{t('groups.delete')}</button>
				</div>
			{/if}
		</section>
	{/if}
</div>
