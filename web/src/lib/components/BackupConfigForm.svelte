<script lang="ts">
	// The backup cycle (schedule, time, retention) and the locations every backup goes to. Saves as
	// soon as something changes. Used by Settings → Backups and the setup wizard (step 8), so both
	// always look the same (spec 10 §6).
	import Settings from '@lucide/svelte/icons/settings';
	import { api, type BackupConfig, type BackupsView } from '#lib/api/client.ts';
	import { i18n, t, tErr } from '#lib/i18n.svelte.ts';
	import { locName } from '#lib/backup.ts';
	import BackupIcon from './BackupIcon.svelte';

	let { view = $bindable() }: { view: BackupsView } = $props();

	let cfg = $state<BackupConfig>(structuredClone($state.snapshot(view.config)));
	$effect(() => {
		cfg = structuredClone($state.snapshot(view.config)); // a newer view from the server
	});
	let error = $state('');

	let saveTimer: ReturnType<typeof setTimeout> | undefined;
	function save(delay = 400) {
		clearTimeout(saveTimer);
		saveTimer = setTimeout(async () => {
			error = '';
			try {
				view = await api.setBackupConfig($state.snapshot(cfg));
			} catch (e) {
				error = tErr(e);
			}
		}, delay);
	}

	// --- cycle
	const days = [1, 2, 3, 4, 5, 6, 0]; // Monday first
	const dayName = (d: number) => new Date(2026, 10, 22 + d).toLocaleDateString(i18n.lang, { weekday: 'short' }); // 22 Nov 2026 is a Sunday
	function toggleDay(d: number) {
		const ds = new Set(cfg.days ?? []);
		if (ds.has(d)) ds.delete(d);
		else ds.add(d);
		cfg.days = [...ds];
		if (cfg.days.length) save(0);
	}
	let customTime = $derived(!!cfg.time);
	let byDays = $derived(!!cfg.keep_days);

	// --- locations
	let editing = $state<string | null>(null);
	let adding = $state<'' | 'usb' | 'folder'>('');
	let newPath = $state('');
	let newName = $state('');
	const usedDrives = $derived(new Set(cfg.locations.filter((x) => x.kind === 'usb').map((x) => x.path)));
	function addLocation() {
		if (!newPath.trim()) return;
		cfg.locations.push({ id: `${adding}-${Date.now().toString(36)}`, name: newName.trim(), kind: adding as 'usb' | 'folder', path: newPath.trim(), enabled: true });
		adding = '';
		newPath = newName = '';
		save(0);
	}
	function removeLocation(id: string) {
		cfg.locations = cfg.locations.filter((l) => l.id !== id);
		editing = null;
		save(0);
	}
</script>

{#snippet row(title: string, hint: string)}
	<div class="min-w-0 flex-1">
		<div class="font-medium">{title}</div>
		<div class="muted text-sm">{hint}</div>
	</div>
{/snippet}

	<!-- Backup cycle -->
	<section class="card space-y-5" data-testid="backup-cycle">
		<div>
			<h2 class="h2">{t('backup.cycle')}</h2>
			<p class="muted text-sm">{t('backup.cycle_hint')}</p>
		</div>
		<div class="flex flex-wrap items-center gap-4">
			{@render row(t('backup.schedule'), t('backup.schedule_hint'))}
			<select class="input w-full sm:w-56" bind:value={cfg.schedule} onchange={() => {
				if (cfg.schedule === 'days' && !cfg.days?.length) cfg.days = [1, 2, 3, 4, 5];
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
						cfg.time = (e.currentTarget as HTMLSelectElement).value === 'custom' ? view.auto_time : '';
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
						if (byDays) cfg.keep_days = n;
						else cfg.copies = n;
						save();
					}} data-testid="backup-retention" />
				<select class="input flex-1" value={byDays ? 'days' : 'copies'} onchange={(e) => {
					const d = (e.currentTarget as HTMLSelectElement).value === 'days';
					cfg.keep_days = d ? 14 : 0;
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
						<BackupIcon kind={l.kind} />
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
								<Settings class="size-5" aria-hidden="true" />
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
