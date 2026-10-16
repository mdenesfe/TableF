<script lang="ts" module>
	import type { Cell } from '#lib/types.ts';

	export interface GridColumn {
		name: string;
		type?: string;
		primaryKey?: boolean;
		nullable?: boolean;
	}
	export type RowStatus = 'new' | 'deleted' | undefined;

	/** How columns are arranged; keyed by column name so it survives reloads. */
	export interface GridLayout {
		order: string[];
		hidden: string[];
		pinned: string[];
		widths: Record<string, number>;
	}
	export const emptyLayout = (): GridLayout => ({ order: [], hidden: [], pinned: [], widths: {} });

	/** One line of the body: a data row, a group header, or a "load more" line inside a group. */
	export type GridItem =
		| { kind: 'row'; r: number; depth?: number }
		| { kind: 'group'; key: string; depth: number; column: string; value: Cell; count: number; open: boolean; loading?: boolean }
		| { kind: 'more'; key: string; depth: number; loaded: number; total: number; loading?: boolean; error?: string };

	/** A pasted cell, already mapped to its real column index. */
	export interface PastedCell {
		c: number;
		v: string;
	}

	export const COLUMN_MIME = 'application/x-tablef-column';
</script>

<script lang="ts">
	import { tick } from 'svelte';
	import { prefs, rowHeights } from '#lib/prefs.svelte.ts';
	import { boolText, isNumber, kindOf, now, numericOnly, uuid, type CellKind } from '#lib/celltype.ts';
	import DatePicker from './DatePicker.svelte';
	import { drag } from '#lib/drag.ts';
	import {
		ArrowDown,
		ArrowUp,
		ArrowUpRight,
		Braces,
		CalendarDays,
		ChevronRight,
		CircleSlash,
		Clock,
		Copy,
		Fingerprint,
		KeyRound,
		Link2,
		LoaderCircle,
		Trash2
	} from '@lucide/svelte';

	let {
		columns,
		rows,
		items,
		editable = false,
		rowStatus = () => undefined,
		cellChanged = () => false,
		onedit,
		ondeleterows,
		sort,
		onsort,
		selectedRows = $bindable(new Set<number>()),
		layout = $bindable(emptyLayout()),
		rowOffset = 0,
		onactivate,
		onpastecells,
		ongrouptoggle,
		onmore,
		relations = {},
		onpeek,
		onerror
	}: {
		columns: GridColumn[];
		rows: Cell[][];
		/** Display order with groups; defaults to every row in order. */
		items?: GridItem[];
		editable?: boolean;
		rowStatus?: (i: number) => RowStatus;
		cellChanged?: (i: number, c: number) => boolean;
		onedit?: (row: number, col: number, value: Cell) => void;
		ondeleterows?: (rows: number[]) => void;
		sort?: { column: string; dir: 'asc' | 'desc' | '' };
		onsort?: (column: string) => void;
		selectedRows?: Set<number>;
		layout?: GridLayout;
		rowOffset?: number;
		/** Called with the row index whenever the active (focused) row changes. */
		onactivate?: (row: number) => void;
		/** Clipboard grid pasted at the active row; one inner array per pasted line. */
		onpastecells?: (row: number, lines: PastedCell[][]) => void;
		ongrouptoggle?: (key: string) => void;
		onmore?: (key: string) => void;
		/** Reports a value the grid refused, e.g. text in a numeric column. */
		onerror?: (message: string) => void;
		/** Foreign-key columns: column name → "table.column" it references. */
		relations?: Record<string, string>;
		/** Follow a foreign key from a cell; rect is the cell's position on screen. */
		onpeek?: (row: number, col: number, rect: DOMRect) => void;
	} = $props();

	const ROW_H = $derived(rowHeights[prefs.value.density]);
	const OVERSCAN = 12;
	const CHAR = 7.2;

	let viewport = $state<HTMLDivElement>();
	let scrollTop = $state(0);
	let viewH = $state(400);
	let viewW = $state(800);
	let active = $state<{ r: number; c: number } | null>(null);
	/** Moves the active cell and tells the parent (called directly, not from an effect,
	 *  so whatever the parent reads doesn't become a dependency of the grid). */
	function activate(r: number, c: number) {
		const changed = active?.r !== r;
		active = { r, c };
		if (changed) onactivate?.(r);
	}
	let editing = $state<{ r: number; c: number; value: string } | null>(null);
	let menu = $state<{ x: number; y: number; r: number; c: number } | null>(null);
	let anchorRow = -1;

	// ---------- columns ----------

	function shortType(t?: string) {
		return (t ?? '')
			.replace(/character varying/i, 'varchar')
			.replace(/timestamp with(out)? time zone/i, (m) => (m.includes('out') ? 'timestamp' : 'timestamptz'))
			.replace(/double precision/i, 'float8');
	}

	function display(v: Cell): string {
		if (v === null || v === undefined) return prefs.value.nullText;
		if (typeof v === 'string') return v.length > 300 ? v.slice(0, 300) + '…' : v;
		return String(v);
	}

	function autoWidth(ci: number, sample: Cell[][]) {
		const col = columns[ci];
		let w = (col.name.length + shortType(col.type).length) * CHAR + (col.primaryKey ? 18 : 0) + 56;
		for (const r of sample) w = Math.max(w, display(r[ci]).length * CHAR + 24);
		return Math.round(Math.min(Math.max(w, 72), 420));
	}

	// Auto widths are measured once per column set; user widths live in layout.widths.
	let autoWidths = $state<Record<string, number>>({});
	let measuredFor = '';
	$effect(() => {
		const key = columns.map((c) => c.name).join('\u0000');
		if (key === measuredFor || !rows.length) return;
		measuredFor = key;
		const sample = rows.slice(0, 80);
		autoWidths = Object.fromEntries(columns.map((c, i) => [c.name, autoWidth(i, sample)]));
	});

	const view = $derived.by(() => {
		// Sets keep this O(columns) instead of O(columns²).
		const index = new Map(columns.map((c, i) => [c.name, i]));
		const inOrder = new Set(layout.order);
		const hidden = new Set(layout.hidden);
		const ordered = [...layout.order.filter((n) => index.has(n)), ...columns.map((c) => c.name).filter((n) => !inOrder.has(n))];
		const visible = ordered.filter((n) => !hidden.has(n));
		const shown = new Set(visible);
		const pinned = layout.pinned.filter((n) => shown.has(n));
		const isPinned = new Set(pinned);
		const names = [...pinned, ...visible.filter((n) => !isPinned.has(n))];
		return { cols: names.map((n) => index.get(n)!), pinnedCount: pinned.length };
	});
	// While a column edge is dragged only this changes; the layout is written once on release.
	let liveWidth = $state<{ name: string; w: number } | null>(null);
	const widthOf = (ci: number) => {
		const name = columns[ci].name;
		return liveWidth?.name === name ? liveWidth.w : (layout.widths[name] ?? autoWidths[name] ?? 140);
	};
	const leadW = $derived(Math.max(44, String(rows.length + rowOffset).length * CHAR + 24));
	const template = $derived(`${leadW}px ${view.cols.map((ci) => widthOf(ci) + 'px').join(' ')}`);
	const pinLeft = $derived.by(() => {
		const out: number[] = [];
		let x = leadW;
		for (let k = 0; k < view.pinnedCount; k++) {
			out.push(x);
			x += widthOf(view.cols[k]);
		}
		return out;
	});
	const totalW = $derived(leadW + view.cols.reduce((s, ci) => s + widthOf(ci), 0));

	// ---------- load animation ----------
	// New data drifts in row by row. Only rows on screen at load time animate,
	// so scrolling later doesn't replay it.
	let entering = $state(false);
	let enterTimer: ReturnType<typeof setTimeout> | undefined;
	let lastRows: Cell[][] | undefined;
	$effect(() => {
		const r = rows;
		if (r === lastRows) return;
		const first = lastRows === undefined || lastRows.length === 0 || r.length === 0 || r[0] !== lastRows[0];
		lastRows = r;
		// New data: an active cell or editor past the end would point at nothing.
		if (active && active.r >= r.length) active = null;
		if (editing && editing.r >= r.length) editing = null;
		if (!first || !r.length) return;
		entering = true;
		clearTimeout(enterTimer);
		enterTimer = setTimeout(() => (entering = false), 700);
	});

	// ---------- body items ----------

	// Without grouping, item i is row i: positions are the identity and nothing
	// per row is allocated, so edits and reloads don't pay O(rows) here.
	// With grouping, the lookups are built once per change of `items`.
	const count = $derived(items ? items.length : rows.length);
	const itemAt = (i: number): GridItem => (items ? items[i] : { kind: 'row', r: i });
	const grouped = $derived.by(() => {
		if (!items) return null;
		const order: number[] = [];
		const pos = new Map<number, number>();
		const item = new Map<number, number>();
		items.forEach((it, i) => {
			if (it.kind !== 'row') return;
			pos.set(it.r, order.length);
			item.set(it.r, i);
			order.push(it.r);
		});
		return { order, pos, item };
	});
	/** Number of rows in display order. */
	const rowTotal = $derived(grouped ? grouped.order.length : rows.length);
	const posOf = (r: number) => (grouped ? (grouped.pos.get(r) ?? 0) : r);
	const rowAt = (p: number) => (grouped ? grouped.order[p] : p);
	const itemOf = (r: number) => (grouped ? (grouped.item.get(r) ?? 0) : r);
	/** Row indexes in display order (built on demand, for select-all and copy). */
	const displayOrder = () => (grouped ? grouped.order : Array.from({ length: rows.length }, (_, i) => i));

	const start = $derived(Math.max(0, Math.floor(scrollTop / ROW_H) - OVERSCAN));
	const end = $derived(Math.min(count, Math.ceil((scrollTop + viewH) / ROW_H) + OVERSCAN));
	const visible = $derived(Array.from({ length: Math.max(0, end - start) }, (_, k) => ({ it: itemAt(start + k), i: start + k })));

	const mod = (e: KeyboardEvent | MouseEvent) => e.metaKey || e.ctrlKey;
	const pageRows = $derived(Math.max(1, Math.floor(viewH / ROW_H) - 2));

	/** Row index `delta` displayed rows away from r. */
	function stepRow(r: number, delta: number) {
		return rowAt(Math.max(0, Math.min(rowTotal - 1, posOf(r) + delta)));
	}
	/** Column index `delta` visible columns away from c. */
	function stepCol(c: number, delta: number) {
		const p = Math.max(0, view.cols.indexOf(c));
		return view.cols[Math.max(0, Math.min(view.cols.length - 1, p + delta))];
	}

	function selectRange(a: number, b: number) {
		const pa = posOf(a);
		const pb = posOf(b);
		const next = new Set<number>();
		for (let p = Math.min(pa, pb); p <= Math.max(pa, pb); p++) next.add(rowAt(p));
		selectedRows = next;
	}

	function selectRow(i: number, e: MouseEvent) {
		if (e.shiftKey && anchorRow >= 0) return selectRange(anchorRow, i);
		const next = new Set(mod(e) ? selectedRows : []);
		if (mod(e) && next.has(i)) next.delete(i);
		else {
			next.add(i);
			anchorRow = i;
		}
		selectedRows = next;
	}

	const allSelected = $derived(rowTotal > 0 && selectedRows.size === rowTotal);
	function toggleAll() {
		selectedRows = allSelected ? new Set() : new Set(displayOrder());
	}

	function clickCell(r: number, c: number, e: MouseEvent) {
		activate(r, c);
		selectRow(r, e);
		viewport?.focus();
	}

	// Drag across row numbers to select a range; ⇧ extends, ⌘ toggles.
	let dragFrom = -1;
	function leadDown(i: number, e: PointerEvent) {
		if (e.button !== 0) return;
		e.preventDefault();
		viewport?.focus();
		if (e.shiftKey && anchorRow >= 0) return selectRange(anchorRow, i);
		if (mod(e)) {
			const next = new Set(selectedRows);
			if (next.has(i)) next.delete(i);
			else next.add(i);
			selectedRows = next;
			anchorRow = i;
			return;
		}
		dragFrom = anchorRow = i;
		activate(i, active?.c ?? view.cols[0]);
		selectRange(i, i);
		const up = () => {
			dragFrom = -1;
			window.removeEventListener('pointerup', up);
		};
		window.addEventListener('pointerup', up);
	}
	function leadEnter(i: number) {
		if (dragFrom >= 0) selectRange(dragFrom, i);
	}

	// ---------- editing ----------

	async function startEdit(r: number, c: number, initial?: string) {
		if (!editable || rowStatus(r) === 'deleted') return;
		const v = rows[r][c];
		editing = { r, c, value: initial ?? (v === null ? '' : String(v)) };
		await tick();
		const el = viewport?.querySelector<HTMLInputElement | HTMLSelectElement>('.cell-editor');
		el?.focus();
		if (initial === undefined && el instanceof HTMLInputElement) el.select();
	}

	const kindAt = (c: number): CellKind => kindOf(columns[c]?.type);
	const isDate = (k: CellKind) => k === 'date' || k === 'datetime' || k === 'time';

	// Calendar for date/time cells, opened from the inline editor.
	let picking = $state<{ r: number; c: number; kind: CellKind; x: number; y: number; value: string } | null>(null);
	function openPicker(e: MouseEvent) {
		if (!editing) return;
		const cell = (e.currentTarget as HTMLElement).closest('.cell') as HTMLElement;
		const rect = cell.getBoundingClientRect();
		picking = { r: editing.r, c: editing.c, kind: kindAt(editing.c), x: rect.left, y: rect.bottom + 2, value: editing.value };
	}

	function commitEdit(value: Cell | undefined = editing?.value) {
		if (!editing) return;
		const { r, c } = editing;
		// Numeric columns only take numbers; empty means NULL.
		if (kindAt(c) === 'number' && typeof value === 'string') {
			if (value.trim() === '') value = columns[c]?.nullable === false ? undefined : null;
			else if (!isNumber(value)) {
				editing = null;
				onerror?.(`${columns[c].name} needs a number`);
				viewport?.focus();
				return;
			}
		}
		editing = null;
		const old = rows[r][c];
		const unchanged = old === null ? value === '' || value === null : String(value) === String(old);
		if (value !== undefined && !unchanged) onedit?.(r, c, value);
		viewport?.focus();
	}

	function editorKey(e: KeyboardEvent) {
		if (!editing) return;
		const { r, c } = editing;
		if (e.key === 'Enter') {
			e.preventDefault();
			commitEdit();
			moveTo(stepRow(r, e.shiftKey ? -1 : 1), c);
		} else if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			editing = null;
			viewport?.focus();
		} else if (e.key === 'Tab') {
			e.preventDefault();
			commitEdit();
			const nc = stepCol(c, e.shiftKey ? -1 : 1);
			if (nc !== c) {
				activate(r, nc);
				startEdit(r, nc);
			}
		}
	}

	function scrollIntoView(r: number, c?: number) {
		if (!viewport) return;
		const top = itemOf(r) * ROW_H;
		if (top < viewport.scrollTop) viewport.scrollTop = top;
		else if (top + ROW_H * 2 > viewport.scrollTop + viewH) viewport.scrollTop = top + ROW_H * 2 - viewH;

		// Horizontally: keep the cell clear of the row numbers and pinned columns.
		if (c === undefined) return;
		const k = view.cols.indexOf(c);
		if (k < view.pinnedCount || k < 0) return;
		let left = leadW;
		for (let j = 0; j < k; j++) left += widthOf(view.cols[j]);
		const right = left + widthOf(c);
		const covered = pinLeft.length ? pinLeft[pinLeft.length - 1] + widthOf(view.cols[view.pinnedCount - 1]) : leadW;
		if (left - covered < viewport.scrollLeft) viewport.scrollLeft = left - covered;
		else if (right > viewport.scrollLeft + viewW) viewport.scrollLeft = right - viewW;
	}

	/** Moves the active cell; with extend the row selection grows from the anchor. */
	function moveTo(nr: number | undefined, nc: number | undefined, extend = false) {
		if (nr === undefined || nc === undefined) return;
		activate(nr, nc);
		if (extend && anchorRow >= 0) selectRange(anchorRow, nr);
		else {
			selectedRows = new Set([nr]);
			anchorRow = nr;
		}
		scrollIntoView(nr, nc);
	}

	function gridKey(e: KeyboardEvent) {
		if (editing) return;
		if (!active) {
			if (rowTotal && /^(Arrow|Home|End|Page)/.test(e.key)) {
				e.preventDefault();
				moveTo(rowAt(0), view.cols[0]);
			}
			return;
		}
		const { r, c } = active;
		const m = mod(e);
		const firstCol = view.cols[0];
		const lastCol = view.cols[view.cols.length - 1];
		switch (e.key) {
			case 'ArrowDown':
				moveTo(m ? rowAt(rowTotal - 1) : stepRow(r, 1), c, e.shiftKey);
				break;
			case 'ArrowUp':
				moveTo(m ? rowAt(0) : stepRow(r, -1), c, e.shiftKey);
				break;
			case 'ArrowLeft':
				moveTo(r, m ? firstCol : stepCol(c, -1), e.shiftKey);
				break;
			case 'ArrowRight':
				moveTo(r, m ? lastCol : stepCol(c, 1), e.shiftKey);
				break;
			case 'Home':
				moveTo(m ? rowAt(0) : r, firstCol, e.shiftKey);
				break;
			case 'End':
				moveTo(m ? rowAt(rowTotal - 1) : r, lastCol, e.shiftKey);
				break;
			case 'PageDown':
				moveTo(stepRow(r, pageRows), c, e.shiftKey);
				break;
			case 'PageUp':
				moveTo(stepRow(r, -pageRows), c, e.shiftKey);
				break;
			case 'Tab':
				if (e.shiftKey) moveTo(c === firstCol ? stepRow(r, -1) : r, c === firstCol ? lastCol : stepCol(c, -1));
				else moveTo(c === lastCol ? stepRow(r, 1) : r, c === lastCol ? firstCol : stepCol(c, 1));
				break;
			case 'Enter':
			case 'F2':
				startEdit(r, c);
				break;
			case 'ContextMenu':
			case 'F10': {
				if (e.key === 'F10' && !e.shiftKey) return;
				// Keyboard access to the context menu, at the active cell.
				const cell = viewport?.querySelector<HTMLElement>('.cell.active');
				const rect = cell?.getBoundingClientRect();
				menu = { x: rect ? rect.left + 8 : 0, y: rect ? rect.bottom : 0, r, c };
				break;
			}
			case 'Escape':
				if (menu) {
					menu = null;
					break;
				}
				if (selectedRows.size <= 1) return;
				selectedRows = new Set([r]);
				break;
			case 'Backspace':
			case 'Delete':
				if (!editable || !selectedRows.size) return;
				ondeleterows?.([...selectedRows]);
				break;
			case 'a':
			case 'A':
				if (!m) return startTyping(e);
				selectedRows = new Set(displayOrder());
				break;
			default:
				return startTyping(e);
		}
		e.preventDefault();
	}

	// Typing a printable character starts editing the active cell with it, like a spreadsheet.
	function startTyping(e: KeyboardEvent) {
		if (!active || !editable || mod(e) || e.altKey || e.key.length !== 1) return;
		e.preventDefault();
		// Space flips a boolean cell instead of typing into it.
		if (e.key === ' ' && kindAt(active.c) === 'bool' && rowStatus(active.r) !== 'deleted') {
			onedit?.(active.r, active.c, boolText(rows[active.r][active.c]) !== 'true');
			return;
		}
		startEdit(active.r, active.c, e.key);
	}

	// ---------- clipboard ----------

	const cellText = (v: Cell) => (v === null ? '' : String(v));

	function selectionText(): string {
		if (!active) return '';
		if (selectedRows.size > 1)
			return displayOrder()
				.filter((i) => selectedRows.has(i))
				.map((i) => view.cols.map((ci) => cellText(rows[i][ci])).join('\t'))
				.join('\n');
		return cellText(rows[active.r][active.c]);
	}

	function oncopy(e: ClipboardEvent) {
		if (editing || !active) return;
		e.preventDefault();
		e.clipboardData?.setData('text/plain', selectionText());
	}

	function onpaste(e: ClipboardEvent) {
		if (editing || !active || !editable || !onpastecells) return;
		const text = e.clipboardData?.getData('text/plain') ?? '';
		if (!text) return;
		e.preventDefault();
		// Pasted columns follow the visible order; rows follow the displayed order.
		const startCol = view.cols.indexOf(active.c);
		const lines = text
			.replace(/\r/g, '')
			.replace(/\n$/, '')
			.split('\n')
			.map((line) =>
				line
					.split('\t')
					.map((v, dc) => ({ c: view.cols[startCol + dc], v }))
					.filter((x) => x.c !== undefined)
			);
		onpastecells(active.r, lines);
	}

	async function copy() {
		await navigator.clipboard?.writeText(selectionText()).catch(() => {});
	}

	async function copyRowJSON(r: number) {
		const obj = Object.fromEntries(view.cols.map((ci) => [columns[ci].name, rows[r][ci]]));
		await navigator.clipboard?.writeText(JSON.stringify(obj, null, 2)).catch(() => {});
	}

	function openMenu(e: MouseEvent, r: number, c: number) {
		e.preventDefault();
		if (!selectedRows.has(r)) selectedRows = new Set([r]);
		activate(r, c);
		menu = { x: e.clientX, y: e.clientY, r, c };
	}

	// ---------- column sizing and order ----------

	function setWidth(name: string, w: number) {
		layout = { ...layout, widths: { ...layout.widths, [name]: Math.round(w) } };
	}

	// The click that ends a resize lands on the header button; it must not sort.
	let resizing = false;

	function resizeStart(e: PointerEvent, ci: number) {
		const x0 = e.clientX;
		const w0 = widthOf(ci);
		const name = columns[ci].name;
		resizing = true;
		drag(
			e,
			(ev) => (liveWidth = { name, w: Math.max(56, w0 + ev.clientX - x0) }),
			() => {
				if (liveWidth) setWidth(name, liveWidth.w);
				liveWidth = null;
				setTimeout(() => (resizing = false));
			}
		);
	}

	// Double-click a column edge to fit it to its contents.
	function autofit(ci: number) {
		setWidth(columns[ci].name, Math.min(800, autoWidth(ci, rows)));
	}

	let dropTarget = $state('');
	function dragStart(e: DragEvent, name: string) {
		e.dataTransfer?.setData(COLUMN_MIME, name);
		e.dataTransfer?.setData('text/plain', name);
		if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copyMove';
	}
	function dragOver(e: DragEvent, name: string) {
		if (!e.dataTransfer?.types.includes(COLUMN_MIME)) return;
		e.preventDefault();
		dropTarget = name;
	}
	function drop(e: DragEvent, target: string) {
		const name = e.dataTransfer?.getData(COLUMN_MIME);
		dropTarget = '';
		if (!name || name === target) return;
		e.preventDefault();
		const names = view.cols.map((ci) => columns[ci].name);
		const all = [...names, ...columns.map((c) => c.name).filter((n) => !names.includes(n))].filter((n) => n !== name);
		all.splice(all.indexOf(target), 0, name);
		// Dropping into or out of the pinned block pins or unpins the column.
		const pinned = layout.pinned.filter((n) => n !== name);
		if (layout.pinned.includes(target)) pinned.splice(pinned.indexOf(target), 0, name);
		layout = { ...layout, order: all, pinned, hidden: layout.hidden.filter((n) => n !== name) };
	}

	const isPinned = (k: number) => k < view.pinnedCount;
