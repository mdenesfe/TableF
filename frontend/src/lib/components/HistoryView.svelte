<script lang="ts">
	// Query history, newest first and grouped by day. Click a run to open it
	// in a new query tab; Run executes it again, Copy copies the SQL.
	import { Copy, History, LoaderCircle, Play, RefreshCw, Search, Trash2 } from '@lucide/svelte';
	import { api } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import { dialogs } from '#lib/dialog.svelte.ts';
	import { compact } from '#lib/format.ts';
	import type { HistoryEntry } from '#lib/types.ts';

	let { active }: { active: boolean } = $props();

	const PAGE = 1000;
	let entries = $state.raw<HistoryEntry[]>([]);
	let loading = $state(false);
	let error = $state('');
	let search = $state('');
	let everywhere = $state(false);
	let limit = $state(PAGE);

	// Reload when the tab is shown, after each query run, and as the search
	// or scope changes (the search waits for a pause in typing).
	let seq = 0;
	$effect(() => {
		if (!active) return;
		void app.historyVersion;
		const conn = everywhere ? '' : (app.connection?.id ?? '');
		const q = search;
		const n = limit;
		const timer = setTimeout(
			async () => {
				const mine = ++seq;
				loading = true;
				try {
					const list = await api.listHistory(conn, q, n);
					if (mine === seq) ((entries = list), (error = ''));
				} catch (e) {
					if (mine === seq) error = (e as Error).message;
				} finally {
					if (mine === seq) loading = false;
				}
			},
			q ? 200 : 0
		);
		return () => clearTimeout(timer);
	});

	// ---------- grouping by day ----------
	const dayKey = (d: Date) => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
	const dayFormat = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
	const timeFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });

	function dayLabel(d: Date): string {
		const today = new Date();
		const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
		if (dayKey(d) === dayKey(today)) return 'Today';
		if (dayKey(d) === dayKey(yesterday)) return 'Yesterday';
		return dayFormat.format(d);
	}

	const days = $derived.by(() => {
		const out: { key: string; label: string; items: { e: HistoryEntry; time: string }[] }[] = [];
		for (const e of entries) {
			const d = new Date(e.at);
			const key = dayKey(d);
			let day = out.at(-1);
			if (day?.key !== key) out.push((day = { key, label: dayLabel(d), items: [] }));
			day.items.push({ e, time: timeFormat.format(d) });
		}
		return out;
	});

	/** The script on one line, whitespace collapsed. */
	const oneLine = (sql: string) => sql.replace(/\s+/g, ' ').trim();

	const connectionName = (id: string) => app.connections.find((c) => c.id === id)?.name ?? 'Deleted connection';

	function open(e: HistoryEntry) {
		app.openQuery(e.sql.endsWith('\n') ? e.sql : e.sql + '\n');
	}

	async function copySql(e: HistoryEntry) {
		await navigator.clipboard?.writeText(e.sql).catch(() => {});
		app.notify('info', 'Copied SQL');
	}

	async function clear() {
		const scope = everywhere ? 'every connection' : (app.connection?.name ?? 'this connection');
		const ok = await dialogs.confirm({
			title: 'Clear query history',
			message: `Delete the query history of ${scope}? This can't be undone.`,
			confirmLabel: 'Clear',
			danger: true
		});
		if (!ok) return;
		try {
			await api.clearHistory(everywhere ? '' : (app.connection?.id ?? ''));
			entries = [];
			app.notify('info', 'History cleared');
		} catch (e) {
			app.fail(e);
		}
	}
</script>

