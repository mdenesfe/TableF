<script lang="ts">
	// Traffic lights for fullscreen on macOS. There macOS would show its own in an
	// opaque bar over the header, so that bar is hidden (chrome_darwin.go) and the
	// header draws these in the same spot: close, minimise (disabled in
	// fullscreen, as in macOS) and zoom, which leaves fullscreen.
	const rt = () => window.runtime;
</script>

<div class="lights no-drag" role="group" aria-label="Window">
	<button class="close" onclick={() => rt()?.Quit()} aria-label="Close" title="Close">
		<svg viewBox="0 0 8 8" aria-hidden="true"><path d="M1.5 1.5l5 5M6.5 1.5l-5 5" /></svg>
	</button>
	<button class="min" disabled aria-label="Minimise (unavailable in full screen)"></button>
	<button class="zoom" onclick={() => rt()?.WindowUnfullscreen()} aria-label="Exit full screen" title="Exit Full Screen">
		<svg viewBox="0 0 8 8" aria-hidden="true"><path d="M1.6 4.6V1.6h3zM6.4 3.4v3h-3z" /></svg>
	</button>
</div>

<style>
	/* Same geometry as the native buttons: 14pt frames 23pt apart, the first
	   centred 26pt from the left edge, vertically centred in the 52pt header. */
	.lights {
		position: absolute;
		top: 50%;
		left: calc(19px / var(--zoom, 1));
		display: flex;
		gap: calc(9px / var(--zoom, 1));
		transform: translateY(-50%);
		z-index: 1;
	}
	button {
		position: relative;
		display: grid;
		place-items: center;
		width: calc(14px / var(--zoom, 1));
		height: calc(14px / var(--zoom, 1));
		padding: 0;
		border: none;
		background: transparent;
	}
	button::before {
		content: '';
		position: absolute;
		inset: calc(1px / var(--zoom, 1));
		border-radius: 50%;
		box-shadow: inset 0 0 0 0.5px oklch(0 0 0 / 0.2);
	}
	.close::before {
		background: #ff5f57;
	}
	.min::before {
		background: color-mix(in oklab, var(--foreground) 22%, transparent);
	}
	.zoom::before {
		background: #28c840;
	}
	svg {
		position: relative;
		width: calc(8px / var(--zoom, 1));
		height: calc(8px / var(--zoom, 1));
		fill: oklch(0 0 0 / 0.55);
		stroke: oklch(0 0 0 / 0.55);
		stroke-width: 1.2;
		stroke-linecap: round;
		opacity: 0;
	}
	.close svg {
		fill: none;
	}
	.zoom svg {
		stroke: none;
	}
	.lights:hover svg {
		opacity: 1;
	}
</style>
