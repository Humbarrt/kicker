<script lang="ts">
	import { api, type Info } from '#lib/api/client.ts';

	let info = $state<Info | null>(null);
	let error = $state<string | null>(null);

	$effect(() => {
		api
			.info()
			.then((i) => (info = i))
			.catch((e: unknown) => (error = String(e)));
	});
</script>

<main class="mx-auto flex min-h-screen max-w-xl flex-col items-center justify-center gap-4 p-8">
	<h1 class="text-4xl font-bold tracking-tight">Kicker</h1>
	{#if info}
		<p class="text-neutral-600">
			Server {info.version}
			{#if info.sim}<span class="ml-2 rounded bg-amber-100 px-2 py-0.5 text-amber-800">simulator</span>{/if}
		</p>
	{:else if error}
		<p class="text-red-600">Server not reachable: {error}</p>
	{:else}
		<p class="text-neutral-400">Connecting…</p>
	{/if}
</main>
