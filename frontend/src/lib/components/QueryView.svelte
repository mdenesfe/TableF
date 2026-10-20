<script lang="ts">
	import { AlignLeft, Bookmark, LoaderCircle, CircleAlert, CircleCheck, CircleX, Columns3, Play, Square } from '@lucide/svelte';
	import { format } from 'sql-formatter';
	import { dialogs } from '#lib/dialog.svelte.ts';
	import type { Grid } from '#lib/export.ts';
	import ExportMenu from './ExportMenu.svelte';
	import { drag } from '#lib/drag.ts';
	import { formatterOf, isSQL } from '#lib/engines.ts';
	import { api, isMac, modLabel, targetIn } from '#lib/api.ts';
	import { prefs } from '#lib/prefs.svelte.ts';
	import { firstTable, isDestructive, sourceTable, statementRanges } from '#lib/sql.ts';
	import { compareCells, nextSort, type Sort } from '#lib/sort.ts';
	import { app, type Tab } from '#lib/state.svelte.ts';
	import type { Cell, ForeignKey, QueryResult } from '#lib/types.ts';
	import { onDestroy, untrack } from 'svelte';
	import DataGrid, { emptyLayout, type GridLayout } from './DataGrid.svelte';
	import FieldsPanel from './FieldsPanel.svelte';
	import RecordPane from './RecordPane.svelte';
	import RelationPeek from './RelationPeek.svelte';
	import SqlEditor from './SqlEditor.svelte';

	let { tab }: { tab: Extract<Tab, { kind: 'query' }> } = $props();

	// Results are replaced wholesale, never mutated, so they skip deep reactivity:
	// thousands of rows × columns would otherwise each get a reactive proxy.
	let results = $state.raw<QueryResult[]>([]);
	let current = $state(0); // result index, or -1 for messages
	// The log keeps a summary per statement, not its rows.
	type LogEntry = { at: Date; statement: string; error?: string; summary: string; count: number; durationMs: number };
	let messages = $state.raw<LogEntry[]>([]);
	let detailRow = $state<number | null>(null);
	let running = $state(false);
	let split = $state(45); // editor height in percent
	let container: HTMLDivElement;
	let editor: SqlEditor;

	const result = $derived(current >= 0 ? results[current] : undefined);

	// Results behave like a table: typed headers, client-side sort, fields panel.
	let layout = $state<GridLayout>(emptyLayout());
	let fieldsOpen = $state(false);
	let sort = $state<Sort>({ column: '', dir: '' });
	const gridColumns = $derived(result?.columns.map((name, i) => ({ name, type: result.columnTypes?.[i] || undefined })) ?? []);
	const sortedRows = $derived.by(() => {
		if (!result) return [];
		const ci = result.columns.indexOf(sort.column);
		if (!sort.dir || ci < 0) return result.rows;
		const dir = sort.dir === 'asc' ? 1 : -1;
		return [...result.rows].sort((a, b) => compareCells(a[ci], b[ci], dir));
	});

	function setSort(column: string) {
		sort = nextSort(sort, column);
		detailRow = null;
	}

	// ---------- export and format ----------

	// Export works on the open tab: the current result grid, or the messages log.
	const exportable = $derived(current === -1 ? messages.length > 0 : !!result?.columns.length);
	function exportSource(): Grid | null {
		if (current === -1) {
			const cols = ['time', 'status', 'statement', 'rows', 'duration_ms', 'error'];
			const rows = messages.map((m) => [m.at.toISOString(), m.error ? 'error' : 'ok', m.statement, m.count, Number(m.durationMs.toFixed(2)), m.error ?? null]);
			return { columns: cols, rows };
		}
		return result ? { columns: result.columns, rows: sortedRows } : null;
	}

	function formatSql() {
		try {
			tab.sql = format(tab.sql, {
				language: formatterOf(app.session?.driver ?? 'postgres') as never,
				keywordCase: prefs.value.keywordCase,
				tabWidth: prefs.value.editorTabSize
			});
		} catch (e) {
			app.fail(`Can't format: ${(e as Error).message}`);
		}
	}

	// ---------- editing results ----------
	// A plain single-table SELECT that includes the table's primary key can be
	// edited like the table itself. Edits are keyed by row object so sorting
	// doesn't lose them.
	let source = $state<{ schema: string; table: string; keys: string[]; statement: string } | null>(null);
	// Replaced on every change (never mutated), so plain references suffice.
	let edits = $state.raw(new Map<Cell[], Record<string, Cell>>());
	let removed = $state.raw(new Set<Cell[]>());
	const pendingCount = $derived(edits.size + removed.size);
	const dirty = $derived(pendingCount > 0);
	const DISCARD_KEYS = isMac ? '⇧⌘⌫' : 'Ctrl+Shift+⌫';
	const editable = $derived(!!source && !!app.session?.features?.edit);

	// Files are named after what they hold: the queried table, else the tab.
	const resultName = $derived(source?.table ?? (result && firstTable(result.statement)) ?? tab.title.replace(/\.sql$/, ''));
	const exportName = $derived(current === -1 ? 'messages' : resultName);

	// Foreign keys of the table a result comes from: link icons in the header,
	// related-row peek on click and relations in the record pane.
	let rel = $state.raw<{ statement: string; out: ForeignKey[]; in: ForeignKey[] } | null>(null);
	const relFor = $derived(rel && result && rel.statement === result.statement ? rel : null);
	const relations = $derived(Object.fromEntries((relFor?.out ?? []).map((f) => [f.column, `${f.refTable}.${f.refColumn}`])));
	let peek = $state<{ fk: ForeignKey; value: Cell; x: number; y: number } | null>(null);

	async function loadRelations(r: QueryResult) {
		const src = sourceTable(r.statement);
		if (!src || !app.session?.features?.foreignKeys) return;
		try {
			const res = await api.getRelations(app.session.id, src.schema, src.table);
			if (result?.statement !== r.statement) return;
			const has = new Set(r.columns);
			rel = {
				statement: r.statement,
				out: res.foreignKeys.filter((f) => has.has(f.column)),
				in: res.referencedBy.filter((f) => has.has(f.refColumn))
			};
		} catch {
			/* not a table (a view, a function): no relations */
		}
	}

	async function detectSource(r: QueryResult | undefined) {
		edits = new Map();
		removed = new Set();
		// A committed result is a new object for the same statement: keep its source.
		if (r && source?.statement === r.statement) return;
		source = null;
		if (r && !r.error && r.columns.length && rel?.statement !== r.statement) loadRelations(r);
		if (!r || r.error || !r.columns.length || !app.session?.features?.edit) return;
		const src = sourceTable(r.statement);
		if (!src) return;
		try {
			const cols = await api.getColumns(app.session.id, src.schema, src.table);
			const keys = cols.filter((c) => c.primaryKey).map((c) => c.name);
			if (keys.length && keys.every((k) => r.columns.includes(k)) && result === r) source = { ...src, keys, statement: r.statement };
		} catch {
			/* not a table we can edit (e.g. a view or a function) */
		}
	}
	$effect(() => {
		const r = result;
		untrack(() => detectSource(r));
	});

	const shownRows = $derived.by(() => {
		if (!edits.size) return sortedRows;
		const cols = result!.columns;
		return sortedRows.map((row) => {
			const e = edits.get(row);
			return e ? row.map((v, i) => (cols[i] in e ? e[cols[i]] : v)) : row;
		});
	});

	function onedit(i: number, c: number, value: Cell) {
		const row = sortedRows[i];
		const col = result!.columns[c];
		const next = new Map(edits);
		const e = { ...(next.get(row) ?? {}) };
		if (value === row[c]) delete e[col];
		else e[col] = value;
		if (Object.keys(e).length) next.set(row, e);
		else next.delete(row);
		edits = next;
	}

	function ondeleterows(idx: number[]) {
		const next = new Set(removed);
		for (const i of idx) {
			const row = sortedRows[i];
			if (next.has(row)) next.delete(row);
			else next.add(row);
		}
		removed = next;
	}

	const colIndex = $derived(new Map(result?.columns.map((c, i) => [c, i]) ?? []));

	function keysOf(row: Cell[]) {
		return Object.fromEntries(source!.keys.map((k) => [k, row[colIndex.get(k)!]]));
	}

	async function commit() {
		if (!app.session || !source || !result || !dirty) return;
		const r = result;
		try {
			await app.track(
				api.applyChanges(app.session.id, {
					schema: source.schema,
					table: source.table,
					updates: [...edits].filter(([row]) => !removed.has(row)).map(([row, values]) => ({ keys: keysOf(row), values })),
					inserts: [],
					deletes: [...removed].map(keysOf)
				})
			);
			// Results aren't deeply reactive: build the updated result instead of mutating it.
			const rows = r.rows.flatMap((row) => {
				if (removed.has(row)) return [];
				const e = edits.get(row);
				return [e ? row.map((v, i) => (r.columns[i] in e ? e[r.columns[i]] : v)) : row];
			});
			const n = edits.size + removed.size;
			edits = new Map();
			removed = new Set();
			results = results.map((x) => (x === r ? { ...r, rows } : x));
			app.notify('info', `Saved ${n} row${n === 1 ? '' : 's'} to ${source.table}`);
		} catch (e) {
			app.fail(e);
		}
	}

	function discardEdits() {
		edits = new Map();
		removed = new Set();
	}

	// The saved query this tab edits, and whether the editor differs from it.
	const saved = $derived(tab.savedId ? app.savedQueries.find((q) => q.id === tab.savedId) : undefined);
	const unsaved = $derived(!!saved && saved.sql !== tab.sql);

	function keydown(e: KeyboardEvent) {
		if (tab.id !== app.activeTabId) return;
		if (e.shiftKey && e.altKey && e.code === 'KeyF') {
			e.preventDefault();
			formatSql();
		} else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
			// ⌘S commits pending result edits; otherwise it saves the query (⇧⌘S: as a new one).
			e.preventDefault();
			if (dirty) commit();
			else app.saveQueryTab(tab, e.shiftKey);
		} else if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'Backspace' && dirty && !targetIn(e, '.cm-editor, input, textarea')) {
			e.preventDefault();
			discardEdits();
		}
	}

	function selectResult(i: number) {
		current = i;
		detailRow = null;
		sort = { column: '', dir: '' };
		layout = emptyLayout();
	}
	const fmtTime = (d: Date) => d.toLocaleTimeString([], { hour12: false });

	// Tabs opened by "Select top N rows" and the like run once on open.
	$effect(() => {
		if (!tab.autorun || !app.session) return;
		tab.autorun = false;
		untrack(() => run(tab.sql));
	});

	async function run(sql: string) {
		if (!app.session || running || !sql.trim()) return;
		if (prefs.value.confirmDestructive) {
			const risky = statementRanges(sql)
				.map((r) => sql.slice(r.from, r.to))
				.filter(isDestructive);
			if (
				risky.length &&
				!(await dialogs.confirm({
					title: 'Run destructive statements?',
					message: 'These change data without a WHERE clause or drop objects.',
					detail: risky.map((x) => x.split('\n')[0]).join('\n'),
					confirmLabel: 'Run anyway',
					danger: true
				}))
			)
				return;
		}
		stopLoading();
		running = true;
		try {
			results = await app.track(api.executeQuery(app.session.id, sql));
			loadRest();
			detailRow = null;
			sort = { column: '', dir: '' };
			layout = emptyLayout();
			const at = new Date();
			const entries = results.map((r) => ({
				at,
				statement: r.statement,
				error: r.error,
				summary: summary(r),
				count: r.columns.length ? r.rows.length : r.rowsAffected,
				durationMs: r.durationMs
			}));
			messages = [...messages, ...entries].slice(-300);
			const failed = results.findIndex((r) => r.error);
			const lastWithRows = results.findLastIndex((r) => r.columns.length > 0);
			current = failed >= 0 ? failed : lastWithRows >= 0 ? lastWithRows : results.length - 1;
			if (results.some((r) => !r.error && !r.columns.length)) app.loadTables();
		} catch (e) {
			results = [];
			app.fail(e);
		} finally {
			running = false;
			app.historyVersion++;
		}
	}

	function cancel() {
		if (app.session) api.cancelQuery(app.session.id);
	}

	// ---------- the rest of a large result ----------
	// The server sends the first rows at once and keeps the result open; the
	// rest arrives here in chunks that grow as it goes.
	let loading = $state<{ cursor: string; stopped: boolean } | null>(null);

	async function loadRest() {
		const sid = app.session?.id;
		const cursor = results.find((x) => x.cursor)?.cursor;
		if (!sid || !cursor) return;
		const state = { cursor, stopped: false };
		loading = state;
		let size = 10_000;
		try {
			for (;;) {
				const page = await api.fetchRows(sid, cursor, size);
				if (state.stopped) break;
				// Found by cursor, not by object: committed edits replace the result object.
				results = results.map((x) =>
					x.cursor === cursor ? { ...x, rows: x.rows.concat(page.rows), truncated: !page.done, cursor: page.done ? undefined : cursor } : x
				);
				if (page.done) break;
				size = Math.min(50_000, size * 2);
			}
		} catch (e) {
			if (!state.stopped) app.fail(e);
		} finally {
			if (loading === state) loading = null;
		}
	}

	/** Stops reading the rest of the result; what arrived so far stays. */
	function stopLoading() {
		const l = loading;
		if (!l) return;
		l.stopped = true;
		loading = null;
		if (app.session) api.closeCursor(app.session.id, l.cursor).catch(() => {});
		results = results.map((x) => (x.cursor === l.cursor ? { ...x, cursor: undefined, truncated: true } : x));
	}

	onDestroy(stopLoading);

	function dragStart(e: PointerEvent) {
		const rect = container.getBoundingClientRect();
		drag(e, (ev) => (split = Math.min(85, Math.max(15, ((ev.clientY - rect.top) / rect.height) * 100))));
	}

	function summary(r: QueryResult) {
		if (r.error) return 'error';
		if (r.columns.length) return `${r.rows.length.toLocaleString()}${r.truncated ? '+' : ''} rows`;
		return `${r.rowsAffected} affected`;
	}

	function oneLine(sql: string) {
		return sql.replace(/\s+/g, ' ').trim();
	}
