<script lang="ts">
	// Backups (admins, spec 10 §2), laid out like Home Assistant's: the backup cycle (schedule,
	// time, retention), the locations every backup goes to, the list of backups with download /
	// restore / delete, and uploading a backup. Restoring tries the current passphrase first and
	// only asks for one when the backup was made with another.
	import EllipsisVertical from '@lucide/svelte/icons/ellipsis-vertical';
	import { api, type BackupEntry, type BackupsView } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { i18n, t, tErr, type Key } from '#lib/i18n.svelte.ts';
	import { ago, dateTime } from '#lib/format.ts';
	import { locName } from '#lib/backup.ts';
	import BackupConfigForm from '#lib/components/BackupConfigForm.svelte';
	import BackupIcon from '#lib/components/BackupIcon.svelte';

	let view = $state<BackupsView | null>(null);
	let error = $state('');
	let busy = $state(false);
	let now = $state(Date.now());

	function apply(v: BackupsView) {
		view = v;
		now = Date.now();
	}
	$effect(() => {
		void live.backupRev; // a run finished, the list changed
		api.backups().then(apply).catch((e) => (error = tErr(e)));
	});

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

	const locById = (id: string) => view?.config.locations.find((l) => l.id === id);

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


<div class="space-y-6">
	{#if view?.pending}
		<p class="notice" data-testid="restore-pending">{t('backup.pending', { name: view.pending.name, when: dateTime(view.pending.created, i18n.lang) })}</p>
	{/if}

	{#if view}
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

		<BackupConfigForm bind:view={view!} />

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
												<span title={locName(locById(id))}><BackupIcon kind={locById(id)?.kind ?? 'folder'} /></span>
											{/each}
										</div>
									</td>
									<td class="relative py-2.5 text-right">
										<button class="grid size-8 place-items-center rounded-full text-stone-600 hover:bg-stone-100" aria-label={t('backup.actions')}
											onclick={() => (menu = menu === b.file ? null : b.file)} data-testid="backup-menu"><EllipsisVertical class="size-5" aria-hidden="true" /></button>
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
