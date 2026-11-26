<script lang="ts">
	// Modules: discovery, adoption with roles, identify, console (D7, D8). Used by Settings → Modules
	// and the setup wizard (step 7).
	import { api, ApiError, type FirmwareJob, type FirmwareStatus, type ModuleInfo, type PortView } from '#lib/api/client.ts';
	import { dateTime } from '#lib/format.ts';
	import Download from '@lucide/svelte/icons/download';
	import Upload from '@lucide/svelte/icons/upload';
	import CircleCheck from '@lucide/svelte/icons/circle-check';
	import CircleX from '@lucide/svelte/icons/circle-x';
	import { live } from '#lib/live.svelte.ts';
	import { i18n, t, tErr, type Key } from '#lib/i18n.svelte.ts';
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

	// --- firmware (spec 10 §4)
	let fw = $state<FirmwareStatus | null>(null);
	let fwError = $state('');
	let logs = $state<Record<string, boolean>>({});
	let driveFile = $state<Record<string, string>>({});
	async function loadFirmware() {
		try {
			fw = await api.firmware();
		} catch {
			fw = null; // not an admin, or an old server
		}
	}
	$effect(() => {
		void live.firmwareRev;
		loadFirmware();
	});
	const offerFor = (uid: string) => fw?.offers.find((o) => o.uid === uid);
	const jobFor = (uid: string) => fw?.jobs.find((j) => j.uid === uid);
	const active = (j: FirmwareJob | undefined) => !!j && j.step !== 'done' && j.step !== 'failed';
	const fwSteps = ['download', 'bootloader', 'write', 'restart'] as const;
	async function fwAct(f: () => Promise<FirmwareStatus>) {
		fwError = '';
		try {
			fw = await f();
		} catch (e) {
			fwError = tErr(e);
		}
	}
	function pickUF2(onfile: (f: File) => void) {
		const input = document.createElement('input');
		input.type = 'file';
		input.accept = '.uf2';
		input.onchange = () => input.files?.[0] && onfile(input.files[0]);
		input.click();
	}
	function upload(m: ModuleInfo) {
		if (!confirm(t('fw.upload_confirm', { name: m.name }))) return;
		pickUF2((f) => fwAct(() => api.uploadFirmware(m.uid, f)));
	}

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