</script>

{#if peek}
	<RelationPeek fk={peek.fk} value={peek.value} x={peek.x} y={peek.y} onclose={() => (peek = null)} />
{/if}

<svelte:window onkeydown={keydown} />

<div class="page">
	<div class="body" bind:this={container}>
		<div class="editor" style:height="{split}%">
			<SqlEditor bind:this={editor} bind:value={tab.sql} driver={app.session?.driver ?? 'postgres'} schema={app.completionSchema} onrun={run} />
		</div>
		<div class="bar" role="separator" aria-orientation="horizontal" onpointerdown={dragStart}>
			<div class="bar-left" onpointerdown={(e) => e.stopPropagation()} role="tablist" tabindex="-1">
				<button
					class="ctl icon fields"
					class:on={fieldsOpen && current !== -1}
					disabled={!result?.columns.length || current === -1}
					onclick={() => (fieldsOpen = !fieldsOpen)}
					title="Fields"><Columns3 size={14} /></button
				>
				{#each results as r, i (i)}
					<button class="rtab" class:on={i === current} class:err={!!r.error} onclick={() => selectResult(i)}>
						{#if r.error}<CircleX size={12} />{/if}
						{results.length > 1 ? `Result ${i + 1}` : 'Results'}
					</button>
				{:else}
					<button class="rtab" class:on={current !== -1} onclick={() => (current = 0)}>Results</button>
				{/each}
				<button class="rtab" class:on={current === -1} onclick={() => (current = -1)}>Messages</button>

				<span class="gap"></span>
				<ExportMenu source={exportSource} name={exportName} disabled={!exportable} />
			</div>

			<div class="bar-right" onpointerdown={(e) => e.stopPropagation()} role="group" aria-label="Editor actions">
				<button
					class="ctl icon save"
					class:unsaved
					onclick={(e) => app.saveQueryTab(tab, e.shiftKey)}
					title={saved ? `Save “${saved.name}” (${modLabel}S) · ⇧ click: save as new` : `Save query (${modLabel}S)`}
				>
					<Bookmark size={14} fill={saved ? 'currentColor' : 'none'} />
				</button>
				{#if isSQL(app.session?.driver ?? 'postgres')}<button class="ctl icon" onclick={formatSql} title="Format SQL (⇧⌥F)"><AlignLeft size={14} /></button
					>{/if}
				{#if editable && current !== -1}
					<button class="ctl" onclick={discardEdits} disabled={!dirty} title="Discard changes ({DISCARD_KEYS})">Discard <kbd>{DISCARD_KEYS}</kbd></button>
					<button
						class="ctl commit"
						onclick={commit}
						disabled={!dirty}
						title={dirty ? `Commit ${pendingCount} change${pendingCount === 1 ? '' : 's'} (${modLabel}S)` : 'Nothing to commit'}
					>
						Commit
						{#if dirty}{#key pendingCount}<span class="badge">{pendingCount}</span>{/key}{/if}
						<kbd>{modLabel}S</kbd>
					</button>
				{/if}
				<div class="run-group">
					{#if running}
						<button class="run stop" onclick={cancel}><Square size={10} fill="currentColor" /> Stop</button>
					{:else}
						<button class="run" onclick={() => run(editor.allStatements())} title="Run all (⇧⌘↵)">Run all</button>
						<button class="run primary" onclick={() => run(editor.currentStatement())} title="Run current statement (⌘↵)">
							<Play size={10} fill="currentColor" /> Run <kbd>⌘↵</kbd>
						</button>
					{/if}
				</div>
			</div>
		</div>
		<div class="results">
			<div class="result-body">
				{#if current === -1}
					<div class="log mono">
						{#each messages as m, i (i)}
							<div class="log-row" class:err={!!m.error}>
								<span class="muted">{fmtTime(m.at)}</span>
								{#if m.error}<CircleX size={12} />{:else}<CircleCheck size={12} />{/if}
								<span class="log-sql" title={m.statement}>{oneLine(m.statement)}</span>
								<span class="log-meta">{m.error ?? m.summary}</span>
								<span class="muted">{m.durationMs.toFixed(1)} ms</span>
							</div>
						{:else}
							<div class="placeholder muted">-- no statements executed yet</div>
						{/each}
					</div>
				{:else if !result}
					<div class="placeholder mono muted">-- ⌘↵ run current · ⇧⌘↵ run all</div>
				{:else if result.error}
					<div class="message err mono">
						<CircleAlert size={14} />
						<pre>{result.error}</pre>
					</div>
				{:else if result.columns.length}
					<div class="split">
						{#if fieldsOpen}
							<FieldsPanel columns={gridColumns} bind:layout onclose={() => (fieldsOpen = false)} />
						{/if}
						<div class="grid-wrap">
							<DataGrid
								columns={gridColumns}
								rows={shownRows}
								{editable}
								rowStatus={(i) => (removed.has(sortedRows[i]) ? 'deleted' : undefined)}
								cellChanged={(i, c) => !!edits.get(sortedRows[i]) && result!.columns[c] in edits.get(sortedRows[i])!}
								{onedit}
								{ondeleterows}
								{sort}
								onsort={setSort}
								bind:layout
								onactivate={(r) => (detailRow = r)}
								onerror={(m) => app.fail(m)}
								{relations}
								onpeek={(r, c, rect) => {
									const fk = relFor?.out.find((f) => f.column === result!.columns[c]);
									if (fk) peek = { fk, value: shownRows[r][c], x: rect.left, y: rect.bottom + 4 };
								}}
							/>
						</div>
						{#if detailRow !== null && sortedRows[detailRow]}
							<RecordPane
								columns={gridColumns}
								row={shownRows[detailRow]}
								title={`row ${detailRow + 1}`}
								table={resultName}
								foreignKeys={relFor?.out ?? []}
								referencedBy={relFor?.in ?? []}
								{editable}
								changed={(c) => !!edits.get(sortedRows[detailRow!]) && result!.columns[c] in edits.get(sortedRows[detailRow!])!}
								onedit={(c, v) => onedit(detailRow!, c, v)}
								onclose={() => (detailRow = null)}
							/>
						{/if}
					</div>
				{:else}
					<div class="message ok mono">
						<CircleCheck size={14} />
						<span>Query OK, {result.rowsAffected} row{result.rowsAffected === 1 ? '' : 's'} affected ({result.durationMs.toFixed(1)} ms)</span>
					</div>
				{/if}
			</div>
			{#if result?.columns.length}
				<div class="foot">
					{#key result.statement + result.durationMs}
						<span class="tf-text"
							><b>{result.rows.length.toLocaleString()}</b> rows{#if !result.cursor && result.truncated}<span class="muted"> (stopped)</span>{/if}</span
						>
						<span class="muted tf-text">{result.durationMs.toFixed(1)} ms</span>
					{/key}
					{#if result.cursor && loading}
						<span class="loading-rest"><LoaderCircle size={12} class="spin" /> loading more…</span>
						<button class="stop-link" onclick={stopLoading}>Stop</button>
					{/if}
					{#if source}<span class="muted editable">editable · {source.schema ? `${source.schema}.` : ''}{source.table}</span>{/if}
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	/* Clips side panels while they slide in and out. */
	.split {
		overflow: hidden;
	}
	.log-row {
		animation: tf-drift var(--dur) var(--ease-out) both;
	}
	.page {
		display: flex;
		flex-direction: column;
		height: 100%;
		background: var(--card);
	}
	.body {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
	}
	.editor {
		flex: none;
		min-height: 3rem;
		overflow: hidden;
	}
	.bar {
		flex: none;
		display: flex;
		align-items: center;
		gap: 0.5rem;
		height: 2.6rem;
		padding: 0 0.5rem 0 0.75rem;
		border-top: 1px solid var(--border);
		border-bottom: 1px solid var(--border);
		background: var(--background);
		cursor: row-resize;
	}
	.bar-left,
	.bar-right {
		display: flex;
		align-items: center;
		gap: 0.25rem;
		cursor: default;
	}
	.bar-right {
		margin-left: auto;
		gap: 0.4rem;
	}
	.rtab {
		display: flex;
		align-items: center;
		gap: 0.3rem;
		height: 2.6rem;
		padding: 0 0.55rem;
		border: none;
		border-bottom: 2px solid transparent;
		background: transparent;
		color: var(--muted-foreground);
		font-size: 12.5px;
		font-weight: 500;
	}
	.rtab:hover {
		color: var(--foreground);
	}
	.rtab.on {
		color: var(--foreground);
		border-bottom-color: var(--foreground);
	}
	.rtab.err {
		color: var(--destructive);
	}
	.ctl {
		display: flex;
		align-items: center;
		gap: 0.3rem;
		height: 1.75rem;
		padding: 0 0.55rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--foreground);
		font-size: 12.5px;
		font-weight: 500;
	}
	.ctl.save {
		position: relative;
	}
	.ctl.save :global(svg) {
		color: var(--brand-ink);
	}
	/* Edited since the last save. */
	.ctl.save.unsaved::after {
		content: '';
		position: absolute;
		top: 0.2rem;
		right: 0.2rem;
		width: 0.35rem;
		height: 0.35rem;
		border-radius: 50%;
		background: var(--brand-ink);
	}
	.ctl.icon {
		width: 1.75rem;
		padding: 0;
		justify-content: center;
		border-color: transparent;
		background: transparent;
		color: var(--muted-foreground);
	}
	.ctl:hover,
	.ctl.on {
		background: var(--accent);
		color: var(--foreground);
	}
	.bar-left .ctl:not(.icon) {
		margin-left: 0.5rem;
	}
	.bar-left .fields {
		margin-right: 0.25rem;
	}
	.loading-rest {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		color: var(--muted-foreground);
	}
	.loading-rest :global(.spin) {
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	.stop-link {
		padding: 0 0.35rem;
		border: 1px solid var(--border);
		border-radius: 0.25rem;
		background: transparent;
		color: var(--foreground);
		font-size: 11px;
		line-height: 1.2rem;
	}
	.stop-link:hover {
		background: var(--accent);
	}
	.ctl:disabled {
		opacity: 0.4;
		background: transparent;
		pointer-events: none;
	}
	.rtab:focus-visible {
		outline: none;
		color: var(--foreground);
	}
	.gap {
		width: 0.5rem;
	}
	.ctl:disabled {
		opacity: 0.4;
		cursor: default;
	}
	.ctl kbd {
		padding: 0 0.25rem;
		border-radius: 0.2rem;
		background: color-mix(in oklch, currentColor 12%, transparent);
		font-family: var(--font-mono);
		font-size: 10px;
		line-height: 1.05rem;
		opacity: 0.75;
	}
	.ctl.commit {
		background: var(--brand);
		border-color: var(--brand);
		color: #fff;
	}
	.ctl.commit:hover:not(:disabled) {
		filter: brightness(1.15);
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
	.editable {
		margin-left: auto;
	}
	.run-group {
		display: flex;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
	}
	.run {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		height: 1.75rem;
		padding: 0 0.65rem;
		border: none;
		border-right: 1px solid var(--border);
		background: var(--card);
		color: var(--foreground);
		font-size: 12.5px;
		font-weight: 500;
	}
	.run:last-child {
		border-right: none;
	}
	.run:hover {
		background: var(--accent);
	}
	.run.primary {
		background: var(--brand);
		color: #fff;
	}
	.run.primary:hover {
		filter: brightness(1.15);
	}
	.run kbd {
		height: 1.05rem;
		border: none;
		background: oklch(1 0 0 / 0.16);
		color: inherit;
	}
	.run.stop {
		color: var(--destructive);
	}
	.results {
		flex: 1;
		min-height: 3rem;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}
	.result-body {
		flex: 1;
		min-height: 0;
	}
	.split {
		display: flex;
		height: 100%;
	}
	.foot {
		display: flex;
		align-items: center;
		gap: 0.9rem;
		min-height: 2.1rem;
		padding: 0 0.75rem;
		border-top: 1px solid var(--border);
		font-size: var(--text-sm);
		color: var(--muted-foreground);
		font-variant-numeric: tabular-nums;
	}
	.foot b {
		color: var(--foreground);
		font-weight: 500;
	}
	.grid-wrap {
		flex: 1;
		min-width: 0;
	}
	.placeholder {
		padding: 0.9rem 1rem;
		font-size: var(--code);
	}
	.log {
		height: 100%;
		overflow: auto;
		padding: 0.25rem 0;
		font-size: 11.5px;
	}
	.log-row {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		height: 1.6rem;
		padding: 0 0.75rem;
		white-space: nowrap;
	}
	.log-row:hover {
		background: color-mix(in oklch, var(--accent) 55%, var(--card));
	}
	.log-row :global(svg) {
		flex: none;
		color: var(--success);
	}
	.log-row.err,
	.log-row.err :global(svg) {
		color: var(--destructive);
	}
	.log-sql {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		user-select: text;
	}
	.log-meta {
		max-width: 40%;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.message {
		display: flex;
		gap: 0.6rem;
		align-items: flex-start;
		margin: 0.75rem;
		padding: 0.65rem 0.8rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		user-select: text;
	}
	.message :global(svg) {
		margin-top: 1px;
		flex: none;
	}
	.message.err {
		border-color: color-mix(in oklch, var(--destructive) 35%, var(--border));
		color: var(--destructive);
	}
	.message.ok :global(svg) {
		color: var(--success);
	}
	.message pre {
		margin: 0;
		font: inherit;
		white-space: pre-wrap;
		word-break: break-word;
	}
</style>
