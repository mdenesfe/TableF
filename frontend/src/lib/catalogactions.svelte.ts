// Actions behind the sidebar's context menu: scripts, export, pins and the
// destructive operations (each confirmed first).
import { api } from './api.ts';
import { dialogs } from './dialog.svelte.ts';
import { isSQL } from './engines.ts';
import type { ExportKind } from './export.ts';
import { exportGrid } from './exportaction.ts';
import { app } from './state.svelte.ts';
import { load, save } from './storage.ts';
import type { Column, Routine, Table } from './types.ts';

const EXPORT_CAP = 100_000;
const PAGE = 10_000;

const family = () => app.session?.features?.family ?? '';

/** Whether the editor speaks SQL here (MongoDB and Redis take their own commands). */
export const canQuery = () => !!app.session && isSQL(app.session.driver);

/** Whether this connection runs SQL and can change data (DDL items need both). */
export const canModify = () => !!app.session && isSQL(app.session.driver) && app.session.features?.edit !== false && !app.readOnly;

/** schema.table quoted for the current engine (unqualified where there are no schemas). */
export function qualified(t: Pick<Table, 'schema' | 'name'>): string {
	return app.session?.features?.schemas && t.schema ? `${app.quoteIdent(t.schema)}.${app.quoteIdent(t.name)}` : app.quoteIdent(t.name);
}

async function copy(text: string, what: string) {
	await navigator.clipboard?.writeText(text).catch(() => {});
	app.notify('info', `Copied ${what}`);
}

export const copyName = (name: string) => copy(name, 'name');
export const copyQualified = (t: Table) => copy(qualified(t), 'qualified name');
export const copySql = (sql: string) => copy(sql, 'SQL');

// ---------- scripts ----------

export type ScriptKind = 'select' | 'insert' | 'update' | 'delete';

async function columnsOf(t: Table): Promise<Column[]> {
	return app.session ? api.getColumns(app.session.id, t.schema, t.name) : [];
}

/** A ready-to-edit statement for the table, built from its columns. */
export async function script(t: Table, kind: ScriptKind): Promise<string> {
	const q = qualified(t);
	if (kind === 'select') return family() === 'mssql' ? `SELECT TOP 100 *\nFROM ${q};` : `SELECT *\nFROM ${q}\nLIMIT 100;`;
	const cols = await columnsOf(t);
	const keys = cols.filter((c) => c.primaryKey);
	const where = (keys.length ? keys : cols.slice(0, 1)).map((c) => `${app.quoteIdent(c.name)} = ?`).join('\n  AND ');
	const editable = cols.filter((c) => !c.primaryKey);
	if (kind === 'insert') {
		return `INSERT INTO ${q} (\n${cols.map((c) => `  ${app.quoteIdent(c.name)}`).join(',\n')}\n) VALUES (\n${cols.map(() => '  ?').join(',\n')}\n);`;
	}
	if (kind === 'update') {
		return `UPDATE ${q}\nSET\n${(editable.length ? editable : cols).map((c) => `  ${app.quoteIdent(c.name)} = ?`).join(',\n')}\nWHERE ${where};`;
	}
	return `DELETE FROM ${q}\nWHERE ${where};`;
}

export async function copyScript(t: Table, kind: ScriptKind) {
	try {
		await copy(await script(t, kind), `${kind.toUpperCase()} script`);
	} catch (e) {
		app.fail(e);
	}
}

/** SELECT of the first n rows in the engine's own syntax. */
export function selectTop(t: Table, n: number): string {
	const q = qualified(t);
	if (family() === 'mssql') return `SELECT TOP ${n} * FROM ${q};`;
	if (family() === 'oracle') return `SELECT * FROM ${q} FETCH FIRST ${n} ROWS ONLY;`;
	return `SELECT * FROM ${q} LIMIT ${n};`;
}

export const runSelectTop = (t: Table, n: number) => app.runQuery(selectTop(t, n), `${t.name}_top${n}.sql`);
export const runCount = (t: Table) => app.runQuery(`SELECT COUNT(*) FROM ${qualified(t)};`, `${t.name}_count.sql`);

// ---------- export ----------

/** Exports the whole table (up to EXPORT_CAP rows), page by page. */
export async function exportTable(t: Table, kind: ExportKind) {
	if (!app.session) return;
	const id = app.session.id;
	try {
		let columns: string[] = [];
		const rows: unknown[][] = [];
		for (let offset = 0; offset < EXPORT_CAP; offset += PAGE) {
			const page = await app.track(api.getTableData(id, { schema: t.schema, table: t.name, limit: PAGE, offset, orderBy: '', orderDir: '', where: '' }));
			columns = page.columns.map((c) => c.name);
			for (const r of page.rows) rows.push(r);
			if (page.rows.length < PAGE || rows.length >= page.total) break;
		}
		if (rows.length >= EXPORT_CAP) app.notify('info', `Exporting the first ${EXPORT_CAP.toLocaleString()} rows`);
		await exportGrid(kind, { columns, rows }, t.name);
	} catch (e) {
		app.fail(e);
	}
}

