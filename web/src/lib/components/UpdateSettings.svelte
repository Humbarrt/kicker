<script lang="ts">
	// The update switches: check daily, install automatically (spec 10 §3). Used by Settings →
	// Updates and the setup wizard (step 8); saved as soon as they change.
	import { api, type UpdatesView } from '#lib/api/client.ts';
	import { t, tErr, type Key } from '#lib/i18n.svelte.ts';

	let { view = $bindable() }: { view: UpdatesView } = $props();
	let error = $state('');

	async function toggle(key: 'check' | 'auto') {
		error = '';
		const c = { ...view.config, [key]: !view.config[key] };
		try {
			view = await api.setUpdateConfig(c);
		} catch (e) {
			error = tErr(e);
		}
	}
</script>

<section class="card space-y-3" data-testid="update-settings">
	<h2 class="h2">{t('upd.settings')}</h2>
	<ul class="divide-y divide-stone-100">
		{#each [['check', 'upd.check_daily', 'upd.check_daily_hint'], ['auto', 'upd.auto', 'upd.auto_hint']] as [key, title, hint] (key)}
			{@const on = view.config[key as 'check' | 'auto']}
			<li class="flex items-center gap-4 py-3">
				<div class="min-w-0 flex-1">
					<div class="font-medium">{t(title as Key)}</div>
					<div class="muted text-sm">{t(hint as Key)}</div>
				</div>
				<button role="switch" aria-checked={on} aria-label={t(title as Key)} data-testid="update-switch-{key}"
					class="relative h-6 w-11 shrink-0 rounded-full transition {on ? 'bg-emerald-600' : 'bg-stone-300'}"
					onclick={() => toggle(key as 'check' | 'auto')}>
					<span class="absolute top-0.5 size-5 rounded-full bg-white shadow transition-all {on ? 'left-[1.375rem]' : 'left-0.5'}"></span>
				</button>
			</li>
		{/each}
	</ul>
	{#if error}<p class="error">{error}</p>{/if}
</section>
