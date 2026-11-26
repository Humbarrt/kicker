<script lang="ts">
	// Backups (admins, spec 10 §2), laid out like Home Assistant's: the backup cycle (schedule,
	// time, retention), the locations every backup goes to, the list of backups with download /
	// restore / delete, and uploading a backup. Restoring tries the current passphrase first and
	// only asks for one when the backup was made with another.
	import { api, type BackupConfig, type BackupEntry, type BackupLocation, type BackupsView } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, type Key } from '#lib/i18n.svelte.ts';
	import { ago, dateTime } from '#lib/format.ts';

	let view = $state<BackupsView | null>(null);
	let cfg = $state<BackupConfig | null>(null);
	let error = $state('');
	let busy = $state(false);
	let now = $state(Date.now());

	function apply(v: BackupsView) {
		view = v;
		cfg = structuredClone(v.config); // from the response: reactive copies can't be cloned
		now = Date.now();
	}
	$effect(() => {
		void live.backupRev; // a run finished, the list changed
		api.backups().then(apply).catch((e) => (error = tErr(e)));
	});

	// The cycle and the locations save as soon as they change.
	let saveTimer: ReturnType<typeof setTimeout> | undefined;
	function save(delay = 400) {
		clearTimeout(saveTimer);
		saveTimer = setTimeout(async () => {
			if (!cfg) return;
			error = '';
			try {
				apply(await api.setBackupConfig($state.snapshot(cfg)));
			} catch (e) {
				error = tErr(e);
			}
		}, delay);
	}

	async function act(f: () => Promise<BackupsView | void>) {
		busy = true;
		error = '';
		try {
			const v = await f();
			if (v) apply(v);
			await session.refresh();
		} catch (e) {
			error = tErr(e);
		} finally {
			busy = false;
		}
	}

	// --- cycle
	const days = [1, 2, 3, 4, 5, 6, 0]; // Monday first
	const dayName = (d: number) => new Date(2026, 10, 22 + d).toLocaleDateString(i18n.lang, { weekday: 'short' }); // 22 Nov 2026 is a Sunday
	function toggleDay(d: number) {
		if (!cfg) return;
		const ds = new Set(cfg.days ?? []);
		if (ds.has(d)) ds.delete(d);
		else ds.add(d);
		cfg.days = [...ds];
		if (cfg.days.length) save(0);
	}
	let customTime = $derived(!!cfg?.time);
	let byDays = $derived(!!cfg?.keep_days);

	// --- locations
	let editing = $state<string | null>(null);
	let adding = $state<'' | 'usb' | 'folder'>('');
	let newPath = $state('');
	let newName = $state('');
	const usedDrives = $derived(new Set(cfg?.locations.filter((x) => x.kind === 'usb').map((x) => x.path)));
	function addLocation() {
		if (!cfg || !newPath.trim()) return;
		cfg.locations.push({ id: `${adding}-${Date.now().toString(36)}`, name: newName.trim(), kind: adding as 'usb' | 'folder', path: newPath.trim(), enabled: true });
		adding = '';
		newPath = newName = '';
		save(0);
	}
	function removeLocation(id: string) {
		if (!cfg) return;
		cfg.locations = cfg.locations.filter((l) => l.id !== id);
		editing = null;
		save(0);
	}
	function locName(l: BackupLocation | undefined): string {
		if (!l) return '';
		if (l.name) return l.name;
		if (l.kind === 'local') return t('backup.local');
		if (l.kind === 'usb') return t('backup.usb_named', { name: l.path });
		return l.path;
	}
	const locById = (id: string) => cfg?.locations.find((l) => l.id === id);

	// --- the list
	type Filter = 'all' | 'auto' | 'manual' | 'update' | 'uploaded';
	let filter = $state<Filter>('all');
	const shown = $derived(
		(view?.backups ?? []).filter((b) =>
			filter === 'all' ? true : filter === 'update' ? b.kind === 'pre-update' || b.kind === 'pre-migration' : b.kind === filter
		)
	);
	let menu = $state<string | null>(null);
	const size = (n: number) => (n < 1 << 20 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);
	const lastOk = $derived.by(() => {
		const all = Object.values(view?.status.locations ?? {}).map((r) => r.last_ok).filter(Boolean) as string[];
		return all.sort().at(-1);
	});

	// --- restore: the current passphrase first, then ask
	let restoring = $state<BackupEntry | null>(null);
	let needPass = $state(false);
	let passphrase = $state('');
	let restoreError = $state('');
	let restarting = $state(false);
	let dialog: HTMLDialogElement | undefined = $state();
	function askRestore(b: BackupEntry) {
		restoring = b;
		needPass = false;
		passphrase = restoreError = '';
		menu = null;
		dialog?.showModal();
	}
	async function restore() {
		if (!restoring) return;
		busy = true;
		restoreError = '';
		try {
			const r = await api.restoreStored(restoring.file, needPass ? passphrase : '');
			restarting = true;
			if (r.restarting) await waitForRestart();
		} catch (e) {
			if ((e as { code?: string }).code === 'wrong_passphrase') {
				if (needPass) restoreError = tErr(e);
				needPass = true;
			} else restoreError = tErr(e);
		} finally {
			busy = false;
		}
	}
	async function waitForRestart() {
		await new Promise((r) => setTimeout(r, 1500));
		for (let i = 0; i < 120; i++) {
			try {
				if ((await fetch('/api/info', { cache: 'no-store' })).ok) {
					location.href = '/';
					return;
				}
			} catch {
				/* not back yet */
			}
			await new Promise((r) => setTimeout(r, 1000));
		}
		restoreError = t('backup.not_back');
	}

	// --- upload: it joins the list, then the restore starts
	let uploadFile = $state<File | null>(null);
	async function upload(e: SubmitEvent) {
		e.preventDefault();
		if (!uploadFile) return;
		await act(async () => {
			const { file } = await api.uploadBackup(uploadFile!);
			const v = await api.backups();
			apply(v);
			uploadFile = null;
			(e.target as HTMLFormElement).reset();
			const b = v.backups.find((x) => x.file === file);
			if (b) askRestore(b);
		});
	}
