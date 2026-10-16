<script lang="ts">
	// "Export" dropdown: copy as Markdown / JSON, download CSV / Excel.
	import { Braces, ChevronDown, Copy, Download, Sheet } from '@lucide/svelte';
	import { api } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import { toCSV, toJSON, toMarkdown, toXLSX, type ExportKind, type Grid } from '#lib/export.ts';

	let {
		source,
		name,
		single = false,
		align = 'left'
	}: {
		/** Called when an item is picked, so it always exports what is on screen. */
		source: () => Grid | null;
		/** File name without extension. */
		name: string;
		/** One record: JSON is an object and Markdown a field/value table. */
		single?: boolean;
		align?: 'left' | 'right';
	} = $props();

	let open = $state(false);
	let root: HTMLDivElement;

	async function run(kind: ExportKind) {
		open = false;
		const g = source();
		if (!g) return;
		const n = g.rows.length;
		const what = single ? 'row' : `${n.toLocaleString()} row${n === 1 ? '' : 's'}`;
		if (kind === 'json' || kind === 'markdown') {
			let out: string;
			if (single && kind === 'json') out = JSON.stringify(Object.fromEntries(g.columns.map((c, i) => [c, g.rows[0]?.[i] ?? null])), null, 2);
			else if (single) out = toMarkdown({ columns: ['field', 'value'], rows: g.columns.map((c, i) => [c, g.rows[0]?.[i]]) });
			else out = kind === 'json' ? toJSON(g) : toMarkdown(g);
			await navigator.clipboard?.writeText(out).catch(() => {});
			app.notify('info', `Copied ${what} as ${kind === 'json' ? 'JSON' : 'Markdown'}`);
		} else if (kind === 'csv') {
			if (await api.saveFile(`${name}.csv`, toCSV(g))) app.notify('info', `Saved ${what} as CSV`);
		} else {
			const data = toXLSX(g, name);
			if (await api.saveFile(`${name}.xlsx`, data, 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'))
				app.notify('info', `Saved ${what} as Excel`);
		}
	}
</script>

<svelte:window
	onpointerdown={(e) => open && !root.contains(e.target as Node) && (open = false)}
	onkeydown={(e) => open && e.key === 'Escape' && (open = false)}
/>

<div class="export" bind:this={root}>
	<button class="trigger" class:open onclick={() => (open = !open)} aria-haspopup="menu" aria-expanded={open}>Export <ChevronDown size={13} /></button>
	{#if open}
		<div class="menu" class:right={align === 'right'} role="menu">
			<button role="menuitem" onclick={() => run('markdown')}><Copy size={14} /> Copy as Markdown</button>
			<button role="menuitem" onclick={() => run('json')}><Braces size={14} /> Copy as JSON</button>
			<span class="sep"></span>
			<button role="menuitem" onclick={() => run('csv')}><Download size={14} /> Download CSV</button>
			<button role="menuitem" onclick={() => run('xlsx')}><Sheet size={14} /> Download Excel</button>
		</div>
	{/if}
</div>

<style>
	.export {
		position: relative;
	}
	.trigger {
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
		white-space: nowrap;
	}
	.trigger:hover,
	.trigger.open {
		background: var(--accent);
	}
	.menu {
		position: absolute;
		top: calc(100% + 0.3rem);
		left: 0;
		z-index: 60;
		min-width: 12.5rem;
		padding: 0.25rem;
		display: flex;
		flex-direction: column;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		box-shadow: var(--shadow);
	}
	.menu.right {
		left: auto;
		right: 0;
		transform-origin: top right;
	}
	.menu button {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		height: 1.9rem;
		padding: 0 0.55rem;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		text-align: left;
		white-space: nowrap;
	}
	.menu button :global(svg) {
		flex: none;
		color: var(--muted-foreground);
	}
	.menu button:hover {
		background: var(--accent);
	}
	.sep {
		height: 1px;
		margin: 0.2rem 0.3rem;
		background: var(--border);
	}
</style>
