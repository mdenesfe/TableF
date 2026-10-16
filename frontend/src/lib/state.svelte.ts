import { prefs } from '#lib/prefs.svelte.ts';
import { familyOf } from '#lib/engines.ts';
import { load, remove, save } from '#lib/storage.ts';
import { dialogs } from '#lib/dialog.svelte.ts';
import { collator } from '#lib/sort.ts';
import { api } from './api';
import { groupsOf, leaf, removeLeaf, splitLeaf, type LayoutNode, type Side } from './layout';
import type { Cell, ConnectionConfig, Routine, SavedQuery, SessionInfo, Table } from './types';

/** The views of a table tab. */
export type TableViewKind = 'data' | 'structure' | 'indexes';

/** A SQL literal for a cell value. */
export function sqlLiteral(v: Cell): string {
	if (v === null) return 'NULL';
	if (typeof v === 'number' || typeof v === 'boolean') return String(v);
	return `'${String(v).replaceAll("'", "''")}'`;
}

/** group is the pane (see layout) the tab lives in. */
export type Tab =
	| { id: string; kind: 'table'; title: string; group: number; table: Table; filter?: string; view?: TableViewKind }
	| {
			id: string;
			kind: 'query';
			title: string;
			group: number;
			sql: string;
			/** Run the SQL once when the tab opens. */
			autorun?: boolean;
			/** The saved query this tab edits; saving updates it in place. */
			savedId?: string;
	  };

/** How many panes a layout may hold. */
export const MAX_PANES = 8;

/** What reopens after a page reload: the connection, where you were, and your tabs. */
interface SavedSession {
	connectionId: string;
	database: string;
	schema: string;
	tabs: ({ kind: 'table'; table: Table; filter?: string; group?: number } | { kind: 'query'; title: string; sql: string; group?: number; savedId?: string })[];
	active: number;
	layout?: LayoutNode;
}
const SESSION_KEY = 'tablef.session';

const readSaved = () => load<SavedSession | null>(SESSION_KEY, null);

/** Tab title of a routine's definition, e.g. "billing.charge.sql" ("public" is implied). */
export const routineTitle = (r: Pick<Routine, 'schema' | 'name'>) => `${r.schema && r.schema !== 'public' ? `${r.schema}.` : ''}${r.name}.sql`;

let seq = 0;
const nextId = () => `t${++seq}`;

class AppState {
	connections = $state<ConnectionConfig[]>([]);
	connection = $state<ConnectionConfig | null>(null);
	session = $state<SessionInfo | null>(null);
	// Catalog lists are replaced wholesale on load; raw state avoids a reactive
	// proxy per table (schemas can hold tens of thousands).
	tables = $state.raw<Table[]>([]);
	routines = $state.raw<Routine[]>([]);
	schemas = $state.raw<string[]>([]);
	/** Table names for SQL completion, built once and shared by every editor. */
	completionSchema = $derived(
		Object.fromEntries(this.tables.map((t) => [t.schema && t.schema !== 'public' && t.schema !== 'main' ? `${t.schema}.${t.name}` : t.name, []]))
	);
	/** Schema shown in the sidebar. */
	schema = $state('');
	tablesLoading = $state(false);
	tabs = $state<Tab[]>([]);
	/** Pane arrangement, the active tab of each pane, and which pane has focus. */
	layout = $state<LayoutNode>(leaf(0));
	activeByGroup = $state<Record<number, string>>({ 0: '' });
	focusedGroup = $state(0);
	private nextGroup = 1;

	get activeTabId() {
		return this.activeByGroup[this.focusedGroup];
	}
	set activeTabId(id: string) {
		const t = this.tabs.find((x) => x.id === id);
		const g = t?.group ?? this.focusedGroup;
		this.activeByGroup[g] = id;
		this.focusedGroup = g;
	}

	get split() {
		return this.layout.kind === 'split';
	}

