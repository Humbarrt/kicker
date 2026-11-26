<script lang="ts">
	// Icon for a backup location: the kicker itself, a USB drive or a network share.
	import HardDrive from '@lucide/svelte/icons/hard-drive';
	import Usb from '@lucide/svelte/icons/usb';
	import ServerNas from '@lucide/svelte/icons/server-nas';

	let { kind }: { kind: string } = $props();
	const Icon = $derived(kind === 'local' ? HardDrive : kind === 'usb' ? Usb : ServerNas);
</script>

<Icon class="size-5 shrink-0 text-stone-700" aria-hidden="true" />
