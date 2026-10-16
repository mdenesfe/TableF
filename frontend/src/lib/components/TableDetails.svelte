<script lang="ts">
	// Table details as a small dashboard: key figures, where the bytes go,
	// row health and access patterns (when the engine reports them), the
	// column type mix, relations, and every remaining fact in a searchable list.
	import { Columns3, Database, Eye, HardDrive, KeyRound, Link2, ListTree, LoaderCircle, Rows3, Search, Table2, X } from '@lucide/svelte';
	import { api } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import { panel } from '#lib/motion.ts';
	import { drag } from '#lib/drag.ts';
	import { load, save } from '#lib/storage.ts';
	import type { Column, ForeignKey, InfoField, Table } from '#lib/types.ts';
	import { prefs } from '#lib/prefs.svelte.ts';
	import { bytes, compact } from '#lib/format.ts';
	import { Tween } from 'svelte/motion';
	import { cubicOut } from 'svelte/easing';

	const WIDTH_KEY = 'tablef.recordPane.width';

	let {
		table,
		columns,
		foreignKeys = [],
		referencedBy = [],
		total = null,
		onclose
	}: {
		table: Table;
		columns: Column[];
		foreignKeys?: ForeignKey[];
		referencedBy?: ForeignKey[];
		/** Exact row count from the data view, when it has one. */
		total?: number | null;
		onclose: () => void;
	} = $props();

	let width = $state(load(WIDTH_KEY, 360));
	let filter = $state('');
	let fields = $state.raw<InfoField[]>([]);
	let loading = $state(false);
	let error = $state('');

	// Reload when the table (or connection) changes; stale replies are dropped.
	let seq = 0;
	$effect(() => {
		const id = app.session?.id;
		const { schema, name } = table;
		if (!id) return;
		const n = ++seq;
		loading = true;
		error = '';
		api
			.getTableInfo(id, schema, name)
			.then((f) => n === seq && (fields = f))
			.catch((e) => n === seq && ((fields = []), (error = (e as Error).message)))
			.finally(() => n === seq && (loading = false));
	});

	// ---------- formatting ----------
	const pct = (part: number, whole: number) => (whole > 0 ? (part / whole) * 100 : 0);
	const pctText = (p: number) => (p > 0 && p < 1 ? '<1%' : `${Math.round(p)}%`);

	const isCount = (k: string) => /(_rows|_count|_scans|^blocks|^auto_increment|^last_autoincrement|^oid|^object_id|^root_page)$/.test(k);
	const isBytes = (k: string) => k.endsWith('_size');
	function show(f: InfoField): { text: string; exact?: string } {
		if (f.value === null) return { text: 'NULL' };
		const n = Number(f.value);
		if (f.value.trim() !== '' && Number.isFinite(n)) {
			if (isBytes(f.key)) return { text: bytes(n), exact: `${n.toLocaleString()} bytes` };
			if (isCount(f.key)) return { text: n.toLocaleString() };
		}
		return { text: f.value };
	}

	// ---------- figures ----------
	const byKey = $derived(new Map(fields.map((f) => [f.key, f.value])));
	function num(key: string): number | null {
		const v = byKey.get(key);
		if (v === null || v === undefined || v.trim() === '') return null;
		const n = Number(v);
		return Number.isFinite(n) ? n : null;
	}

	const rows = $derived(total ?? num('live_rows') ?? num('estimated_rows'));
	const rowsExact = $derived(total !== null);
	const totalSize = $derived(num('total_size'));
	const indexCount = $derived(num('index_count'));
	const avgRow = $derived(num('avg_row_size') ?? (totalSize !== null && rows ? Math.round((num('data_size') ?? totalSize) / rows) : null));

	// Where the bytes go. PostgreSQL's data_size includes TOAST, so it is split out.
	type Part = { label: string; value: number; color: string };
	const storage = $derived.by((): Part[] => {
		const toast = num('toast_size') ?? 0;
		const data = (num('data_size') ?? num('compressed_size') ?? 0) - toast;
		const parts: Part[] = [
			{ label: 'Data', value: data, color: 'var(--chart-1)' },
			{ label: 'Indexes', value: num('index_size') ?? 0, color: 'var(--chart-2)' },
			{ label: 'TOAST', value: toast, color: 'var(--chart-3)' },
			{ label: 'Free', value: num('free_size') ?? num('unused_size') ?? 0, color: 'var(--chart-4)' }
		];
		return parts.filter((p) => p.value > 0);
	});
	const storageTotal = $derived(storage.reduce((s, p) => s + p.value, 0));

	// Row health: live vs dead tuples (PostgreSQL).
	const live = $derived(num('live_rows'));
	const dead = $derived(num('dead_rows'));
	// Access: sequential vs index scans (PostgreSQL).
	const seqScans = $derived(num('seq_scans'));
	const idxScans = $derived(num('index_scans'));
	// Writes since the statistics were reset (PostgreSQL).
	const writes = $derived(
		[
			{ label: 'Inserted', value: num('inserted_rows') },
			{ label: 'Updated', value: num('updated_rows') },
			{ label: 'Deleted', value: num('deleted_rows') }
		].filter((w): w is { label: string; value: number } => w.value !== null)
	);
	const writesMax = $derived(Math.max(1, ...writes.map((w) => w.value)));

	// Column types, most common first.
	const typeMix = $derived.by(() => {
		const counts = new Map<string, number>();
		for (const c of columns) {
			const t = (c.type || 'unknown').toLowerCase().replace(/\(.*$/, '').trim();
			counts.set(t, (counts.get(t) ?? 0) + 1);
		}
		const list = [...counts].sort((a, b) => b[1] - a[1]);
		const top = list.slice(0, 5);
		const rest = list.slice(5).reduce((s, [, n]) => s + n, 0);
		return rest ? [...top, ['other', rest] as [string, number]] : top;
	});
	const typeMax = $derived(Math.max(1, ...typeMix.map(([, n]) => n)));

	const pk = $derived(columns.filter((c) => c.primaryKey).map((c) => c.name));
	const fkOut = $derived(new Set(foreignKeys.map((f) => f.name)).size);
	const fkIn = $derived(new Set(referencedBy.map((f) => `${f.schema}.${f.table}.${f.name}`)).size);
	const nullable = $derived(columns.filter((c) => c.nullable).length);

	const comment = $derived(byKey.get('comment') ?? null);

	// Key figures count up to their value (instantly with reduced motion).
	const tween = () => new Tween(0, { duration: prefs.still ? 0 : 650, easing: cubicOut });
	const tRows = tween();
	const tSize = tween();
	const tCols = tween();
	const tIdx = tween();
	$effect(() => {
		tRows.set(rows ?? 0);
		tSize.set(totalSize ?? 0);
		tCols.set(columns.length);
		tIdx.set(indexCount ?? 0);
	});

	// Donut: each part's arc on a circle of circumference 100.
	const arcs = $derived.by(() => {
		let at = 0;
		return storage.map((p) => {
			const len = pct(p.value, storageTotal);
			const arc = { ...p, len, offset: 25 - at };
			at += len;
			return arc;
		});
	});

	// Waffle: one square per column, by role.
	const fkCols = $derived(new Set(foreignKeys.map((f) => f.column)));
	type Role = 'pk' | 'fk' | 'required' | 'nullable';
	const roleOf = (c: Column): Role => (c.primaryKey ? 'pk' : fkCols.has(c.name) ? 'fk' : c.nullable ? 'nullable' : 'required');
	const ROLES: { role: Role; label: string }[] = [
		{ role: 'pk', label: 'Primary key' },
		{ role: 'fk', label: 'Foreign key' },
		{ role: 'required', label: 'Required' },
		{ role: 'nullable', label: 'Nullable' }
	];
	const roleCounts = $derived(ROLES.map((r) => ({ ...r, n: columns.filter((c) => roleOf(c) === r.role).length })).filter((r) => r.n));
	const liveShare = $derived(live !== null && dead !== null && live + dead > 0 ? pct(live, live + dead) : null);

	// Everything else, as a searchable list (figures already charted are kept:
	// the list is where exact values live).
	const facts = $derived.by(() => {
		const q = filter.trim().toLowerCase();
		const base: InfoField[] = [
			{ key: 'name', value: table.name },
			...(table.schema ? [{ key: 'schema', value: table.schema }] : []),
			{ key: 'type', value: table.type },
			...(total !== null ? [{ key: 'exact_rows', value: String(total) }] : [])
		];
		return [...base, ...fields.filter((f) => f.key !== 'comment' && f.key !== 'kind')].filter(
			(f) => !q || f.key.includes(q) || (f.value ?? '').toLowerCase().includes(q)
		);
	});

	function resizeStart(e: PointerEvent) {
		const x0 = e.clientX;
		const w0 = width;
		const max = Math.max(320, window.innerWidth * 0.6);
		drag(
			e,
			(ev) => (width = Math.round(Math.min(max, Math.max(260, w0 - (ev.clientX - x0))))),
			() => save(WIDTH_KEY, width)
		);
	}
</script>

<aside class="pane" style:width="{width}px" transition:panel={{ side: 'right' }} aria-label="Table details">
	<div class="handle" role="separator" aria-orientation="vertical" aria-label="Resize" onpointerdown={resizeStart}></div>

	<header>
		<span class="icon-box"
			>{#if table.type === 'view'}<Eye />{:else}<Table2 />{/if}</span
		>
		<span class="title" title={table.schema ? `${table.schema}.${table.name}` : table.name}>{table.name}</span>
		{#if table.schema}<span class="sub mono">{table.schema}</span>{:else}<span class="sub"></span>{/if}
		{#if loading}<LoaderCircle size={13} class="spin" />{/if}
		<button class="btn ghost icon xs" onclick={onclose} title="Hide details"><X size={14} /></button>
	</header>

	<div class="body">
		{#key table.schema + '.' + table.name}
			<div class="kpis">
				<div class="kpi" style:--i={0}>
					<span class="kpi-label"><span class="icon-box sm"><Rows3 /></span>Rows</span>
					<span class="kpi-value">{rows === null ? '—' : compact(Math.round(tRows.current))}</span>
					<span class="kpi-sub">{rows === null ? 'unknown' : rowsExact ? 'exact' : 'estimate'}</span>
				</div>
				<div class="kpi" style:--i={1}>
					<span class="kpi-label"><span class="icon-box sm"><HardDrive /></span>Size</span>
					<span class="kpi-value" title={totalSize === null ? undefined : `${totalSize.toLocaleString()} bytes`}
						>{totalSize === null ? '—' : bytes(Math.round(tSize.current))}</span
					>
					<span class="kpi-sub">{avgRow ? `~${bytes(avgRow)}/row` : 'on disk'}</span>
				</div>
				<div class="kpi" style:--i={2}>
					<span class="kpi-label"><span class="icon-box sm"><Columns3 /></span>Columns</span>
					<span class="kpi-value">{Math.round(tCols.current)}</span>
					<span class="kpi-sub">{nullable} nullable</span>
				</div>
				<div class="kpi" style:--i={3}>
					<span class="kpi-label"><span class="icon-box sm"><ListTree /></span>Indexes</span>
					<span class="kpi-value">{indexCount === null ? '—' : Math.round(tIdx.current)}</span>
					<span class="kpi-sub">{num('trigger_count') !== null ? `${num('trigger_count')} trigger${num('trigger_count') === 1 ? '' : 's'}` : 'defined'}</span>
				</div>
			</div>

			<!-- Storage: parts of one whole → donut. -->
			{#if storage.length}
				<section class="card">
					<h3>Storage</h3>
					<div class="donut-row">
						<svg class="donut" viewBox="0 0 42 42" role="img" aria-label="Storage breakdown">
							<circle class="track" cx="21" cy="21" r="15.915" />
							{#each arcs as a, i (a.label)}
								<circle
									class="seg"
									cx="21"
									cy="21"
									r="15.915"
									stroke={a.color}
									stroke-dasharray="{Math.max(0.6, a.len - (arcs.length > 1 ? 0.8 : 0))} {100 - Math.max(0.6, a.len - (arcs.length > 1 ? 0.8 : 0))}"
									stroke-dashoffset={a.offset}
									style:--i={i}><title>{a.label}: {bytes(a.value)}</title></circle
								>
							{/each}
							<text x="21" y="20.5" class="donut-value">{bytes(storageTotal)}</text>
							<text x="21" y="26" class="donut-label">on disk</text>
						</svg>
						<div class="legend">
							{#each storage as p (p.label)}
								<span><i style:background={p.color}></i>{p.label}<b>{bytes(p.value)}</b><em>{pctText(pct(p.value, storageTotal))}</em></span>
							{/each}
						</div>
					</div>
				</section>
			{/if}

			<!-- Row health: one share that should stay high → gauge. -->
			{#if liveShare !== null && live !== null && dead !== null}
				{@const bad = liveShare < 80}
				<section class="card">
					<h3>Row health <span class:warn={bad}>{bad ? 'needs VACUUM' : 'healthy'}</span></h3>
					<div class="gauge-row">
						<svg class="gauge" viewBox="0 0 40 23" role="img" aria-label={`${Math.round(liveShare)}% live rows`}>
							<path class="track" pathLength="100" d="M 4 20 A 16 16 0 0 1 36 20" />
							<path class="arc" class:bad pathLength="100" d="M 4 20 A 16 16 0 0 1 36 20" stroke-dasharray="{liveShare} 100" />
							<text x="20" y="18.5" class="gauge-value">{pctText(liveShare)}</text>
						</svg>
						<div class="legend">
							<span><i style:background={bad ? 'var(--warning)' : 'var(--success)'}></i>Live rows<b>{compact(live)}</b></span>
							<span><i style:background="var(--chart-4)"></i>Dead rows<b>{compact(dead)}</b></span>
						</div>
					</div>
				</section>
			{/if}

			<!-- Access: how often reads use an index → share meter. -->
			{#if seqScans !== null && idxScans !== null && seqScans + idxScans > 0}
				{@const share = pct(idxScans, seqScans + idxScans)}
				<section class="card">
					<h3>Index usage <span>{compact(seqScans + idxScans)} scans</span></h3>
					<div class="meter-value"><b>{pctText(share)}</b> of scans use an index</div>
					<div class="meter" role="img" aria-label={`${Math.round(share)}% index scans`}>
						<span style:width="{share}%"></span>
					</div>
					<div class="meter-scale"><span>{compact(idxScans)} index</span><span>{compact(seqScans)} sequential</span></div>
				</section>
			{/if}

			<!-- Writes: three counts side by side → columns. -->
			{#if writes.length && writes.some((w) => w.value > 0)}
				<section class="card">
					<h3>Writes <span>since stats reset</span></h3>
					<div class="columns-chart">
						{#each writes as w, i (w.label)}
							<div class="col">
								<b>{compact(w.value)}</b>
								<span class="col-bar"><span style:height="{Math.max(3, pct(w.value, writesMax))}%" style:--i={i}></span></span>
								<em>{w.label}</em>
							</div>
						{/each}
					</div>
				</section>
			{/if}

			<!-- Columns: every column is one square, colored by its role → waffle. -->
			{#if columns.length}
				<section class="card">
					<h3>Column roles <span>{columns.length} columns</span></h3>
					<div class="waffle" role="img" aria-label="Columns by role">
						{#each columns as c, i (c.name)}
							<span class="cell {roleOf(c)}" style:--i={Math.min(i, 40)} title={`${c.name} · ${c.type || '?'}`}></span>
						{/each}
					</div>
					<div class="legend inline">
						{#each roleCounts as r (r.role)}<span><i class="cell {r.role}"></i>{r.label}<b>{r.n}</b></span>{/each}
					</div>
				</section>
			{/if}

			<!-- Column types: a ranking → horizontal bars. -->
			{#if typeMix.length > 1}
				<section class="card">
					<h3>Column types <span>{typeMix.length} kinds</span></h3>
					<div class="hbars">
						{#each typeMix as [t, n], i (t)}
							<span class="hb-label mono" title={t}>{t}</span>
							<span class="hb-track"><span style:width="{pct(n, typeMax)}%" style:--i={i}></span></span>
							<span class="hb-value">{n}</span>
						{/each}
					</div>
				</section>
			{/if}
		{/key}

		<section class="card">
			<h3>Relations</h3>
			<div class="chips">
				<span class="chip"
					><span class="icon-box sm"><KeyRound /></span><span class="mono" title={pk.join(', ')}>{pk.length ? pk.join(', ') : 'no primary key'}</span></span
				>
				<span class="chip"><span class="icon-box sm"><Link2 /></span>{fkOut} outgoing</span>
				<span class="chip"><span class="icon-box sm"><Database /></span>{fkIn} referencing</span>
			</div>
		</section>

		{#if comment}
			<section class="card">
				<h3>Comment</h3>
				<p class="comment">{comment}</p>
			</section>
		{/if}

		<section class="card list">
			<h3>All details</h3>
			<div class="filter">
				<Search size={13} />
				<input placeholder="Search for field…" bind:value={filter} spellcheck="false" />
			</div>
			{#each facts as f (f.key)}
				{@const v = show(f)}
				<div class="fact">
					<span class="mono">{f.key}</span>
					<span class="mono" class:null={f.value === null} title={v.exact ?? v.text}>{v.text}</span>
				</div>
			{:else}
				<p class="none">No matching fields</p>
			{/each}
		</section>
		{#if error}<p class="none err" title={error}>{error}</p>{/if}
	</div>
</aside>

<style>
	.pane {
		/* Chart palette from the brand ink, readable in both themes. */
		--chart-1: var(--brand-ink);
		--chart-2: color-mix(in oklab, var(--brand-ink) 50%, var(--muted-foreground));
		--chart-3: color-mix(in oklab, var(--brand-ink) 30%, var(--success));
		--chart-4: color-mix(in oklab, var(--muted-foreground) 45%, transparent);
		position: relative;
		display: flex;
		flex-direction: column;
		flex: none;
		min-width: 0;
		height: 100%;
		border-left: 1px solid var(--border);
		background: var(--background);
	}
	.handle {
		position: absolute;
		top: 0;
		bottom: 0;
		left: -3px;
		width: 6px;
		cursor: col-resize;
		z-index: 3;
	}
	.handle:hover,
	.handle:active {
		background: color-mix(in oklch, var(--foreground) 15%, transparent);
	}
	header {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		height: 2.5rem;
		flex: none;
		padding: 0 0.35rem 0 0.75rem;
		border-bottom: 1px solid var(--border);
	}
	.title {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 600;
	}
	.sub {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 11.5px;
		color: var(--muted-foreground);
	}
	header :global(.spin) {
		color: var(--muted-foreground);
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	.body {
		flex: 1;
		overflow: auto;
		padding: 0.6rem;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	/* ---------- key figures: one row ---------- */
	.kpis {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: 0.4rem;
	}
	.kpi,
	.card {
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--card);
		animation: tf-drift var(--dur) var(--ease-out) both;
	}
	.kpi {
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
		min-width: 0;
		padding: 0.45rem 0.5rem 0.4rem;
		animation-delay: calc(var(--i, 0) * 45ms);
	}
	.kpi-label {
		display: flex;
		align-items: center;
		gap: 0.3rem;
		min-width: 0;
		font-size: 10.5px;
		font-weight: 500;
		color: var(--muted-foreground);
		white-space: nowrap;
	}
	.kpi-label :global(.icon-box) {
		width: 0.9rem;
		height: 0.9rem;
	}
	.kpi-label :global(.icon-box svg) {
		width: 0.55rem;
		height: 0.55rem;
	}
	.kpi-value {
		font-size: 16px;
		font-weight: 600;
		letter-spacing: -0.02em;
		font-variant-numeric: tabular-nums;
		line-height: 1.3;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.kpi-sub {
		font-size: 10px;
		color: var(--muted-foreground);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	/* ---------- cards ---------- */
	.card {
		padding: 0.6rem 0.65rem 0.65rem;
	}
	h3 {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
		margin: 0 0 0.5rem;
		font-size: 11.5px;
		font-weight: 600;
	}
	h3 span {
		margin-left: auto;
		font-weight: 400;
		font-size: 11px;
		color: var(--muted-foreground);
		font-variant-numeric: tabular-nums;
	}
	h3 span.warn {
		color: var(--warning);
		font-weight: 600;
	}
	.legend {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		min-width: 0;
		font-size: 11.5px;
	}
	.legend span {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		animation: tf-drift var(--dur) var(--ease-out) both;
	}
	.legend i {
		width: 0.5rem;
		height: 0.5rem;
		flex: none;
		border-radius: 2px;
	}
	.legend b {
		margin-left: auto;
		font-weight: 500;
		font-variant-numeric: tabular-nums;
	}
	.legend em {
		width: 2.4rem;
		text-align: right;
		font-style: normal;
		color: var(--muted-foreground);
		font-variant-numeric: tabular-nums;
	}
	.legend.inline {
		flex-direction: row;
		flex-wrap: wrap;
		gap: 0.25rem 0.8rem;
		margin-top: 0.55rem;
	}
	.legend.inline b {
		margin-left: 0.1rem;
		color: var(--muted-foreground);
	}

	/* Donut (storage): segments draw in one after another. */
	.donut-row,
	.gauge-row {
		display: flex;
		align-items: center;
		gap: 0.9rem;
	}
	.donut-row .legend,
	.gauge-row .legend {
		flex: 1;
	}
	.donut {
		width: 5.6rem;
		height: 5.6rem;
		flex: none;
	}
	.donut circle {
		fill: none;
		stroke-width: 5;
	}
	.donut .track {
		stroke: var(--muted);
	}
	.donut .seg {
		animation: draw 0.8s var(--ease-out) both;
		animation-delay: calc(var(--i, 0) * 120ms);
		transition:
			stroke-dasharray var(--dur-slow) var(--ease-out),
			stroke-dashoffset var(--dur-slow) var(--ease-out);
	}
	.donut text,
	.gauge text {
		text-anchor: middle;
		fill: var(--foreground);
		font-variant-numeric: tabular-nums;
	}
	.donut-value {
		font-size: 5.6px;
		font-weight: 600;
	}
	.donut-label {
		font-size: 3.4px;
		fill: var(--muted-foreground) !important;
	}
	@keyframes draw {
		from {
			stroke-dasharray: 0 100;
		}
	}

	/* Gauge (row health). */
	.gauge {
		width: 6.2rem;
		flex: none;
	}
	.gauge path {
		fill: none;
		stroke-width: 4.5;
		stroke-linecap: round;
	}
	.gauge .track {
		stroke: var(--muted);
	}
	.gauge .arc {
		stroke: var(--success);
		animation: draw 0.9s var(--ease-out) both;
	}
	.gauge .arc.bad {
		stroke: var(--warning);
	}
	.gauge-value {
		font-size: 7px;
		font-weight: 600;
	}

	/* Meter (index usage). */
	.meter-value {
		margin-bottom: 0.4rem;
		font-size: 11.5px;
		color: var(--muted-foreground);
	}
	.meter-value b {
		font-size: 15px;
		color: var(--foreground);
		font-variant-numeric: tabular-nums;
	}
	.meter {
		height: 0.55rem;
		border-radius: 999px;
		background: var(--chart-4);
		overflow: hidden;
	}
	.meter span {
		display: block;
		height: 100%;
		border-radius: 999px;
		background: var(--chart-1);
		transform-origin: left;
		animation: grow-x 0.8s var(--ease-out) both;
	}
	.meter-scale {
		display: flex;
		justify-content: space-between;
		margin-top: 0.3rem;
		font-size: 10.5px;
		color: var(--muted-foreground);
		font-variant-numeric: tabular-nums;
	}

	/* Vertical columns (writes). */
	.columns-chart {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: 0.6rem;
	}
	.col {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.25rem;
		font-size: 11px;
	}
	.col b {
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.col em {
		font-style: normal;
		color: var(--muted-foreground);
	}
	.col-bar {
		display: flex;
		align-items: flex-end;
		width: 100%;
		height: 3.5rem;
		border-radius: var(--radius);
		background: var(--muted);
		overflow: hidden;
	}
	.col-bar span {
		width: 100%;
		background: var(--chart-1);
		border-radius: var(--radius) var(--radius) 0 0;
		transform-origin: bottom;
		animation: grow-y 0.7s var(--ease-out) both;
		animation-delay: calc(var(--i, 0) * 90ms);
	}

	/* Waffle (column roles): squares pop in, in column order. */
	.waffle {
		display: grid;
		grid-template-columns: repeat(auto-fill, 0.85rem);
		gap: 3px;
	}
	.cell {
		display: block;
		width: 0.85rem;
		height: 0.85rem;
		border-radius: 3px;
	}
	.waffle .cell {
		animation: pop 0.35s var(--ease-out) both;
		animation-delay: calc(var(--i, 0) * 14ms);
	}
	.waffle .cell:hover {
		outline: 1.5px solid var(--foreground);
		outline-offset: 1px;
	}
	.legend i.cell {
		width: 0.6rem;
		height: 0.6rem;
	}
	.cell.pk {
		background: var(--warning);
	}
	.cell.fk {
		background: var(--chart-1);
	}
	.cell.required {
		background: var(--chart-2);
	}
	.cell.nullable {
		background: transparent;
		box-shadow: inset 0 0 0 1.5px var(--chart-2);
	}

	/* Horizontal bars (column types). */
	.hbars {
		display: grid;
		grid-template-columns: minmax(3.5rem, auto) 1fr auto;
		align-items: center;
		gap: 0.3rem 0.55rem;
		font-size: 11.5px;
	}
	.hb-label {
		max-width: 8rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--muted-foreground);
	}
	.hb-track {
		height: 0.45rem;
		border-radius: 999px;
		background: var(--muted);
		overflow: hidden;
	}
	.hb-track span {
		display: block;
		height: 100%;
		border-radius: 999px;
		background: var(--chart-1);
		transform-origin: left;
		animation: grow-x 0.7s var(--ease-out) both;
		animation-delay: calc(var(--i, 0) * 60ms);
	}
	.hb-value {
		font-weight: 500;
		font-variant-numeric: tabular-nums;
	}
	@keyframes grow-x {
		from {
			transform: scaleX(0);
		}
	}
	@keyframes grow-y {
		from {
			transform: scaleY(0);
		}
	}
	@keyframes pop {
		from {
			transform: scale(0.2);
			opacity: 0;
		}
	}

	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		max-width: 100%;
		height: 1.6rem;
		padding: 0 0.5rem 0 0.25rem;
		border: 1px solid var(--border);
		border-radius: 999px;
		font-size: 11.5px;
	}
	.chip > span:last-child {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.comment {
		margin: 0;
		font-size: 12px;
		white-space: pre-wrap;
		word-break: break-word;
		user-select: text;
	}

	/* ---------- all details ---------- */
	.filter {
		position: relative;
		margin-bottom: 0.35rem;
	}
	.filter :global(svg) {
		position: absolute;
		left: 0.5rem;
		top: 50%;
		transform: translateY(-50%);
		color: var(--muted-foreground);
	}
	.filter input {
		width: 100%;
		height: 1.65rem;
		padding: 0 0.5rem 0 1.7rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--background);
		color: var(--foreground);
		font-size: 12px;
		outline: none;
		user-select: text;
	}
	.filter input:focus {
		border-color: color-mix(in oklch, var(--foreground) 35%, var(--border));
	}
	.fact {
		display: flex;
		align-items: baseline;
		gap: 0.75rem;
		padding: 0.3rem 0.1rem;
		border-bottom: 1px solid color-mix(in oklch, var(--border) 60%, transparent);
		font-size: 11.5px;
	}
	.fact:last-child {
		border-bottom: none;
	}
	.fact span:first-child {
		flex: none;
		color: var(--muted-foreground);
	}
	.fact span:last-child {
		margin-left: auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		text-align: right;
		user-select: text;
	}
	.fact span.null {
		opacity: 0.55;
	}
	.none {
		margin: 0;
		padding: 0.5rem 0.1rem;
		color: var(--muted-foreground);
		font-size: 12px;
	}
	.none.err {
		color: var(--destructive);
		font-size: 11.5px;
		word-break: break-word;
	}
</style>
