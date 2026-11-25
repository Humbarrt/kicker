<script lang="ts">
	// Add a player to a side by hand (spec 02 §4, `table.players`): for players without a coin.
	import { api, type Side } from '#lib/api/client.ts';
	import { live } from '#lib/live.svelte.ts';
	import { players } from '#lib/players.svelte.ts';
	import { t, tErr, tPlayer, tSide } from '#lib/i18n.svelte.ts';

	let { open = $bindable(false), side }: { open: boolean; side: Side } = $props();

	let dialog: HTMLDialogElement | undefined = $state();
	let choice = $state('');
	let error = $state('');
	let busy = $state(false);

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			choice = error = '';
			players.reload();
			dialog.showModal();
		} else if (!open && dialog.open) dialog.close();
	});

	async function add(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			live.setTable(await api.register(side, Number(choice)));
			open = false;
		} catch (err) {
			error = tErr(err);
		} finally {
			busy = false;
		}
	}
</script>

<dialog bind:this={dialog} onclose={() => (open = false)}
	class="m-auto w-[min(26rem,calc(100vw-2rem))] rounded-2xl bg-white p-0 shadow-xl backdrop:bg-stone-900/40">
	<form class="space-y-4 p-6" onsubmit={add}>
		<h2 class="h2">{t('reg.add_title', { side: tSide(side) })}</h2>
		<p class="muted text-sm">{t('reg.add_hint')}</p>
		<label class="block"><span class="label">{t('coins.player')}</span>
			<select class="input" bind:value={choice} required>
				<option value="" disabled>–</option>
				{#each players.active as p (p.id)}<option value={String(p.id)}>{tPlayer(p)}</option>{/each}
			</select></label>
		{#if error}<p class="error">{error}</p>{/if}
		<div class="flex justify-end gap-2">
			<button type="button" class="btn" onclick={() => (open = false)}>{t('app.cancel')}</button>
			<button class="btn btn-primary" disabled={busy}>{t('reg.add')}</button>
		</div>
	</form>
</dialog>