	private resetLayout() {
		this.layout = leaf(0);
		this.activeByGroup = { 0: '' };
		this.focusedGroup = 0;
		this.nextGroup = 1;
	}
	toast = $state<{ kind: 'error' | 'info'; text: string } | null>(null);
	busy = $state(false);
	prefsOpen = $state(false);
	/** Requests in flight; the header shows progress while this is above zero. */
	pending = $state(0);

	/** Runs a request while the header shows its loading fill. */
	async track<T>(work: Promise<T>): Promise<T> {
		this.pending++;
		try {
			return await work;
		} finally {
			this.pending--;
		}
	}

	get activeTab() {
		return this.tabs.find((t) => t.id === this.activeTabId);
	}

	notify(kind: 'error' | 'info', text: string) {
		this.toast = { kind, text };
		const current = this.toast;
		setTimeout(
			() => {
				if (this.toast === current) this.toast = null;
			},
			kind === 'error' ? 8000 : 3000
		);
	}

	fail(e: unknown) {
		this.notify('error', e instanceof Error ? e.message : String(e));
	}

	/** Engines the backend was built with; null until known (then everything is offered). */
	engineIds = $state.raw<Set<string> | null>(null);

	async loadConnections() {
		try {
			const [list, ids, queries] = await Promise.all([api.listConnections(), this.engineIds ? null : api.engines(), api.listSavedQueries()]);
			this.connections = list;
			this.savedQueries = queries;
			if (ids) this.engineIds = new Set(ids);
		} catch (e) {
			this.fail(e);
		}
	}

	/**
	 * Runs fn; if it fails because the SSH server's host key isn't known yet,
	 * shows the fingerprint, and on approval trusts the key and runs fn again.
	 */
	async withHostTrust<T>(c: ConnectionConfig, fn: () => Promise<T>): Promise<T> {
		try {
			return await fn();
		} catch (e) {
			const m = /^unknown SSH host key for (\S+) \((.+)\)/.exec((e as Error).message ?? '');
			if (!m) throw e;
			const ok = await dialogs.confirm({
				title: 'Trust this SSH server?',
				message: `TableF hasn't connected to ${m[1]} before. Check the fingerprint with the server's administrator.`,
				detail: m[2],
				confirmLabel: 'Trust and connect'
			});
			if (!ok) throw new Error('SSH host key not trusted');
			await api.trustHostKey(c);
			return fn();
		}
	}

	/** The connection being opened, for the full-page connecting animation. */
	connecting = $state<ConnectionConfig | null>(null);

	async connect(c: ConnectionConfig, openQuery = true) {
		this.busy = true;
		this.connecting = c;
		const started = performance.now();
		try {
			this.session = await this.track(this.withHostTrust(c, () => api.connect(c.id)));
			this.connection = c;
			this.tabs = [];
			this.resetLayout();
			await this.loadTables();
			if (openQuery) this.openQuery();
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
			// Keep the animation up long enough to read, so fast connects don't flicker.
			const wait = Math.max(0, 650 - (performance.now() - started));
			setTimeout(() => this.connecting === c && (this.connecting = null), wait);
		}
	}

	private persistTimer: ReturnType<typeof setTimeout> | undefined;

	/**
	 * Saves the session shortly after the last change. Typing in an editor
	 * changes it on every keystroke, and serialising every tab each time would
	 * cost O(all SQL) per key.
	 */
	schedulePersist() {
		clearTimeout(this.persistTimer);
		this.persistTimer = setTimeout(() => this.persist(), 400);
	}

	/** Saves a pending session right away (e.g. when the page is being hidden). */
	flushPersist() {
		if (this.persistTimer === undefined) return;
		clearTimeout(this.persistTimer);
		this.persistTimer = undefined;
		this.persist();
	}

	/** Remembers the open connection and tabs so a reload lands back here. */
	persist() {
		this.persistTimer = undefined;
		if (!this.session || !this.connection) return;
		const saved: SavedSession = {
			connectionId: this.connection.id,
			database: this.session.database,
			schema: this.schema,
			tabs: this.tabs.map((t) =>
				t.kind === 'table'
					? { kind: 'table', table: t.table, filter: t.filter, group: t.group }
					: { kind: 'query', title: t.title, sql: t.sql, group: t.group, savedId: t.savedId }
			),
			active: Math.max(
				0,
				this.tabs.findIndex((t) => t.id === this.activeTabId)
			),
			layout: this.layout
		};
		save(SESSION_KEY, saved);
	}

