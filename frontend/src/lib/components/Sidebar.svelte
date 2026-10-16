<script lang="ts" module>
	const WIDTH_KEY = 'tablef.sidebar.width';
	const COLLAPSED_KEY = 'tablef.sidebar.sections';
</script>

<script lang="ts">
	import { modLabel } from '#lib/api.ts';
	import { ChevronRight, Eye, Plus, RefreshCw, Search, SquareFunction, Table2, Workflow } from '@lucide/svelte';
	import { app, routineTitle } from '#lib/state.svelte.ts';
	import { drag } from '#lib/drag.ts';
	import { load, save } from '#lib/storage.ts';
	import { panel } from '#lib/motion.ts';
	import DbLogo from './DbLogo.svelte';
	import type { Routine, Table } from '#lib/types.ts';

	let { searchInput = $bindable() }: { searchInput?: HTMLInputElement } = $props();

	let width = $state(Math.min(480, Math.max(200, load(WIDTH_KEY, 248))));
	let collapsed = $state<Record<string, boolean>>(load(COLLAPSED_KEY, {}));
	let search = $state('');

	const q = $derived(search.trim().toLowerCase());

	// Lower-cased names are computed once per catalog load, not on every keystroke.
	const inSchema = $derived(app.tables.filter((t) => t.schema === app.schema).map((t) => ({ t, key: t.name.toLowerCase() })));
	const routinesInSchema = $derived(app.routines.filter((r) => r.schema === app.schema).map((r) => ({ r, key: r.name.toLowerCase() })));
	const tables = $derived(inSchema.filter((x) => x.t.type === 'table' && (!q || x.key.includes(q))).map((x) => x.t));
	const views = $derived(inSchema.filter((x) => x.t.type === 'view' && (!q || x.key.includes(q))).map((x) => x.t));
	const routines = $derived(routinesInSchema.filter((x) => !q || x.key.includes(q)).map((x) => x.r));

	// Huge schemas render in pages so the DOM stays small; search narrows further.
	const PAGE = 300;
	let shown = $state<Record<string, number>>({});
	const limitOf = (key: string) => shown[key] ?? PAGE;
	const more = (key: string) => (shown = { ...shown, [key]: limitOf(key) + PAGE * 4 });
	$effect(() => {
		void app.schema;
		void q;
		shown = {};
	});
	const version = $derived(
		(app.session?.version ?? '')
			.split(/[\s,]+/)
			.slice(0, 2)
			.join(' ')
	);

	// While searching every section is open so matches are visible.
	const isOpen = (key: string) => !!q || !collapsed[key];

	function toggle(key: string) {
		collapsed = { ...collapsed, [key]: !collapsed[key] };
		save(COLLAPSED_KEY, collapsed);
	}

	// What is open in the active tab, computed once per tab switch instead of per row.
	const activeTable = $derived.by(() => {
		const a = app.activeTab;
		return a?.kind === 'table' ? a.table : null;
	});
	const activeTitle = $derived(app.activeTab?.kind === 'query' ? app.activeTab.title : '');
	const tableActive = (t: Table) => activeTable !== null && activeTable.name === t.name && activeTable.schema === t.schema;
	const routineActive = (r: Routine) => activeTitle === routineTitle(r);

	function searchKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			search = '';
			(e.currentTarget as HTMLInputElement).blur();
		} else if (e.key === 'Enter') {
			// Enter opens the first match.
			const t = tables[0] ?? views[0];
			if (t) app.openTable(t);
			else if (routines[0]) app.openRoutine(routines[0]);
		}
	}

	function resizeStart(e: PointerEvent) {
		const x0 = e.clientX;
		const w0 = width;
		drag(
			e,
			(ev) => (width = Math.round(Math.min(480, Math.max(200, w0 + ev.clientX - x0)))),
			() => save(WIDTH_KEY, width)
		);
	}
</script>

