<script lang="ts">
	// A player's picture, or their color with initials.
	import { avatarUrl, type Player } from '#lib/api/client.ts';
	import { tPlayer } from '#lib/i18n.svelte.ts';

	let { player, size = 40 }: { player: Pick<Player, 'name' | 'color' | 'avatar' | 'guest'>; size?: number } = $props();

	const name = $derived(tPlayer(player));
	const initials = $derived(
		name
			.split(/\s+/)
			.filter(Boolean)
			.slice(0, 2)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
	);
</script>

{#if player.avatar}
	<img src={avatarUrl(player.avatar, size > 128 ? 512 : 128)} alt={name}
		class="shrink-0 rounded-full object-cover" style:width="{size}px" style:height="{size}px" />
{:else}
	<span class="grid shrink-0 place-items-center rounded-full font-semibold text-white select-none"
		style:width="{size}px" style:height="{size}px" style:background-color={player.color} style:font-size="{Math.round(size * 0.38)}px"
		aria-label={name}>{initials}</span>
{/if}
