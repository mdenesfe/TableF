<script lang="ts">
	// Shows the row a foreign key value points to, next to the cell it came from.
	import { ArrowUpRight, LoaderCircle, Table2, X } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { api } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import { prefs } from '#lib/prefs.svelte.ts';
	import type { Cell, Column, ForeignKey } from '#lib/types.ts';

	let { fk, value, x, y, onclose }: { fk: ForeignKey; value: Cell; x: number; y: number; onclose: () => void } = $props();

	let loading = $state(true);
	let error = $state('');
	let columns = $state<Column[]>([]);
	let row = $state<Cell[] | null>(null);
	let root: HTMLDivElement;

	// Keep the card on screen.
	const left = $derived(Math.max(8, Math.min(x, window.innerWidth - 336)));
	const top = $derived(Math.max(8, Math.min(y, window.innerHeight - 380)));

	onMount(async () => {
		if (!app.session) return;
		try {
			const res = await app.track(
				api.getTableData(app.session.id, {
					schema: fk.refSchema,
					table: fk.refTable,
					limit: 1,
					offset: 0,
					orderBy: '',
					orderDir: '',
					where: '',
					match: [{ column: fk.refColumn, value }]
				})
			);
			columns = res.columns;
			row = res.rows[0] ?? null;
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loading = false;
		}
	});

	function open() {
		app.openRelated(fk.refSchema, fk.refTable, fk.refColumn, value);
		onclose();
	}

	const show = (v: Cell) => (v === null ? prefs.value.nullText : String(v));
</script>

<svelte:window onpointerdown={(e) => !root.contains(e.target as Node) && onclose()} onkeydown={(e) => e.key === 'Escape' && onclose()} />

<div class="peek" bind:this={root} style:left="{left}px" style:top="{top}px" role="dialog" aria-label="Related row">
	<header>
		<span class="icon-box"><Table2 /></span>
		<span class="title mono">{fk.refTable}<span class="muted">.{fk.refColumn} = {show(value)}</span></span>
		<button class="icon" onclick={onclose} aria-label="Close"><X size={13} /></button>
	</header>
	<div class="body">
		{#if loading}
			<p class="state"><LoaderCircle size={13} class="spin" /> Loading…</p>
		{:else if error}
			<p class="state err">{error}</p>
		{:else if !row}
			<p class="state">No matching row</p>
		{:else}
			{#each columns as c, i (c.name)}
				<div class="field">
					<span class="k mono">{c.name}</span>
					<span class="v mono" class:null={row[i] === null} title={show(row[i])}>{show(row[i])}</span>
				</div>
			{/each}
		{/if}
	</div>
	<footer>
		<button class="open" onclick={open}><ArrowUpRight size={13} /> Open {fk.refTable}</button>
	</footer>
</div>

<style>
	.peek {
		position: fixed;
		z-index: 300;
		width: 20rem;
		max-height: 23rem;
		display: flex;
		flex-direction: column;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
		overflow: hidden;
	}
	header {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		height: 2.2rem;
		flex: none;
		padding: 0 0.3rem 0 0.7rem;
		border-bottom: 1px solid var(--border);
	}

	.title {
		flex: 1;
		min-width: 0;
		font-size: 12px;
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.title .muted {
		font-weight: 400;
	}
	.icon {
		display: grid;
		place-items: center;
		width: 1.5rem;
		height: 1.5rem;
		padding: 0;
		border: none;
		border-radius: 0.25rem;
		background: transparent;
		color: var(--muted-foreground);
	}
	.icon:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	.body {
		flex: 1;
		overflow: auto;
		padding: 0.3rem 0;
	}
	.field {
		display: grid;
		grid-template-columns: 7.5rem 1fr;
		gap: 0.6rem;
		padding: 0.2rem 0.75rem;
		font-size: 12px;
	}
	.field:hover {
		background: var(--accent);
	}
	.k {
		color: var(--muted-foreground);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.v {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		user-select: text;
	}
	.v.null {
		color: var(--muted-foreground);
		opacity: 0.7;
	}
	.state {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		margin: 0;
		padding: 0.75rem;
		color: var(--muted-foreground);
		font-size: 12px;
	}
	.state.err {
		color: var(--destructive);
	}
	footer {
		padding: 0.35rem;
		border-top: 1px solid var(--border);
	}
	.open {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		width: 100%;
		height: 1.85rem;
		padding: 0 0.5rem;
		border: none;
		border-radius: 0.3rem;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		font-weight: 500;
		text-align: left;
	}
	.open :global(svg) {
		color: var(--brand-ink);
	}
	.open:hover {
		background: var(--accent);
	}
</style>
