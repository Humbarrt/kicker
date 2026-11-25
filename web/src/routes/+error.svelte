<script lang="ts">
	import { page } from '$app/state';
	import { t } from '#lib/i18n.svelte.ts';
</script>

<main class="mx-auto grid min-h-[70vh] max-w-md place-items-center p-6 text-center">
	<div>
		<p class="font-score text-8xl font-bold text-stone-300">{page.status}</p>
		<h1 class="h1 mt-2">{page.status === 404 ? t('notfound.title') : t('error.title')}</h1>
		<p class="muted mt-2">{page.status === 404 ? t('notfound.text') : page.error?.message}</p>
		<a href="/" class="btn btn-primary mt-6">{t('notfound.back')}</a>
	</div>
</main>
