<script lang="ts">
	// A field of binary digits in the connection color behind the header. Bright
	// patches drift slowly across it; while loading they move faster and glow.
	import { onMount } from 'svelte';
	import { binaryField, smooth, type Field } from '#lib/binaryfield.ts';
	import { prefs } from '#lib/prefs.svelte.ts';

	// Fades out under the window controls and header buttons so they stay legible.
	const HEADER_MASK = 'linear-gradient(to right, transparent, #000 7rem, #000 calc(100% - 13rem), transparent calc(100% - 4rem))';

	let {
		color = '',
		loading = false,
		mask = HEADER_MASK
	}: {
		color?: string;
		loading?: boolean;
		/** CSS mask-image over the field (where the digits may show). */
		mask?: string;
	} = $props();

	let canvas: HTMLCanvasElement;
	let field: Field | undefined;
	// Eases towards 1 while loading, back to 0 after.
	let energy = 0;

	const ink = () => color || getComputedStyle(canvas).getPropertyValue('--brand-ink').trim() || '#0a3d62';

	onMount(() => {
		const mono = getComputedStyle(canvas).getPropertyValue('--font-mono') || 'monospace';
		field = binaryField(
			canvas,
			{
				cell: { w: 7, h: 9 },
				font: `600 8px ${mono}`,
				fps: 24,
				frame(w, _h, t, dt) {
					energy += ((loading ? 1 : 0) - energy) * Math.min(1, dt * 4);
					const speed = 1 + energy * 2.5;
					const lo = 0.35 - energy * 0.4;
					const gain = 0.55 + energy * 0.35;
					const fadeEnd = w * 0.55;
					return (x, y, out) => {
						// Slow waves crossing at angles make continent-like patches.
						const v =
							Math.sin(x * 0.011 - t * 0.5 * speed) * Math.cos(y * 0.09 + x * 0.004 + t * 0.3) +
							0.6 * Math.sin((x + y * 4) * 0.006 + t * 0.7 * speed) +
							0.35 * Math.sin(x * 0.031 + y * 0.21 - t * 1.1);
						const lit = smooth(lo, 0.95, v);
						// Quieter behind the pickers on the left, fuller towards the right.
						out.alpha = (0.08 + lit * gain) * (0.35 + 0.65 * smooth(0, fadeEnd, x));
						out.speed = (0.4 + lit) * speed;
					};
				}
			},
			ink(),
			prefs.still
		);
		return () => field?.destroy();
	});

	// Follow the connection color and the theme (the fallback ink differs per theme).
	$effect(() => {
		void color;
		void prefs.value.theme;
		field?.setColor(ink());
	});
</script>

<canvas class="dots" bind:this={canvas} style:mask-image={mask} aria-hidden="true"></canvas>

<style>
	.dots {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		z-index: -1;
		pointer-events: none;
	}
</style>
