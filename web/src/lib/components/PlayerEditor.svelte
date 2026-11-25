<script lang="ts">
	// Create or edit a player: name, color, picture; archive and delete for those allowed.
	import { api, type Player } from '#lib/api/client.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr, tPlayer } from '#lib/i18n.svelte.ts';
	import { squareJpeg } from '#lib/picture.ts';
	import PlayerAvatar from '#lib/components/PlayerAvatar.svelte';

	let {
		open = $bindable(false),
		player = null,
		mine = false,
		onsaved
	}: { open: boolean; player?: Player | null; mine?: boolean; onsaved: (p: Player | null) => void } = $props();

	let dialog: HTMLDialogElement | undefined = $state();
	let name = $state('');
	let color = $state('');
	let palette = $state<string[]>([]);
	let current = $state<Player | null>(null);
	let error = $state('');
	let busy = $state(false);
	let fileInput: HTMLInputElement | undefined = $state();

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			error = '';
			current = player;
			name = player ? tPlayer(player) : '';
			color = player?.color ?? '';
			api.palette().then((p) => {
				palette = p.colors;
				if (!color) color = p.suggested;
			}).catch(() => {});
			dialog.showModal();
		} else if (!open && dialog.open) dialog.close();
	});

	const canArchive = $derived(!!current && !current.guest && session.can('profile.any'));

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			let p: Player;
			if (!current) p = mine ? await api.createMyPlayer(name, color) : await api.createPlayer(name, color);
			else {
				const change: { name?: string; color?: string } = {};
				if (name.trim() !== tPlayer(current)) change.name = name;
				if (color !== current.color) change.color = color;
				p = Object.keys(change).length ? await api.updatePlayer(current.id, change) : current;
			}
			onsaved(p);
			open = false;
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}

	async function upload(e: Event) {
		const file = (e.currentTarget as HTMLInputElement).files?.[0];
		if (!file || !current) return;
		busy = true;
		error = '';
		try {
			const jpeg = await squareJpeg(file).catch(() => {
				throw new Error(t('players.picture_error'));
			});
			current = await api.uploadAvatar(current.id, jpeg);
			onsaved(current);
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
			if (fileInput) fileInput.value = '';
		}
	}

	async function act(fn: () => Promise<Player | void>, close = false) {
		busy = true;
		error = '';
		try {
			const p = await fn();
			if (p) current = p;
			onsaved(p ?? null);
			if (close) open = false;
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<dialog bind:this={dialog} onclose={() => (open = false)}
	class="m-auto w-[min(32rem,calc(100vw-2rem))] rounded-2xl bg-white p-0 shadow-xl backdrop:bg-stone-900/40">
	<form class="flex max-h-[85vh] flex-col" onsubmit={save}>
		<div class="flex items-center gap-4 border-b border-stone-100 px-6 py-4">
			<PlayerAvatar player={{ name: name || '?', color: color || '#a8a29e', avatar: current?.avatar, guest: current?.guest ?? false }} size={48} />
			<h2 class="h2">{current ? t('players.edit') : mine ? t('players.create_mine') : t('players.new')}</h2>
		</div>
		<div class="space-y-5 overflow-y-auto px-6 py-5">
			<label class="block"><span class="label">{t('players.name')}</span>
				<input class="input" bind:value={name} maxlength="40" required /></label>

			<fieldset>
				<legend class="label">{t('players.color')}</legend>
				<div class="flex flex-wrap items-center gap-2">
					{#each palette as c (c)}
						<button type="button" aria-label={c} aria-pressed={color === c} onclick={() => (color = c)}
							class="size-8 rounded-full ring-offset-2 transition {color === c ? 'ring-2 ring-stone-900' : 'hover:scale-110'}"
							style:background-color={c}></button>
					{/each}
					<label class="ml-1 flex items-center gap-2 text-sm text-stone-600">
						<input type="color" class="size-8 cursor-pointer rounded border border-stone-300" bind:value={color} />
						{t('players.custom_color')}
					</label>
				</div>
			</fieldset>

			{#if current}
				<div>
					<span class="label">{t('players.picture')}</span>
					<div class="flex flex-wrap items-center gap-3">
						<input bind:this={fileInput} type="file" accept="image/*" class="hidden" onchange={upload} />
						<button type="button" class="btn" disabled={busy} onclick={() => fileInput?.click()}>{t('players.upload')}</button>
						{#if current.avatar}
							<button type="button" class="btn btn-danger" disabled={busy} onclick={() => act(() => api.removeAvatar(current!.id))}>{t('players.remove_picture')}</button>
						{/if}
					</div>
					<p class="muted mt-1">{t('players.picture_hint')}</p>
				</div>
			{/if}

			{#if canArchive && current}
				<div class="rounded-xl bg-stone-50 p-3">
					<button type="button" class="btn" disabled={busy}
						onclick={() => act(() => api.updatePlayer(current!.id, { archived: !current!.archived }))}>
						{current.archived ? t('players.unarchive') : t('players.archive')}
					</button>
					<p class="muted mt-1">{t('players.archive_hint')}</p>
				</div>
			{/if}
			{#if error}<p class="error">{error}</p>{/if}
		</div>
		<div class="flex items-center gap-2 border-t border-stone-100 px-6 py-4">
			{#if current && session.admin && !current.guest && !current.has_account}
				<button type="button" class="btn btn-danger" disabled={busy}
					onclick={() => confirm(t('players.delete_confirm')) && act(() => api.deletePlayer(current!.id), true)}>{t('players.delete')}</button>
			{/if}
			<span class="flex-1"></span>
			<button type="button" class="btn" onclick={() => (open = false)}>{t('app.cancel')}</button>
			<button class="btn btn-primary" disabled={busy}>{current ? t('app.save') : t('app.create')}</button>
		</div>
	</form>
</dialog>
