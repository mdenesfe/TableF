<script lang="ts">
	import { Database, Eye, History, Keyboard, LogOut, PanelLeft, Plus, Settings, SquareFunction, SquareTerminal, Table2, X } from '@lucide/svelte';
	import { tick, untrack } from 'svelte';
	import { modLabel, tabModLabel, targetIn, isMacDesktop } from '#lib/api.ts';
	import Picker from './Picker.svelte';
	import { groupsOf, measure, resizeAt, type Divider, type Side } from '#lib/layout.ts';
	import { app, MAX_PANES, routineTitle } from '#lib/state.svelte.ts';
	import { drag } from '#lib/drag.ts';
	import QueryView from './QueryView.svelte';
	import HistoryView from './HistoryView.svelte';
	import SafeModeBadge from './SafeModeBadge.svelte';
	import UserMenu from './UserMenu.svelte';
	import Shortcuts from './Shortcuts.svelte';
	import HeaderDots from './HeaderDots.svelte';
	import FullscreenLights from './FullscreenLights.svelte';
	import { engine } from '#lib/engines.ts';
	import { prefs } from '#lib/prefs.svelte.ts';
	import Sidebar from './Sidebar.svelte';
	import TableView from './TableView.svelte';

	let sidebarOpen = $state(true);
	let helpOpen = $state(false);
	let sidebarSearch = $state<HTMLInputElement>();
	const routineTitles = $derived(new Set(app.routines.map(routineTitle)));

	// The header carries a soft wash of the connection's color, so it's clear which database you're in.
	const tint = $derived(app.connection?.color || '');

	// ---------- tabs: flexible split panes, drag and drop, context menu ----------
	const TAB_MIME = 'application/x-tablef-tab';
	const STRIP = '2.35rem';
	const layoutInfo = $derived(measure(app.layout));
	const groups = $derived(groupsOf(app.layout));
	const rectOf = (g: number) => layoutInfo.rects.get(g) ?? { x: 0, y: 0, w: 1, h: 1 };
	const pct = (v: number) => `${(v * 100).toFixed(4)}%`;
	let canvas = $state<HTMLDivElement>();
	let dragging = $state('');
	let dropBefore = $state('');
	let zone = $state<{ group: number; side: Side | 'center' } | null>(null);
	let menu = $state<{ x: number; y: number; id: string } | null>(null);

	function dragStart(e: DragEvent, id: string) {
		dragging = id;
		e.dataTransfer?.setData(TAB_MIME, id);
		if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move';
	}
	function dragEnd() {
		dragging = dropBefore = '';
		zone = null;
	}
	function accepts(e: DragEvent) {
		if (!e.dataTransfer?.types.includes(TAB_MIME)) return false;
		e.preventDefault();
		e.stopPropagation();
		return true;
	}
	function dropOnStrip(e: DragEvent, group: number, beforeId?: string) {
		const id = e.dataTransfer?.getData(TAB_MIME);
		dragEnd();
		if (!id) return;
		e.preventDefault();
		e.stopPropagation();
		app.moveTab(id, group, beforeId);
	}

	/** Which part of a pane the pointer is over: an edge splits, the middle moves. */
	function zoneAt(e: DragEvent, group: number) {
		const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
		const fx = (e.clientX - r.left) / r.width;
		const fy = (e.clientY - r.top) / r.height;
		const edge = Math.min(fx, 1 - fx, fy, 1 - fy);
		let side: Side | 'center' = 'center';
		if (edge < 0.25) side = edge === fx ? 'left' : edge === 1 - fx ? 'right' : edge === fy ? 'top' : 'bottom';
		zone = { group, side };
	}
	function dropOnPane(e: DragEvent, group: number) {
		const id = e.dataTransfer?.getData(TAB_MIME);
		const z = zone;
		dragEnd();
		if (!id || !z) return;
		e.preventDefault();
		if (z.side === 'center') app.moveTab(id, group);
		else app.splitTab(id, group, z.side);
	}
	const zoneStyle = (side: Side | 'center') =>
		({
			center: 'inset: 0',
			left: 'inset: 0 50% 0 0',
			right: 'inset: 0 0 0 50%',
			top: 'inset: 0 0 50% 0',
			bottom: 'inset: 50% 0 0 0'
		})[side];

	function resizeDivider(e: PointerEvent, d: Divider) {
		const rect = canvas!.getBoundingClientRect();
		drag(e, (ev) => {
			const pos = d.dir === 'row' ? (ev.clientX - rect.left) / rect.width : (ev.clientY - rect.top) / rect.height;
			app.layout = resizeAt(app.layout, d, pos);
		});
	}

	// A vertical wheel scrolls the tab strip sideways.
	function stripWheel(e: WheelEvent) {
		const el = e.currentTarget as HTMLElement;
		if (Math.abs(e.deltaY) <= Math.abs(e.deltaX) || el.scrollWidth <= el.clientWidth) return;
		el.scrollLeft += e.deltaY; // wheel listeners are passive; nothing else scrolls here
	}

	/** Scrolls a tab into view when it becomes the active one. */
	function revealWhen(node: HTMLElement, active: boolean) {
		const reveal = (on: boolean) => on && node.scrollIntoView({ block: 'nearest', inline: 'nearest' });
		reveal(active);
		return { update: reveal };
	}

	function openMenu(e: MouseEvent, id: string) {
		e.preventDefault();
		menu = { x: Math.min(e.clientX, window.innerWidth - 230), y: Math.min(e.clientY, window.innerHeight - 280), id };
	}
	function run(fn: () => void) {
		menu = null;
		fn();
	}

	// Keep the saved session current so a page reload lands back here.
	$effect(() => {
		void [app.session?.database, app.schema, app.activeTabId, app.tabs.map((t) => t.group + (t.kind === 'query' ? t.title + t.sql : t.id))];
		untrack(() => app.schedulePersist());
	});

	// Loading fills the header with that color; when work finishes it completes and fades.
	let phase = $state<'idle' | 'loading' | 'done'>('idle');
	let doneTimer: ReturnType<typeof setTimeout> | undefined;
	$effect(() => {
		const busy = app.pending > 0;
		untrack(() => {
			if (busy) {
				clearTimeout(doneTimer);
				phase = 'loading';
			} else if (phase === 'loading') {
				phase = 'done';
				doneTimer = setTimeout(() => (phase = 'idle'), 500);
			}
		});
	});

	const connectionItems = $derived(
		app.connections.map((c) => ({
			value: c.id,
			label: c.name,
			hint: c.tags?.length ? c.tags.join(' · ') : engine(c.driver).label,
			color: c.color,
			driver: c.driver
		}))
	);
	const databaseItems = $derived((app.session?.databases ?? []).map((d) => ({ value: d, label: d })));
	const schemaItems = $derived(app.schemas.map((s) => ({ value: s, label: s })));

	function cycleTab(step: number) {
		const n = app.tabs.length;
		if (!n) return;
		const i = app.tabs.findIndex((t) => t.id === app.activeTabId);
		app.activeTabId = app.tabs[(i + step + n) % n].id;
	}

	async function focusSearch() {
		sidebarOpen = true;
		await tick();
		sidebarSearch?.focus();
		sidebarSearch?.select();
	}

	function keydown(e: KeyboardEvent) {
		// Ctrl+Tab cycles tabs on every platform.
		if (e.ctrlKey && e.key === 'Tab') {
			e.preventDefault();
			return cycleTab(e.shiftKey ? -1 : 1);
		}
		const mod = e.metaKey || e.ctrlKey;
		if (!mod || e.altKey) return;
		const key = e.key.toLowerCase();
		const inEditor = targetIn(e, '.cm-editor');
		if (key === 't') {
			e.preventDefault();
			app.openQuery();
		} else if (key === 'w' && app.activeTabId) {
			e.preventDefault();
			app.closeTab(app.activeTabId);
		} else if (key === 'h' && e.shiftKey) {
			e.preventDefault();
			app.openHistory();
		} else if (key === 'b') {
			e.preventDefault();
			sidebarOpen = !sidebarOpen;
		} else if (key === 'p') {
			e.preventDefault();
			focusSearch();
		} else if (key === '/' && !inEditor) {
			e.preventDefault();
			helpOpen = !helpOpen;
		} else if (/^[1-9]$/.test(e.key)) {
			e.preventDefault();
			const t = e.key === '9' ? app.tabs.at(-1) : app.tabs[Number(e.key) - 1];
			if (t) app.activeTabId = t.id;
		} else if (e.key === '\\' && app.activeTab) {
			// Split the focused pane: ⌘\ to the right, ⇧⌘\ downwards.
			e.preventDefault();
			const t = app.activeTab;
			app.splitTab(t.id, t.group, e.shiftKey ? 'bottom' : 'right');
		} else if (e.shiftKey && (e.key === '[' || e.key === '{' || e.key === ']' || e.key === '}')) {
			e.preventDefault();
			cycleTab(e.key === '[' || e.key === '{' ? -1 : 1);
		}
	}