<div class="history">
	<div class="bar">
		<span class="icon-box"><History /></span>
		<span class="title">History</span>
		<div class="search">
			<Search size={13} />
			<input placeholder="Search SQL…" bind:value={search} spellcheck="false" />
		</div>
		<div class="toggle" role="tablist" aria-label="Scope">
			<button role="tab" aria-selected={!everywhere} class:on={!everywhere} onclick={() => (everywhere = false)}>This connection</button>
			<button role="tab" aria-selected={everywhere} class:on={everywhere} onclick={() => (everywhere = true)}>All</button>
		</div>
		<button class="btn ghost icon" onclick={() => app.historyVersion++} title="Refresh">
			{#if loading}<LoaderCircle size={14} class="spin" />{:else}<RefreshCw size={14} />{/if}
		</button>
		<button class="btn ghost icon danger" onclick={clear} title="Clear history" disabled={!entries.length}><Trash2 size={14} /></button>
	</div>

	<div class="list">
		{#if error}
			<p class="note err">{error}</p>
		{/if}
		{#if days.length}
			<div class="cols" class:everywhere role="row">
				<span>Time</span><span>Query</span>{#if everywhere}<span>Connection</span>{/if}<span>Database</span><span class="r">Rows</span><span class="r"
					>Duration</span
				><span></span>
			</div>
		{/if}
		{#each days as day (day.key)}
			<section>
				<h3><i class="day-dot"></i>{day.label}<span>{day.items.length.toLocaleString()} {day.items.length === 1 ? 'run' : 'runs'}</span></h3>
				{#each day.items as { e, time } (e.id)}
					<div class="run" class:everywhere class:failed={!!e.error} role="row">
						<button class="open" onclick={() => open(e)} title={e.error ? `${e.sql}\n\n${e.error}` : e.sql}>
							<span class="when mono"><i class="dot"></i>{time}</span>
							<span class="sql mono">{oneLine(e.sql)}</span>
						</button>
						{#if everywhere}<span class="cell ellipsis">{connectionName(e.connectionId)}</span>{/if}
						<span class="cell ellipsis mono">{e.database || '—'}</span>
						{#if e.error}
							<span class="cell r err" title={e.error}>error</span>
						{:else}
							<span class="cell r num">{compact(e.rows)}{e.partial ? '+' : ''}</span>
						{/if}
						<span class="cell r num">{e.durationMs < 1000 ? `${e.durationMs.toFixed(1)} ms` : `${(e.durationMs / 1000).toFixed(2)} s`}</span>
						<span class="acts">
							<button class="act" onclick={() => app.runQuery(e.sql)} title="Run again in a new tab"><Play size={11} fill="currentColor" /></button>
							<button class="act" onclick={() => copySql(e)} title="Copy SQL"><Copy size={11} /></button>
						</span>
					</div>
				{/each}
			</section>
		{:else}
			{#if !loading && !error}
				<p class="note">{search ? 'No runs match this search.' : 'Queries you run show up here, grouped by day.'}</p>
			{/if}
		{/each}
		{#if entries.length >= limit}
			<button class="more" onclick={() => (limit += PAGE)}>Show older runs</button>
		{/if}
	</div>
</div>

<style>
	.history {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
	}
	.bar {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		height: 2.6rem;
		flex: none;
		padding: 0 0.6rem 0 0.75rem;
		border-bottom: 1px solid var(--border);
	}
	.title {
		font-weight: 600;
	}
	.search {
		position: relative;
		flex: 1;
		max-width: 26rem;
		margin-left: 0.5rem;
	}
	.search :global(svg) {
		position: absolute;
		left: 0.5rem;
		top: 50%;
		transform: translateY(-50%);
		color: var(--muted-foreground);
	}
	.search input {
		width: 100%;
		height: 1.75rem;
		padding: 0 0.5rem 0 1.7rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--foreground);
		font-size: 12.5px;
		outline: none;
	}
	.search input:focus {
		border-color: color-mix(in oklch, var(--foreground) 35%, var(--border));
	}
	.toggle {
		display: flex;
		padding: 2px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
	}
	.toggle button {
		height: 1.45rem;
		padding: 0 0.55rem;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--muted-foreground);
		font-size: 12px;
		white-space: nowrap;
	}
	.toggle button.on {
		background: var(--accent);
		color: var(--foreground);
	}
	.bar :global(.spin) {
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	.danger:hover:not(:disabled) {
		color: var(--destructive);
	}
	.list {
		flex: 1;
		overflow: auto;
		padding: 0 0 1.5rem;
	}
	/* One grid for the column labels and every run, so everything lines up. */
	.cols,
	.run {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 7rem 4.5rem 5rem 3.6rem;
		align-items: center;
		column-gap: 0.75rem;
		padding: 0 0.6rem 0 0.75rem;
	}
	.cols.everywhere,
	.run.everywhere {
		grid-template-columns: minmax(0, 1fr) 8rem 7rem 4.5rem 5rem 3.6rem;
	}
	.cols {
		position: sticky;
		top: 0;
		z-index: 2;
		height: 1.7rem;
		border-bottom: 1px solid var(--border);
		background: var(--background);
		font-size: 10.5px;
		font-weight: 500;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--muted-foreground);
	}
	/* Time and query share the first track (the run's open button spans them). */
	.cols > span:first-child {
		display: none;
	}
	.cols > span:nth-child(2) {
		padding-left: 4.4rem;
	}
	.r {
		text-align: right;
	}
	h3 {
		position: sticky;
		top: 1.7rem;
		z-index: 1;
		display: flex;
		align-items: baseline;
		height: 2.3rem;
		box-sizing: border-box;
		gap: 0.4rem;
		margin: 0;
		padding: 0.9rem 0.75rem 0.25rem;
		background: var(--background);
		font-size: 11.5px;
		font-weight: 600;
	}
	h3 span {
		font-weight: 400;
		font-size: 11px;
		color: var(--muted-foreground);
		font-variant-numeric: tabular-nums;
	}
	/* Timeline: one rail down the left edge, a ring per day, a dot per run. */
	section {
		position: relative;
		animation: tf-drift var(--dur) var(--ease-out) both;
	}
	section::before {
		content: '';
		position: absolute;
		left: calc(0.75rem + 2.5px);
		top: 1.15rem;
		bottom: 0;
		width: 1px;
		background: color-mix(in oklch, var(--foreground) 24%, transparent);
	}
	section:last-of-type::before {
		bottom: 0.95rem; /* the rail ends at the oldest run */
	}
	.day-dot {
		position: relative;
		z-index: 1;
		width: 8px;
		height: 8px;
		flex: none;
		margin: 0 0.15rem 0 -1px;
		align-self: center;
		border: 2px solid var(--brand-ink);
		border-radius: 50%;
		background: var(--background);
	}
	.run {
		height: 1.9rem;
		border-bottom: 1px solid color-mix(in oklch, var(--border) 55%, transparent);
		font-size: 12px;
	}
	.run:hover {
		background: color-mix(in oklch, var(--accent) 55%, transparent);
	}
	.open {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		min-width: 0;
		height: 100%;
		padding: 0;
		border: none;
		background: transparent;
		color: var(--foreground);
		text-align: left;
	}
	.when {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		flex: none;
		width: 3.65rem;
		font-size: 11px;
		color: var(--muted-foreground);
		font-variant-numeric: tabular-nums;
	}
	.dot {
		position: relative;
		z-index: 1;
		width: 6px;
		height: 6px;
		flex: none;
		border-radius: 50%;
		background: var(--success);
		box-shadow: 0 0 0 3px var(--background);
	}
	.run:hover .dot {
		box-shadow: 0 0 0 3px color-mix(in oklch, var(--accent) 55%, var(--background));
	}
	.failed .dot {
		background: var(--destructive);
	}
	.sql {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 12px;
	}
	.cell {
		min-width: 0;
		font-size: 11.5px;
		color: var(--muted-foreground);
	}
	.ellipsis {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.num {
		font-variant-numeric: tabular-nums;
	}
	.err {
		color: var(--destructive);
	}
	.acts {
		display: flex;
		justify-content: flex-end;
		gap: 0.2rem;
	}
	.act {
		display: grid;
		place-items: center;
		width: 1.45rem;
		height: 1.45rem;
		padding: 0;
		border: none;
		border-radius: 0.3rem;
		background: transparent;
		color: var(--muted-foreground);
	}
	.act:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	.note {
		margin: 0;
		padding: 2rem 1rem;
		text-align: center;
		color: var(--muted-foreground);
	}
	.note.err {
		color: var(--destructive);
	}
	.more {
		display: block;
		margin: 0.5rem auto;
		padding: 0.3rem 0.8rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 12px;
	}
	.more:hover {
		background: var(--accent);
	}
</style>