{#snippet section(key: string, label: string, count: number, action?: { title: string; run: () => void })}
	<div class="section">
		<button class="section-toggle" onclick={() => toggle(key)} aria-expanded={isOpen(key)}>
			<ChevronRight size={12} class={isOpen(key) ? 'chev open' : 'chev'} />
			<span>{label}</span>
			{#key count}<span class="count tf-text">{count}</span>{/key}
		</button>
		{#if action}
			<button class="section-act" onclick={action.run} title={action.title}><Plus size={13} /></button>
		{/if}
	</div>
{/snippet}

<aside class="sidebar" style:width="{width}px" transition:panel>
	<div class="search">
		<Search size={13} />
		<input bind:this={searchInput} bind:value={search} onkeydown={searchKey} placeholder="Search" spellcheck="false" />
		{#if !search}<kbd>{modLabel}P</kbd>{/if}
	</div>

	<nav class="tree">
		{@render section('tables', 'Tables', tables.length, { title: 'New table', run: () => app.newTable() })}
		{#if isOpen('tables')}
			{#each tables.slice(0, limitOf('tables')) as t, i (t.name)}
				<button class="item" style:--i={i} class:active={tableActive(t)} onclick={() => app.openTable(t)} title={t.name}>
					<span class="icon-box"><Table2 /></span>
					<span>{t.name}</span>
				</button>
			{:else}
				<p class="empty">{app.tablesLoading ? 'Loading…' : q ? 'No matches' : 'No tables'}</p>
			{/each}
			{#if tables.length > limitOf('tables')}
				<button class="more" onclick={() => more('tables')}>Show more ({(tables.length - limitOf('tables')).toLocaleString()} left)</button>
			{/if}
		{/if}

		{#if views.length || !q}
			{@render section('views', 'Views', views.length)}
			{#if isOpen('views')}
				{#each views.slice(0, limitOf('views')) as t, i (t.name)}
					<button class="item" style:--i={i} class:active={tableActive(t)} onclick={() => app.openTable(t)} title={t.name}>
						<span class="icon-box"><Eye /></span>
						<span>{t.name}</span>
					</button>
				{:else}
					<p class="empty">No views</p>
				{/each}
				{#if views.length > limitOf('views')}
					<button class="more" onclick={() => more('views')}>Show more ({(views.length - limitOf('views')).toLocaleString()} left)</button>
				{/if}
			{/if}
		{/if}

		{#if app.session?.features?.routines && (routines.length || !q)}
			{@render section('functions', 'Functions', routines.length)}
			{#if isOpen('functions')}
				{#each routines.slice(0, limitOf('functions')) as r, i (r.id)}
					<button
						class="item"
						style:--i={i}
						class:active={routineActive(r)}
						onclick={() => app.openRoutine(r)}
						title={`${r.name}(${r.arguments})${r.returnType ? ` → ${r.returnType}` : ''}`}
					>
						<span class="icon-box"
							>{#if r.kind === 'procedure'}<Workflow />{:else}<SquareFunction />{/if}</span
						>
						<span>{r.name}</span>
					</button>
				{:else}
					<p class="empty">No functions</p>
				{/each}
				{#if routines.length > limitOf('functions')}
					<button class="more" onclick={() => more('functions')}>Show more ({(routines.length - limitOf('functions')).toLocaleString()} left)</button>
				{/if}
			{/if}
		{/if}
	</nav>

	<footer class="status">
		{#if app.session}<DbLogo driver={app.session.driver} size={14} />{/if}
		{#key version}<span class="driver tf-text">{version || app.session?.driver}</span>{/key}
		<button class="refresh" onclick={() => app.loadTables()} title="Refresh schema" disabled={app.tablesLoading}>
			<RefreshCw size={12} class={app.tablesLoading ? 'spin' : ''} />
		</button>
	</footer>

	<div class="resize" role="separator" aria-orientation="vertical" aria-label="Resize sidebar" onpointerdown={resizeStart}></div>
</aside>

<style>
	.sidebar {
		position: relative;
		display: flex;
		flex-direction: column;
		flex: none;
		min-width: 0;
		height: 100%;
		background: var(--sidebar);
		border-right: 1px solid var(--sidebar-border);
	}
	.search {
		position: relative;
		display: flex;
		align-items: center;
		gap: 0.45rem;
		height: 1.9rem;
		margin: 0.6rem 0.6rem 0.35rem;
		padding: 0 0.4rem 0 0.6rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--background);
		color: var(--muted-foreground);
	}
	.search:focus-within {
		border-color: color-mix(in oklch, var(--brand) 55%, var(--border));
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
		user-select: text;
	}
	.search input::placeholder {
		color: var(--muted-foreground);
	}
	.search kbd {
		height: 1.1rem;
		font-size: 10px;
	}
	.tree {
		flex: 1;
		overflow: auto;
		padding: 0.15rem 0.45rem 0.75rem;
	}
	.section {
		display: flex;
		align-items: center;
		margin-top: 0.55rem;
	}
	.section:first-child {
		margin-top: 0.2rem;
	}
	.section-toggle {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		flex: 1;
		height: 1.6rem;
		padding: 0 0.35rem;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--muted-foreground);
		font-size: 11.5px;
		font-weight: 600;
		text-align: left;
	}
	.section-toggle:hover {
		color: var(--foreground);
	}
	.section-toggle :global(.chev) {
		transition: transform 0.12s;
	}
	.section-toggle :global(.chev.open) {
		transform: rotate(90deg);
	}
	.count {
		margin-left: auto;
		font-weight: 500;
		font-size: 11px;
		font-variant-numeric: tabular-nums;
		opacity: 0.8;
	}
	.section-act {
		display: grid;
		place-items: center;
		width: 1.4rem;
		height: 1.4rem;
		margin-left: 0.15rem;
		padding: 0;
		border: none;
		border-radius: 0.3rem;
		background: transparent;
		color: var(--muted-foreground);
		opacity: 0;
	}
	.section:hover .section-act,
	.section-act:focus-visible {
		opacity: 1;
	}
	.section-act:hover {
		background: var(--sidebar-accent);
		color: var(--foreground);
	}
	.item {
		animation: tf-drift var(--dur-slow) var(--ease-out) both;
		animation-delay: calc(min(var(--i, 0), 20) * 12ms);
		display: flex;
		align-items: center;
		gap: 0.55rem;
		width: 100%;
		height: 1.75rem;
		padding: 0 0.5rem 0 1.35rem;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 13px;
		text-align: left;
		white-space: nowrap;
	}
	.item span {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.item :global(.icon-box) {
		opacity: 0.82;
	}
	.item:hover {
		background: color-mix(in oklch, var(--sidebar-accent) 70%, transparent);
	}
	.item.active {
		background: var(--sidebar-accent);
		font-weight: 500;
	}
	.item.active :global(.icon-box),
	.item:hover :global(.icon-box) {
		opacity: 1;
	}
	.item:focus-visible {
		outline: none;
		box-shadow: inset 0 0 0 1px color-mix(in oklch, var(--brand) 60%, transparent);
	}
	.more {
		width: 100%;
		height: 1.6rem;
		padding: 0 0.5rem 0 1.35rem;
		border: none;
		background: transparent;
		color: var(--muted-foreground);
		font-size: 12px;
		text-align: left;
	}
	.more:hover {
		color: var(--foreground);
	}
	.empty {
		animation: tf-fade var(--dur-slow) ease-out both;
		margin: 0;
		padding: 0.25rem 0.5rem 0.25rem 1.35rem;
		color: var(--muted-foreground);
		font-size: 12px;
	}
	.status {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		height: 2.1rem;
		flex: none;
		padding: 0 0.4rem 0 0.85rem;
		border-top: 1px solid var(--sidebar-border);
		color: var(--muted-foreground);
		font-size: 11.5px;
	}
	.driver {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.refresh {
		display: grid;
		place-items: center;
		width: 1.5rem;
		height: 1.5rem;
		padding: 0;
		border: none;
		border-radius: 0.3rem;
		background: transparent;
		color: var(--muted-foreground);
	}
	.refresh:hover:not(:disabled) {
		background: var(--sidebar-accent);
		color: var(--foreground);
	}
	.resize {
		transition: background-color var(--dur) ease-out;
		position: absolute;
		top: 0;
		bottom: 0;
		right: -3px;
		width: 6px;
		cursor: col-resize;
		z-index: 4;
	}
	.resize:hover,
	.resize:active {
		background: color-mix(in oklch, var(--brand) 35%, transparent);
	}
</style>
