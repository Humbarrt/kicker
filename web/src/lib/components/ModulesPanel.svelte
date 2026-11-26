<script lang="ts">
	// Modules: discovery, adoption with roles, identify, console (D7, D8). Used by Settings → Modules
	// and the setup wizard (step 7).
	import { api, ApiError, type ModuleInfo, type PortView } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { t, tErr } from '#lib/i18n.svelte.ts';
	import ModuleConsole from '#lib/components/ModuleConsole.svelte';
	import CapSettings from '#lib/components/CapSettings.svelte';

	let mods = $state<ModuleInfo[]>([]);
	let ports = $state<PortView[]>([]);
	let loaded = $state(false);
	let drafts = $state<Record<string, { name: string; roles: Record<string, string> }>>({});
	let consoles = $state<Record<string, boolean>>({});
	let errors = $state<Record<string, string>>({});
	let probing = $state<Record<string, boolean>>({});
	let settings = $state<Record<string, boolean>>({});

	async function load() {
		try {
			const [m, p] = await Promise.all([api.modules(), api.ports()]);
			mods = (m ?? []).sort((a, b) => Number(b.adopted) - Number(a.adopted) || a.name.localeCompare(b.name));
			ports = p ?? [];
			for (const mod of mods) {
				if (!drafts[mod.uid]) {
					drafts[mod.uid] = {
						name: mod.name,
						roles: Object.fromEntries((mod.caps ?? []).map((c) => [c.instance, c.role]))
					};
				}
			}
		} finally {
			loaded = true;
		}
	}
	$effect(() => {
		live.modulesRev;
		load();
	});

	async function act(uid: string, fn: () => Promise<void>, reset = true) {
		errors[uid] = '';
		try {
			await fn();
			if (reset) delete drafts[uid];
			await load();
		} catch (e) {
			// role conflicts carry the details ("… is already assigned to …")
			errors[uid] = e instanceof ApiError && e.code === 'invalid_roles' ? `${tErr(e)} ${e.message}` : tErr(e);
		}
	}

	const statusColor = (s: string) =>
		s === 'ok' ? 'bg-emerald-100 text-emerald-800' : s === 'fault' ? 'bg-red-100 text-red-800' : s === 'degraded' ? 'bg-amber-100 text-amber-800' : '';
</script>

{#if loaded && mods.length === 0}
	<p class="card muted">{t('modules.none')}</p>
{/if}

<div class="space-y-4">
	{#each mods as m (m.uid)}
		{@const d = drafts[m.uid]}
		<section class="card">
			<div class="flex flex-wrap items-start gap-3">
				<div class="min-w-0 flex-1">
					<div class="flex flex-wrap items-center gap-2">
						<span class="size-2.5 rounded-full {m.online ? 'bg-emerald-500' : 'bg-stone-300'}" title={m.online ? t('modules.online') : t('modules.offline')}></span>
						<h2 class="h2 truncate">{m.name}</h2>
						<span class="chip {m.adopted ? 'bg-emerald-50 text-emerald-700' : 'bg-sky-100 text-sky-800'}">{m.adopted ? t('modules.adopted') : t('modules.new')}</span>
						{#if m.virtual}<span class="chip bg-amber-100 text-amber-800">{t('modules.virtual')}</span>{/if}
						{#if !m.online}<span class="chip">{t('modules.offline')}</span>{/if}
					</div>
					<p class="muted mt-1 font-mono text-xs">
						{m.uid} · {m.port || '—'} · {m.manifest} · {t('modules.firmware')} {m.firmware} {m.fw_version}
					</p>
				</div>
				<div class="flex flex-wrap gap-2">
					<button class="btn btn-sm" disabled={!m.online} title={t('modules.identify_hint')} onclick={() => act(m.uid, () => api.identify(m.uid), false)}>{t('modules.identify')}</button>
					<button class="btn btn-sm" onclick={() => (consoles[m.uid] = !consoles[m.uid])}>{t('modules.console')}</button>
				</div>
			</div>

			{#if d}
				<div class="mt-4 overflow-x-auto">
					<table class="w-full text-sm">
						<thead class="text-left text-xs text-stone-500 uppercase">
							<tr><th class="py-1 pr-4">{t('modules.instance')}</th><th class="pr-4">{t('modules.capability')}</th><th class="pr-4">{t('modules.status')}</th><th>{t('modules.role')}</th></tr>
						</thead>
						<tbody>
							{#each m.caps ?? [] as c (c.instance)}
								<tr class="border-t border-stone-100">
									<td class="py-2 pr-4 font-mono">{c.instance}</td>
									<td class="pr-4">{c.capability}</td>
									<td class="pr-4">
										{#if c.status}<span class="chip {statusColor(c.status)}" title={c.status_msg}>{c.status}{c.status_msg ? ': ' + c.status_msg : ''}</span>{/if}
									</td>
									<td class="py-1">
										<div class="flex items-center gap-2">
										{#if c.supported && c.roles?.length}
											<select class="input py-1" bind:value={d.roles[c.instance]}>
												<option value="">{t('modules.no_role')}</option>
												{#each c.roles as r (r)}<option value={r}>{r}</option>{/each}
											</select>
										{:else}
											<span class="muted">{c.supported ? '—' : t('modules.unsupported')}</span>
										{/if}
										{#if m.adopted && c.supported && c.capability.startsWith('goal_sensor@')}
											<button class="btn btn-sm" onclick={() => (settings[m.uid + '/' + c.instance] = !settings[m.uid + '/' + c.instance])}>{t('modules.settings')}</button>
										{/if}
										</div>
									</td>
								</tr>
								{#if settings[m.uid + '/' + c.instance]}
									<tr><td colspan="4" class="pb-3"><CapSettings uid={m.uid} cap={c} onsaved={load} /></td></tr>
								{/if}
							{/each}
						</tbody>
					</table>
				</div>

				<div class="mt-4 flex flex-wrap items-end gap-3">
					<label class="min-w-48 flex-1">
						<span class="label">{t('modules.name')}</span>
						<input class="input" bind:value={d.name} maxlength="80" />
					</label>
					<button class="btn btn-primary" disabled={!m.online && !m.adopted}
						onclick={() => act(m.uid, () => api.adopt(m.uid, d.name, d.roles))}>
						{m.adopted ? t('modules.save') : t('modules.adopt')}
					</button>
					{#if m.adopted}
						<button class="btn btn-danger" onclick={() => confirm(t('modules.release_confirm')) && act(m.uid, () => api.release(m.uid))}>{t('modules.release')}</button>
					{/if}
				</div>
			{/if}
			{#if errors[m.uid]}<p class="error mt-3">{errors[m.uid]}</p>{/if}
			{#if consoles[m.uid]}<ModuleConsole uid={m.uid} />{/if}
		</section>
	{/each}

	{#if ports.length}
		<section class="card">
			<h2 class="h2">{t('modules.ports')}</h2>
			<p class="muted mb-3">{t('modules.ports_hint')}</p>
			<ul class="divide-y divide-stone-100">
				{#each ports as p (p.name)}
					<li class="flex items-center gap-3 py-2 text-sm">
						<span class="flex-1 font-mono">{p.name} <span class="text-stone-400">{p.vid}:{p.pid} {p.product}</span></span>
						<button class="btn btn-sm" disabled={probing[p.name]} onclick={async () => { probing[p.name] = true; await api.probe(p.name); }}>
							{probing[p.name] ? t('modules.probing') : t('modules.probe')}
						</button>
					</li>
				{/each}
			</ul>
		</section>
	{/if}
</div>
