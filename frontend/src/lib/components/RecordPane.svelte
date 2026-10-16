<script lang="ts" module>
	export interface PaneColumn {
		name: string;
		type?: string;
		primaryKey?: boolean;
		nullable?: boolean;
	}

	const WIDTH_KEY = 'tablef.recordPane.width';
	const COLLAPSED_KEY = 'tablef.recordPane.collapsed';
</script>

<script lang="ts">
	import { ArrowUpRight, CalendarDays, ChevronDown, ChevronRight, CornerDownRight, KeyRound, Link2, X } from '@lucide/svelte';
	import { api } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import { panel } from '#lib/motion.ts';
	import ExportMenu from './ExportMenu.svelte';
	import { drag } from '#lib/drag.ts';
	import { load, save } from '#lib/storage.ts';
	import DatePicker from './DatePicker.svelte';
	import { boolText, isNumber, kindOf, now, numericOnly, prettyJSON, uuid, type CellKind } from '#lib/celltype.ts';
	import type { Cell, ForeignKey } from '#lib/types.ts';

	let {
		columns,
		row,
		title,
		editable = false,
		changed = () => false,
		onedit,
		onclose,
		foreignKeys = [],
		referencedBy = []
	}: {
		columns: PaneColumn[];
		row: Cell[];
		title: string;
		editable?: boolean;
		changed?: (col: number) => boolean;
		onedit?: (col: number, value: Cell) => void;
		onclose: () => void;
		foreignKeys?: ForeignKey[];
		/** References pointing at this table; listed with how many rows point at this one. */
		referencedBy?: ForeignKey[];
	} = $props();

	const fkByColumn = $derived(new Map(foreignKeys.map((f) => [f.column, f])));
	const colIndex = $derived(new Map(columns.map((c, i) => [c.name, i])));
	const fkOf = (name: string) => fkByColumn.get(name);
	const valueOf = (name: string) => row[colIndex.get(name) ?? -1];

	// Count the rows in each referencing table that point at this row. Only the
	// referenced key values matter (editing other fields doesn't refetch), arrow
	// keys through the grid are debounced, and stale replies are dropped.
	let counts = $state<Record<string, number | null>>({});
	const refKeys = $derived(referencedBy.map((f) => valueOf(f.refColumn)));
	const refKey = $derived(JSON.stringify(refKeys));
	let countSeq = 0;
	$effect(() => {
		void refKey;
		const refs = referencedBy;
		const values = refKeys;
		const seq = ++countSeq;
		counts = {};
		if (!app.session || !refs.length) return;
		const id = app.session.id;
		const timer = setTimeout(() => {
			refs.forEach((f, k) => {
				const v = values[k];
				if (v === null || v === undefined) return;
				api
					.getTableData(id, {
						schema: f.schema,
						table: f.table,
						limit: 1,
						offset: 0,
						orderBy: '',
						orderDir: '',
						where: '',
						match: [{ column: f.column, value: v }]
					})
					.then((res) => seq === countSeq && (counts = { ...counts, [f.name]: res.total }))
					.catch(() => seq === countSeq && (counts = { ...counts, [f.name]: null }));
			});
		}, 200);
		return () => clearTimeout(timer);
	});

	let width = $state(load(WIDTH_KEY, 360));
	// Referenced by starts closed; expand it when needed.
	let collapsed = $state<Record<string, boolean>>({ refs: true, ...load(COLLAPSED_KEY, {}) });
	let filter = $state('');

	const groups = $derived.by(() => {
		const q = filter.toLowerCase();
		const idx = columns.map((c, i) => ({ c, i })).filter(({ c }) => !q || c.name.toLowerCase().includes(q));
		return [
			{ key: 'pk', label: 'Primary key', items: idx.filter(({ c }) => c.primaryKey) },
			{ key: 'cols', label: 'Columns', items: idx.filter(({ c }) => !c.primaryKey) }
		].filter((g) => g.items.length);
	});

	function toggle(key: string) {
		collapsed = { ...collapsed, [key]: !collapsed[key] };
		save(COLLAPSED_KEY, collapsed);
	}

	/** Pretty-prints JSON documents (up to 100 KB); everything else is shown as-is. */
	function text(v: Cell): string {
		if (v === null || v === undefined) return '';
		const s = String(v);
		if (s.length <= 100_000 && /^\s*[[{]/.test(s)) {
			try {
				return JSON.stringify(JSON.parse(s), null, 2);
			} catch {
				/* not JSON */
			}
		}
		return s;
	}

	let picking = $state<{ i: number; kind: CellKind; x: number; y: number; nullable: boolean } | null>(null);
	// Each field's text, formatted once per row instead of on every render.
	const texts = $derived(row.map(text));

	function commit(ci: number, value: string) {
		const old = row[ci];
		if (old === null ? value === '' : value === texts[ci]) return;
		onedit?.(ci, value);
	}

	/** Sizes a textarea to its content; re-runs when the shown value changes. */
	function grow(el: HTMLTextAreaElement, _value?: string) {
		let frame = 0;
		const fit = () => {
			cancelAnimationFrame(frame);
			// In a frame, so many fields measure in one layout pass instead of one each.
			frame = requestAnimationFrame(() => {
				el.style.height = 'auto';
				const h = el.scrollHeight + 2; // + borders
				el.style.height = Math.min(h, 320) + 'px';
				el.style.overflowY = h > 320 ? 'auto' : 'hidden';
			});
		};
		fit();
		el.addEventListener('input', fit);
		return {
			update: fit,
			destroy: () => {
				cancelAnimationFrame(frame);
				el.removeEventListener('input', fit);
			}
		};
	}

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

<aside class="pane" style:width="{width}px" transition:panel={{ side: 'right' }}>
	<div class="handle" role="separator" aria-orientation="vertical" aria-label="Resize" onpointerdown={resizeStart}></div>

	<header>
		<span class="title mono">{title}</span>
		<ExportMenu source={() => ({ columns: columns.map((c) => c.name), rows: [row] })} name={title.replace(/\W+/g, '_')} single align="right" />
		<button class="btn ghost icon xs" onclick={onclose} title="Close (Esc)"><X size={14} /></button>
	</header>

	<div class="filter">
		<input class="mono" placeholder="filter fields…" bind:value={filter} spellcheck="false" />
	</div>

	<div class="body">
		{#each groups as g (g.key)}
			<section>
				<button class="group" onclick={() => toggle(g.key)}>
					{#if collapsed[g.key]}<ChevronRight size={12} />{:else}<ChevronDown size={12} />{/if}
					<span>{g.label}</span>
					<span class="count">{g.items.length}</span>
				</button>
				{#if !collapsed[g.key]}
					{#each g.items as { c, i } (c.name)}
						{@const v = row[i]}
						<div class="field" class:changed={changed(i)}>
							<div class="label mono">
								{#if c.primaryKey}<span class="icon-box" title="Primary key"><KeyRound /></span>{/if}
								<span class="name">{c.name}</span>
								{#if fkOf(c.name) && v !== null}
									{@const f = fkOf(c.name)!}
									<button
										class="rel"
										onclick={() => app.openRelated(f.refSchema, f.refTable, f.refColumn, v)}
										title={`Open ${f.refTable} where ${f.refColumn} = ${v}`}
									>
										<span class="icon-box"><Link2 /></span>{f.refTable}
									</button>
								{/if}
								{#if c.type}<span class="type">{c.type}</span>{/if}
							</div>
							{#if editable}
								{@const kind = kindOf(c.type)}
								{#if kind === 'bool'}
									<select
										class="bool-select mono"
										value={boolText(v)}
										onchange={(e) => onedit?.(i, e.currentTarget.value === 'null' ? null : e.currentTarget.value === 'true')}
									>
										<option value="true">true</option>
										<option value="false">false</option>
										{#if c.nullable !== false}<option value="null">NULL</option>{/if}
									</select>
								{:else}
									<div class="edit">
										<textarea
											class="mono"
											rows="1"
											value={texts[i]}
											placeholder={v === null ? 'NULL' : ''}
											spellcheck="false"
											use:grow={texts[i]}
											inputmode={kind === 'number' ? 'decimal' : undefined}
											onbeforeinput={kind === 'number' ? numericOnly : undefined}
											onchange={(e) => {
												const t = e.currentTarget.value;
												if (kind === 'number' && t.trim() !== '' && !isNumber(t)) {
													app.fail(`${c.name} needs a number`);
													e.currentTarget.value = texts[i];
													return;
												}
												if (kind === 'number' && t.trim() === '') {
													if (v !== null && c.nullable !== false) onedit?.(i, null);
													return;
												}
												commit(i, kind === 'number' ? t.trim() : t);
											}}
											onkeydown={(e) => {
												if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) e.currentTarget.blur();
											}}></textarea>
										<div class="tools">
											{#if kind === 'date' || kind === 'datetime' || kind === 'time'}
												<button
													class="tool-btn"
													onclick={(e) => {
														const r = (e.currentTarget.closest('.edit') as HTMLElement).getBoundingClientRect();
														picking = { i, kind, x: r.left, y: r.bottom + 4, nullable: c.nullable !== false };
													}}
													title="Pick a date"><CalendarDays size={12} /></button
												>
												<button class="tool-btn mono" onclick={() => onedit?.(i, now(kind))} title="Set to now">now</button>
											{:else if kind === 'uuid'}
												<button class="tool-btn mono" onclick={() => onedit?.(i, uuid())} title="Generate a random UUID">gen</button>
											{:else if kind === 'json' && v !== null}
												<button
													class="tool-btn mono"
													onclick={() => {
														const p = prettyJSON(String(v));
														if (p) onedit?.(i, JSON.stringify(JSON.parse(p)));
														else app.fail('Not valid JSON');
													}}
													title="Validate and compact JSON">{'{}'}</button
												>
											{/if}
											{#if v !== null && c.nullable !== false}
												<button class="tool-btn mono" onclick={() => onedit?.(i, null)} title="Set NULL">NULL</button>
											{/if}
										</div>
									</div>
								{/if}
							{:else}
								<pre class="value mono" class:null={v === null}>{v === null ? 'NULL' : texts[i]}</pre>
							{/if}
						</div>
					{/each}
				{/if}
			</section>
		{:else}
			<p class="none">No fields</p>
		{/each}

		{#if referencedBy.length}
			<section>
				<button class="group" onclick={() => toggle('refs')}>
					{#if collapsed.refs}<ChevronRight size={12} />{:else}<ChevronDown size={12} />{/if}
					<span>Referenced by</span>
					<span class="count">{referencedBy.length}</span>
				</button>
				{#if !collapsed.refs}
					{#each referencedBy as f (f.name)}
						{@const v = valueOf(f.refColumn)}
						<button class="ref" onclick={() => app.openRelated(f.schema, f.table, f.column, v)} disabled={v === null || v === undefined}>
							<CornerDownRight size={13} />
							<span class="ref-name mono">{f.table}<span class="muted">.{f.column}</span></span>
							<span class="ref-count">{counts[f.name] ?? '·'}</span>
							<ArrowUpRight size={13} class="go" />
						</button>
					{/each}
				{/if}
			</section>
		{/if}
	</div>
</aside>

{#if picking}
	{@const p = picking}
	<DatePicker
		kind={p.kind}
		value={text(row[p.i])}
		x={p.x}
		y={p.y}
		nullable={p.nullable}
		onpick={(v) => {
			onedit?.(p.i, v);
			picking = null;
		}}
		onclose={() => (picking = null)}
	/>
{/if}

<style>
	.pane {
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
		gap: 0.1rem;
		height: 2.5rem;
		flex: none;
		padding: 0 0.35rem 0 0.75rem;
		border-bottom: 1px solid var(--border);
	}
	.title {
		flex: 1;
		min-width: 0;
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.filter {
		padding: 0.5rem 0.6rem;
		border-bottom: 1px solid var(--border);
	}
	.filter input {
		width: 100%;
		height: 1.65rem;
		padding: 0 0.5rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--foreground);
		outline: none;
		user-select: text;
	}
	.filter input:focus {
		border-color: color-mix(in oklch, var(--foreground) 35%, var(--border));
	}
	.body {
		flex: 1;
		overflow: auto;
		padding-bottom: 1rem;
	}
	.group {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		width: 100%;
		height: 1.85rem;
		padding: 0 0.6rem;
		border: none;
		background: transparent;
		color: var(--muted-foreground);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
	}
	.group:hover {
		color: var(--foreground);
	}
	.count {
		margin-left: auto;
		font-weight: 400;
		letter-spacing: 0;
	}
	.field {
		padding: 0.3rem 0.75rem 0.45rem;
	}
	.field.changed textarea {
		background: var(--changed);
	}
	.label {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		margin-bottom: 0.25rem;
		font-size: 11px;
	}
	.label :global(svg) {
		color: var(--muted-foreground);
	}
	.name {
		font-weight: 600;
	}
	.type {
		margin-left: auto;
		color: var(--muted-foreground);
		font-size: 10.5px;
	}
	.rel {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		height: 1.25rem;
		padding: 0 0.4rem 0 0.1rem;
		border: 1px solid var(--border);
		border-radius: 0.3rem;
		background: transparent;
		color: var(--foreground);
		font: inherit;
		font-size: 10.5px;
	}
	.rel:hover {
		background: var(--accent);
	}
	.ref {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		width: 100%;
		height: 1.9rem;
		padding: 0 0.75rem;
		border: none;
		background: transparent;
		color: var(--foreground);
		text-align: left;
	}
	.ref :global(svg) {
		flex: none;
		color: var(--muted-foreground);
	}
	.ref:hover:not(:disabled) {
		background: var(--accent);
	}
	.ref:hover:not(:disabled) :global(.go) {
		color: var(--brand-ink);
	}
	.ref-name {
		flex: 1;
		min-width: 0;
		font-size: 12px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.ref-count {
		font-size: 11px;
		color: var(--muted-foreground);
		font-variant-numeric: tabular-nums;
	}
	.edit {
		position: relative;
	}
	textarea {
		display: block;
		width: 100%;
		min-height: 1.75rem;
		padding: 0.3rem 0.5rem;
		padding-right: 5.5rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--foreground);
		line-height: 1.45;
		resize: none;
		outline: none;
		overflow-y: hidden;
		user-select: text;
	}
	textarea:focus {
		border-color: color-mix(in oklch, var(--foreground) 35%, var(--border));
	}
	textarea::placeholder {
		color: var(--muted-foreground);
		opacity: 0.6;
		font-size: 11px;
	}
	.tools {
		position: absolute;
		top: 0.25rem;
		right: 0.25rem;
		display: flex;
		gap: 0.2rem;
		opacity: 0;
		transition: opacity var(--dur-fast) ease-out;
	}
	.edit:hover .tools,
	.edit:focus-within .tools {
		opacity: 1;
	}
	.tool-btn {
		display: grid;
		place-items: center;
		height: 1.2rem;
		min-width: 1.2rem;
		padding: 0 0.3rem;
		border: 1px solid var(--border);
		border-radius: 0.2rem;
		background: var(--background);
		color: var(--muted-foreground);
		font-size: 9.5px;
	}
	.tool-btn:hover {
		color: var(--foreground);
	}
	.bool-select {
		height: 1.75rem;
		min-width: 7rem;
		padding: 0 0.45rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--foreground);
		font-size: 12px;
	}
	.value {
		margin: 0;
		padding: 0.3rem 0.5rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		white-space: pre-wrap;
		word-break: break-word;
		max-height: 20rem;
		overflow: auto;
		user-select: text;
	}
	.value.null {
		color: var(--muted-foreground);
		font-size: 11px;
	}
	.none {
		margin: 0;
		padding: 1rem 0.75rem;
		color: var(--muted-foreground);
	}
</style>
