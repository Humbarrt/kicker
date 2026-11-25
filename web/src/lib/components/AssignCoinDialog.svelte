<script lang="ts">
	// Assign an (unknown) coin to a player – or to a new player created on the spot.
	import { api } from '#lib/api/client.ts';
	import { players } from '#lib/players.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { t, tErr, tPlayer } from '#lib/i18n.svelte.ts';

	let { open = $bindable(false), uid }: { open: boolean; uid: string } = $props();

	let dialog: HTMLDialogElement | undefined = $state();
	let choice = $state<string>('');
	let newName = $state('');
	let label = $state('');
	let error = $state('');
	let busy = $state(false);

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			choice = '';
			newName = label = error = '';
			players.reload();
			dialog.showModal();
		} else if (!open && dialog.open) dialog.close();
	});

	async function assign(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			let pid = Number(choice);
			if (choice === 'new') {
				pid = (await api.createPlayer(newName)).id;
				await players.reload();
			}
			await api.assignCoin(uid, pid, label);
			open = false;
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<dialog bind:this={dialog} onclose={() => (open = false)}
	class="m-auto w-[min(28rem,calc(100vw-2rem))] rounded-2xl bg-white p-0 shadow-xl backdrop:bg-stone-900/40">
	<form class="space-y-4 p-6" onsubmit={assign}>
		<h2 class="h2">{t('coins.assign_title', { uid })}</h2>
		<label class="block"><span class="label">{t('coins.player')}</span>
			<select class="input" bind:value={choice} required>
				<option value="" disabled>–</option>
				{#each players.active as p (p.id)}<option value={String(p.id)}>{tPlayer(p)}</option>{/each}
				{#if session.can('players.create')}<option value="new">{t('coins.new_player')}</option>{/if}
			</select></label>
		{#if choice === 'new'}
			<label class="block"><span class="label">{t('coins.new_player_name')}</span>
				<input class="input" bind:value={newName} maxlength="40" required /></label>
		{/if}
		<label class="block"><span class="label">{t('coins.label')}</span>
			<input class="input" bind:value={label} maxlength="40" placeholder={t('coins.label_hint')} /></label>
		{#if error}<p class="error">{error}</p>{/if}
		<div class="flex justify-end gap-2">
			<button type="button" class="btn" onclick={() => (open = false)}>{t('app.cancel')}</button>
			<button class="btn btn-primary" disabled={busy}>{t('coins.assign')}</button>
		</div>
	</form>
</dialog>
