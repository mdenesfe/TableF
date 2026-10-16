<script lang="ts" generics="T">
	// A select in TableF's own style: a full-width field that opens a list under
	// it. Keyboard: Enter, Space or ↓ to open, ↑/↓ to move, Enter to pick, Esc to close.
	import { ChevronsUpDown } from '@lucide/svelte';

	let {
		value,
		options,
		onchange,
		mono = false,
		label = ''
	}: {
		value: T;
		options: { value: T; label: string }[];
		onchange: (v: T) => void;
		mono?: boolean;
		/** Accessible name. */
		label?: string;
	} = $props();

	let open = $state(false);
	let cursor = $state(0);
	let trigger: HTMLButtonElement;
	let list = $state<HTMLDivElement>();
	// The list is fixed-positioned so scrolling containers don't clip it.
	let pos = $state({ x: 0, y: 0, w: 0, up: false });

	const current = $derived(options.find((o) => o.value === value));

	function show() {
		const r = trigger.getBoundingClientRect();
		const height = Math.min(options.length * 30 + 8, 260);
		const up = r.bottom + height > window.innerHeight - 8;
		pos = { x: r.left, y: up ? r.top - height - 4 : r.bottom + 4, w: r.width, up };
		cursor = Math.max(
			0,
			options.findIndex((o) => o.value === value)
		);
		open = true;
	}

	function pick(i: number) {
		open = false;
		trigger.focus();
		const o = options[i];
		if (o && o.value !== value) onchange(o.value);
	}

	function key(e: KeyboardEvent) {
		if (!open) {
			if (['Enter', ' ', 'ArrowDown', 'ArrowUp'].includes(e.key)) {
				e.preventDefault();
				show();
			}
			return;
		}
		if (e.key === 'ArrowDown') cursor = Math.min(options.length - 1, cursor + 1);
		else if (e.key === 'ArrowUp') cursor = Math.max(0, cursor - 1);
		else if (e.key === 'Home') cursor = 0;
		else if (e.key === 'End') cursor = options.length - 1;
		else if (e.key === 'Enter' || e.key === ' ') pick(cursor);
		else if (e.key === 'Escape' || e.key === 'Tab') {
			open = false;
			if (e.key === 'Tab') return;
		} else return;
		e.preventDefault();
		e.stopPropagation();
	}
</script>

<svelte:window
	onpointerdown={(e) => open && !trigger.contains(e.target as Node) && !list?.contains(e.target as Node) && (open = false)}
	onscrollcapture={(e) => open && !list?.contains(e.target as Node) && (open = false)}
	onresize={() => (open = false)}
/>

<button
	bind:this={trigger}
	type="button"
	class="select"
	class:mono
	class:open
	aria-haspopup="listbox"
	aria-expanded={open}
	aria-label={label || undefined}
	onclick={() => (open ? (open = false) : show())}
	onkeydown={key}
>
	<span class="value">{current?.label ?? ''}</span>
	<ChevronsUpDown size={13} />
</button>

{#if open}
	<div
		bind:this={list}
		class="list"
		class:mono
		role="listbox"
		tabindex="-1"
		style:left="{pos.x}px"
		style:top="{pos.y}px"
		style:width="{pos.w}px"
		style:transform-origin={pos.up ? 'bottom' : 'top'}
	>
		{#each options as o, i (i)}
			<button
				type="button"
				role="option"
				aria-selected={o.value === value}
				class:on={i === cursor}
				class:selected={o.value === value}
				onpointerenter={() => (cursor = i)}
				onclick={() => pick(i)}>{o.label}</button
			>
		{/each}
	</div>
{/if}

<style>
	.select {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		width: 100%;
		min-height: 1.75rem;
		padding: 0.3rem 0.5rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--foreground);
		font-size: 12px;
		line-height: 1.45;
		text-align: left;
	}
	.select:hover,
	.select.open {
		border-color: color-mix(in oklch, var(--foreground) 35%, var(--border));
	}
	.select :global(svg) {
		flex: none;
		color: var(--muted-foreground);
	}
	.value {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.mono {
		font-family: var(--font-mono);
	}
	.list {
		position: fixed;
		z-index: 700;
		max-height: 260px;
		overflow: auto;
		padding: 0.25rem;
		display: flex;
		flex-direction: column;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		box-shadow: var(--shadow);
	}
	.list button {
		display: flex;
		align-items: center;
		height: 1.75rem;
		flex: none;
		padding: 0 0.5rem;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--foreground);
		font: inherit;
		font-size: 12px;
		text-align: left;
	}
	.list button.on {
		background: var(--accent);
	}
	.list button.selected {
		background: var(--brand);
		color: #fff;
	}
</style>
