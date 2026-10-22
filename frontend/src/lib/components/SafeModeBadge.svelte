<script lang="ts">
	// The open connection's safe mode in the header: a quiet shield when
	// silent, a named badge otherwise; click to switch modes.
	import { Lock, ShieldAlert, ShieldOff } from '@lucide/svelte';
	import { app } from '#lib/state.svelte.ts';
	import { SAFE_MODES, type SafeMode } from '#lib/safemode.ts';

	let open = $state(false);
	let root: HTMLDivElement;
	const mode = $derived(app.safeMode);
	const label = $derived(SAFE_MODES.find((m) => m.value === mode)?.label ?? '');

	function pick(m: SafeMode) {
		open = false;
		app.setSafeMode(m);
	}
</script>

<svelte:window
	onpointerdown={(e) => open && !root.contains(e.target as Node) && (open = false)}
	onkeydown={(e) => open && e.key === 'Escape' && (open = false)}
/>

{#snippet icon(m: SafeMode, size: number)}
	{#if m === 'silent'}<ShieldOff {size} />{:else if m === 'read-only'}<Lock {size} />{:else}<ShieldAlert {size} />{/if}
{/snippet}

<div class="safe" bind:this={root}>
	<button
		class="badge"
		class:silent={mode === 'silent'}
		class:alert={mode === 'alert-changes' || mode === 'alert-all'}
		class:readonly={mode === 'read-only'}
		onclick={() => (open = !open)}
		aria-haspopup="menu"
		aria-expanded={open}
		title={`Safe mode: ${label}`}
	>
		{@render icon(mode, 13)}
		{#if mode !== 'silent'}<span>{label}</span>{/if}
	</button>
	{#if open}
		<div class="menu" role="menu">
			<p class="head">Safe mode for {app.connection?.name}</p>
			{#each SAFE_MODES as m (m.value)}
				<button role="menuitemradio" aria-checked={m.value === mode} class:on={m.value === mode} onclick={() => pick(m.value)}>
					<span class="i">{@render icon(m.value, 14)}</span>
					<span class="text"><b>{m.label}</b><small>{m.hint}</small></span>
				</button>
			{/each}
		</div>
	{/if}
</div>

<style>
	.safe {
		position: relative;
	}
	.badge {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		height: 1.6rem;
		padding: 0 0.5rem;
		border: 1px solid transparent;
		border-radius: 999px;
		background: transparent;
		color: color-mix(in oklab, var(--foreground) 82%, transparent);
		font-size: 11.5px;
		font-weight: 500;
		white-space: nowrap;
	}
	.badge.silent {
		width: 1.85rem;
		padding: 0;
		justify-content: center;
		border-radius: var(--radius);
	}
	.badge.alert {
		border-color: color-mix(in oklch, var(--foreground) 30%, transparent);
		background: color-mix(in oklch, var(--background) 35%, transparent);
	}
	.badge.readonly {
		border-color: var(--warning);
		background: color-mix(in oklch, var(--warning) 18%, transparent);
		color: var(--foreground);
	}
	.badge:hover {
		background: color-mix(in oklch, var(--background) 45%, transparent);
	}
	.menu {
		position: absolute;
		top: calc(100% + 0.35rem);
		right: 0;
		z-index: 700;
		width: 19rem;
		padding: 0.3rem;
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
		animation: tf-pop var(--dur-fast) var(--ease-out) both;
	}
	.head {
		margin: 0;
		padding: 0.35rem 0.5rem 0.4rem;
		font-size: 11px;
		color: var(--muted-foreground);
	}
	.menu button {
		display: flex;
		align-items: flex-start;
		gap: 0.6rem;
		padding: 0.45rem 0.5rem;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		text-align: left;
	}
	.menu button:hover {
		background: var(--accent);
	}
	.menu button.on {
		background: var(--brand-soft);
	}
	.i {
		display: grid;
		place-items: center;
		width: 1rem;
		padding-top: 1px;
		color: var(--muted-foreground);
	}
	.text {
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
	}
	.text b {
		font-size: 12.5px;
		font-weight: 500;
	}
	.text small {
		font-size: 11px;
		color: var(--muted-foreground);
	}
</style>
