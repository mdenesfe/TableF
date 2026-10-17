<script lang="ts">
	import { X } from '@lucide/svelte';
	import { isDesktop, isMac as mac, modLabel as M, tabModLabel } from '#lib/api.ts';

	let { onclose }: { onclose: () => void } = $props();

	const S = mac ? '⇧' : 'Shift';
	const A = mac ? '⌥' : 'Alt';
	const TM = tabModLabel;
	const browserNote = mac && !isDesktop;

	const groups: { title: string; items: [string[], string][] }[] = [
		{
			title: 'General',
			items: [
				[[TM, 'T'], 'New query tab'],
				[[TM, 'W'], 'Close tab'],
				[[TM, '1–9'], 'Go to tab'],
				[['Ctrl', 'Tab'], 'Next tab'],
				[['Ctrl', S, 'Tab'], 'Previous tab'],
				[[M, '\\'], 'Split pane right'],
				[[S, M, '\\'], 'Split pane down'],
				[['Drag tab'], 'Reorder, move or split'],
				[[M, 'B'], 'Toggle sidebar'],
				[[M, 'P'], 'Search tables'],
				[[M, '/'], 'Keyboard shortcuts'],
				[[M, ','], 'Preferences']
			]
		},
		{
			title: 'Table',
			items: [
				[[M, 'S'], 'Commit changes'],
				[[M, 'Z'], 'Undo'],
				[[M, S, 'Z'], 'Redo'],
				[[M, 'R'], 'Refresh'],
				[[M, 'F'], 'Filter (WHERE)'],
				[[M, 'I'], 'Insert row'],
				[[M, 'D'], 'Duplicate rows'],
				[['⌫'], 'Delete rows'],
				[['Esc'], 'Close record pane']
			]
		},
		{
			title: 'Grid',
			items: [
				[['↑', '↓', '←', '→'], 'Move'],
				[[S, '↑↓'], 'Extend selection'],
				[[M, '↑↓←→'], 'Jump to edge'],
				[['Home', 'End'], 'First / last column'],
				[['PgUp', 'PgDn'], 'Page up / down'],
				[['Tab'], 'Next cell'],
				[['Enter', 'F2'], 'Edit cell'],
				[['Type'], 'Replace cell value'],
				[[M, 'A'], 'Select all rows'],
				[[M, 'C'], 'Copy'],
				[[M, 'V'], 'Paste'],
				[['Click', S], 'Select range'],
				[['Click', M], 'Toggle row'],
				[['Drag #'], 'Select rows'],
				[[S, A, 'Drag'], 'Select a block of cells'],
				[['Middle-drag'], 'Select a block of cells'],
				[[S, A, '↑↓←→'], 'Grow the block'],
				[[A, 'Click header'], 'Select the column'],
				[['Dbl-click edge'], 'Fit column']
			]
		},
		{
			title: 'Query editor',
			items: [
				[[M, '↵'], 'Run current statement'],
				[[M, S, '↵'], 'Run all'],
				[[M, 'F'], 'Find'],
				[[M, A, 'F'], 'Replace'],
				[[M, '/'], 'Toggle comment'],
				[['Ctrl', 'Space'], 'Autocomplete']
			]
		}
	];

	function keydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			onclose();
		}
	}
</script>

<svelte:window onkeydown={keydown} />

<div class="overlay" role="presentation">
	<div class="dialog" role="dialog" aria-label="Keyboard shortcuts">
		<header>
			<h2>Keyboard shortcuts</h2>
			<button class="btn ghost icon xs" onclick={onclose} aria-label="Close"><X size={15} /></button>
		</header>
		{#if browserNote}
			<p class="note">In the browser ⌘T, ⌘W and ⌘1–9 belong to the browser, so TableF uses ⌃ for them. The desktop app uses ⌘.</p>
		{/if}
		<div class="cols">
			{#each groups as g (g.title)}
				<section>
					<h3>{g.title}</h3>
					{#each g.items as [keys, label] (label)}
						<div class="row">
							<span>{label}</span>
							<span class="keys"
								>{#each keys as k (k)}<kbd>{k}</kbd>{/each}</span
							>
						</div>
					{/each}
				</section>
			{/each}
		</div>
	</div>
</div>

<style>
	.overlay {
		position: fixed;
		inset: 0;
		z-index: 1000;
		display: grid;
		place-items: center;
		padding: 1rem;
		background: var(--overlay);
	}
	.dialog {
		width: min(52rem, 100%);
		max-height: calc(100vh - 2rem);
		overflow: auto;
		background: var(--background);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
	}
	header {
		position: sticky;
		top: 0;
		display: flex;
		align-items: center;
		justify-content: space-between;
		height: 2.75rem;
		padding: 0 0.5rem 0 1rem;
		background: var(--background);
		border-bottom: 1px solid var(--border);
	}
	h2 {
		margin: 0;
		font-size: var(--text-lg);
		font-weight: 600;
	}
	.cols {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(22rem, 1fr));
		gap: 0.5rem 1.5rem;
		padding: 0.75rem 1rem 1rem;
	}
	.note {
		margin: 0.75rem 1rem 0;
		padding: 0.5rem 0.7rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--muted-foreground);
	}
	h3 {
		margin: 0.5rem 0 0.35rem;
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--muted-foreground);
	}
	.row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		height: 1.85rem;
		border-bottom: 1px solid color-mix(in oklch, var(--border) 60%, transparent);
	}
	.keys {
		display: flex;
		gap: 0.2rem;
	}
</style>
