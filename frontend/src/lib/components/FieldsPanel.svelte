<script lang="ts">
	import { Eye, EyeOff, GripVertical, KeyRound, Pin, PinOff, Search, X } from '@lucide/svelte';
	import { panel } from '#lib/motion.ts';
	import { drag } from '#lib/drag.ts';
	import { kindOf, type CellKind } from '#lib/celltype.ts';
	import { load, save } from '#lib/storage.ts';
	import { COLUMN_MIME, type GridColumn, type GridLayout } from './DataGrid.svelte';

	let { columns, layout = $bindable(), onclose }: { columns: GridColumn[]; layout: GridLayout; onclose: () => void } = $props();

	const WIDTH_KEY = 'tablef.fields.width';
	let width = $state(Math.min(520, Math.max(200, load(WIDTH_KEY, 240))));

	function resizeStart(e: PointerEvent) {
		const x0 = e.clientX;
		const w0 = width;
		drag(
			e,
			(ev) => (width = Math.round(Math.min(520, Math.max(200, w0 + ev.clientX - x0)))),
			() => save(WIDTH_KEY, width)
		);
	}

	let q = $state('');
	let dropOn = $state('');

	// Maps and sets keep this O(columns) instead of O(columns²).
	const ordered = $derived.by(() => {
		const byName = new Map(columns.map((c) => [c.name, c]));
		const inOrder = new Set(layout.order);
		const order = [...layout.order.filter((n) => byName.has(n)), ...columns.map((c) => c.name).filter((n) => !inOrder.has(n))];
		const pinned = layout.pinned.filter((n) => byName.has(n));
		const isPinned = new Set(pinned);
		return [...pinned, ...order.filter((n) => !isPinned.has(n))].map((n) => byName.get(n)!);
	});
	const shown = $derived(ordered.filter((c) => !q || c.name.toLowerCase().includes(q.toLowerCase())));
	const visibleCount = $derived.by(() => {
		const names = new Set(columns.map((c) => c.name));
		return columns.length - layout.hidden.filter((n) => names.has(n)).length;
	});

	const glyphs: Record<CellKind, string> = { number: '#', date: 'dt', datetime: 'dt', time: 'dt', bool: '◐', json: '{}', uuid: 'id', text: 'Aa' };
	const glyph = (t?: string) => glyphs[kindOf(t)];

	const toggle = (list: string[], n: string) => (list.includes(n) ? list.filter((x) => x !== n) : [...list, n]);
	const hide = (n: string) => (layout = { ...layout, hidden: toggle(layout.hidden, n) });
	const pin = (n: string) => (layout = { ...layout, pinned: toggle(layout.pinned, n), hidden: layout.hidden.filter((x) => x !== n) });
	const showAll = () => (layout = { ...layout, hidden: [] });

	function dragStart(e: DragEvent, n: string) {
		e.dataTransfer?.setData(COLUMN_MIME, n);
		e.dataTransfer?.setData('text/plain', n);
	}
	function drop(e: DragEvent, target: string) {
		const n = e.dataTransfer?.getData(COLUMN_MIME);
		dropOn = '';
		if (!n || n === target) return;
		e.preventDefault();
		const all = ordered.map((c) => c.name).filter((x) => x !== n);
		all.splice(all.indexOf(target), 0, n);
		const pinned = layout.pinned.filter((x) => x !== n);
		if (layout.pinned.includes(target)) pinned.splice(pinned.indexOf(target), 0, n);
		layout = { ...layout, order: all, pinned };
	}
</script>

