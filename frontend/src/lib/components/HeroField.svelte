<script lang="ts">
	// The home screen's hero: binary digits on the brand color with a glow rising
	// from the bottom. The glow breathes, drifts and leans towards the pointer;
	// digits flip faster the brighter they are.
	import { onMount } from 'svelte';
	import { binaryField } from '#lib/binaryfield.ts';
	import { prefs } from '#lib/prefs.svelte.ts';

	/** Background color; the brand color when empty. */
	let { color = '' }: { color?: string } = $props();

	let canvas: HTMLCanvasElement;

	onMount(() => {
		const css = getComputedStyle(canvas);
		const brand = color || css.getPropertyValue('--brand').trim() || '#0a3d62';
		const mono = css.getPropertyValue('--font-mono') || 'monospace';
		// Pointer position (0..1 across the width), eased.
		let px = 0.5;
		let target = 0.5;
		// Glow geometry for the current frame, shared by underlay and digits.
		let cx = 0;
		let rx = 1;
		let ry = 1;

		const field = binaryField(
			canvas,
			{
				cell: { w: 10, h: 14 },
				font: `500 9px ${mono}`,
				fps: 30,
				underlay(ctx, w, h, t) {
					ctx.fillStyle = brand;
					ctx.fillRect(0, 0, w, h);
					cx = w * (0.5 + (px - 0.5) * 0.25 + Math.sin(t * 0.15) * 0.04);
					const breathe = 1 + Math.sin(t * 0.6) * 0.06;
					rx = Math.max(w * 0.5, 320) * breathe;
					ry = h * 1.05 * breathe;
					ctx.save();
					ctx.translate(cx, h);
					ctx.scale(rx / ry, 1);
					const g = ctx.createRadialGradient(0, 0, 0, 0, 0, ry);
					g.addColorStop(0, 'rgba(214, 238, 255, 0.85)');
					g.addColorStop(0.35, 'rgba(120, 190, 240, 0.45)');
					g.addColorStop(1, 'rgba(60, 130, 200, 0)');
					ctx.fillStyle = g;
					ctx.fillRect(-ry, -ry, ry * 2, ry);
					ctx.restore();
				},
				frame(_w, h, t, dt) {
					px += (target - px) * Math.min(1, dt * 2);
					const base = h * 1.02;
					return (x, y, out) => {
						const dx = (x - cx) / rx;
						const dy = (y - base) / ry;
						const glow = Math.max(0, 1 - Math.sqrt(dx * dx + dy * dy));
						// A slow shimmer crossing the field.
						const wave = 0.5 + 0.5 * Math.sin(x * 0.012 + y * 0.03 - t * 0.8);
						const lit = Math.min(1, glow * 1.6 + wave * 0.12);
						out.alpha = 0.07 + lit * 0.6;
						out.speed = 0.3 + lit * 2;
					};
				}
			},
			'#fff',
			prefs.still
		);

		const move = (e: PointerEvent) => (target = e.clientX / innerWidth);
		if (!prefs.still) addEventListener('pointermove', move, { passive: true });
		return () => {
			field.destroy();
			removeEventListener('pointermove', move);
		};
	});
</script>

<canvas class="field" bind:this={canvas} aria-hidden="true"></canvas>

<style>
	.field {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		pointer-events: none;
	}
</style>