</script>

{#snippet icon(kind: string)}
	<svg viewBox="0 0 24 24" class="size-5 shrink-0 text-stone-700" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
		{#if kind === 'local'}
			<rect x="3" y="4" width="18" height="16" rx="2" /><circle cx="12" cy="11" r="3.5" /><path d="M7 17h.01" />
		{:else if kind === 'usb'}
			<rect x="7" y="9" width="10" height="12" rx="1.5" /><path d="M9 9V4h6v5M10.5 6h.01M13.5 6h.01" />
		{:else}
			<rect x="3" y="4" width="18" height="7" rx="1.5" /><rect x="3" y="13" width="18" height="7" rx="1.5" /><path d="M7 7.5h.01M7 16.5h.01" />
		{/if}
	</svg>
{/snippet}

{#snippet row(title: string, hint: string)}
	<div class="min-w-0 flex-1">
		<div class="font-medium">{title}</div>
		<div class="muted text-sm">{hint}</div>
	</div>
{/snippet}

<div class="space-y-6">
	{#if view?.pending}
		<p class="notice" data-testid="restore-pending">{t('backup.pending', { name: view.pending.name, when: dateTime(view.pending.created, i18n.lang) })}</p>
	{/if}

	{#if cfg && view}
		<!-- Overview -->
		<section class="card flex flex-wrap items-center gap-4" data-testid="backup-overview">
			<div class="min-w-0 flex-1">
				<h2 class="h2">{t('backup.title')}</h2>
				<p class="text-sm {view.problem ? 'text-red-700' : 'muted'}">
					{lastOk ? t('backup.last_ok', { when: ago(Date.parse(lastOk), now, i18n.lang) }) : t('backup.never')}
					{#if view.next_run} · {t('backup.next', { when: dateTime(view.next_run, i18n.lang) })}{/if}
				</p>
			</div>
			<button class="btn btn-primary" disabled={busy} onclick={() => act(api.runBackup)} data-testid="backup-now">{busy ? t('backup.working') : t('backup.now')}</button>
		</section>

		<!-- Backup cycle -->
		<section class="card space-y-5" data-testid="backup-cycle">
			<div>
				<h2 class="h2">{t('backup.cycle')}</h2>
				<p class="muted text-sm">{t('backup.cycle_hint')}</p>
			</div>
			<div class="flex flex-wrap items-center gap-4">
				{@render row(t('backup.schedule'), t('backup.schedule_hint'))}
				<select class="input w-full sm:w-56" bind:value={cfg.schedule} onchange={() => {
					if (cfg!.schedule === 'days' && !cfg!.days?.length) cfg!.days = [1, 2, 3, 4, 5];
					save(0);
				}} data-testid="backup-schedule">
					<option value="daily">{t('backup.daily')}</option>
					<option value="days">{t('backup.custom_days')}</option>
					<option value="manual">{t('backup.manual_only')}</option>
				</select>
				{#if cfg.schedule === 'days'}
					<div class="flex w-full flex-wrap justify-end gap-1">
						{#each days as d (d)}
							<button type="button" class="chip {cfg.days?.includes(d) ? 'bg-emerald-700 text-white' : 'bg-stone-100 text-stone-700'}" onclick={() => toggleDay(d)}>{dayName(d)}</button>
						{/each}
					</div>
				{/if}
			</div>
			{#if cfg.schedule !== 'manual'}
				<div class="flex flex-wrap items-center gap-4">
					{@render row(t('backup.time'), t('backup.time_hint', { time: view.auto_time }))}
					<div class="flex w-full gap-2 sm:w-56">
						<select class="input flex-1" value={customTime ? 'custom' : 'auto'} onchange={(e) => {
							cfg!.time = (e.currentTarget as HTMLSelectElement).value === 'custom' ? view!.auto_time : '';
							save(0);
						}} data-testid="backup-time-mode">
							<option value="auto">{t('backup.time_auto')}</option>
							<option value="custom">{t('backup.time_custom')}</option>
						</select>
						{#if customTime}<input class="input w-32" type="time" bind:value={cfg.time} oninput={() => save()} />{/if}
					</div>
				</div>
			{/if}
			<div class="flex flex-wrap items-center gap-4">
				{@render row(t('backup.retention'), t('backup.retention_hint'))}
				<div class="flex w-full gap-2 sm:w-56">
					<input class="input w-20" type="number" min="1" max={byDays ? 3650 : 1000}
						value={byDays ? cfg.keep_days : cfg.copies}
						oninput={(e) => {
							const n = Number((e.currentTarget as HTMLInputElement).value);
							if (n < 1) return;
							if (byDays) cfg!.keep_days = n;
							else cfg!.copies = n;
							save();
						}} data-testid="backup-retention" />
					<select class="input flex-1" value={byDays ? 'days' : 'copies'} onchange={(e) => {
						const d = (e.currentTarget as HTMLSelectElement).value === 'days';
						cfg!.keep_days = d ? 14 : 0;
						save(0);
					}}>
						<option value="copies">{t('backup.backups')}</option>
						<option value="days">{t('backup.days')}</option>
					</select>
				</div>
			</div>
		</section>

		<!-- Locations -->
		<section class="card space-y-3" data-testid="backup-locations">
			<div>
				<h2 class="h2">{t('backup.locations')}</h2>
				<p class="muted text-sm">{t('backup.locations_hint')}</p>
			</div>
			<ul class="divide-y divide-stone-100">
				{#each cfg.locations as l (l.id)}
					{@const r = view.status.locations[l.id]}
					<li class="py-3" data-testid="location-{l.id}">
						<div class="flex items-center gap-3">
							{@render icon(l.kind)}
							<div class="min-w-0 flex-1">
								<div class="truncate font-medium">{locName(l)}</div>
								{#if r?.error && l.enabled}
									<div class="truncate text-sm text-red-700" title={r.error}>{r.error}</div>
								{:else}
									<div class="muted truncate text-sm">{l.kind === 'local' ? t('backup.local_hint') : l.kind === 'usb' ? t('backup.kind.usb') : l.path}</div>
								{/if}
							</div>
							{#if l.kind !== 'local'}
								<button class="grid size-9 place-items-center rounded-full text-stone-600 hover:bg-stone-100" aria-label={t('backup.location_settings')}
									onclick={() => (editing = editing === l.id ? null : l.id)} data-testid="location-gear">
									<svg viewBox="0 0 24 24" class="size-5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" /></svg>
								</button>
							{/if}
							<button role="switch" aria-checked={l.enabled} aria-label={locName(l)} data-testid="location-switch"
								class="relative h-6 w-11 shrink-0 rounded-full transition {l.enabled ? 'bg-emerald-600' : 'bg-stone-300'}"
								onclick={() => {
									l.enabled = !l.enabled;
									save(0);
								}}>
								<span class="absolute top-0.5 size-5 rounded-full bg-white shadow transition-all {l.enabled ? 'left-[1.375rem]' : 'left-0.5'}"></span>
							</button>
						</div>
						{#if editing === l.id}
							<div class="mt-3 space-y-3 rounded-xl bg-stone-50 p-4">
								<label class="block"><span class="label">{t('backup.location_name')}</span>
									<input class="input" bind:value={l.name} placeholder={locName({ ...l, name: '' })} oninput={() => save()} /></label>
								{#if l.kind === 'folder'}
									<label class="block"><span class="label">{t('backup.folder')}</span>
										<input class="input font-mono" bind:value={l.path} onchange={() => save(0)} /></label>
								{/if}
								<div class="flex justify-between gap-2">
									<button class="btn btn-sm" onclick={() => removeLocation(l.id)} data-testid="location-remove">{t('backup.remove')}</button>
									<button class="btn btn-sm" onclick={() => (editing = null)}>{t('app.close')}</button>
								</div>
							</div>
						{/if}
					</li>
				{/each}
			</ul>
			{#if adding}
				<div class="space-y-3 rounded-xl bg-stone-50 p-4">
					{#if adding === 'usb'}
						{#if view.drives.filter((d) => !usedDrives.has(d)).length}
							<label class="block"><span class="label">{t('backup.drive')}</span>
								<select class="input" bind:value={newPath} data-testid="new-drive">
									<option value="">–</option>
									{#each view.drives.filter((d) => !usedDrives.has(d)) as d (d)}<option value={d}>{d}</option>{/each}
								</select></label>
						{:else}
							<p class="notice">{t('backup.no_drives')}</p>
						{/if}
					{:else}
						<label class="block"><span class="label">{t('backup.folder')}</span>
							<input class="input font-mono" bind:value={newPath} placeholder="/mnt/nas/kicker" data-testid="new-folder" /></label>
						<p class="muted text-xs">{t('backup.folder_hint')}</p>
					{/if}
					<label class="block"><span class="label">{t('backup.location_name')}</span>
						<input class="input" bind:value={newName} placeholder={t('backup.optional')} data-testid="new-name" /></label>
					<div class="flex justify-end gap-2">
						<button class="btn" onclick={() => (adding = '')}>{t('app.cancel')}</button>
						<button class="btn btn-primary" disabled={!newPath.trim()} onclick={addLocation} data-testid="add-location">{t('backup.add')}</button>
					</div>
				</div>
			{:else}
				<div class="flex flex-wrap gap-2 border-t border-stone-100 pt-3">
					<button class="btn btn-sm" onclick={() => (adding = 'usb')} data-testid="add-usb">+ {t('backup.add_usb')}</button>
					<button class="btn btn-sm" onclick={() => (adding = 'folder')} data-testid="add-folder">+ {t('backup.add_folder')}</button>
				</div>
			{/if}
		</section>

		{#if error}<p class="error">{error}</p>{/if}

		<!-- My backups -->
		<section class="card space-y-3" data-testid="backup-list">
			<div class="flex flex-wrap items-center gap-2">
				<h2 class="h2 flex-1">{t('backup.my_backups')}</h2>
				{#each [['all', 'backup.filter.all'], ['auto', 'backup.kind_name.auto'], ['manual', 'backup.kind_name.manual'], ['update', 'backup.filter.update'], ['uploaded', 'backup.kind_name.uploaded']] as [f, label] (f)}
					<button class="chip {filter === f ? 'bg-stone-800 text-white' : 'bg-stone-100 text-stone-700'}" onclick={() => (filter = f as Filter)}>{t(label as Key)}</button>
				{/each}
			</div>
			{#if !shown.length}
				<p class="muted py-6 text-center">{t('backup.no_backups')}</p>
			{:else}
				<div>
					<table class="w-full text-sm">
						<thead class="text-left text-xs text-stone-500">
							<tr>
								<th class="py-2 font-medium">{t('backup.col.created')}</th>
								<th class="py-2 text-right font-medium">{t('backup.col.size')}</th>
								<th class="hidden py-2 pl-4 font-medium sm:table-cell">{t('backup.col.type')}</th>
								<th class="py-2 pl-4 font-medium">{t('backup.col.locations')}</th>
								<th></th>
							</tr>
						</thead>
						<tbody class="divide-y divide-stone-100">
							{#each shown as b (b.file)}
								<tr data-testid="backup-row">
									<td class="py-2.5">
										<div class="font-medium whitespace-nowrap">{dateTime(b.created, i18n.lang)}</div>
										<div class="muted text-xs">{ago(Date.parse(b.created), now, i18n.lang)}{#if !b.own && b.kicker !== 'uploaded'} · {t('backup.from', { name: b.kicker })}{/if}</div>
									</td>
									<td class="py-2.5 text-right whitespace-nowrap tabular-nums">{size(b.size)}</td>
									<td class="hidden py-2.5 pl-4 sm:table-cell"><span class="chip bg-stone-100 text-stone-600">{t(`backup.kind_name.${b.kind}` as Key)}</span></td>
									<td class="py-2.5 pl-4">
										<div class="flex gap-1.5">
											{#each b.locations as id (id)}
												<span title={locName(locById(id))}>{@render icon(locById(id)?.kind ?? 'folder')}</span>
											{/each}
										</div>
									</td>
									<td class="relative py-2.5 text-right">
										<button class="grid size-8 place-items-center rounded-full text-stone-600 hover:bg-stone-100" aria-label={t('backup.actions')}
											onclick={() => (menu = menu === b.file ? null : b.file)} data-testid="backup-menu">⋮</button>
										{#if menu === b.file}
											<div class="absolute right-0 z-10 mt-1 w-48 rounded-xl border border-stone-200 bg-white py-1 text-left shadow-lg">
												<button class="block w-full px-4 py-2 text-left hover:bg-stone-50" onclick={() => askRestore(b)} data-testid="backup-restore">{t('backup.restore_button')}</button>
												{#each b.locations as id (id)}
													<a class="block px-4 py-2 hover:bg-stone-50" href={api.backupFileUrl(b.file, id)} download onclick={() => (menu = null)}>
														{b.locations.length > 1 ? t('backup.download_from', { name: locName(locById(id)) }) : t('backup.download')}</a>
												{/each}
												<button class="block w-full px-4 py-2 text-left text-red-700 hover:bg-stone-50" data-testid="backup-delete"
													onclick={() => {
														menu = null;
														if (confirm(t('backup.confirm_delete'))) act(() => api.deleteBackup(b.file));
													}}>{t('backup.delete')}</button>
											</div>
										{/if}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</section>

		<!-- Upload -->
		<section class="card space-y-3" data-testid="backup-upload">
			<div>
				<h2 class="h2">{t('backup.upload')}</h2>
				<p class="muted text-sm">{t('backup.upload_hint')}</p>
			</div>
			<form class="flex flex-wrap items-end gap-3" onsubmit={upload}>
				<input class="input min-w-60 flex-1" type="file" accept=".age" required data-testid="upload-file"
					onchange={(e) => (uploadFile = (e.currentTarget as HTMLInputElement).files?.[0] ?? null)} />
				<button class="btn" disabled={busy || !uploadFile} data-testid="upload-submit">{t('backup.upload_button')}</button>
			</form>
		</section>
	{:else if error}
		<p class="error">{error}</p>
	{/if}
</div>

<!-- Restoring a backup from the list -->
<dialog bind:this={dialog} class="m-auto w-[min(32rem,calc(100vw-2rem))] rounded-2xl bg-white p-0 text-left shadow-xl backdrop:bg-stone-900/40"
	onclose={() => (restoring = null)} data-testid="restore-dialog">
	{#if restoring}
		<div class="space-y-3 p-5">
			<h2 class="h2">{t('backup.restore_title')}</h2>
			{#if restarting}
				<p>{t('backup.restoring', { name: restoring.kicker, when: dateTime(restoring.created, i18n.lang) })}</p>
				<p class="muted text-sm">{t('backup.restarting')}</p>
			{:else}
				<p class="text-sm">{t('backup.restore_what', { when: dateTime(restoring.created, i18n.lang) })}</p>
				{#if !restoring.own && restoring.kicker !== 'uploaded'}<p class="text-sm">{t('backup.restore_other', { name: restoring.kicker })}</p>{/if}
				<p class="muted text-sm">{t('backup.restore_hint')}</p>
				{#if needPass}
					<p class="notice">{t('backup.other_passphrase')}</p>
					<label class="block"><span class="label">{t('backup.passphrase')}</span>
						<input class="input font-mono" bind:value={passphrase} autocomplete="off" spellcheck="false" data-testid="restore-passphrase" /></label>
				{/if}
			{/if}
			{#if restoreError}<p class="error">{restoreError}</p>{/if}
			{#if !restarting}
				<div class="flex justify-end gap-2 pt-2">
					<button class="btn" onclick={() => dialog?.close()}>{t('app.cancel')}</button>
					<button class="btn btn-primary" disabled={busy || (needPass && !passphrase.trim())} onclick={restore} data-testid="restore-confirm">
						{busy ? t('backup.checking') : t('backup.restore_button')}</button>
				</div>
			{/if}
		</div>
	{/if}
</dialog>
