<script lang="ts" module>
	import type { DriverName } from '#lib/types.ts';

	export interface PickerItem {
		value: string;
		label: string;
		hint?: string;
		color?: string;
		/** Shows the database's own logo in place of the color dot. */
		driver?: DriverName;
	}
</script>

<script lang="ts">
	// A searchable dropdown anchored to its trigger (used for connection, database and schema).
	import { ChevronsUpDown, Search } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import DbLogo from './DbLogo.svelte';

	let {
		label,
		value,
		items,
		placeholder = 'Find…',
		disabled = false,
		onselect,
		trigger,
		footer
	}: {
		label?: string;
		value: string;
		items: PickerItem[];
		placeholder?: string;
		disabled?: boolean;
		onselect: (value: string) => void;
		/** Custom trigger content; defaults to the label and current value. */
		trigger?: Snippet;
		/** Actions shown under the list, e.g. "Create a new schema". */
		footer?: Snippet<[() => void]>;
	} = $props();

	let open = $state(false);
	let q = $state('');
	let cursor = $state(0);
	let root: HTMLDivElement;

	const shown = $derived(q ? items.filter((i) => `${i.label} ${i.hint ?? ''}`.toLowerCase().includes(q.toLowerCase())) : items);
	const current = $derived(items.find((i) => i.value === value));

	function toggle() {
		if (disabled) return;
		open = !open;
		q = '';
		cursor = Math.max(
			0,
			items.findIndex((i) => i.value === value)
		);
	}
	const close = () => (open = false);

	function choose(v: string) {
		open = false;
		if (v !== value) onselect(v);
	}

	function key(e: KeyboardEvent) {
		if (e.key === 'Escape') close();
		else if (e.key === 'ArrowDown') cursor = Math.min(shown.length - 1, cursor + 1);
		else if (e.key === 'ArrowUp') cursor = Math.max(0, cursor - 1);
		else if (e.key === 'Enter' && shown[cursor]) choose(shown[cursor].value);
		else return;
		e.preventDefault();
	}

	const autofocus = (el: HTMLInputElement) => el.focus();
</script>

<svelte:window onpointerdown={(e) => open && !root.contains(e.target as Node) && close()} />

<div class="picker" bind:this={root}>
	<button class="trigger no-drag" class:open {disabled} onclick={toggle}>
		{#if trigger}
			{@render trigger()}
		{:else}
			{#if current?.color}<span class="dot" style:background={current.color}></span>{/if}
			{#if label}<span class="lbl">{label}</span>{/if}
			<span class="val">{current?.label ?? value}</span>
		{/if}
		{#if !disabled}<ChevronsUpDown size={12} class="chev" />{/if}
	</button>

	{#if open}
		<div class="pop" role="dialog">
			{#if items.length > 6}
				<label class="search">
					<Search size={13} />
					<input use:autofocus bind:value={q} oninput={() => (cursor = 0)} onkeydown={key} {placeholder} spellcheck="false" />
				</label>
			{:else}
				<!-- Keyboard still works without the search box. -->
				<input class="sr" use:autofocus onkeydown={key} aria-label={placeholder} />
			{/if}
			<div class="list">
				{#each shown as item, i (item.value)}
					<button
						class="item"
						class:on={i === cursor}
						class:selected={item.value === value}
						style:--c={item.color || undefined}
						aria-current={item.value === value || undefined}
						onmouseenter={() => (cursor = i)}
						onclick={() => choose(item.value)}
					>
						{#if item.driver}<DbLogo driver={item.driver} color={item.color} />{:else if item.color}<span class="dot" style:background={item.color}></span>{/if}
						<span class="name">{item.label}</span>
						{#if item.hint}<span class="hint">{item.hint}</span>{/if}
					</button>
				{:else}
					<p class="none">No results</p>
				{/each}
			</div>
			{#if footer}<div class="foot">{@render footer(close)}</div>{/if}
		</div>
	{/if}
</div>

<style>
	.picker {
		position: relative;
		min-width: 0;
	}
	.trigger {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		max-width: 16rem;
		height: 1.75rem;
		padding: 0 0.4rem 0 0.5rem;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		white-space: nowrap;
	}
	.trigger:hover:not(:disabled),
	.trigger.open {
		background: var(--accent);
	}
	.trigger:disabled {
		cursor: default;
	}
	.lbl {
		color: var(--muted-foreground);
	}
	.trigger :global(.val) {
		min-width: 0;
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.trigger :global(.chev) {
		flex: none;
		color: var(--muted-foreground);
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		flex: none;
		border-radius: 50%;
	}
	.pop {
		position: absolute;
		top: calc(100% + 0.35rem);
		left: 0;
		z-index: 200;
		width: 16rem;
		display: flex;
		flex-direction: column;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
		overflow: hidden;
	}
	.search {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		height: 2.2rem;
		padding: 0 0.7rem;
		border-bottom: 1px solid var(--border);
		color: var(--muted-foreground);
	}
	.search input {
		flex: 1;
		min-width: 0;
		height: 100%;
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		outline: none;
	}
	.sr {
		position: absolute;
		width: 1px;
		height: 1px;
		opacity: 0;
		pointer-events: none;
	}
	.list {
		max-height: 18rem;
		overflow: auto;
		padding: 0.25rem;
	}
	.item {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		width: 100%;
		height: 1.9rem;
		padding: 0 0.55rem;
		border: none;
		border-radius: 0.3rem;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		text-align: left;
	}
	.item.on {
		background: var(--accent);
	}
	.name {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.hint {
		color: var(--muted-foreground);
		font-size: 11px;
	}
	/* The current choice wears its own color (or the connection's), like the header. */
	.item.selected {
		background: color-mix(in oklab, var(--c, var(--tint, var(--brand))) var(--tint-amount), var(--popover));
	}
	.item.selected.on {
		background: color-mix(in oklab, var(--c, var(--tint, var(--brand))) calc(var(--tint-amount) + 10%), var(--popover));
	}
	.none {
		margin: 0;
		padding: 0.6rem;
		color: var(--muted-foreground);
		font-size: 12.5px;
	}
	.foot {
		border-top: 1px solid var(--border);
		padding: 0.25rem;
	}
	.foot :global(button) {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		width: 100%;
		height: 1.9rem;
		padding: 0 0.55rem;
		border: none;
		border-radius: 0.3rem;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		text-align: left;
	}
	.foot :global(button:hover) {
		background: var(--accent);
	}
	.foot :global(svg) {
		color: var(--muted-foreground);
	}
</style>
