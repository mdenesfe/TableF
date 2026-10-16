<script lang="ts">
	// SSMS-style Object Explorer: server > Databases > each database >
	// Tables, Views, Programmability (procedures, functions) and Security
	// (users, roles, schemas). Databases load when expanded; the current one
	// comes from the already loaded catalog.
	import {
		ChevronRight,
		Database,
		Eye,
		Folder,
		FolderOpen,
		KeyRound,
		Layers,
		LoaderCircle,
		Shield,
		SquareFunction,
		Table2,
		User,
		Workflow
	} from '@lucide/svelte';
	import { api } from '#lib/api.ts';
	import { app, routineTitle } from '#lib/state.svelte.ts';
	import { load, save } from '#lib/storage.ts';
	import type { Catalog, Principal, Routine, Table } from '#lib/types.ts';
	import DbLogo from './DbLogo.svelte';

	let {
		query = '',
		onmenu
	}: {
		/** Lower-cased search text: filters objects and opens the folders that match. */
		query?: string;
		/** Right-click on an object of the current database. */
		onmenu?: (e: MouseEvent, target: { table: Table } | { routine: Routine }) => void;
	} = $props();

	const session = $derived(app.session!);
	const current = $derived(session.database);
	const features = $derived(session.features);

	// ---------- expanded folders, remembered per connection ----------
	const openKey = $derived(`tablef.explorer:${app.connection?.id ?? ''}`);
	let expanded = $state<Set<string>>(new Set());
	let loadedFor = '';
	$effect(() => {
		const key = openKey;
		if (key === loadedFor) return;
		loadedFor = key;
		const db = app.session?.database ?? '';
		expanded = new Set(load<string[]>(key, ['srv', 'dbs', `db:${db}`, `db:${db}:tables`]));
	});
	const isOpen = (k: string) => !!query || expanded.has(k);
	function toggle(k: string) {
		const next = new Set(expanded);
		if (next.has(k)) next.delete(k);
		else next.add(k);
		expanded = next;
		save(openKey, [...next]);
	}

	// ---------- catalogs of databases other than the current one ----------
	type Loaded<T> = { status: 'loading' | 'ready' | 'error'; data?: T; error?: string };
	let catalogs = $state<Record<string, Loaded<Catalog>>>({});
	let principals = $state<Record<string, Loaded<Principal[]>>>({});

	// A new session (or database switch) starts from scratch.
	$effect(() => {
		void session.id;
		void current;
		catalogs = {};
		principals = {};
	});

	const currentCatalog = $derived<Catalog>({
		database: current,
		tables: app.tables,
		routines: app.routines,
		schemas: app.schemas,
		security: !!features?.security
	});

	function catalogOf(db: string): Loaded<Catalog> {
		if (db === current) return { status: app.tablesLoading ? 'loading' : 'ready', data: currentCatalog };
		return catalogs[db] ?? { status: 'loading' };
	}

	async function fetchCatalog(db: string) {
		if (db === current || catalogs[db]?.status === 'ready' || catalogs[db]?.status === 'loading') return;
		catalogs = { ...catalogs, [db]: { status: 'loading' } };
		try {
			const data = await api.getCatalog(session.id, db);
			catalogs = { ...catalogs, [db]: { status: 'ready', data } };
		} catch (e) {
			catalogs = { ...catalogs, [db]: { status: 'error', error: (e as Error).message } };
		}
	}

	async function fetchPrincipals(db: string) {
		if (principals[db]?.status === 'ready' || principals[db]?.status === 'loading') return;
		principals = { ...principals, [db]: { status: 'loading' } };
		try {
			const data = await api.listPrincipals(session.id, db === current ? '' : db);
			principals = { ...principals, [db]: { status: 'ready', data } };
		} catch (e) {
			principals = { ...principals, [db]: { status: 'error', error: (e as Error).message } };
		}
	}

	// Load what is open (also folders restored from storage, and on search).
	$effect(() => {
		for (const db of session.databases.length ? session.databases : [current]) {
			if (isOpen(`db:${db}`)) fetchCatalog(db);
			if (isOpen(`db:${db}:sec`) && (isOpen(`db:${db}:users`) || isOpen(`db:${db}:roles`))) fetchPrincipals(db);
		}
	});

	// ---------- display helpers ----------
	const defaultSchemas = new Set(['', 'public', 'dbo', 'main']);
	const qualifiedLabel = (schema: string, name: string, db: string) => (defaultSchemas.has(schema) || schema === db ? name : `${schema}.${name}`);
	const matches = (name: string) => !query || name.toLowerCase().includes(query);

	const PAGE = 300;
	let shown = $state<Record<string, number>>({});
	const limitOf = (k: string) => shown[k] ?? PAGE;
	const more = (k: string) => (shown = { ...shown, [k]: limitOf(k) + PAGE * 4 });

	// What is open in the active tab.
	const activeTable = $derived(app.activeTab?.kind === 'table' ? app.activeTab.table : null);
	const activeTitle = $derived(app.activeTab?.kind === 'query' ? app.activeTab.title : '');

	// Objects of another database: switch to it first, then open.
	async function openTableIn(db: string, t: Table) {
		if (db !== current) {
			await app.useDatabase(db);
			const found = app.tables.find((x) => x.schema === t.schema && x.name === t.name);
			if (found) app.openTable(found);
		} else app.openTable(t);
	}
	async function openRoutineIn(db: string, r: Routine) {
		if (db !== current) await app.useDatabase(db);
		app.openRoutine(r);
	}

	const databases = $derived(session.databases.length ? session.databases : [current]);
	const version = $derived(
		session.version
			.split(/[\s,]+/)
			.slice(0, 2)
			.join(' ')
	);
