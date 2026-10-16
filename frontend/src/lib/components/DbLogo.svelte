<script lang="ts">
	// A database's own logo: a white mark on a box in the given color, or in
	// the database's brand color when none is given. Engines without an
	// official mark show a two-letter monogram.
	import { engine } from '#lib/engines.ts';

	let { driver, color = '', size = 16 }: { driver: string; color?: string; size?: number } = $props();

	const e = $derived(engine(driver));
</script>

<span class="db-logo" style:--c={color || e.color} style:--s="{size}px" title={e.label}>
	{#if e.icon}
		<svg viewBox="0 0 24 24" aria-hidden="true"><path d={e.icon.path} /></svg>
	{:else}
		<span class="mono-mark">{e.mono}</span>
	{/if}
</span>

<style>
	.db-logo {
		display: grid;
		place-items: center;
		flex: none;
		width: var(--s);
		height: var(--s);
		border-radius: calc(var(--s) * 0.24);
		background: var(--c);
		color: #fff;
	}
	svg {
		width: 64%;
		height: 64%;
		fill: currentColor;
	}
	.mono-mark {
		font-size: calc(var(--s) * 0.42);
		font-weight: 600;
		letter-spacing: -0.02em;
		line-height: 1;
	}
</style>
