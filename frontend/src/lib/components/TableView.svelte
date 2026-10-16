<script lang="ts">
	import { Columns3, Filter, Info, Play, Plus, X } from '@lucide/svelte';
	import Pagination from './Pagination.svelte';
	import { api, isMac, modLabel, targetIn } from '#lib/api.ts';
	import { splitFilter } from '#lib/sql.ts';
	import { dialogs } from '#lib/dialog.svelte.ts';
	import { compareCells, nextSort, type Sort } from '#lib/sort.ts';
	import { kindOf, type CellKind } from '#lib/celltype.ts';
	import { isSQL } from '#lib/engines.ts';
	import { load as loadStored, save as saveStored } from '#lib/storage.ts';
	import { prefs } from '#lib/prefs.svelte.ts';
	import { app, type TableViewKind } from '#lib/state.svelte.ts';
	import type { Cell, ChangeSet, Column, ColumnMatch, ForeignKey, Index, Table } from '#lib/types.ts';
	import { bytes } from '#lib/format.ts';
	import { untrack } from 'svelte';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import DataGrid, { COLUMN_MIME, emptyLayout, type GridColumn, type GridItem, type GridLayout, type PastedCell } from './DataGrid.svelte';
	import FieldsPanel from './FieldsPanel.svelte';
	import RecordPane from './RecordPane.svelte';
	import TableDetails from './TableDetails.svelte';
	import SqlLine from './SqlLine.svelte';
	import RelationPeek from './RelationPeek.svelte';

	let {
		table,
		active,
		initialFilter = '',
		initialView = 'data'
	}: { table: Table; active: boolean; initialFilter?: string; initialView?: TableViewKind } = $props();

	const GROUP_PAGE = 100;

	// svelte-ignore state_referenced_locally
	let view = $state<TableViewKind>(initialView);

	let columns = $state<Column[]>([]);
	let foreignKeys = $state<ForeignKey[]>([]);
	let referencedBy = $state<ForeignKey[]>([]);
	let peek = $state<{ fk: ForeignKey; value: Cell; x: number; y: number } | null>(null);
	// column name → "table.column" it references
	const relations = $derived(Object.fromEntries(foreignKeys.map((f) => [f.column, `${f.refTable}.${f.refColumn}`])));
	// Rows are replaced wholesale on every load, so they skip deep reactivity.
	let base = $state.raw<Cell[][]>([]);
	let total = $state(0);
	let elapsed = $state(0);
	let loading = $state(false);
	let error = $state('');

	let limit = $state(prefs.value.pageSize);
	let page = $state(0);
	// The filter line is free SQL: a condition, optionally with ORDER BY.
	// svelte-ignore state_referenced_locally
	let filter = $state(initialFilter);
	// svelte-ignore state_referenced_locally
	let filterInput = $state(initialFilter);
	const parts = $derived(splitFilter(filter));
	const where = $derived(parts.where);
	let sort = $state<Sort>({ column: '', dir: '' });

	// ---------- layout (fields, pins, widths, grouping), remembered per table ----------
	const storeKey = $derived(`tablef.grid:${app.connection?.id}:${table.schema}.${table.name}`);
	let layout = $state<GridLayout>(emptyLayout());
	let groupBy = $state<string[]>([]);
	let fieldsOpen = $state(false);
	let restoredFor = '';
	$effect(() => {
		const key = storeKey;
		if (key === restoredFor) return;
		restoredFor = key;
		const saved = loadStored<{ layout?: Partial<GridLayout>; groupBy?: string[]; fieldsOpen?: boolean } | null>(key, null);
		if (saved) {
			layout = { ...emptyLayout(), ...saved.layout };
			groupBy = saved.groupBy ?? [];
			fieldsOpen = !!saved.fieldsOpen;
		}
	});
	$effect(() => {
		// Reading the snapshot tracks every field; the write itself is debounced.
		const data = $state.snapshot({ layout, groupBy, fieldsOpen });
		const key = untrack(() => storeKey);
		const id = setTimeout(() => saveStored(key, data), 300);
		return () => clearTimeout(id);
	});

	// ---------- indexes (loaded when the Indexes view is first shown) ----------
	let indexes = $state.raw<Index[] | null>(null);
	let indexError = $state('');
	let indexSeq = 0;
	async function loadIndexes() {
		if (!app.session) return;
		const n = ++indexSeq;
		indexError = '';
		try {
			const list = await api.getIndexes(app.session.id, table.schema, table.name);
			if (n === indexSeq) indexes = list;
		} catch (e) {
			if (n === indexSeq) ((indexes = []), (indexError = (e as Error).message));
		}
	}
	$effect(() => {
		if (view === 'indexes' && indexes === null) untrack(loadIndexes);
	});

	// ---------- structure / index row details ----------
	let selCol = $state<number | null>(null);
	let selIx = $state<number | null>(null);
	const fact = (name: string) => ({ name });
	const yesNo = (b: boolean) => (b ? 'YES' : 'NO');
	const colPane = $derived.by(() => {
		const c = selCol === null ? undefined : columns[selCol];
		if (!c) return null;
		const fk = foreignKeys.find((f) => f.column === c.name);
		const refs = referencedBy.filter((f) => f.refColumn === c.name).map((f) => `${f.table}.${f.column}`);
		const inIndexes = (indexes ?? []).filter((x) => x.columns.includes(c.name)).map((x) => x.name);
		const facts: [string, Cell][] = [
			['column_name', c.name],
			['data_type', c.type],
			['ordinal_position', selCol! + 1],
			['is_nullable', yesNo(c.nullable)],
			['column_default', c.default],
			['primary_key', yesNo(c.primaryKey)],
			['references', fk ? `${fk.refSchema ? fk.refSchema + '.' : ''}${fk.refTable}.${fk.refColumn}` : null],
			['foreign_key', fk?.name ?? null],
			['referenced_by', refs.length ? refs.join('\n') : null],
			...(indexes ? ([['indexes', inIndexes.length ? inIndexes.join('\n') : null]] as [string, Cell][]) : [])
		];
		return { title: c.name, columns: facts.map(([k]) => fact(k)), row: facts.map(([, v]) => v) };
	});
	const ixPane = $derived.by(() => {
		const x = selIx === null ? undefined : indexes?.[selIx];
		if (!x) return null;
		const facts: [string, Cell][] = [
			['index_name', x.name],
			['columns', x.columns.join('\n') || null],
			['method', x.method || null],
			['is_unique', yesNo(x.unique)],
			['is_primary', yesNo(x.primary)],
			...(x.size >= 0 ? ([['size', `${bytes(x.size)} (${x.size.toLocaleString()} bytes)`]] as [string, Cell][]) : []),
			...(x.scans >= 0 ? ([['scans', x.scans]] as [string, Cell][]) : []),
			['definition', x.definition || null]
		];
		return { title: x.name, columns: facts.map(([k]) => fact(k)), row: facts.map(([, v]) => v) };
	});

	// ---------- structure and indexes as read-only grids ----------
	let structSort = $state<Sort>({ column: '', dir: '' });
	let ixSort = $state<Sort>({ column: '', dir: '' });
	let structLayout = $state<GridLayout>(emptyLayout());
	let ixLayout = $state<GridLayout>(emptyLayout());

	/** Rows sorted by the header sort; source maps a shown row back to its item. */
	function sortedGrid(columns: GridColumn[], rows: Cell[][], sort: Sort) {
		const source = rows.map((_, i) => i);
		const c = columns.findIndex((x) => x.name === sort.column);
		if (c >= 0 && sort.dir) {
			const dir = sort.dir === 'asc' ? 1 : -1;
			source.sort((a, b) => compareCells(rows[a][c], rows[b][c], dir));
		}
		return { columns, rows: source.map((i) => rows[i]), source };
	}

	const structGrid = $derived(
		sortedGrid(
			[{ name: 'column_name' }, { name: 'data_type' }, { name: 'is_nullable' }, { name: 'column_default' }, { name: 'key' }, { name: 'references' }],
			columns.map((c) => [c.name, c.type, c.nullable ? 'YES' : 'NO', c.default, c.primaryKey ? 'PRIMARY' : null, relations[c.name] ?? null]),
			structSort
		)
	);

	const ixGrid = $derived(
		sortedGrid(
			[
				{ name: 'index_name' },
				{ name: 'columns' },
				{ name: 'method' },
				{ name: 'constraint' },
				{ name: 'size' },
				{ name: 'scans', type: 'number' },
				{ name: 'definition' }
			],
			(indexes ?? []).map((x) => [
				x.name,
				x.columns.join(', '),
				x.method || null,
				x.primary ? 'PRIMARY' : x.unique ? 'UNIQUE' : null,
				x.size >= 0 ? bytes(x.size) : null,
				x.scans >= 0 ? x.scans : null,
				x.definition || null
			]),
			ixSort
		)
	);

	// ---------- pending edits ----------
	const edits = new SvelteMap<number, Record<string, Cell>>();
	const deleted = new SvelteSet<number>();
	let inserted = $state<Cell[][]>([]);
	let selectedRows = $state(new Set<number>());
	let detailRow = $state<number | null>(null);
	// Table details stay open by default (shared by every table tab).
	const DETAILS_KEY = 'tablef.tableDetails.open';
	let detailsOpen = $state(loadStored(DETAILS_KEY, true));
	function toggleDetails() {
		// With a record open, the button switches the pane back to the details.
		detailsOpen = detailRow !== null || !detailsOpen;
		detailRow = null;
		saveStored(DETAILS_KEY, detailsOpen);
	}

	const pendingCount = $derived(edits.size + deleted.size + inserted.length);
	const dirty = $derived(pendingCount > 0);
	const DISCARD_KEYS = isMac ? '⇧⌘⌫' : 'Ctrl+Shift+⌫';
	const keyColumns = $derived.by(() => {
		const pk = columns.filter((c) => c.primaryKey).map((c) => c.name);
		return pk.length ? pk : columns.map((c) => c.name);
	});
	const editable = $derived(table.type === 'table' && columns.length > 0 && app.session?.features?.edit !== false);
	const canGroup = $derived(app.session?.features?.groups !== false);
	// The filter line speaks the engine's own language.
	const sqlFilter = $derived(isSQL(app.session?.driver ?? 'postgres'));
	// Column names as they can be typed: bare when that is valid, quoted otherwise.
	const ident = (name: string) => (/^[a-z_][a-z0-9_]*$/.test(name) ? name : app.quoteIdent(name));
	// The hint is built from this table's own columns instead of a made-up example.
	const filterHint = $derived.by(() => {
		const family = app.session?.features?.family ?? '';
		if (family === 'mongodb') return '{ field: "value" }  — a MongoDB filter';
		if (family === 'redis') return 'user:1*  (key pattern)';
		const pick = (k: CellKind) => columns.find((c) => kindOf(c.type) === k);
		const key = columns.find((c) => c.primaryKey) ?? pick('number') ?? columns[0];
		const text = pick('text');
		const when = pick('datetime') ?? pick('date');
		if (!key) return 'condition, optionally followed by ORDER BY';
		const cond = text && text !== key ? `${ident(text.name)} LIKE '%…%'` : `${ident(key.name)} = …`;
		return `${cond} ORDER BY ${ident((when ?? key).name)} DESC`;
	});
	const pages = $derived(Math.max(1, Math.ceil(total / limit)));
	const gridColumns = $derived(columns.map((c) => ({ name: c.name, type: c.type, primaryKey: c.primaryKey, nullable: c.nullable })));

	const colIndex = $derived(new Map(columns.map((c, i) => [c.name, i])));

	// Rows as displayed: only edited rows are copied, so an edit costs O(edited rows), not O(page).
	const rows = $derived.by(() => {
		const out = base.slice();
		for (const [i, e] of edits) out[i] = columns.map((c, ci) => (c.name in e ? e[c.name] : base[i][ci]));
		return inserted.length ? out.concat(inserted) : out;
	});

	const detailTitle = $derived.by(() => {
		if (detailRow === null || !rows[detailRow]) return '';
		if (detailRow >= base.length) return 'new row';
		const pk = columns.flatMap((c, i) => (c.primaryKey ? [`${c.name} = ${rows[detailRow!][i]}`] : []));
		return pk.length ? pk.join(', ') : `row ${detailRow + 1}`;
	});

	// ---------- grouping (server side) ----------
	interface GNode {
		key: string;
		depth: number;
		column: string;
		value: Cell;
		count: number;
		match: ColumnMatch[];
		open: boolean;
		loading: boolean;
		error?: string;
		children?: GNode[];
		rowIdx?: number[];
	}
	let roots = $state<GNode[]>([]);
	let groupTotal = $state(0);

	const grouped = $derived(groupBy.length > 0);

	function makeNodes(groups: { value: Cell; count: number }[], depth: number, parent: ColumnMatch[]): GNode[] {
		const column = groupBy[depth];
		return groups.map((g) => {
			const match = [...parent, { column, value: g.value }];
			return { key: JSON.stringify(match.map((m) => m.value)), depth, column, value: g.value, count: g.count, match, open: false, loading: false };
		});
	}

	const items = $derived.by<GridItem[] | undefined>(() => {
		if (!grouped) return undefined;
		const out: GridItem[] = inserted.map((_, k) => ({ kind: 'row', r: base.length + k }));
		const walk = (nodes: GNode[]) => {
			for (const n of nodes) {
				out.push({ kind: 'group', key: n.key, depth: n.depth, column: n.column, value: n.value, count: n.count, open: n.open, loading: n.loading });
				if (!n.open) continue;
				if (n.children) walk(n.children);
				else if (n.rowIdx) {
					for (const r of n.rowIdx) out.push({ kind: 'row', r, depth: n.depth + 1 });
					if (n.rowIdx.length < n.count || n.error)
						out.push({ kind: 'more', key: n.key, depth: n.depth, loaded: n.rowIdx.length, total: n.count, loading: n.loading, error: n.error });
				}
			}
		};
		walk(roots);
		return out;
	});

	function findNode(key: string, nodes = roots): GNode | undefined {
		for (const n of nodes) {
			if (n.key === key) return n;
			const hit = n.children && findNode(key, n.children);
			if (hit) return hit;
		}
	}

	async function loadRows(n: GNode) {
		if (!app.session) return;
		n.loading = true;
		n.error = undefined;
		try {
			const res = await app.track(
				api.getTableData(app.session.id, {
					schema: table.schema,
					table: table.name,
					limit: GROUP_PAGE,
					offset: n.rowIdx?.length ?? 0,
					orderBy: sort.dir ? sort.column : '',
					orderDir: sort.dir,
					orderSql: parts.order,
					where,
					match: n.match
				})
			);
			if (!columns.length) columns = res.columns;
			const startIdx = base.length;
			base = [...base, ...res.rows];
			n.rowIdx = [...(n.rowIdx ?? []), ...res.rows.map((_, k) => startIdx + k)];
		} catch (e) {
			n.error = (e as Error).message;
		} finally {
			n.loading = false;
		}
	}

	async function toggleGroup(key: string) {
		const n = findNode(key);
		if (!n || !app.session) return;
		if (n.open) {
			n.open = false;
			return;
		}
		n.open = true;
		if (n.depth < groupBy.length - 1) {
			if (n.children) return;
			n.loading = true;
			try {
				const res = await app.track(
					api.getGroups(app.session.id, {
						schema: table.schema,
						table: table.name,
						where,
						match: n.match,
						column: groupBy[n.depth + 1]
					})
				);
				n.children = makeNodes(res.groups, n.depth + 1, n.match);
			} catch (e) {
				app.fail(e);
				n.open = false;
			} finally {
				n.loading = false;
			}
		} else if (!n.rowIdx) {
			await loadRows(n);
		}
	}

	function moreRows(key: string) {
		const n = findNode(key);
		if (n) loadRows(n);
	}

	// ---------- loading ----------
	// Loads can overlap (fast paging, sorting); only the latest one may apply.
	let loadSeq = 0;
	async function load() {
		indexes = null; // refetched when the Indexes view shows
		selCol = selIx = null;
		if (!app.session) return;
		const seq = ++loadSeq;
		loading = true;
		error = '';
		const t0 = performance.now();
		try {
			if (grouped) {
				// Columns come from a zero-row page so the header renders before any group opens.
				const [meta, groups] = await app.track(
					Promise.all([
						api.getTableData(app.session.id, { schema: table.schema, table: table.name, limit: 1, offset: 0, orderBy: '', orderDir: '', where }),
						api.getGroups(app.session.id, { schema: table.schema, table: table.name, where, match: [], column: groupBy[0] })
					])
				);
				if (seq !== loadSeq) return;
				columns = meta.columns;
				foreignKeys = meta.foreignKeys ?? [];
				referencedBy = meta.referencedBy ?? [];
				total = groups.rows;
				groupTotal = groups.total;
				base = [];
				roots = makeNodes(groups.groups, 0, []);
			} else {
				const res = await app.track(
					api.getTableData(app.session.id, {
						schema: table.schema,
						table: table.name,
						limit,
						offset: page * limit,
						orderBy: sort.dir ? sort.column : '',
						orderDir: sort.dir,
						orderSql: parts.order,
						where
					})
				);
				if (seq !== loadSeq) return;
				columns = res.columns;
				foreignKeys = res.foreignKeys ?? [];
				referencedBy = res.referencedBy ?? [];
				base = res.rows;
				total = res.total;
				roots = [];
			}
			discard();
		} catch (e) {
			if (seq === loadSeq) error = (e as Error).message;
		} finally {
			if (seq === loadSeq) {
				elapsed = performance.now() - t0;
				loading = false;
			}
		}
	}

	// Reload only when the table or database changes; other triggers call load() directly.
	$effect(() => {
		void table;
		void app.session?.database;
		untrack(load);
	});

	async function guard(): Promise<boolean> {
		if (!dirty || !prefs.value.confirmDiscard) return true;
		return dialogs.confirm({
			title: 'Discard unsaved changes?',
			message: 'Your edits to this table have not been committed.',
			confirmLabel: 'Discard',
			danger: true
		});
	}

	function discard() {
		detailRow = null;
		undoStack = [];
		redoStack = [];
		edits.clear();
		deleted.clear();
		inserted = [];
		selectedRows = new Set();
	}

	async function setSort(column: string) {
		if (!(await guard())) return;
		sort = nextSort(sort, column);
		if (sqlFilter) {
			// The filter line always shows the query in effect: the header sort is
			// written into it as ORDER BY, to edit, extend or delete like any text.
			const order = sort.dir ? `ORDER BY ${ident(sort.column)} ${sort.dir.toUpperCase()}` : '';
			filter = filterInput = [parts.where, order].filter(Boolean).join(' ');
		} else if (parts.order) {
			filter = filterInput = parts.where;
		}
		page = 0;
		load();
	}

	async function applyFilter() {
		if (!(await guard())) return;
		filter = filterInput.trim();
		// The ORDER BY in the line is what sorts. The header arrow stays only while
		// the line still holds exactly the ORDER BY the header sort wrote.
		const typed = splitFilter(filter).order;
		const fromHeader = sort.dir ? `${ident(sort.column)} ${sort.dir.toUpperCase()}` : '';
		if (sqlFilter ? typed !== fromHeader : !!typed) sort = { column: '', dir: '' };
		page = 0;
		load();
	}

	function clearFilter() {
		filterInput = '';
		applyFilter();
	}

	async function goto(p: number) {
		if (p < 0 || p >= pages || !(await guard())) return;
		page = p;
		load();
	}

	async function setGroups(next: string[]) {
		if (!(await guard())) return;
		groupBy = next;
		page = 0;
		load();
	}

	// Columns dragged from the header or the fields panel can be dropped to group by them.
	let dragging = $state(false);
	let overGroup = $state(false);
	function dropGroup(e: DragEvent) {
		const name = e.dataTransfer?.getData(COLUMN_MIME);
		overGroup = dragging = false;
		if (!name) return;
		e.preventDefault();
		if (!groupBy.includes(name)) setGroups([...groupBy, name]);
	}

	// ---------- undo / redo ----------
	// Each user action is recorded as the small changes it made (with the value
	// before and after), so recording and undoing cost O(change), not O(all edits).
	type Op =
		| { t: 'cell'; r: number; name: string; before: Cell | undefined; after: Cell | undefined }
		| { t: 'new'; k: number; c: number; before: Cell; after: Cell }
		| { t: 'del'; r: number }
		| { t: 'rows'; before: Cell[][]; after: Cell[][] };
	const HISTORY = 200;
	let undoStack: Op[][] = [];
	let redoStack: Op[][] = [];
	let recording: Op[] | null = null;

	/** Runs fn as one undoable action. */
	function action(fn: () => void) {
		recording = [];
		fn();
		if (recording.length) {
			undoStack.push(recording);
			if (undoStack.length > HISTORY) undoStack.shift();
			redoStack = [];
		}
		recording = null;
	}

	function setCell(r: number, name: string, value: Cell | undefined) {
		const e = { ...(edits.get(r) ?? {}) };
		if (value === undefined) delete e[name];
		else e[name] = value;
		if (Object.keys(e).length) edits.set(r, e);
		else edits.delete(r);
	}

	function setInserted(next: Cell[][]) {
		recording?.push({ t: 'rows', before: inserted, after: next });
		inserted = next;
	}

	function replay(ops: Op[], back: boolean) {
		for (const op of back ? [...ops].reverse() : ops) {
			if (op.t === 'cell') setCell(op.r, op.name, back ? op.before : op.after);
			else if (op.t === 'new') inserted[op.k][op.c] = back ? op.before : op.after;
			else if (op.t === 'del') {
				if (deleted.has(op.r)) deleted.delete(op.r);
				else deleted.add(op.r);
			} else inserted = back ? op.before : op.after;
		}
	}

	function undo() {
		const ops = undoStack.pop();
		if (!ops) return;
		replay(ops, true);
		redoStack.push(ops);
	}

	function redo() {
		const ops = redoStack.pop();
		if (!ops) return;
		replay(ops, false);
		undoStack.push(ops);
	}

	function onedit(r: number, c: number, value: Cell) {
		action(() => applyEdit(r, c, value));
	}

	// Pasted lines go to the active row and the rows displayed after it.
	function onpastecells(r0: number, lines: PastedCell[][]) {
		const order = items ? items.flatMap((it) => (it.kind === 'row' ? [it.r] : [])) : null;
		const start = order ? order.indexOf(r0) : r0;
		action(() =>
			lines.forEach((line, dr) => {
				const r = order ? order[start + dr] : start + dr;
				if (r === undefined || r >= rows.length || deleted.has(r)) return;
				for (const { c, v } of line) applyEdit(r, c, v);
			})
		);
	}

	function duplicateRows(list: number[]) {
		if (!list.length) return;
		// Primary keys are left empty so the database assigns new ones.
		const copies = list.sort((a, b) => a - b).map((i) => rows[i].map((v, ci) => (columns[ci].primaryKey ? null : v)));
		action(() => setInserted([...inserted, ...copies]));
	}

	function applyEdit(r: number, c: number, value: Cell) {
		const name = columns[c].name;
		if (r >= base.length) {
			const k = r - base.length;
			recording?.push({ t: 'new', k, c, before: inserted[k][c], after: value });
			inserted[k][c] = value;
			return;
		}
		const before = edits.get(r)?.[name];
		// Typing the original value back removes the edit.
		const after = String(base[r][c] ?? '\u0000') === String(value ?? '\u0000') ? undefined : value;
		if (before === after) return;
		recording?.push({ t: 'cell', r, name, before, after });
		setCell(r, name, after);
	}

	function ondeleterows(list: number[]) {
		action(() => {
			const newOnes = new Set(list.filter((i) => i >= base.length).map((i) => i - base.length));
			if (newOnes.size) setInserted(inserted.filter((_, k) => !newOnes.has(k)));
			for (const i of list) {
				if (i >= base.length) continue;
				recording?.push({ t: 'del', r: i });
				if (deleted.has(i)) deleted.delete(i);
				else deleted.add(i);
			}
		});
		selectedRows = new Set();
	}

	function addRow() {
		action(() => setInserted([...inserted, columns.map(() => null)]));
	}

	function keysOf(i: number): Record<string, Cell> {
		return Object.fromEntries(keyColumns.map((k) => [k, base[i][colIndex.get(k)!]]));
	}

	async function commit() {
		if (!app.session || !dirty) return;
		const cs: ChangeSet = {
			schema: table.schema,
			table: table.name,
			deletes: [...deleted].map(keysOf),
			updates: [...edits].filter(([i]) => !deleted.has(i)).map(([i, values]) => ({ keys: keysOf(i), values })),
			// Untouched NULL cells are omitted so column defaults apply.
			inserts: inserted.map((r) => Object.fromEntries(r.flatMap((v, ci) => (v === null ? [] : [[columns[ci].name, v]]))))
		};
		try {
			const stmts = await app.track(api.applyChanges(app.session.id, cs));
			app.notify('info', `Committed ${stmts.length} change${stmts.length === 1 ? '' : 's'}`);
			await load();
		} catch (e) {
			app.fail(e);
		}
	}

	let filterLine = $state<SqlLine>();

	function keydown(e: KeyboardEvent) {
		if (!active) return;
		const mod = e.metaKey || e.ctrlKey;
		const key = e.key.toLowerCase();
		// Text fields keep their own undo, copy and paste.
		const inText = targetIn(e, 'input, textarea, select, .cm-editor');
		if (mod && key === 's') {
			e.preventDefault();
			commit();
		} else if (mod && e.shiftKey && e.key === 'Backspace' && dirty) {
			e.preventDefault();
			discard();
		} else if (mod && key === 'r') {
			e.preventDefault();
			guard().then((ok) => {
				if (ok) load();
			});
		} else if (mod && key === 'f') {
			e.preventDefault();
			view = 'data';
			filterLine?.focus();
		} else if (inText) {
			return;
		} else if (mod && key === 'z') {
			e.preventDefault();
			if (e.shiftKey) redo();
			else undo();
		} else if (mod && key === 'y') {
			e.preventDefault();
			redo();
		} else if (mod && key === 'd' && editable) {
			e.preventDefault();
			duplicateRows([...selectedRows]);
		} else if (mod && key === 'i' && editable) {
			e.preventDefault();
			addRow();
		} else if (e.key === 'Escape' && (detailRow !== null || selCol !== null || selIx !== null)) {
			detailRow = selCol = selIx = null;
		}
	}
