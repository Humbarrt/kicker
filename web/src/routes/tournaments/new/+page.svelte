<script lang="ts">
	import { t } from '#lib/i18n.svelte.ts';
	import SetupEditor from '#lib/components/tournament/SetupEditor.svelte';
</script>

<main class="mx-auto max-w-4xl space-y-6 px-4 py-6">
	<h1 class="h1">{t('tourn.new')}</h1>
	<SetupEditor />
</main>