</script>

{#snippet folder(key: string, depth: number, label: string, count?: number | string, Icon: typeof Folder = Folder, onopen?: () => void)}
	{@const open = isOpen(key)}
	<button
		class="node"
		style:--d={depth}
		aria-expanded={open}
		onclick={() => {
			toggle(key);
			if (!open) onopen?.();
		}}
	>
		<ChevronRight size={12} class={open ? 'chev open' : 'chev'} />
		<span class="icon-box folder"
			>{#if Icon === Folder && open}<FolderOpen />{:else}<Icon />{/if}</span
		>
		<span class="label">{label}</span>
		{#if count !== undefined}<span class="count">{count}</span>{/if}
	</button>
{/snippet}

{#snippet leaf(depth: number, label: string, Icon: typeof Folder, active: boolean, title: string, onclick: () => void, oncontext?: (e: MouseEvent) => void)}
	<button class="node leaf" class:active style:--d={depth} {title} {onclick} oncontextmenu={oncontext}>
		<span class="icon-box"><Icon /></span>
		<span class="label">{label}</span>
	</button>
{/snippet}

{#snippet list<T>(key: string, depth: number, items: T[], render: (item: T) => ReturnType<import('svelte').Snippet>, empty: string)}
	{#each items.slice(0, limitOf(key)) as item, i (i)}
		{@render render(item)}
	{:else}
		<p class="empty" style:--d={depth}>{query ? 'No matches' : empty}</p>
	{/each}
	{#if items.length > limitOf(key)}
		<button class="more" style:--d={depth} onclick={() => more(key)}>Show more ({(items.length - limitOf(key)).toLocaleString()} left)</button>
	{/if}
{/snippet}

<div class="explorer" role="tree" aria-label="Object Explorer">
	<button class="node server" style:--d={0} aria-expanded={isOpen('srv')} onclick={() => toggle('srv')}>
		<ChevronRight size={12} class={isOpen('srv') ? 'chev open' : 'chev'} />
		<DbLogo driver={session.driver} color={app.connection?.color} size={17} />
		<span class="label">{app.connection?.name}</span>
		<span class="count">{version}</span>
	</button>

	{#if isOpen('srv')}
		{@render folder('dbs', 1, 'Databases', databases.length)}
		{#if isOpen('dbs')}
			{#each databases.filter((d) => !query || isOpen(`db:${d}`) || d.toLowerCase().includes(query)) as db (db)}
				{@const k = `db:${db}`}
				{@const cat = catalogOf(db)}
				<button
					class="node"
					class:current={db === current}
					style:--d={2}
					aria-expanded={isOpen(k)}
					onclick={() => {
						const opening = !isOpen(k);
						toggle(k);
						if (opening) fetchCatalog(db);
					}}
					title={db === current ? `${db} (current)` : db}
				>
					<ChevronRight size={12} class={isOpen(k) ? 'chev open' : 'chev'} />
					<span class="icon-box"><Database /></span>
					<span class="label">{db}</span>
					{#if cat.status === 'loading' && isOpen(k)}<LoaderCircle size={12} class="spin" />{/if}
				</button>

				{#if isOpen(k)}
					{#if cat.status === 'error'}
						<p class="empty err" style:--d={3} title={cat.error}>{cat.error}</p>
					{:else if cat.data}
						{@const c = cat.data}
						{@const tables = c.tables.filter((t) => t.type === 'table' && matches(t.name))}
						{@const views = c.tables.filter((t) => t.type === 'view' && matches(t.name))}
						{@const procs = c.routines.filter((r) => r.kind === 'procedure' && matches(r.name))}
						{@const funcs = c.routines.filter((r) => r.kind !== 'procedure' && matches(r.name))}

						{@render folder(`${k}:tables`, 3, 'Tables', tables.length)}
						{#if isOpen(`${k}:tables`)}
							{#snippet tableRow(t: Table)}
								{@render leaf(
									4,
									qualifiedLabel(t.schema, t.name, db),
									Table2,
									db === current && activeTable?.schema === t.schema && activeTable?.name === t.name,
									`${t.schema ? `${t.schema}.` : ''}${t.name}`,
									() => openTableIn(db, t),
									db === current && onmenu ? (e) => onmenu(e, { table: t }) : undefined
								)}
							{/snippet}
							{@render list(`${k}:tables`, 4, tables, tableRow, 'No tables')}
						{/if}

						{@render folder(`${k}:views`, 3, 'Views', views.length)}
						{#if isOpen(`${k}:views`)}
							{#snippet viewRow(t: Table)}
								{@render leaf(
									4,
									qualifiedLabel(t.schema, t.name, db),
									Eye,
									db === current && activeTable?.schema === t.schema && activeTable?.name === t.name,
									`${t.schema ? `${t.schema}.` : ''}${t.name}`,
									() => openTableIn(db, t),
									db === current && onmenu ? (e) => onmenu(e, { table: t }) : undefined
								)}
							{/snippet}
							{@render list(`${k}:views`, 4, views, viewRow, 'No views')}
						{/if}

						{#if features?.routines}
							{@render folder(`${k}:prog`, 3, 'Programmability', procs.length + funcs.length)}
							{#if isOpen(`${k}:prog`)}
								{#snippet routineRow(r: Routine)}
									{@render leaf(
										5,
										qualifiedLabel(r.schema, r.name, db),
										r.kind === 'procedure' ? Workflow : SquareFunction,
										db === current && activeTitle === routineTitle(r),
										`${r.name}(${r.arguments})${r.returnType ? ` → ${r.returnType}` : ''}`,
										() => openRoutineIn(db, r),
										db === current && onmenu ? (e) => onmenu(e, { routine: r }) : undefined
									)}
								{/snippet}
								{@render folder(`${k}:procs`, 4, 'Stored Procedures', procs.length)}
								{#if isOpen(`${k}:procs`)}{@render list(`${k}:procs`, 5, procs, routineRow, 'No procedures')}{/if}
								{@render folder(`${k}:funcs`, 4, 'Functions', funcs.length)}
								{#if isOpen(`${k}:funcs`)}{@render list(`${k}:funcs`, 5, funcs, routineRow, 'No functions')}{/if}
							{/if}
						{/if}

						{#if c.security || c.schemas.length}
							{@render folder(`${k}:sec`, 3, 'Security', undefined, Shield)}
							{#if isOpen(`${k}:sec`)}
								{#if c.security}
									{@const p = principals[db]}
									{@const users = (p?.data ?? []).filter((x) => x.kind === 'user' && matches(x.name))}
									{@const roles = (p?.data ?? []).filter((x) => x.kind === 'role' && matches(x.name))}
									{#snippet principalRow(x: Principal)}
										{@render leaf(5, x.name, x.kind === 'role' ? KeyRound : User, false, x.name, () => {})}
									{/snippet}
									{@render folder(`${k}:users`, 4, 'Users', p?.status === 'ready' ? users.length : undefined, Folder, () => fetchPrincipals(db))}
									{#if isOpen(`${k}:users`)}
										{#if p?.status === 'error'}<p class="empty err" style:--d={5} title={p.error}>{p.error}</p>
										{:else if p?.status !== 'ready'}<p class="empty" style:--d={5}>Loading…</p>
										{:else}{@render list(`${k}:users`, 5, users, principalRow, 'No users')}{/if}
									{/if}
									{@render folder(`${k}:roles`, 4, 'Roles', p?.status === 'ready' ? roles.length : undefined, Folder, () => fetchPrincipals(db))}
									{#if isOpen(`${k}:roles`)}
										{#if p?.status === 'error'}<p class="empty err" style:--d={5} title={p.error}>{p.error}</p>
										{:else if p?.status !== 'ready'}<p class="empty" style:--d={5}>Loading…</p>
										{:else}{@render list(`${k}:roles`, 5, roles, principalRow, 'No roles')}{/if}
									{/if}
								{/if}
								{#if c.schemas.length}
									{@const schemas = c.schemas.filter(matches)}
									{#snippet schemaRow(s: string)}
										{@render leaf(5, s, Layers, db === current && app.schema === s, s, () => (db === current ? (app.schema = s) : undefined))}
									{/snippet}
									{@render folder(`${k}:schemas`, 4, 'Schemas', schemas.length)}
									{#if isOpen(`${k}:schemas`)}{@render list(`${k}:schemas`, 5, schemas, schemaRow, 'No schemas')}{/if}
								{/if}
							{/if}
						{/if}
					{/if}
				{/if}
			{/each}
		{/if}
	{/if}
</div>

<style>
	.explorer {
		display: flex;
		flex-direction: column;
	}
	.node {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		width: 100%;
		height: 1.65rem;
		padding: 0 0.45rem 0 calc(0.3rem + var(--d) * 0.85rem);
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		text-align: left;
		white-space: nowrap;
	}
	.node:hover {
		background: color-mix(in oklch, var(--sidebar-accent) 70%, transparent);
	}
	.node.active {
		background: var(--sidebar-accent);
		font-weight: 500;
	}
	.node.leaf {
		/* Leaves have no chevron: line their icon up with folders' icons. */
		padding-left: calc(0.3rem + var(--d) * 0.85rem + 12px + 0.4rem);
	}
	.node.server {
		height: 1.9rem;
		font-weight: 600;
	}
	.node.current .label {
		font-weight: 600;
	}
	.node :global(.chev) {
		flex: none;
		color: var(--muted-foreground);
		transition: transform var(--dur-fast) ease-out;
	}
	.node :global(.chev.open) {
		transform: rotate(90deg);
	}
	.icon-box.folder {
		background: color-mix(in oklab, var(--brand) 70%, var(--sidebar));
	}
	.label {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.count {
		margin-left: auto;
		padding-left: 0.5rem;
		font-size: 11px;
		font-variant-numeric: tabular-nums;
		color: var(--muted-foreground);
	}
	.empty,
	.more {
		margin: 0;
		padding: 0.15rem 0.45rem 0.15rem calc(0.3rem + var(--d) * 0.85rem + 12px + 0.4rem);
		font-size: 12px;
		color: var(--muted-foreground);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.empty.err {
		color: var(--destructive);
	}
	.more {
		width: 100%;
		height: 1.5rem;
		border: none;
		background: transparent;
		text-align: left;
	}
	.more:hover {
		color: var(--foreground);
	}
	.node :global(.spin) {
		margin-left: auto;
		color: var(--muted-foreground);
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