	/** True while a saved session is being reopened after a reload. */
	restoring = $state(readSaved() !== null);

	/** Reopens the session saved before the reload, if its connection still exists. */
	async restore() {
		if (!prefs.value.restoreSession) return;
		const saved = readSaved();
		const c = saved && this.connections.find((x) => x.id === saved.connectionId);
		try {
			if (!saved || !c) return;
			await this.connect(c, false);
			if (!this.session) return;
			if (saved.database && saved.database !== this.session.database && this.session.databases.includes(saved.database)) {
				await this.useDatabase(saved.database);
			}
			if (this.schemas.includes(saved.schema)) this.schema = saved.schema;
			if (saved.layout) {
				this.layout = saved.layout;
				const groups = groupsOf(saved.layout);
				this.nextGroup = Math.max(0, ...groups) + 1;
				this.activeByGroup = Object.fromEntries(groups.map((g) => [g, '']));
			}
			for (const t of saved.tabs) {
				this.focusedGroup = groupsOf(this.layout).includes(t.group ?? 0) ? (t.group ?? 0) : groupsOf(this.layout)[0];
				if (t.kind === 'query') this.openQuery(t.sql, t.title, t.savedId);
				else {
					const table = this.tables.find((x) => x.schema === t.table.schema && x.name === t.table.name);
					if (table) this.openTable(table, t.filter ?? '');
				}
			}
			this.pruneGroups();
			const active = this.tabs[Math.min(saved.active, this.tabs.length - 1)];
			if (active) this.activeTabId = active.id;
			else this.openQuery();
		} finally {
			this.restoring = false;
		}
	}

	async disconnect() {
		remove(SESSION_KEY);
		if (this.session) await api.disconnect(this.session.id).catch(() => {});
		this.session = null;
		this.connection = null;
		this.tables = [];
		this.routines = [];
		this.schemas = [];
		this.schema = '';
		this.tabs = [];
		this.resetLayout();
	}

