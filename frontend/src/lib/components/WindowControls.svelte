<script lang="ts">
	// Minimise, maximise/restore and close for the frameless desktop window on
	// Windows (and Linux), laid out like Windows 11: 46px wide, the full height
	// of the header, red behind close on hover.
	import { onMount } from 'svelte';

	/** White glyphs, for the home screen's brand-colored band. */
	let { onBrand = false }: { onBrand?: boolean } = $props();

	let maximised = $state(false);
	const rt = () => window.runtime;

	async function sync() {
		maximised = (await rt()?.WindowIsMaximised()) ?? false;
	}

	onMount(() => {
		sync();
		addEventListener('resize', sync);
		return () => removeEventListener('resize', sync);
	});
</script>

<div class="controls no-drag" class:on-brand={onBrand} role="group" aria-label="Window">
	<button onclick={() => rt()?.WindowMinimise()} aria-label="Minimise" title="Minimise">
		<svg viewBox="0 0 10 10" aria-hidden="true"><path d="M0 5h10" /></svg>
	</button>
	<button onclick={() => rt()?.WindowToggleMaximise()} aria-label={maximised ? 'Restore' : 'Maximise'} title={maximised ? 'Restore' : 'Maximise'}>
		{#if maximised}
			<svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2.5 2.5V.5h7v7h-2M.5 2.5h7v7h-7z" /></svg>
		{:else}
			<svg viewBox="0 0 10 10" aria-hidden="true"><path d="M.5.5h9v9h-9z" /></svg>
		{/if}
	</button>
	<button class="close" onclick={() => rt()?.Quit()} aria-label="Close" title="Close">
		<svg viewBox="0 0 10 10" aria-hidden="true"><path d="M.5.5l9 9M9.5.5l-9 9" /></svg>
	</button>
</div>

<style>
	.controls {
		position: fixed;
		top: 0;
		right: 0;
		z-index: 1200; /* above dialogs: the window can always be moved, minimised or closed */
		display: flex;
		height: var(--chrome-h);
	}
	button {
		display: grid;
		place-items: center;
		width: 46px;
		height: 100%;
		padding: 0;
		border: none;
		border-radius: 0;
		background: transparent;
		color: var(--foreground);
		transition: background-color var(--dur-fast) ease-out;
	}
	button:hover {
		background: color-mix(in oklab, var(--foreground) 10%, transparent);
	}
	button:active {
		background: color-mix(in oklab, var(--foreground) 16%, transparent);
	}
	.on-brand button {
		color: #fff;
	}
	.on-brand button:hover {
		background: oklch(1 0 0 / 0.14);
	}
	.close:hover {
		background: #c42b1c;
		color: #fff;
	}
	.close:active {
		background: #b22a1b;
		color: #fff;
	}
	svg {
		width: 10px;
		height: 10px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1;
		/* Crisp 1px strokes regardless of the interface zoom. */
		shape-rendering: crispEdges;
	}
</style>
