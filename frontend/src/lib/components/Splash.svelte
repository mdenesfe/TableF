<script lang="ts">
	// Full-page loading screen: just the binary field over the whole window.
	// Used while the app starts and while connecting.
	import { fade } from 'svelte/transition';
	import { dissolve } from '#lib/motion.ts';
	import { prefs } from '#lib/prefs.svelte.ts';
	import HeroField from './HeroField.svelte';
	import { onMount } from 'svelte';

	let {
		message,
		color = '',
		instant = false
	}: { message: string; color?: string; /** Appear without fading in (at start-up). */ instant?: boolean } = $props();

	const duration = $derived(prefs.still ? 0 : 320);

	/** A deep version of a #rrggbb color, so the white digits and glow stand out on it. */
	function deepen(hex: string): string {
		const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
		if (!m) return '';
		const n = parseInt(m[1], 16);
		const ch = (shift: number) => Math.round(((n >> shift) & 255) * 0.42);
		return `rgb(${ch(16)}, ${ch(8)}, ${ch(0)})`;
	}
	const background = $derived(color ? deepen(color) : '');

	// The page's static brand-colored cover (app.html) can go once this is up.
	onMount(() => document.getElementById('boot')?.remove());
</script>

<div
	class="splash drag"
	role="status"
	aria-live="polite"
	style:background={background || 'var(--brand)'}
	in:fade={{ duration: instant ? 0 : duration * 0.6 }}
	out:dissolve
>
	<HeroField color={background} />
	<!-- Only the animation is shown; the message is for screen readers. -->
	<span class="sr">{message}</span>
</div>

<style>
	.splash {
		position: fixed;
		inset: 0;
		/* Above the app, below dialogs (an SSH host-key prompt can appear while connecting). */
		z-index: 1050;
		overflow: hidden;
		isolation: isolate;
	}
	.splash :global(.field) {
		z-index: -1;
	}
	.sr {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}
</style>