	async useDatabase(name: string) {
		if (!this.session) return;
		this.busy = true;
		try {
			this.session = await this.track(api.useDatabase(this.session.id, name));
			this.tabs = this.tabs.filter((t) => t.kind === 'query');
			for (const g of groupsOf(this.layout)) {
				if (!this.tabs.some((t) => t.id === this.activeByGroup[g])) this.activeByGroup[g] = this.tabs.find((t) => t.group === g)?.id ?? '';
			}
			this.pruneGroups();
			await this.loadTables();
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	async loadTables() {
		if (!this.session) return;
		this.tablesLoading = true;
		try {
			const [tables, routines, schemas] = await this.track(
				Promise.all([
					api.listTables(this.session.id),
					api.listRoutines(this.session.id).catch(() => [] as Routine[]),
					api.listSchemas(this.session.id).catch(() => [] as string[])
				])
			);
			this.tables = tables;
			this.routines = routines;
			this.schemas = schemas.length ? schemas : [...new Set(tables.map((t) => t.schema))];
			if (!this.schemas.includes(this.schema)) {
				this.schema = this.schemas.includes('public') ? 'public' : (this.schemas[0] ?? '');
			}
		} catch (e) {
			this.fail(e);
		} finally {
			this.tablesLoading = false;
		}
	}

	/** Opens a table tab; with a filter (e.g. from a relation) it gets its own tab. */
	/** Opens a table, reusing its tab unless `newTab`; `view` picks data or structure. */
	openTable(table: Table, filter = '', opts: { newTab?: boolean; view?: TableViewKind } = {}) {
		const existing =
			!opts.newTab &&
			!opts.view &&
			this.tabs.find((t) => t.kind === 'table' && t.table.schema === table.schema && t.table.name === table.name && (t.filter ?? '') === filter);
		if (existing) {
			this.activeTabId = existing.id;
			return;
		}
		const title = table.schema && table.schema !== 'main' && table.schema !== 'public' ? `${table.schema}.${table.name}` : table.name;
		const tab: Tab = { id: nextId(), kind: 'table', title, group: this.focusedGroup, table, filter: filter || undefined, view: opts.view };
		this.tabs.push(tab);
		this.activeTabId = tab.id;
	}

	/** Opens a query tab and runs its SQL right away. */
	runQuery(sql: string, title = '') {
		const n = this.tabs.filter((t) => t.kind === 'query').length + 1;
		const tab: Tab = { id: nextId(), kind: 'query', title: title || `query_${n}.sql`, group: this.focusedGroup, sql, autorun: true };
		this.tabs.push(tab);
		this.activeTabId = tab.id;
	}

	openQuery(sql = '', title = '', savedId?: string) {
		const existing = title && this.tabs.find((t) => t.kind === 'query' && t.title === title && t.savedId === savedId);
		if (existing) {
			this.activeTabId = existing.id;
			return;
		}
		const n = this.tabs.filter((t) => t.kind === 'query').length + 1;
		const tab: Tab = { id: nextId(), kind: 'query', title: title || `query_${n}.sql`, group: this.focusedGroup, sql, savedId };
		this.tabs.push(tab);
		this.activeTabId = tab.id;
	}

	// ---------- saved queries ----------
	savedQueries = $state.raw<SavedQuery[]>([]);

	/** Saved queries of the current connection plus the shared ones, by name. */
	get visibleQueries(): SavedQuery[] {
		const id = this.connection?.id ?? '';
		return this.savedQueries.filter((q) => !q.connectionId || q.connectionId === id).sort((a, b) => collator.compare(a.name, b.name));
	}

	async loadSavedQueries() {
		try {
			this.savedQueries = await api.listSavedQueries();
		} catch (e) {
			this.fail(e);
		}
	}

	/** Opens a saved query, or focuses the tab that already edits it. */
	openSaved(q: SavedQuery) {
		const existing = this.tabs.find((t) => t.kind === 'query' && t.savedId === q.id);
		if (existing) this.activeTabId = existing.id;
		else this.openQuery(q.sql, q.name, q.id);
	}

	/**
	 * Saves a query tab. A tab that edits a saved query updates it; otherwise
	 * (or with asNew) the user names a new one, which the tab then edits.
	 */
	async saveQueryTab(tab: Extract<Tab, { kind: 'query' }>, asNew = false) {
		const current = !asNew && tab.savedId ? this.savedQueries.find((q) => q.id === tab.savedId) : undefined;
		let name = current?.name;
		if (!name) {
			const picked = await dialogs.prompt({
				title: 'Save query',
				message: 'Saved queries are listed in the sidebar for this connection.',
				placeholder: 'Query name',
				value: tab.title.replace(/\.sql$/, ''),
				confirmLabel: 'Save'
			});
			name = picked?.trim();
			if (!name) return;
		}
		try {
			const saved = await api.saveQuery({
				id: current?.id ?? '',
				name,
				sql: tab.sql,
				connectionId: current ? current.connectionId : (this.connection?.id ?? ''),
				updated: ''
			});
			this.savedQueries = [...this.savedQueries.filter((q) => q.id !== saved.id), saved];
			tab.savedId = saved.id;
			tab.title = saved.name;
			this.schedulePersist();
			this.notify('info', `Saved “${saved.name}”`);
		} catch (e) {
			this.fail(e);
		}
	}

	async renameSaved(q: SavedQuery) {
		const picked = await dialogs.prompt({ title: 'Rename query', value: q.name, placeholder: 'Query name', confirmLabel: 'Rename' });
		const name = picked?.trim();
		if (!name || name === q.name) return;
		try {
			const saved = await api.saveQuery({ ...q, name });
			this.savedQueries = this.savedQueries.map((x) => (x.id === saved.id ? saved : x));
			for (const t of this.tabs) if (t.kind === 'query' && t.savedId === saved.id) t.title = saved.name;
			this.schedulePersist();
		} catch (e) {
			this.fail(e);
		}
	}

	async deleteSaved(q: SavedQuery) {
		const ok = await dialogs.confirm({
			title: 'Delete saved query',
			message: `Delete “${q.name}”? Open tabs keep their SQL.`,
			confirmLabel: 'Delete',
			danger: true
		});
		if (!ok) return;
		try {
			await api.deleteSavedQuery(q.id);
			this.savedQueries = this.savedQueries.filter((x) => x.id !== q.id);
			for (const t of this.tabs) if (t.kind === 'query' && t.savedId === q.id) t.savedId = undefined;
			this.schedulePersist();
		} catch (e) {
			this.fail(e);
		}
	}

	async openRoutine(r: Routine) {
		if (!this.session) return;
		try {
			const sql = await this.track(api.getRoutineDefinition(this.session.id, r));
			this.openQuery(sql.trim() + '\n', routineTitle(r));
		} catch (e) {
			this.fail(e);
		}
	}

	/** Disconnects and opens another saved connection. */
	async switchConnection(id: string) {
		const c = this.connections.find((x) => x.id === id);
		if (!c || c.id === this.connection?.id) return;
		await this.disconnect();
		await this.connect(c);
	}

	async createSchema() {
		const name = await dialogs.prompt({ title: 'New schema', placeholder: 'schema_name', confirmLabel: 'Create' });
		if (!name || !this.session) return;
		try {
			await api.createSchema(this.session.id, name);
			await this.loadTables();
			this.schema = name;
		} catch (e) {
			this.fail(e);
		}
	}

	/** Opens a CREATE TABLE template for the current schema in a query tab. */
	newTable() {
		const fam = this.session?.features?.family ?? familyOf(this.session?.driver ?? 'postgres');
		const q = (s: string) => this.quoteIdent(s);
		const schema = this.session?.features?.schemas ? `${q(this.schema)}.` : '';
		const ids: Record<string, string> = {
			mysql: 'id bigint unsigned auto_increment primary key',
			sqlite: 'id integer primary key autoincrement',
			mssql: 'id bigint identity(1,1) primary key',
			oracle: 'id number generated always as identity primary key',
			clickhouse: 'id UInt64',
			bigquery: 'id INT64',
			cassandra: 'id uuid primary key'
		};
		const id = ids[fam] ?? 'id bigint generated always as identity primary key';
		const created =
			fam === 'postgres'
				? 'timestamptz not null default now()'
				: fam === 'mssql'
					? 'datetime2 default sysdatetime()'
					: fam === 'bigquery'
						? 'TIMESTAMP'
						: 'timestamp default current_timestamp';
		const tail = fam === 'clickhouse' ? ' ENGINE = MergeTree ORDER BY id' : '';
		this.openQuery(`create table ${schema}${q('new_table')} (\n  ${id},\n  created_at ${created}\n)${tail};\n`, 'new_table.sql');
	}

	/** Opens the table a foreign key points to, filtered to the referenced row(s). */
	openRelated(schema: string, table: string, column: string, value: Cell) {
		const t = this.tables.find((x) => x.schema === schema && x.name === table) ?? { schema, name: table, type: 'table' as const };
		this.openTable(t, `${this.quoteIdent(column)} = ${sqlLiteral(value)}`);
	}

	quoteIdent(name: string) {
		const fam = this.session?.features?.family ?? familyOf(this.session?.driver ?? 'postgres');
		if (fam === 'mssql') return `[${name.replaceAll(']', ']]')}]`;
		const q = ['mysql', 'clickhouse', 'bigquery'].includes(fam) ? '`' : '"';
		return q + name.replaceAll(q, q + q) + q;
	}

	closeTab(id: string) {
		const i = this.tabs.findIndex((t) => t.id === id);
		if (i < 0) return;
		const g = this.tabs[i].group;
		const siblings = this.tabs.filter((t) => t.group === g);
		const pos = siblings.findIndex((t) => t.id === id);
		this.tabs.splice(i, 1);
		if (this.activeByGroup[g] === id) {
			const rest = siblings.filter((t) => t.id !== id);
			this.activeByGroup[g] = rest[Math.min(pos, rest.length - 1)]?.id ?? '';
		}
		this.pruneGroups();
	}

	/** Drops panes that have no tabs left (always keeping one). */
	private pruneGroups() {
		for (const g of groupsOf(this.layout)) {
			if (groupsOf(this.layout).length > 1 && !this.tabs.some((t) => t.group === g)) {
				this.layout = removeLeaf(this.layout, g);
				delete this.activeByGroup[g];
			}
		}
		const groups = groupsOf(this.layout);
		if (groups.length === 1) {
			// Everything lives in the remaining pane.
			for (const t of this.tabs) t.group = groups[0];
		}
		if (!groups.includes(this.focusedGroup)) this.focusedGroup = groups[0];
		// A pane's active tab must still live in that pane (it may have been moved away).
		for (const g of groups) {
			if (!this.tabs.some((t) => t.id === this.activeByGroup[g] && t.group === g)) this.activeByGroup[g] = this.tabs.find((t) => t.group === g)?.id ?? '';
		}
	}

	/** Moves a tab into a pane, before `beforeId` or at the end. */
	moveTab(id: string, group: number, beforeId?: string) {
		const i = this.tabs.findIndex((t) => t.id === id);
		if (i < 0 || id === beforeId) return;
		const from = this.tabs[i].group;
		// Position inside its pane, to pick the neighbour that takes its place.
		const pos = this.tabs.filter((t) => t.group === from).findIndex((t) => t.id === id);
		const [tab] = this.tabs.splice(i, 1);
		tab.group = group;
		const at = beforeId ? this.tabs.findIndex((t) => t.id === beforeId) : -1;
		if (at >= 0) this.tabs.splice(at, 0, tab);
		else this.tabs.push(tab);
		this.activeByGroup[group] = id;
		if (from !== group && this.activeByGroup[from] === id) {
			// The pane it left shows the neighbouring tab instead.
			const rest = this.tabs.filter((t) => t.group === from);
			this.activeByGroup[from] = rest[Math.min(pos, rest.length - 1)]?.id ?? '';
		}
		this.focusedGroup = group;
		this.pruneGroups();
	}

	/** Splits `target` pane on `side` and moves the tab into the new pane. */
	splitTab(id: string, target: number, side: Side) {
		const tab = this.tabs.find((t) => t.id === id);
		if (!tab) return;
		// Splitting a pane with its only tab would leave the original empty: nothing to do.
		if (tab.group === target && !this.tabs.some((t) => t.group === target && t.id !== id)) return;
		if (groupsOf(this.layout).length >= MAX_PANES) return this.moveTab(id, target);
		const g = this.nextGroup++;
		this.layout = splitLeaf(this.layout, target, g, side);
		this.activeByGroup[g] = '';
		this.moveTab(id, g);
	}

	duplicateTab(id: string) {
		const t = this.tabs.find((x) => x.id === id);
		if (!t) return;
		const copy: Tab = t.kind === 'query' ? { ...t, id: nextId(), title: t.title.replace(/(\.sql)?$/, ' copy$1'), sql: t.sql } : { ...t, id: nextId() };
		this.tabs.splice(this.tabs.indexOf(t) + 1, 0, copy);
		this.activeTabId = copy.id;
	}

	closeOthers(id: string) {
		const t = this.tabs.find((x) => x.id === id);
		if (!t) return;
		this.tabs = this.tabs.filter((x) => x.id === id || x.group !== t.group);
		this.activeByGroup[t.group] = id;
		this.pruneGroups();
	}

	closeToRight(id: string) {
		const t = this.tabs.find((x) => x.id === id);
		if (!t) return;
		const group = this.tabs.filter((x) => x.group === t.group);
		const drop = new Set(group.slice(group.indexOf(t) + 1).map((x) => x.id));
		this.tabs = this.tabs.filter((x) => !drop.has(x.id));
		if (drop.has(this.activeByGroup[t.group])) this.activeByGroup[t.group] = id;
	}
}

export const app = new AppState();