<aside class="fields" style:width="{width}px" transition:panel>
	<div class="resize" role="separator" aria-orientation="vertical" aria-label="Resize fields panel" onpointerdown={resizeStart}></div>
	<header>
		<Search size={13} />
		<input bind:value={q} placeholder="Find field" spellcheck="false" />
		<button class="icon" onclick={onclose} title="Close"><X size={14} /></button>
	</header>
	<div class="sub">
		{#key visibleCount}<span class="tf-text">{visibleCount} / {columns.length} visible</span>{/key}
		{#if visibleCount < columns.length}<button class="link" onclick={showAll}>Show all</button>{/if}
	</div>
	<div class="list">
		{#each shown as c, i (c.name)}
			{@const hidden = layout.hidden.includes(c.name)}
			{@const pinned = layout.pinned.includes(c.name)}
			<div
				class="field"
				style:--i={i}
				class:hidden
				class:drop={dropOn === c.name}
				draggable="true"
				role="listitem"
				ondragstart={(e) => dragStart(e, c.name)}
				ondragover={(e) => {
					if (e.dataTransfer?.types.includes(COLUMN_MIME)) {
						e.preventDefault();
						dropOn = c.name;
					}
				}}
				ondragleave={() => dropOn === c.name && (dropOn = '')}
				ondrop={(e) => drop(e, c.name)}
			>
				<GripVertical size={13} class="grip" />
				<span class="icon-box text glyph">{glyph(c.type)}</span>
				<span class="name" title={c.type}>{c.name}</span>
				{#if c.primaryKey}<span class="icon-box" title="Primary key"><KeyRound /></span>{/if}
				<button class="icon act" class:on={pinned} onclick={() => pin(c.name)} title={pinned ? 'Unpin' : 'Pin left'}>
					{#if pinned}<PinOff size={13} />{:else}<Pin size={13} />{/if}
				</button>
				<button class="icon act" class:always={hidden} onclick={() => hide(c.name)} title={hidden ? 'Show' : 'Hide'}>
					{#if hidden}<EyeOff size={13} />{:else}<Eye size={13} />{/if}
				</button>
			</div>
		{/each}
	</div>
</aside>

<style>
	.fields {
		position: relative;
		display: flex;
		flex-direction: column;
		flex: none;
		min-height: 0;
		border-right: 1px solid var(--border);
		background: var(--card);
	}
	.resize {
		transition: background-color var(--dur) ease-out;
		position: absolute;
		top: 0;
		bottom: 0;
		right: -3px;
		width: 6px;
		z-index: 5;
		cursor: col-resize;
	}
	.resize:hover,
	.resize:active {
		background: color-mix(in oklab, var(--brand) 40%, transparent);
	}
	header {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		height: 2.25rem;
		flex: none;
		padding: 0 0.3rem 0 0.7rem;
		border-bottom: 1px solid var(--border);
		color: var(--muted-foreground);
	}
	header input {
		flex: 1;
		min-width: 0;
		height: 100%;
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		outline: none;
		user-select: text;
	}
	.sub {
		display: flex;
		align-items: center;
		justify-content: space-between;
		height: 1.85rem;
		flex: none;
		padding: 0 0.7rem;
		border-bottom: 1px solid var(--border);
		font-size: var(--text-xs);
		color: var(--muted-foreground);
	}
	.link {
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: var(--text-xs);
		padding: 0;
		text-decoration: underline;
		text-underline-offset: 2px;
	}
	.list {
		flex: 1;
		overflow: auto;
		padding: 0.25rem;
	}
	.field {
		animation: tf-drift var(--dur-slow) var(--ease-out) both;
		animation-delay: calc(min(var(--i, 0), 16) * 12ms);
		display: flex;
		align-items: center;
		gap: 0.4rem;
		height: 1.85rem;
		padding: 0 0.15rem 0 0.25rem;
		border-radius: 0.3rem;
		font-size: 12.5px;
		cursor: grab;
	}
	.field:hover {
		background: var(--accent);
	}
	.field.drop {
		box-shadow: inset 0 2px 0 var(--foreground);
	}
	.field.hidden .name {
		color: var(--muted-foreground);
	}
	.field :global(.grip) {
		color: var(--muted-foreground);
		opacity: 0.5;
		flex: none;
	}
	.glyph {
		min-width: 1.35rem;
	}
	.field.hidden .glyph {
		opacity: 0.45;
	}
	.name {
		flex: 1;
		min-width: 0;
		font-family: var(--font-mono);
		font-size: 12px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.icon {
		display: grid;
		place-items: center;
		width: 1.5rem;
		height: 1.5rem;
		flex: none;
		padding: 0;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--muted-foreground);
	}
	.icon:hover {
		color: var(--foreground);
		background: color-mix(in oklch, var(--foreground) 8%, transparent);
	}
	.act {
		opacity: 0;
	}
	.field:hover .act,
	.act.on,
	.act.always {
		opacity: 1;
	}
	.act.on {
		color: var(--foreground);
	}
</style>