</script>

<svelte:window onclick={() => (menu = null)} />

{#snippet groupValue(v: Cell)}
	{#if v === null}<span class="g-empty">{prefs.value.nullText}</span>{:else}<span class="g-value">{display(v)}</span>{/if}
{/snippet}

<div
	class="grid"
	class:entering
	style:--row-h="{ROW_H}px"
	bind:this={viewport}
	bind:clientHeight={viewH}
	bind:clientWidth={viewW}
	onscroll={(e) => (scrollTop = e.currentTarget.scrollTop)}
	onkeydown={gridKey}
	{oncopy}
	{onpaste}
	tabindex="0"
	role="grid"
	aria-rowcount={count + 1}
	aria-colcount={view.cols.length}
>
	<div class="header" role="row" aria-rowindex={1} style:grid-template-columns={template}>
		<button class="hcell lead" onclick={toggleAll} title="Select all (⌘A)">#</button>
		{#each view.cols as ci, k (columns[ci].name)}
			{@const col = columns[ci]}
			{@const sorted = sort?.column === col.name && !!sort.dir}
			<button
				class="hcell"
				role="columnheader"
				aria-sort={sorted ? (sort?.dir === 'asc' ? 'ascending' : 'descending') : 'none'}
				class:sorted
				class:pinned={isPinned(k)}
				class:pin-edge={k === view.pinnedCount - 1}
				class:drop={dropTarget === col.name}
				style:left={isPinned(k) ? `${pinLeft[k]}px` : undefined}
				title={col.type ? `${col.name} · ${col.type}` : col.name}
				draggable="true"
				ondragstart={(e) => dragStart(e, col.name)}
				ondragover={(e) => dragOver(e, col.name)}
				ondragleave={() => dropTarget === col.name && (dropTarget = '')}
				ondrop={(e) => drop(e, col.name)}
				onclick={() => !resizing && onsort?.(col.name)}
			>
				{#if col.primaryKey}<span class="icon-box" title="Primary key"><KeyRound /></span>{/if}
				{#if relations[col.name]}<span class="icon-box" title={`→ ${relations[col.name]}`}><Link2 /></span>{/if}
				<span class="hname">{col.name}</span>
				{#if col.type}<span class="htype">{shortType(col.type)}</span>{/if}
				{#if sorted}<span class="sort"
						>{#if sort?.dir === 'asc'}<ArrowUp size={12} />{:else}<ArrowDown size={12} />{/if}</span
					>{/if}
				<span
					class="resize"
					role="separator"
					aria-orientation="vertical"
					draggable="false"
					onpointerdown={(e) => resizeStart(e, ci)}
					ondblclick={(e) => (e.stopPropagation(), autofit(ci))}
				></span>
			</button>
		{/each}
	</div>

	<div class="body" style:height="{count * ROW_H}px" style:width="{totalW}px">
		{#each visible as { it, i } (it.kind === 'row' ? `r${it.r}` : `${it.kind}:${it.key}`)}
			{#if it.kind === 'group'}
				<div class="row group" class:open={it.open} class:deep={it.depth > 0} style:top="{i * ROW_H}px" style:--i={i - (visible[0]?.i ?? 0)}>
					<button class="g-head" style:padding-left="{10 + it.depth * 20}px" onclick={() => ongrouptoggle?.(it.key)} aria-expanded={it.open}>
						{#if it.loading}<LoaderCircle size={13} class="spin" />{:else}<ChevronRight size={13} class="chev" />{/if}
						<span class="g-col">{it.column}</span>
						{@render groupValue(it.value)}
						<span class="g-count">{it.count.toLocaleString()}</span>
					</button>
				</div>
			{:else if it.kind === 'more'}
				<div class="row more" style:top="{i * ROW_H}px">
					<span class="g-head static" style:padding-left="{30 + it.depth * 20}px">
						<span class="muted">{it.loaded.toLocaleString()} / {it.total.toLocaleString()} rows</span>
						{#if it.error}
							<span class="err">{it.error}</span>
							<button class="link" onclick={() => onmore?.(it.key)}>Retry</button>
						{:else}
							<button class="link" onclick={() => onmore?.(it.key)} disabled={it.loading}>{it.loading ? 'Loading…' : 'Load more'}</button>
						{/if}
					</span>
				</div>
			{:else}
				{@const r = it.r}
				{@const row = rows[r]}
				{@const status = rowStatus(r)}
				<div
					class="row"
					role="row"
					aria-rowindex={i + 2}
					aria-selected={selectedRows.has(r)}
					class:odd={prefs.value.zebra && i % 2 === 1}
					class:selected={selectedRows.has(r)}
					class:new={status === 'new'}
					class:deleted={status === 'deleted'}
					style:top="{i * ROW_H}px"
					style:--i={i - (visible[0]?.i ?? 0)}
					style:grid-template-columns={template}
				>
					<button class="cell lead" tabindex="-1" onpointerdown={(e) => leadDown(r, e)} onpointerenter={() => leadEnter(r)}>
						{status === 'new' ? '+' : items ? posOf(r) + 1 : r + 1 + rowOffset}
					</button>
					{#each view.cols as ci, k (ci)}
						{@const v = row[ci]}
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<div
							class="cell"
							role="gridcell"
							tabindex="-1"
							class:pinned={isPinned(k)}
							class:pin-edge={k === view.pinnedCount - 1}
							style:left={isPinned(k) ? `${pinLeft[k]}px` : undefined}
							class:null={v === null}
							class:num={typeof v === 'number'}
							class:changed={cellChanged(r, ci)}
							class:rel={!!relations[columns[ci].name]}
							class:active={active?.r === r && active?.c === ci}
							onclick={(e) => clickCell(r, ci, e)}
							ondblclick={() => startEdit(r, ci)}
							oncontextmenu={(e) => openMenu(e, r, ci)}
						>
							{#if editing?.r === r && editing?.c === ci}
								{@const kind = kindAt(ci)}
								{#if kind === 'bool'}
									<select
										class="cell-editor"
										value={boolText(v)}
										onchange={(e) => commitEdit(e.currentTarget.value === 'null' ? null : e.currentTarget.value === 'true')}
										onkeydown={editorKey}
										onblur={() => editing && (editing = null)}
									>
										<option value="true">true</option>
										<option value="false">false</option>
										{#if columns[ci].nullable !== false}<option value="null">NULL</option>{/if}
									</select>
								{:else}
									<input
										class="cell-editor"
										class:with-tool={isDate(kind)}
										class:invalid={kind === 'number' && editing.value.trim() !== '' && !isNumber(editing.value)}
										inputmode={kind === 'number' ? 'decimal' : undefined}
										onbeforeinput={kind === 'number' ? numericOnly : undefined}
										bind:value={editing.value}
										onkeydown={editorKey}
										onblur={(e) => !(e.relatedTarget as HTMLElement | null)?.closest('.cell-tool') && !picking && commitEdit()}
									/>
									{#if isDate(kind)}
										<button class="cell-tool" tabindex="-1" onmousedown={(e) => e.preventDefault()} onclick={openPicker} title="Pick a date"
											><CalendarDays size={12} /></button
										>
									{/if}
								{/if}
							{:else}
								{display(v)}
								{#if v !== null && relations[columns[ci].name] && onpeek}
									<button
										class="peek"
										tabindex="-1"
										title={`Show ${relations[columns[ci].name]}`}
										onclick={(e) => {
											e.stopPropagation();
											onpeek(r, ci, (e.currentTarget as HTMLElement).closest('.cell')!.getBoundingClientRect());
										}}><ArrowUpRight size={13} strokeWidth={2.5} /></button
									>
								{/if}
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		{/each}
	</div>

	{#if count === 0}
		<div class="empty">No rows</div>
	{/if}
</div>

{#if menu}
	{@const m = menu}
	<div class="menu" style:left="{m.x}px" style:top="{m.y}px" role="menu">
		<button role="menuitem" onclick={() => copy()}><Copy size={14} /> Copy</button>
		<button role="menuitem" onclick={() => copyRowJSON(m.r)}><Braces size={14} /> Copy as JSON</button>
		{#if editable}
			<div class="sep"></div>
			<button role="menuitem" onclick={() => onedit?.(m.r, m.c, null)}><CircleSlash size={14} /> Set NULL</button>
			{#if isDate(kindAt(m.c))}
				<button role="menuitem" onclick={() => onedit?.(m.r, m.c, now(kindAt(m.c)))}><Clock size={14} /> Set to now</button>
			{:else if kindAt(m.c) === 'uuid'}
				<button role="menuitem" onclick={() => onedit?.(m.r, m.c, uuid())}><Fingerprint size={14} /> Generate UUID</button>
			{:else if kindAt(m.c) === 'bool'}
				<button role="menuitem" onclick={() => onedit?.(m.r, m.c, true)}>Set true</button>
				<button role="menuitem" onclick={() => onedit?.(m.r, m.c, false)}>Set false</button>
			{/if}
			<div class="sep"></div>
			<button role="menuitem" class="danger" onclick={() => ondeleterows?.([...selectedRows])}>
				<Trash2 size={14} /> Delete {selectedRows.size > 1 ? `${selectedRows.size} rows` : 'row'}
			</button>
		{/if}
	</div>
{/if}

{#if picking}
	{@const p = picking}
	<DatePicker
		kind={p.kind}
		value={p.value}
		x={p.x}
		y={p.y}
		nullable={columns[p.c]?.nullable !== false}
		onpick={(v) => {
			picking = null;
			if (editing) editing = null;
			const old = rows[p.r][p.c];
			if (v !== old) onedit?.(p.r, p.c, v);
			viewport?.focus();
		}}
		onclose={() => {
			picking = null;
			viewport?.focus();
		}}
	/>
{/if}

<style>
	.grid {
		--zebra: color-mix(in oklch, var(--foreground) 3%, var(--card));
		--hover: color-mix(in oklch, var(--foreground) 6%, var(--card));
		position: relative;
		overflow: auto;
		height: 100%;
		outline: none;
		font-family: var(--font-mono);
		font-size: 12px;
		background: var(--card);
	}
	.header {
		position: sticky;
		top: 0;
		z-index: 3;
		display: grid;
		width: max-content;
		min-width: 100%;
		background: var(--muted);
		border-bottom: 1px solid var(--border);
	}
	.hcell {
		position: relative;
		display: flex;
		align-items: center;
		gap: 0.4rem;
		height: 2rem;
		padding: 0 0.55rem;
		border: none;
		border-right: 1px solid var(--border);
		background: var(--muted);
		color: var(--muted-foreground);
		font: inherit;
		font-family: var(--font-sans);
		font-weight: 500;
		text-align: left;
		overflow: hidden;
		cursor: default;
	}
	.hcell:hover,
	.hcell.sorted {
		color: var(--foreground);
	}
	.hcell.pinned,
	.cell.pinned {
		position: sticky;
		z-index: 1;
	}
	.hcell.pinned {
		z-index: 4;
	}
	.pin-edge {
		box-shadow: inset -1px 0 0 color-mix(in oklch, var(--foreground) 22%, transparent);
	}
	.hcell.drop {
		box-shadow: inset 2px 0 0 var(--foreground);
	}
	.hcell.lead,
	.cell.lead {
		position: sticky;
		left: 0;
		z-index: 2;
		justify-content: flex-end;
		padding: 0 0.55rem;
		border: none;
		border-right: 1px solid var(--border);
		background: inherit;
		color: var(--muted-foreground);
		font: inherit;
		font-size: 11px;
		font-weight: 400;
		font-variant-numeric: tabular-nums;
	}
	.hcell.lead {
		z-index: 5;
		background: var(--muted);
	}
	.cell.lead:hover {
		color: var(--foreground);
	}
	.htype {
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: 10.5px;
		font-weight: 400;
		color: var(--muted-foreground);
		opacity: 0.75;
		text-transform: lowercase;
		white-space: nowrap;
	}
	/* Room on the right so the follow button never covers the value. */
	.cell.rel {
		padding-right: 1.85rem;
	}
	.cell .peek {
		position: absolute;
		top: 50%;
		right: 0.3rem;
		display: grid;
		place-items: center;
		width: 1.3rem;
		height: 1.3rem;
		padding: 0;
		border: none;
		border-radius: 0.3rem;
		background: var(--brand);
		color: #fff;
		box-shadow:
			0 0 0 1px oklch(1 0 0 / 0.28),
			0 1px 2px oklch(0 0 0 / 0.3);
		transform: translateY(-50%);
		opacity: 0;
		transition: opacity 0.1s;
	}
	.cell:hover .peek,
	.cell.active .peek {
		opacity: 1;
	}
	.cell .peek:hover {
		filter: brightness(1.25);
	}
	.hname {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.sort {
		display: flex;
		flex: none;
	}
	.resize {
		position: absolute;
		right: -3px;
		top: 0;
		bottom: 0;
		width: 6px;
		cursor: col-resize;
		z-index: 2;
	}
	.body {
		position: relative;
		min-width: 100%;
	}
	.row {
		position: absolute;
		left: 0;
		right: 0;
		display: grid;
		height: var(--row-h, 28px);
		background: var(--card);
		border-bottom: 1px solid color-mix(in oklch, var(--border) 60%, transparent);
	}
	.grid.entering .row {
		animation: tf-drift var(--dur-slow) var(--ease-out) both;
		animation-delay: calc(min(var(--i, 0), 24) * 14ms);
	}
	/* Opaque backgrounds so sticky (pinned) cells cover what scrolls under them. */
	.row.odd {
		background: var(--zebra);
	}
	.row:hover {
		background: var(--hover);
	}
	.row.selected {
		background: var(--row-selected);
	}
	.row.new {
		background: color-mix(in oklch, var(--success) 12%, var(--card));
	}
	.row.deleted {
		background: color-mix(in oklch, var(--destructive) 10%, var(--card));
	}
	.row.deleted .cell:not(.lead) {
		color: var(--muted-foreground);
		text-decoration: line-through;
	}
	.cell {
		position: relative;
		display: block;
		padding: 0 0.55rem;
		border-right: 1px solid color-mix(in oklch, var(--border) 60%, transparent);
		background: inherit;
		line-height: 27px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}
	.cell.lead {
		display: flex;
		align-items: center;
	}
	.cell.null {
		color: var(--muted-foreground);
		font-size: 11px;
		opacity: 0.6;
	}
	.cell.num {
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
	.cell.changed {
		background: color-mix(in oklch, var(--warning) 18%, var(--card));
	}
	.cell.active {
		box-shadow: inset 0 0 0 1px var(--brand-ink);
	}
	.cell-editor {
		width: calc(100% + 0.8rem);
		height: 1.4rem;
		margin: 0 -0.4rem;
		padding: 0 0.4rem;
		border: 1px solid color-mix(in oklch, var(--foreground) 40%, var(--input));
		border-radius: 0.25rem;
		background: var(--background);
		outline: none;
		vertical-align: middle;
		font: inherit;
		user-select: text;
	}
	.cell-editor.invalid {
		border-color: var(--destructive);
	}
	.cell-editor.with-tool {
		padding-right: 1.5rem;
	}
	select.cell-editor {
		padding: 0 0.25rem;
	}
	.cell-tool {
		position: absolute;
		top: 50%;
		right: 0.15rem;
		display: grid;
		place-items: center;
		width: 1.2rem;
		height: 1.2rem;
		padding: 0;
		border: none;
		border-radius: 0.2rem;
		background: transparent;
		color: var(--muted-foreground);
		transform: translateY(-50%);
	}
	.cell-tool:hover {
		color: var(--foreground);
		background: var(--accent);
	}
	.row.group,
	.row.more {
		display: block;
		font-family: var(--font-sans);
	}
	.row.group {
		background: color-mix(in oklch, var(--muted) 70%, var(--card));
	}
	.row.group.deep {
		background: var(--card);
	}
	.row.group:hover {
		background: var(--hover);
	}
	.g-head {
		position: sticky;
		left: 0;
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		height: 100%;
		padding-right: 1rem;
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: 12px;
		text-align: left;
		white-space: nowrap;
	}
	.g-head.static {
		gap: 0.75rem;
	}
	.g-head :global(.chev) {
		color: var(--muted-foreground);
		transition: transform 0.12s;
	}
	.row.group.open .g-head :global(.chev) {
		transform: rotate(90deg);
	}
	:global(.spin) {
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	.g-col {
		color: var(--muted-foreground);
	}
	.g-col::after {
		content: ':';
	}
	.g-value {
		font-family: var(--font-mono);
		font-weight: 500;
	}
	.g-empty {
		color: var(--muted-foreground);
		font-style: italic;
	}
	.g-count {
		display: inline-grid;
		place-items: center;
		height: 1.15rem;
		padding: 0 0.35rem;
		border: 1px solid var(--border);
		border-radius: 0.25rem;
		color: var(--muted-foreground);
		font-size: 11px;
		font-variant-numeric: tabular-nums;
	}
	.link {
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: 12px;
		text-decoration: underline;
		text-underline-offset: 2px;
		padding: 0;
	}
	.err {
		color: var(--destructive);
	}
	.empty {
		position: absolute;
		top: 5rem;
		left: 0;
		right: 0;
		text-align: center;
		color: var(--muted-foreground);
		font-family: var(--font-sans);
	}
	.menu {
		font-family: var(--font-sans);
		font-size: 1rem;
		position: fixed;
		z-index: 60;
		min-width: 11rem;
		padding: 0.25rem;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		box-shadow: var(--shadow);
		display: flex;
		flex-direction: column;
	}
	.menu button {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		height: 2rem;
		padding: 0 0.55rem;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		text-align: left;
	}
	.menu button :global(svg) {
		color: var(--muted-foreground);
	}
	.menu button:hover {
		background: var(--accent);
	}
	.menu button.danger,
	.menu button.danger :global(svg) {
		color: var(--destructive);
	}
	.sep {
		height: 1px;
		margin: 0.25rem -0.25rem;
		background: var(--border);
	}
</style>
