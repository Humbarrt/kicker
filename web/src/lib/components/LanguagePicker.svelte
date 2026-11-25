<script lang="ts">
	import { i18n, languages } from '#lib/i18n.svelte.ts';
	import { setLocalLanguage } from '#lib/session.svelte.ts';
</script>

<select class="input w-auto" value={i18n.lang} onchange={(e) => setLocalLanguage(e.currentTarget.value)}>
	{#each languages as l (l.code)}<option value={l.code}>{l.name}</option>{/each}
</select>
