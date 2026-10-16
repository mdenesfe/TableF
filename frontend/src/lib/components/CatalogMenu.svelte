<script lang="ts" module>
	import type { Routine, SavedQuery, Table } from '#lib/types.ts';
	export type MenuTarget = { table: Table } | { routine: Routine } | { query: SavedQuery };
</script>

<script lang="ts">
	// Right-click menu for a table, view, routine or saved query in the sidebar.
	import { ChevronRight } from '@lucide/svelte';
	import { app } from '#lib/state.svelte.ts';
	import {
		canDropRoutine,
		canModify,
		cloneTable,
		copyName,
		copySql,
		copyQualified,
		copyScript,
		dropRoutine,
		dropTable,
		exportTable,
		pins,
		truncateTable,
		runSelectTop,
		runCount,
		canQuery
	} from '#lib/catalogactions.svelte.ts';

	let { target, x, y, onclose }: { target: MenuTarget; x: number; y: number; onclose: () => void } = $props();

	let root: HTMLDivElement;
	// Keep the menu on screen.
	const left = $derived(Math.min(x, window.innerWidth - 240));
	const top = $derived(Math.min(y, window.innerHeight - ('table' in target ? 420 : 'query' in target ? 210 : 140)));

	// Start the action before closing: closing clears the target it reads.
	function act(fn: () => unknown) {
		fn();
		onclose();
	}
</script>

<svelte:window onpointerdown={(e) => !root.contains(e.target as Node) && onclose()} onkeydown={(e) => e.key === 'Escape' && onclose()} onblur={onclose} />

<div class="menu" bind:this={root} style:left="{left}px" style:top="{top}px" role="menu">
	{#if 'table' in target}
		{@const t = target.table}
		<button role="menuitem" onclick={() => act(() => app.openTable(t))}>Open</button>
		<button role="menuitem" onclick={() => act(() => app.openTable(t, '', { newTab: true }))}>Open in new tab</button>
		<button role="menuitem" onclick={() => act(() => app.openTable(t, '', { view: 'structure' }))}>Open structure</button>
		{#if canQuery()}
			<span class="sep"></span>
			<button role="menuitem" onclick={() => act(() => runSelectTop(t, 100))}>Select top 100 rows</button>
			<button role="menuitem" onclick={() => act(() => runSelectTop(t, 1000))}>Select top 1000 rows</button>
			<button role="menuitem" onclick={() => act(() => runCount(t))}>Count rows</button>
		{/if}
		<span class="sep"></span>
		<button role="menuitem" onclick={() => act(() => copyName(t.name))}>Copy name</button>
		<button role="menuitem" onclick={() => act(() => copyQualified(t))}>Copy qualified name</button>
		<span class="sep"></span>
		<button role="menuitem" onclick={() => act(() => pins.toggle(t))}>{pins.has(t) ? 'Unpin' : 'Pin to top'}</button>
		<span class="sep"></span>
		<div class="sub" role="none">
			<button role="menuitem" aria-haspopup="menu">Export <ChevronRight size={13} /></button>
			<div class="menu flyout" role="menu">
				<button role="menuitem" onclick={() => act(() => exportTable(t, 'markdown'))}>Copy as Markdown</button>
				<button role="menuitem" onclick={() => act(() => exportTable(t, 'json'))}>Copy as JSON</button>
				<span class="sep"></span>
				<button role="menuitem" onclick={() => act(() => exportTable(t, 'csv'))}>Download CSV</button>
				<button role="menuitem" onclick={() => act(() => exportTable(t, 'xlsx'))}>Download Excel</button>
			</div>
		</div>
		{#if canModify()}
			<div class="sub" role="none">
				<button role="menuitem" aria-haspopup="menu">Copy script as <ChevronRight size={13} /></button>
				<div class="menu flyout" role="menu">
					<button role="menuitem" class="mono" onclick={() => act(() => copyScript(t, 'select'))}>SELECT</button>
					<button role="menuitem" class="mono" onclick={() => act(() => copyScript(t, 'insert'))}>INSERT</button>
					<button role="menuitem" class="mono" onclick={() => act(() => copyScript(t, 'update'))}>UPDATE</button>
					<button role="menuitem" class="mono" onclick={() => act(() => copyScript(t, 'delete'))}>DELETE</button>
				</div>
			</div>
			<div class="sub" role="none">
				<button role="menuitem" aria-haspopup="menu">New <ChevronRight size={13} /></button>
				<div class="menu flyout" role="menu">
					<button role="menuitem" onclick={() => act(() => app.newTable())}>Table</button>
					<button role="menuitem" onclick={() => act(() => app.openQuery())}>Query</button>
				</div>
			</div>
			<span class="sep"></span>
			{#if t.type === 'table'}
				<button role="menuitem" onclick={() => act(() => cloneTable(t))}>Clone…</button>
				<button role="menuitem" class="danger" onclick={() => act(() => truncateTable(t))}>Truncate…</button>
			{/if}
			<button role="menuitem" class="danger" onclick={() => act(() => dropTable(t))}>Drop {t.type === 'view' ? 'view' : 'table'}…</button>
		{/if}
	{:else if 'query' in target}
		{@const sq = target.query}
		<button role="menuitem" onclick={() => act(() => app.openSaved(sq))}>Open</button>
		<button role="menuitem" onclick={() => act(() => app.runQuery(sq.sql, sq.name))}>Run</button>
		<span class="sep"></span>
		<button role="menuitem" onclick={() => act(() => app.renameSaved(sq))}>Rename…</button>
		<button role="menuitem" onclick={() => act(() => copySql(sq.sql))}>Copy SQL</button>
		<span class="sep"></span>
		<button role="menuitem" class="danger" onclick={() => act(() => app.deleteSaved(sq))}>Delete…</button>
	{:else}
		{@const r = target.routine}
		<button role="menuitem" onclick={() => act(() => app.openRoutine(r))}>Open definition</button>
		<button role="menuitem" onclick={() => act(() => copyName(r.name))}>Copy name</button>
		{#if canDropRoutine()}
			<span class="sep"></span>
			<button role="menuitem" class="danger" onclick={() => act(() => dropRoutine(r))}>Drop {r.kind === 'procedure' ? 'procedure' : 'function'}…</button>
		{/if}
	{/if}
</div>

<style>
	.menu {
		position: fixed;
		z-index: 600;
		min-width: 13rem;
		padding: 0.25rem;
		display: flex;
		flex-direction: column;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
	}
	button {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		height: 1.85rem;
		padding: 0 0.6rem;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		text-align: left;
		white-space: nowrap;
	}
	button :global(svg) {
		color: var(--muted-foreground);
	}
	button:hover,
	.sub:hover > button,
	.sub:focus-within > button {
		background: var(--brand);
		color: #fff;
	}
	button:hover :global(svg),
	.sub:hover > button :global(svg) {
		color: #fff;
	}
	.danger {
		color: var(--destructive);
	}
	.danger:hover {
		background: var(--destructive);
	}
	.sep {
		height: 1px;
		margin: 0.25rem 0.4rem;
		background: var(--border);
	}
	/* Submenus open to the right on hover (or keyboard focus). */
	.sub {
		position: relative;
	}
	.sub > button {
		width: 100%;
	}
	.flyout {
		position: absolute;
		top: -0.3rem;
		left: calc(100% + 0.2rem);
		display: none;
		min-width: 11rem;
	}
	.sub:hover > .flyout,
	.sub:focus-within > .flyout {
		display: flex;
	}
</style>
