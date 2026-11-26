<script lang="ts">
	// Table settings (spec 02 §13, D17). Admins only.
	import { t } from '#lib/i18n.svelte.ts';
	import TableSettingsForm from '#lib/components/TableSettingsForm.svelte';
</script>

<h1 class="h1 mb-4">{t('set.title')}</h1>
<TableSettingsForm />