</script>

<svelte:window
	onkeydown={keydown}
	ondragstart={(e) => (dragging = !!e.dataTransfer?.types.includes(COLUMN_MIME))}
	ondragend={() => (overGroup = dragging = false)}
/>

{#if peek}
	<RelationPeek fk={peek.fk} value={peek.value} x={peek.x} y={peek.y} onclose={() => (peek = null)} />
{/if}

<div class="page">
	<div class="toolbar">
		<div class="seg">
			<button class="tool" class:on={fieldsOpen} onclick={() => (fieldsOpen = !fieldsOpen)} title="Fields" disabled={view !== 'data'}>
				<Columns3 size={14} />
			</button>
		</div>

		{#if view === 'data'}
			<div class="where" class:err={!!error && columns.length > 0}>
				<Filter size={13} class="where-icon" />
				{#key columns.length}
					<SqlLine
						bind:this={filterLine}
						bind:value={filterInput}
						driver={app.session?.driver ?? 'postgres'}
						columns={columns.map((c) => c.name)}
						placeholder={filterHint}
						onsubmit={applyFilter}
						oncancel={() => (filterInput = filter)}
					/>
				{/key}
				{#if error && columns.length}<span class="err-text mono" title={error}>{error}</span>{/if}
				{#if filter}
					<button class="mini" onclick={clearFilter} title="Clear filter"><X size={13} /></button>
				{/if}
				<button class="tool run" class:dirty={filterInput.trim() !== filter} onclick={applyFilter} title="Apply (↵)">
					<Play size={12} fill="currentColor" />
				</button>
			</div>

			{#if canGroup && (grouped || dragging)}
				<div
					class="groups"
					class:over={overGroup}
					class:zone={dragging}
					role="list"
					ondragover={(e) => {
						if (e.dataTransfer?.types.includes(COLUMN_MIME)) {
							e.preventDefault();
							overGroup = true;
						}
					}}
					ondragleave={() => (overGroup = false)}
					ondrop={dropGroup}
				>
					{#if grouped}
						<span class="label">Group</span>
						{#each groupBy as g, i (g)}
							{#if i > 0}<span class="arrow">›</span>{/if}
							<span class="chip mono" role="listitem">
								{g}
								<button onclick={() => setGroups(groupBy.filter((x) => x !== g))} aria-label={`Ungroup ${g}`}><X size={11} /></button>
							</span>
						{/each}
					{:else}
						<span class="label">Drop to group</span>
					{/if}
				</div>
			{/if}
		{:else}
			<span class="grow"></span>
		{/if}

		<div class="end">
			<button class="tool" class:on={detailsOpen && detailRow === null} onclick={toggleDetails} title="Table details">
				<Info size={14} />
			</button>
			<div class="toggle">
				<button class:on={view === 'data'} onclick={() => (view = 'data')}>Data</button>
				<button class:on={view === 'structure'} onclick={() => (view = 'structure')}>Structure</button>
				<button class:on={view === 'indexes'} onclick={() => (view = 'indexes')}>Indexes</button>
			</div>
			{#if editable && view === 'data'}
				<button class="add-row" onclick={addRow} title="Insert row ({modLabel}I)"><Plus size={13} /> Row</button>
			{/if}
			<!-- Always there; usable once something is pending. -->
			<button class="add-row" onclick={discard} disabled={!dirty} title="Discard changes ({DISCARD_KEYS})">
				Discard <kbd>{DISCARD_KEYS}</kbd>
			</button>
			<button
				class="add-row primary"
				onclick={commit}
				disabled={!dirty}
				title={dirty ? `Commit ${pendingCount} change${pendingCount === 1 ? '' : 's'} (${modLabel}S)` : 'Nothing to commit'}
			>
				Commit
				{#if dirty}{#key pendingCount}<span class="badge">{pendingCount}</span>{/key}{/if}
				<kbd>{modLabel}S</kbd>
			</button>
		</div>
	</div>

	<div class="split">
		{#if fieldsOpen && view === 'data' && columns.length}
			<FieldsPanel columns={gridColumns} bind:layout onclose={() => (fieldsOpen = false)} />
		{/if}
		<div class="content">
			{#if error && !columns.length}
				<div class="error mono">{error}</div>
			{:else if view === 'data'}
				<DataGrid
					columns={gridColumns}
					{rows}
					{items}
					{editable}
					rowOffset={page * limit}
					rowStatus={(i) => (i >= base.length ? 'new' : deleted.has(i) ? 'deleted' : undefined)}
					cellChanged={(i, c) => !!edits.get(i) && columns[c].name in edits.get(i)!}
					{onedit}
					{ondeleterows}
					{sort}
					onsort={setSort}
					onactivate={(r) => (detailRow = r)}
					{onpastecells}
					onerror={(m) => app.fail(m)}
					{relations}
					onpeek={(r, c, rect) => {
						const fk = foreignKeys.find((f) => f.column === columns[c].name);
						if (fk) peek = { fk, value: rows[r][c], x: rect.left, y: rect.bottom + 4 };
					}}
					ongrouptoggle={toggleGroup}
					onmore={moreRows}
					bind:selectedRows
					bind:layout
				/>
			{:else if view === 'structure'}
				<DataGrid
					columns={structGrid.columns}
					rows={structGrid.rows}
					sort={structSort}
					onsort={(c) => (structSort = nextSort(structSort, c))}
					onactivate={(r) => (selCol = structGrid.source[r])}
					onerror={(m) => app.fail(m)}
					bind:layout={structLayout}
				/>
			{:else if indexes === null}
				<p class="ix-note">Loading indexes…</p>
			{:else if indexError}
				<p class="ix-note err">{indexError}</p>
			{:else if !indexes.length}
				<p class="ix-note">No indexes on {table.name}.</p>
			{:else}
				<DataGrid
					columns={ixGrid.columns}
					rows={ixGrid.rows}
					sort={ixSort}
					onsort={(c) => (ixSort = nextSort(ixSort, c))}
					onactivate={(r) => (selIx = ixGrid.source[r])}
					onerror={(m) => app.fail(m)}
					bind:layout={ixLayout}
				/>
			{/if}
		</div>
		{#if view === 'data' && detailRow !== null && rows[detailRow]}
			{@const r = detailRow}
			<RecordPane
				{columns}
				{foreignKeys}
				referencedBy={r < base.length ? referencedBy : []}
				row={rows[r]}
				title={detailTitle}
				table={table.name}
				editable={editable && !deleted.has(r)}
				changed={(c) => !!edits.get(r) && columns[c].name in edits.get(r)!}
				onedit={(c, v) => onedit(r, c, v)}
				onclose={() => (detailRow = null)}
			/>
		{:else if view === 'structure' && colPane}
			<RecordPane
				kind="column"
				fieldsLabel="Properties"
				columns={colPane.columns}
				row={colPane.row}
				title={colPane.title}
				table={table.name}
				onclose={() => (selCol = null)}
			/>
		{:else if view === 'indexes' && ixPane}
			<RecordPane
				kind="index"
				fieldsLabel="Properties"
				columns={ixPane.columns}
				row={ixPane.row}
				title={ixPane.title}
				table={table.name}
				onclose={() => (selIx = null)}
			/>
		{:else if detailsOpen}
			<TableDetails {table} {columns} {foreignKeys} {referencedBy} total={!grouped && !error && columns.length ? total : null} onclose={toggleDetails} />
		{/if}
	</div>

	{#if view === 'data'}
		{#if grouped}
			<div class="status">
				<span><b>{groupTotal.toLocaleString()}</b> groups · {total.toLocaleString()} rows</span>
				<span class="muted">{elapsed.toFixed(0)} ms</span>
			</div>
		{:else}
			<Pagination
				{page}
				{limit}
				{total}
				onpage={goto}
				onlimit={async (n) => {
					if (!(await guard())) return;
					limit = n;
					page = 0;
					load();
				}}
			/>
		{/if}
	{/if}
</div>

<style>
	/* Clips side panels while they slide in and out. */
	.split {
		overflow: hidden;
	}
	.chip {
		animation: tf-pop var(--dur) var(--ease-out) both;
	}
	.page {
		display: flex;
		flex-direction: column;
		height: 100%;
		background: var(--card);
	}
	.toolbar {
		/* Narrow panes drop the shortcut hints (they stay in the tooltips). */
		container-type: inline-size;
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.6rem;
		padding: 0 0.5rem;
		border-bottom: 1px solid var(--border);
		flex: none;
		flex-wrap: wrap;
	}
	.seg {
		display: flex;
		gap: 0.15rem;
	}
	.tool {
		display: grid;
		place-items: center;
		width: 1.85rem;
		height: 1.85rem;
		flex: none;
		padding: 0;
		border: 1px solid transparent;
		border-radius: var(--radius);
		background: transparent;
		color: var(--muted-foreground);
	}
	.tool:hover:not(:disabled),
	.tool.on {
		background: var(--accent);
		color: var(--foreground);
	}
	.tool:disabled {
		opacity: 0.4;
	}
	.where {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		flex: 1;
		min-width: 14rem;
		height: 1.95rem;
		padding: 0 0.15rem 0 0.65rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--background);
	}
	.where:focus-within {
		border-color: color-mix(in oklch, var(--foreground) 35%, var(--border));
	}
	.where.err {
		border-color: color-mix(in oklch, var(--destructive) 55%, var(--border));
	}
	.where :global(.where-icon) {
		flex: none;
		color: var(--muted-foreground);
	}
	.err-text {
		max-width: 40%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--destructive);
		font-size: 11px;
		user-select: text;
	}
	.mini {
		display: grid;
		place-items: center;
		width: 1.4rem;
		height: 1.4rem;
		flex: none;
		padding: 0;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--muted-foreground);
	}
	.mini:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	.run {
		width: 1.6rem;
		height: 1.6rem;
	}
	.run.dirty {
		background: var(--primary);
		color: var(--primary-foreground);
	}
	.groups {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		height: 1.95rem;
		padding: 0 0.4rem 0 0.6rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font-size: var(--text-sm);
	}
	.groups.zone {
		border-style: dashed;
		min-width: 9rem;
	}
	.groups.over {
		border-color: var(--foreground);
		background: var(--accent);
	}
	.groups .label {
		color: var(--muted-foreground);
	}
	.arrow {
		color: var(--muted-foreground);
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		height: 1.35rem;
		padding: 0 0.2rem 0 0.45rem;
		border-radius: 0.25rem;
		background: var(--accent);
		font-size: 11.5px;
	}
	.chip button {
		display: grid;
		place-items: center;
		width: 1rem;
		height: 1rem;
		padding: 0;
		border: none;
		background: transparent;
		color: var(--muted-foreground);
	}
	.chip button:hover {
		color: var(--foreground);
	}
	.grow {
		flex: 1;
	}
	.end {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		margin-left: auto;
	}
	.toggle {
		display: flex;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
	}
	.toggle button {
		height: calc(1.75rem - 2px);
		padding: 0 0.6rem;
		border: none;
		border-right: 1px solid var(--border);
		background: transparent;
		color: var(--muted-foreground);
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.toggle button:last-child {
		border-right: none;
	}
	.toggle button:hover {
		color: var(--foreground);
	}
	.toggle button.on {
		background: var(--accent);
		color: var(--foreground);
	}
	/* Same size and type as the Data / Structure toggle next to it. */
	.add-row {
		display: flex;
		align-items: center;
		gap: 0.3rem;
		height: 1.75rem;
		padding: 0 0.6rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: var(--text-sm);
		font-weight: 500;
		white-space: nowrap;
	}
	.add-row :global(svg) {
		color: var(--muted-foreground);
	}
	.add-row:hover:not(:disabled) {
		background: var(--accent);
	}
	.add-row:disabled {
		opacity: 0.4;
		cursor: default;
	}
	@container (max-width: 760px) {
		.add-row kbd {
			display: none;
		}
	}
	.add-row kbd {
		padding: 0 0.25rem;
		border-radius: 0.2rem;
		background: color-mix(in oklch, currentColor 12%, transparent);
		font-family: var(--font-mono);
		font-size: 10px;
		line-height: 1.05rem;
		opacity: 0.75;
	}
	.add-row.primary {
		background: var(--primary);
		border-color: var(--primary);
		color: var(--primary-foreground);
		transition: opacity var(--dur-fast) ease-out;
	}
	.add-row.primary:hover:not(:disabled) {
		background: var(--primary);
		opacity: 0.9;
	}
	/* Pending changes, counted on the Commit button. */
	.badge {
		display: inline-grid;
		place-items: center;
		min-width: 1.05rem;
		height: 1.05rem;
		padding: 0 0.25rem;
		border-radius: 999px;
		background: var(--warning);
		color: #000;
		font-size: 10px;
		font-weight: 600;
		font-variant-numeric: tabular-nums;
		animation: tf-pop var(--dur) var(--ease-out) both;
	}
	.split {
		display: flex;
		flex: 1;
		min-height: 0;
	}
	.content {
		position: relative;
		flex: 1;
		min-width: 0;
		min-height: 0;
	}
	.error {
		margin: 0.75rem;
		padding: 0.6rem 0.75rem;
		border: 1px solid color-mix(in oklch, var(--destructive) 35%, var(--border));
		border-radius: var(--radius);
		color: var(--destructive);
		white-space: pre-wrap;
		user-select: text;
	}
	.status {
		display: flex;
		align-items: center;
		gap: 0.9rem;
		min-height: 2.1rem;
		padding: 0 0.75rem;
		border-top: 1px solid var(--border);
		font-size: var(--text-sm);
		color: var(--muted-foreground);
	}
	.status b {
		color: var(--foreground);
		font-weight: 500;
	}
	.ix-note.err {
		color: var(--destructive);
	}
</style>