// ---------- pins ----------

const pinKey = () => `tablef.pins:${app.connection?.id ?? ''}`;
const pinId = (t: Pick<Table, 'schema' | 'name'>) => `${t.schema}\u0000${t.name}`;

class Pins {
	// Pins per connection, loaded from storage on first use. The cache is plain
	// (reading it must be allowed inside derived values); `version` makes reads
	// reactive.
	private cache = new Map<string, Set<string>>();
	private version = $state(0);

	/** The pins of the current connection. */
	current(): Set<string> {
		void this.version;
		const key = pinKey();
		let set = this.cache.get(key);
		if (!set) {
			set = new Set(load<string[]>(key, []));
			this.cache.set(key, set);
		}
		return set;
	}

	has(t: Pick<Table, 'schema' | 'name'>) {
		return this.current().has(pinId(t));
	}

	toggle(t: Pick<Table, 'schema' | 'name'>) {
		const set = this.current();
		const id = pinId(t);
		if (set.has(id)) set.delete(id);
		else set.add(id);
		save(pinKey(), [...set]);
		this.version++;
	}
}

export const pins = new Pins();

// ---------- destructive ----------

async function run(sql: string, done: string) {
	if (!app.session) return;
	try {
		const [r] = await app.track(api.executeQuery(app.session.id, sql));
		if (r?.error) throw new Error(r.error);
		app.notify('info', done);
		await app.loadTables();
	} catch (e) {
		app.fail(e);
	}
}

/** Statement that empties a table on this engine. */
function truncateSQL(t: Table): string {
	const q = qualified(t);
	return ['sqlite', 'partiql', 'cassandra'].includes(family()) ? `DELETE FROM ${q}` : `TRUNCATE TABLE ${q}`;
}

export async function truncateTable(t: Table) {
	const ok = await dialogs.confirm({
		title: `Empty ${t.name}?`,
		message: 'Every row in this table will be deleted. This cannot be undone.',
		detail: truncateSQL(t),
		confirmLabel: 'Truncate',
		danger: true
	});
	if (ok) await run(truncateSQL(t), `Emptied ${t.name}`);
}

export async function dropTable(t: Table) {
	const sql = `DROP ${t.type === 'view' ? 'VIEW' : 'TABLE'} ${qualified(t)}`;
	const ok = await dialogs.confirm({
		title: `Drop ${t.name}?`,
		message: `The ${t.type} and all its data will be removed. This cannot be undone.`,
		detail: sql,
		confirmLabel: 'Drop',
		danger: true
	});
	if (!ok) return;
	await run(sql, `Dropped ${t.name}`);
	// Close its tabs; they would only show errors now.
	for (const tab of app.tabs.filter((x) => x.kind === 'table' && x.table.schema === t.schema && x.table.name === t.name)) app.closeTab(tab.id);
}

export async function cloneTable(t: Table) {
	const name = await dialogs.prompt({
		title: `Clone ${t.name}`,
		message: 'Copies the structure and all rows into a new table.',
		value: `${t.name}_copy`,
		confirmLabel: 'Clone'
	});
	if (!name) return;
	const target = qualified({ schema: t.schema, name });
	const sql = family() === 'mssql' ? `SELECT * INTO ${target} FROM ${qualified(t)}` : `CREATE TABLE ${target} AS SELECT * FROM ${qualified(t)}`;
	await run(sql, `Created ${name}`);
}

/** Routines can be dropped where the statement is unambiguous. */
export const canDropRoutine = () => canModify() && ['postgres', 'mysql', 'mssql'].includes(family());

export async function dropRoutine(r: Routine) {
	const kind = r.kind === 'procedure' ? 'PROCEDURE' : 'FUNCTION';
	const name = qualified({ schema: r.schema, name: r.name });
	// Postgres functions can be overloaded, so the argument types identify one.
	const sql = family() === 'postgres' ? `DROP ${kind} ${name}(${r.arguments})` : `DROP ${kind} ${name}`;
	const ok = await dialogs.confirm({ title: `Drop ${r.name}?`, message: 'This cannot be undone.', detail: sql, confirmLabel: 'Drop', danger: true });
	if (ok) await run(sql, `Dropped ${r.name}`);
}