</script>

<svelte:window onkeydown={keydown} onpointerdown={(e) => menu && !(e.target as HTMLElement).closest('.menu') && (menu = null)} />

<div class="shell" style:--tint={tint || undefined}>
	<header
		class="topbar drag"
		class:tinted={!!tint}
		class:loading={phase === 'loading'}
		class:done={phase === 'done'}
		style:--tint={tint || undefined}
		aria-busy={phase === 'loading'}
	>
		{#if prefs.value.headerAnimation}<HeaderDots color={tint} loading={phase === 'loading'} />{/if}
		{#if isMacDesktop}<div class="traffic"><FullscreenLights /></div>{/if}
		<button class="icon no-drag" onclick={() => (sidebarOpen = !sidebarOpen)} title="Toggle sidebar ({modLabel}B)">
			<PanelLeft size={15} />
		</button>

		<nav class="crumbs no-drag" aria-label="Location">
			<Picker value={app.connection?.id ?? ''} items={connectionItems} placeholder="Find connection…" onselect={(id) => app.switchConnection(id)}>
				{#snippet trigger()}
					<span class="val">{app.connection?.name}</span>
				{/snippet}
				{#snippet footer(close)}
					<button
						onclick={() => {
							close();
							app.disconnect();
						}}><Database size={13} /> All connections</button
					>
				{/snippet}
			</Picker>
			{#if app.session}
				{#if databaseItems.length > 1}
					<span class="sep">/</span>
					<Picker value={app.session.database} items={databaseItems} placeholder="Find database…" disabled={app.busy} onselect={(d) => app.useDatabase(d)} />
				{/if}
				{#if app.session.features?.schemas}
					<span class="sep">/</span>
					<Picker value={app.schema} items={schemaItems} placeholder="Find schema…" onselect={(s) => (app.schema = s)}>
						{#snippet footer(close)}
							{#if app.session?.features?.createSchema && !app.readOnly}
								<button
									onclick={() => {
										close();
										app.createSchema();
									}}><Plus size={13} /> Create schema</button
								>
							{/if}
						{/snippet}
					</Picker>
				{/if}
			{/if}
		</nav>

		<div class="spacer"></div>

		<div class="actions no-drag">
			<UserMenu />
			<SafeModeBadge />
			<button class="icon" onclick={() => app.openHistory()} title="Query history ({modLabel}⇧H)"><History size={15} /></button>
			<button class="icon" onclick={() => (helpOpen = true)} title="Keyboard shortcuts ({modLabel}/)"><Keyboard size={15} /></button>
			<button class="icon" onclick={() => (app.prefsOpen = true)} title="Preferences ({modLabel},)"><Settings size={15} /></button>
			<button class="icon" onclick={() => app.disconnect()} title="Disconnect"><LogOut size={14} /></button>
		</div>
	</header>

	<div class="frame">
		{#if sidebarOpen}<Sidebar bind:searchInput={sidebarSearch} />{/if}

		<main>
			<div class="canvas" bind:this={canvas}>
				{#each groups as g (g)}
					{@const r = rectOf(g)}
					{#if app.tabs.length}
						<div
							class="tabs scroll-none"
							class:focused={app.split && app.focusedGroup === g}
							class:edge-l={r.x > 0}
							role="tablist"
							tabindex="-1"
							aria-label="Tabs"
							style:left={pct(r.x)}
							style:top={pct(r.y)}
							style:width={pct(r.w)}
							ondragover={accepts}
							ondrop={(e) => dropOnStrip(e, g)}
							onwheel={stripWheel}
						>
							{#each app.tabs.filter((t) => t.group === g) as t (t.id)}
								<div
									use:revealWhen={t.id === app.activeByGroup[g]}
									class="tab"
									class:on={t.id === app.activeByGroup[g]}
									class:dim={app.split && app.focusedGroup !== g}
									class:drop={dropBefore === t.id}
									role="tab"
									tabindex="-1"
									aria-selected={t.id === app.activeByGroup[g]}
									draggable="true"
									ondragstart={(e) => dragStart(e, t.id)}
									ondragend={dragEnd}
									ondragover={(e) => {
										if (accepts(e)) dropBefore = t.id;
									}}
									ondragleave={() => dropBefore === t.id && (dropBefore = '')}
									ondrop={(e) => dropOnStrip(e, g, t.id)}
									oncontextmenu={(e) => openMenu(e, t.id)}
								>
									<button
										class="tab-title"
										onclick={() => (app.activeTabId = t.id)}
										onauxclick={(e) => e.button === 1 && app.closeTab(t.id)}
										title={t.kind === 'table' && t.filter ? `${t.title} · ${t.filter}` : t.title}
									>
										<span class="icon-box sm"
											>{#if t.kind === 'query' && routineTitles.has(t.title)}<SquareFunction />{:else if t.kind === 'query'}<SquareTerminal
												/>{:else if t.kind === 'history'}<History />{:else if t.table.type === 'view'}<Eye />{:else}<Table2 />{/if}</span
										>
										<span>{t.title}</span>
										{#if t.kind === 'table' && t.filter}<span class="tab-filter">filtered</span>{/if}
									</button>
									<button class="tab-close" onclick={() => app.closeTab(t.id)} aria-label="Close tab"><X size={12} /></button>
								</div>
							{/each}
							<button class="tab-add" onclick={() => ((app.focusedGroup = g), app.openQuery())} title="New SQL query ({tabModLabel}T)"
								><Plus size={13} /> SQL</button
							>
						</div>
					{/if}
				{/each}

				{#each app.tabs as t (t.id)}
					{@const r = rectOf(t.group)}
					<div
						class="pane"
						class:hidden={t.id !== app.activeByGroup[t.group]}
						class:edge-l={r.x > 0}
						class:edge-t={r.y > 0}
						style:left={pct(r.x)}
						style:top="calc({pct(r.y)} + {STRIP})"
						style:width={pct(r.w)}
						style:height="calc({pct(r.h)} - {STRIP})"
						onpointerdowncapture={() => (app.focusedGroup = t.group)}
						role="tabpanel"
					>
						{#if t.kind === 'table'}
							<TableView table={t.table} initialFilter={t.filter ?? ''} initialView={t.view} active={t.id === app.activeTabId} />
						{:else if t.kind === 'history'}
							<HistoryView active={t.id === app.activeTabId} />
						{:else}
							<QueryView tab={t} />
						{/if}
					</div>
				{:else}
					<div class="empty">
						<p class="title">Nothing open</p>
						<p>Pick a table from the sidebar or start a query.</p>
						<div class="empty-actions">
							<button class="pill" onclick={() => app.openQuery()}><SquareTerminal size={14} /> New query <kbd>{tabModLabel}T</kbd></button>
							<button class="pill" onclick={() => (helpOpen = true)}><Keyboard size={14} /> Shortcuts <kbd>{modLabel}/</kbd></button>
						</div>
					</div>
				{/each}

				{#each layoutInfo.dividers as d, i (i)}
					<div
						class="divider {d.dir}"
						role="separator"
						aria-orientation={d.dir === 'row' ? 'vertical' : 'horizontal'}
						aria-label="Resize panes"
						style:left={pct(d.x)}
						style:top={pct(d.y)}
						style:width={d.dir === 'row' ? undefined : pct(d.len)}
						style:height={d.dir === 'row' ? pct(d.len) : undefined}
						onpointerdown={(e) => resizeDivider(e, d)}
					></div>
				{/each}

				{#if dragging}
					{#each groups as g (g)}
						{@const r = rectOf(g)}
						<div
							class="dropzone"
							role="region"
							aria-label="Drop to move or split"
							style:left={pct(r.x)}
							style:top="calc({pct(r.y)} + {STRIP})"
							style:width={pct(r.w)}
							style:height="calc({pct(r.h)} - {STRIP})"
							ondragover={(e) => {
								if (accepts(e)) zoneAt(e, g);
							}}
							ondragleave={() => zone?.group === g && (zone = null)}
							ondrop={(e) => dropOnPane(e, g)}
						>
							{#if zone?.group === g}
								<div class="zone-fill" style={zoneStyle(zone.side)}>
									<span>{zone.side === 'center' ? 'Move here' : groups.length >= MAX_PANES ? 'Move here' : `Split ${zone.side}`}</span>
								</div>
							{/if}
						</div>
					{/each}
				{/if}
			</div>
		</main>

		{#if menu}
			{@const m = menu}
			{@const t = app.tabs.find((x) => x.id === m.id)}
			<div class="menu" style:left="{m.x}px" style:top="{m.y}px" role="menu">
				<button role="menuitem" onclick={() => run(() => app.closeTab(m.id))}>Close <kbd>{tabModLabel}W</kbd></button>
				<button role="menuitem" onclick={() => run(() => app.closeOthers(m.id))}>Close other tabs</button>
				<button role="menuitem" onclick={() => run(() => app.closeToRight(m.id))}>Close tabs to the right</button>
				<div class="sep"></div>
				<button role="menuitem" onclick={() => run(() => app.duplicateTab(m.id))}>Duplicate</button>
				{#if t}
					<div class="sep"></div>
					<button role="menuitem" onclick={() => run(() => app.splitTab(m.id, t.group, 'right'))}>Split right <kbd>{modLabel}\</kbd></button>
					<button role="menuitem" onclick={() => run(() => app.splitTab(m.id, t.group, 'bottom'))}>Split down <kbd>⇧{modLabel}\</kbd></button>
					{#each groups.filter((g) => g !== t.group) as g, i (g)}
						<button role="menuitem" onclick={() => run(() => app.moveTab(m.id, g))}>Move to pane {groups.indexOf(g) + 1}</button>
					{/each}
				{/if}
			</div>
		{/if}
	</div>
</div>

{#if helpOpen}<Shortcuts onclose={() => (helpOpen = false)} />{/if}

<style>
	/* Clips side panels while they slide in and out. */
	.frame {
		overflow: hidden;
	}
	.tab {
		animation: tf-drift var(--dur) var(--ease-out) both;
	}
	.shell {
		display: flex;
		flex-direction: column;
		height: 100%;
	}
	.topbar {
		position: relative;
		/* Its own layer (for the loading fill) above the panes, so menus open over them. */
		isolation: isolate;
		z-index: 100;
		display: flex;
		align-items: center;
		gap: 0.35rem;
		height: var(--chrome-h);
		transition: height var(--dur-slow) var(--ease-out);
		flex: none;
		padding: 0 calc(0.6rem + var(--controls-w, 0px)) 0 0.6rem;
		border-bottom: 1px solid var(--border);
		background: var(--sidebar);
	}
	/* A soft wash of the connection color; text and controls keep their normal colors. */
	.topbar.tinted {
		background: color-mix(in oklab, var(--tint) var(--tint-amount), var(--sidebar));
	}
	.topbar.tinted .icon {
		color: color-mix(in oklab, var(--foreground) 82%, transparent);
	}
	.topbar.tinted .icon:hover,
	.topbar.tinted :global(.trigger:hover:not(:disabled)),
	.topbar.tinted :global(.trigger.open) {
		background: color-mix(in oklab, var(--tint) calc(var(--tint-amount) + 8%), var(--sidebar));
	}
	/* Loading fill: the header's own color sweeps across behind its content. */
	.topbar::before {
		content: '';
		position: absolute;
		inset: 0 auto 0 0;
		width: 0;
		z-index: -1;
		background: color-mix(in oklab, var(--tint, var(--brand)) 30%, transparent);
		opacity: 0;
		pointer-events: none;
	}
	.topbar.loading::before {
		opacity: 1;
		animation: fill 2.6s cubic-bezier(0.1, 0.65, 0.2, 1) forwards;
	}
	.topbar.done::before {
		animation: finish 0.5s ease-out forwards;
	}
	@keyframes fill {
		from {
			width: 0;
		}
		to {
			width: 92%;
		}
	}
	@keyframes finish {
		0% {
			width: 80%;
			opacity: 1;
		}
		45% {
			width: 100%;
			opacity: 1;
		}
		100% {
			width: 100%;
			opacity: 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.topbar.loading::before,
		.topbar.done::before {
			animation: none;
			width: 100%;
			opacity: 0.6;
		}
		.topbar.done::before {
			opacity: 0;
		}
	}
	/* Room for the macOS traffic lights (see --traffic-w in app.css). */
	/* Room for the traffic lights: macOS' own, or FullscreenLights in fullscreen. */
	.traffic :global(.lights) {
		display: none;
	}
	:global(:root[data-fullscreen]) .traffic :global(.lights) {
		display: flex;
	}
	.traffic {
		width: max(0px, calc(var(--traffic-w) - 0.6rem));
		flex: none;
		transition: width var(--dur-slow) var(--ease-out);
	}
	.icon {
		display: grid;
		place-items: center;
		width: 1.85rem;
		height: 1.85rem;
		flex: none;
		padding: 0;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--muted-foreground);
	}
	.icon:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	.crumbs {
		display: flex;
		align-items: center;
		gap: 0.1rem;
		min-width: 0;
		margin-left: 0.15rem;
	}
	.sep {
		color: var(--border);
		font-size: 15px;
		padding: 0 0.1rem;
		user-select: none;
	}
	.spacer {
		flex: 1;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 0.25rem;
	}
	.pill {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		height: 1.85rem;
		padding: 0 0.7rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--foreground);
		font-size: 12.5px;
		font-weight: 500;
	}
	.pill :global(svg) {
		color: var(--muted-foreground);
	}
	.pill:hover {
		background: var(--accent);
	}

	.frame {
		flex: 1;
		min-height: 0;
		display: flex;
	}
	main {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}
	.tabs {
		display: flex;
		align-items: center;
		gap: 0.2rem;
		height: 2.35rem;
		flex: none;
		padding: 0 0.4rem;
		border-bottom: 1px solid var(--border);
		background: var(--background);
		overflow-x: auto;
	}
	/* Tabs are as wide as their full name; the strip scrolls when they don't fit. */
	.tab {
		display: flex;
		align-items: center;
		height: 1.75rem;
		flex: none;
		border: 1px solid transparent;
		border-radius: var(--radius);
		color: var(--muted-foreground);
	}
	.tab:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	.tab.on {
		background: var(--card);
		border-color: var(--border);
		color: var(--foreground);
		box-shadow: 0 1px 2px oklch(0 0 0 / 0.06);
	}
	.tab-title {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		min-width: 0;
		height: 100%;
		padding: 0 0.15rem 0 0.55rem;
		border: none;
		background: transparent;
		color: inherit;
		font-family: var(--font-mono);
		font-size: 12px;
	}
	.tab :global(.icon-box) {
		opacity: 0.6;
	}
	.tab.on :global(.icon-box),
	.tab:hover :global(.icon-box) {
		opacity: 1;
	}
	.tab-title span {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.tab-close {
		display: grid;
		place-items: center;
		width: 1.25rem;
		height: 1.25rem;
		margin-right: 0.2rem;
		flex: none;
		padding: 0;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--muted-foreground);
		opacity: 0;
	}
	.tab:hover .tab-close,
	.tab.on .tab-close {
		opacity: 1;
	}
	.tab-close:hover {
		background: color-mix(in oklch, var(--foreground) 10%, transparent);
		color: var(--foreground);
	}
	.tab-add {
		display: flex;
		align-items: center;
		gap: 0.2rem;
		height: 1.6rem;
		flex: none;
		padding: 0 0.45rem 0 0.35rem;
		font-family: var(--font-mono);
		font-size: 11.5px;
		font-weight: 600;
		white-space: nowrap;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--muted-foreground);
	}
	.tab-add:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	.canvas {
		position: relative;
		flex: 1;
		min-height: 0;
	}
	.canvas > .tabs {
		position: absolute;
		height: 2.35rem;
	}
	.pane {
		position: absolute;
		min-width: 0;
		min-height: 0;
	}
	.pane.hidden {
		display: none;
	}
	.edge-l {
		border-left: 1px solid var(--border);
	}
	.pane.edge-t {
		border-top: 1px solid var(--border);
	}
	.canvas > .empty {
		position: absolute;
		inset: 0;
	}
	.divider {
		position: absolute;
		z-index: 15;
	}
	.divider.row {
		width: 7px;
		margin-left: -3px;
		cursor: col-resize;
	}
	.divider.col {
		height: 7px;
		margin-top: -3px;
		cursor: row-resize;
	}
	.divider:hover {
		background: color-mix(in oklab, var(--brand) 35%, transparent);
	}
	.dropzone {
		position: absolute;
		z-index: 20;
	}
	.zone-fill {
		position: absolute;
		display: grid;
		place-items: center;
		border: 1px dashed color-mix(in oklab, var(--brand) 60%, var(--border));
		border-radius: var(--radius);
		background: color-mix(in oklab, var(--brand) 14%, transparent);
		color: var(--foreground);
		font-size: var(--text-sm);
		pointer-events: none;
		transition: inset 0.08s ease-out;
	}
	.tabs.focused {
		background: color-mix(in oklab, var(--brand) 4%, var(--background));
	}
	.tab.dim.on {
		box-shadow: none;
		color: var(--muted-foreground);
	}
	.tab.drop {
		box-shadow: inset 2px 0 0 var(--brand-ink);
	}
	.tab-filter {
		flex: none;
		padding: 0 0.25rem;
		border-radius: 0.2rem;
		background: color-mix(in oklab, var(--brand) 14%, transparent);
		color: var(--brand-ink);
		font-family: var(--font-sans);
		font-size: 10px;
	}
	.menu {
		position: fixed;
		z-index: 400;
		min-width: 13rem;
		padding: 0.25rem;
		display: flex;
		flex-direction: column;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		box-shadow: var(--shadow);
	}
	.menu button {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		height: 1.9rem;
		padding: 0 0.55rem;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		text-align: left;
	}
	.menu button:hover {
		background: var(--accent);
	}
	.menu .sep {
		height: 1px;
		margin: 0.25rem -0.25rem;
		background: var(--border);
	}
	.empty {
		height: 100%;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 0.35rem;
		color: var(--muted-foreground);
	}
	.empty p {
		margin: 0;
	}
	.empty .title {
		color: var(--foreground);
		font-size: var(--text-lg);
		font-weight: 600;
	}
	.empty-actions {
		display: flex;
		gap: 0.5rem;
		margin-top: 0.9rem;
	}
	.empty-actions kbd {
		margin-left: 0.2rem;
	}
</style>
