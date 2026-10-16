<script lang="ts">
	import { ChevronDown, ChevronLeft, ChevronRight } from '@lucide/svelte';

	let {
		page,
		limit,
		total,
		noun = 'rows',
		limits = [100, 300, 1000, 5000],
		onpage,
		onlimit
	}: {
		page: number; // zero-based
		limit: number;
		total: number;
		noun?: string;
		limits?: number[];
		onpage: (p: number) => void;
		onlimit: (n: number) => void;
	} = $props();

	const fmt = new Intl.NumberFormat();
	const pages = $derived(Math.max(1, Math.ceil(total / limit)));
	const from = $derived(total === 0 ? 0 : page * limit + 1);
	const to = $derived(Math.min(total, (page + 1) * limit));

	// 1 … p-1 p p+1 … N, always 7 slots at most.
	const items = $derived.by(() => {
		const n = pages;
		const cur = page + 1;
		if (n <= 7) return Array.from({ length: n }, (_, i) => i + 1);
		if (cur <= 4) return [1, 2, 3, 4, 5, 0, n];
		if (cur >= n - 3) return [1, 0, n - 4, n - 3, n - 2, n - 1, n];
		return [1, 0, cur - 1, cur, cur + 1, 0, n];
	});
</script>

<div class="footer">
	<span class="range muted">
		{#key `${from}-${to}-${total}`}<span class="tf-text"><span class="strong">{fmt.format(from)}-{fmt.format(to)}</span> / {fmt.format(total)}</span>{/key}
		{noun}
	</span>
	<span class="spacer"></span>

	<label class="per-page muted">
		<select value={limit} onchange={(e) => onlimit(Number(e.currentTarget.value))} aria-label="Rows per page">
			{#each limits as n (n)}<option value={n}>{n} / page</option>{/each}
		</select>
		<ChevronDown size={13} />
	</label>

	<div class="pages" role="navigation" aria-label="Pagination">
		<button onclick={() => onpage(page - 1)} disabled={page === 0} aria-label="Previous page">
			<ChevronLeft size={14} />
		</button>
		{#each items as n, i (i)}
			{#if n === 0}
				<span class="gap">···</span>
			{:else}
				<button class:on={n === page + 1} onclick={() => onpage(n - 1)}>{n}</button>
			{/if}
		{/each}
		<button onclick={() => onpage(page + 1)} disabled={page + 1 >= pages} aria-label="Next page">
			<ChevronRight size={14} />
		</button>
	</div>
</div>

<style>
	.footer {
		display: flex;
		align-items: center;
		gap: 0.9rem;
		min-height: 2.1rem;
		padding: 0.2rem 0.5rem 0.2rem 0.75rem;
		border-top: 1px solid var(--border);
		font-size: var(--text-sm);
		font-variant-numeric: tabular-nums;
	}
	.strong {
		color: var(--foreground);
	}
	.spacer {
		flex: 1;
	}
	.per-page {
		position: relative;
		display: flex;
		align-items: center;
	}
	.per-page select {
		appearance: none;
		border: none;
		background: transparent;
		color: var(--muted-foreground);
		font-size: var(--text-sm);
		padding: 0 1.1rem 0 0;
		outline: none;
	}
	.per-page select option {
		background: var(--popover);
	}
	.per-page :global(svg) {
		position: absolute;
		right: 0;
		pointer-events: none;
	}
	.pages {
		display: flex;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
	}
	.pages button,
	.gap {
		display: grid;
		place-items: center;
		min-width: 1.6rem;
		height: 1.5rem;
		padding: 0 0.4rem;
		border: none;
		border-right: 1px solid var(--border);
		background: transparent;
		color: var(--muted-foreground);
		font-size: var(--text-sm);
	}
	.pages > :last-child {
		border-right: none;
	}
	.pages button:hover:not(:disabled) {
		background: var(--accent);
		color: var(--foreground);
	}
	.pages button.on {
		background: var(--accent);
		color: var(--foreground);
		font-weight: 500;
	}
	.pages button:disabled {
		opacity: 0.4;
	}
	@media (max-width: 720px) {
		.per-page {
			display: none;
		}
	}
</style>