{#snippet job(j: FirmwareJob)}
	<div class="mt-3 rounded-xl bg-stone-50 p-3 text-sm" data-testid="fw-job-{j.uid}">
		{#if active(j)}
			<div class="flex flex-wrap items-center gap-2">
				<span class="font-medium">{t('fw.updating', { version: j.to || j.file })}</span>
				{#each fwSteps as s, i (s)}
					{@const done = fwSteps.indexOf(j.step as (typeof fwSteps)[number]) > i}
					<span class="chip {done ? 'bg-emerald-100 text-emerald-800' : j.step === s ? 'bg-stone-800 text-white' : 'bg-stone-100 text-stone-500'}">{t(('fw.step.' + s) as Key)}</span>
				{/each}
			</div>
			<p class="muted mt-1">{t('fw.dont_unplug')}</p>
		{:else if j.step === 'done'}
			<div class="flex items-center gap-2 text-emerald-800"><CircleCheck class="size-4" aria-hidden="true" />{j.to ? t('fw.done', { from: j.from || '?', to: j.to }) : t('fw.done_file', { file: j.file })}
				<span class="opacity-75">· {dateTime(j.ended ?? j.started, i18n.lang)}</span></div>
		{:else}
			<div class="flex items-start gap-2 text-red-800"><CircleX class="mt-0.5 size-4 shrink-0" aria-hidden="true" /><span>{t('fw.failed')}: {j.error}</span></div>
		{/if}
		{#if j.log.length}
			<button class="mt-1 text-xs text-stone-500 underline underline-offset-2" onclick={() => (logs[j.uid] = !logs[j.uid])}>{logs[j.uid] ? t('fw.hide_log') : t('fw.show_log')}</button>
			{#if logs[j.uid]}<pre class="mt-1 overflow-x-auto font-mono text-xs whitespace-pre-wrap text-stone-600">{j.log.join('\n')}</pre>{/if}
		{/if}
	</div>
{/snippet}

{#if fw && (fw.offers.length > 1 || fw.drives.length || (fw.offers.length && fw.busy))}
	<section class="card mb-4 space-y-3" data-testid="fw-summary">
		{#if fw.offers.length > 1}
			<div class="flex flex-wrap items-center gap-3">
				<div class="min-w-0 flex-1">
					<h2 class="h2">{t('fw.title')}</h2>
					<p class="muted text-sm">{t('fw.n_available', { n: fw.offers.length })}</p>
				</div>
				<button class="btn btn-primary" disabled={fw.busy || fw.offers.every((o) => active(jobFor(o.uid)))}
					onclick={() => fwAct(() => api.updateFirmware(fw!.offers.map((o) => o.uid)))} data-testid="fw-update-all">
					<Download class="size-4" aria-hidden="true" />{t('fw.update_all')}
				</button>
			</div>
		{/if}
		{#if fw.busy && fw.offers.length}<p class="notice">{t('fw.busy')}</p>{/if}
		{#each fw.drives as d (d.name)}
			<div class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm" data-testid="fw-drive">
				<p class="font-medium">{t('fw.drive', { chip: d.chip, usb: d.usb_path || d.dev })}</p>
				<p class="muted">{t('fw.drive_hint')}</p>
				<div class="mt-3 flex flex-wrap items-center gap-2">
					{#if fw.files.length}
						<select class="input w-auto flex-1" bind:value={driveFile[d.name]}>
							<option value="">{t('fw.choose_file')}</option>
							{#each fw.files as f (f.name)}<option value={f.name}>{f.mf} {f.version} · {f.hw}</option>{/each}
						</select>
						<button class="btn btn-sm" disabled={!driveFile[d.name] || fw.busy} onclick={() => fwAct(() => api.flashDrive(d.name, driveFile[d.name]))}>{t('fw.flash')}</button>
					{/if}
					<button class="btn btn-sm" disabled={fw.busy} onclick={() => confirm(t('fw.upload_confirm_drive')) && pickUF2((f) => fwAct(() => api.uploadToDrive(d.name, f)))}>
						<Upload class="size-4" aria-hidden="true" />{t('fw.upload')}
					</button>
				</div>
			</div>
		{/each}
		{#each fw.jobs.filter((j) => j.uid.startsWith('drive:')) as j (j.uid)}{@render job(j)}{/each}
	</section>
{/if}
{#if fwError}<p class="error mb-4">{fwError}</p>{/if}

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
					{#if offerFor(m.uid) && !active(jobFor(m.uid))}
						{@const o = offerFor(m.uid)!}
						<div class="mt-2 flex flex-wrap items-center gap-2" data-testid="fw-offer">
							<span class="chip bg-sky-100 text-sky-800">{t('fw.available', { version: o.version })}</span>
							<button class="btn btn-sm" disabled={fw?.busy} title={fw?.busy ? t('fw.busy') : ''}
								onclick={() => fwAct(() => api.updateFirmware([m.uid]))} data-testid="fw-update">{t('fw.update')}</button>
						</div>
					{/if}
				</div>
				<div class="flex flex-wrap gap-2">
					<button class="btn btn-sm" disabled={!m.online} title={t('modules.identify_hint')} onclick={() => act(m.uid, () => api.identify(m.uid), false)}>{t('modules.identify')}</button>
					<button class="btn btn-sm" onclick={() => (consoles[m.uid] = !consoles[m.uid])}>{t('modules.console')}</button>
					{#if fw && m.online && m.update === 'uf2'}
						<button class="btn btn-sm" disabled={fw.busy || active(jobFor(m.uid))} title={t('fw.upload_hint')} onclick={() => upload(m)}>
							<Upload class="size-4" aria-hidden="true" />{t('fw.upload')}
						</button>
					{/if}
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
			{#if jobFor(m.uid)}{@render job(jobFor(m.uid)!)}{/if}
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
