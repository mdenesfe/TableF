<script lang="ts">
	import { Code, Database, Info, Monitor, Moon, Palette, ShieldCheck, Sun, X } from '@lucide/svelte';
	import { prefs, type Density, type KeywordCase, type Theme } from '#lib/prefs.svelte.ts';
	import { VERSION } from '#lib/version.ts';

	let { onclose }: { onclose: () => void } = $props();

	const p = $derived(prefs.value);

	const sections = [
		{ id: 'appearance', label: 'Appearance', Icon: Palette },
		{ id: 'editor', label: 'Editor', Icon: Code },
		{ id: 'data', label: 'Data', Icon: Database },
		{ id: 'safety', label: 'Safety', Icon: ShieldCheck },
		{ id: 'about', label: 'About', Icon: Info }
	] as const;
	type Section = (typeof sections)[number]['id'];
	let section = $state<Section>('appearance');

	const themes: { v: Theme; label: string; Icon: typeof Sun }[] = [
		{ v: 'system', label: 'System', Icon: Monitor },
		{ v: 'light', label: 'Light', Icon: Sun },
		{ v: 'dark', label: 'Dark', Icon: Moon }
	];
	const scales = [
		{ v: 0.9, label: 'Compact' },
		{ v: 1, label: 'Default' },
		{ v: 1.1, label: 'Large' }
	];
	const densities: { v: Density; label: string }[] = [
		{ v: 'compact', label: 'Compact' },
		{ v: 'default', label: 'Default' },
		{ v: 'comfortable', label: 'Roomy' }
	];
	const cases: { v: KeywordCase; label: string }[] = [
		{ v: 'upper', label: 'SELECT' },
		{ v: 'lower', label: 'select' },
		{ v: 'preserve', label: 'As typed' }
	];

	function keydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			onclose();
		}
	}
</script>

<svelte:window onkeydown={keydown} />

{#snippet toggle(label: string, hint: string, checked: boolean, set: (v: boolean) => void)}
	<label class="row check">
		<span class="label">
			{label}
			{#if hint}<small>{hint}</small>{/if}
		</span>
		<input type="checkbox" {checked} onchange={(e) => set(e.currentTarget.checked)} />
		<span class="track"><span class="thumb"></span></span>
	</label>
{/snippet}

<div class="overlay" role="presentation" onclick={(e) => e.target === e.currentTarget && onclose()}>
	<div class="dialog" role="dialog" aria-label="Preferences">
		<nav class="side" aria-label="Sections">
			<h2>Preferences</h2>
			{#each sections as s (s.id)}
				<button class:on={section === s.id} onclick={() => (section = s.id)}>
					<span class="icon-box"><s.Icon /></span>
					{s.label}
				</button>
			{/each}
			<span class="spacer"></span>
			<button class="reset" onclick={() => prefs.reset()}>Reset to defaults</button>
		</nav>

		<div class="main">
			<header>
				<h3>{sections.find((s) => s.id === section)?.label}</h3>
				<button class="btn ghost icon xs" onclick={onclose} aria-label="Close"><X size={15} /></button>
			</header>

			{#key section}
				<div class="content">
					{#if section === 'appearance'}
						<div class="row">
							<span class="label">Theme</span>
							<div class="seg">
								{#each themes as t (t.v)}
									<button class:on={p.theme === t.v} onclick={() => prefs.set('theme', t.v)}><t.Icon size={13} /> {t.label}</button>
								{/each}
							</div>
						</div>
						<div class="row">
							<span class="label">Interface size</span>
							<div class="seg">
								{#each scales as s (s.v)}
									<button class:on={p.scale === s.v} onclick={() => prefs.set('scale', s.v)}>{s.label}</button>
								{/each}
							</div>
						</div>
						<div class="row">
							<span class="label">Row height <small>Table and query grids</small></span>
							<div class="seg">
								{#each densities as d (d.v)}
									<button class:on={p.density === d.v} onclick={() => prefs.set('density', d.v)}>{d.label}</button>
								{/each}
							</div>
						</div>
						{@render toggle('Striped rows', 'Shade every other row in grids', p.zebra, (v) => prefs.set('zebra', v))}
						{@render toggle('Header animation', 'The moving 1/0 field in the connection color', p.headerAnimation, (v) => prefs.set('headerAnimation', v))}
						{@render toggle('Reduce motion', 'Turn off interface animations', p.reduceMotion, (v) => prefs.set('reduceMotion', v))}
					{:else if section === 'editor'}
						<div class="row">
							<span class="label">Font size</span>
							<div class="stepper">
								<button onclick={() => prefs.set('editorFontSize', Math.max(10, p.editorFontSize - 1))} aria-label="Smaller">−</button>
								<span class="mono">{p.editorFontSize}px</span>
								<button onclick={() => prefs.set('editorFontSize', Math.min(22, p.editorFontSize + 1))} aria-label="Larger">+</button>
							</div>
						</div>
						<div class="row">
							<span class="label">Tab size</span>
							<div class="seg">
								{#each [2, 4] as n (n)}
									<button class="mono" class:on={p.editorTabSize === n} onclick={() => prefs.set('editorTabSize', n)}>{n}</button>
								{/each}
							</div>
						</div>
						<div class="row">
							<span class="label">Keyword case <small>Used by Format and completions</small></span>
							<div class="seg">
								{#each cases as c (c.v)}
									<button class:mono={c.v !== 'preserve'} class:on={p.keywordCase === c.v} onclick={() => prefs.set('keywordCase', c.v)}>{c.label}</button>
								{/each}
							</div>
						</div>
						{@render toggle('Line numbers', '', p.editorLineNumbers, (v) => prefs.set('editorLineNumbers', v))}
						{@render toggle('Wrap long lines', '', p.editorWrap, (v) => prefs.set('editorWrap', v))}
					{:else if section === 'data'}
						<div class="row">
							<span class="label">Rows per page</span>
							<select class="input sel" value={p.pageSize} onchange={(e) => prefs.set('pageSize', Number(e.currentTarget.value))}>
								{#each [100, 300, 1000, 5000] as n (n)}<option value={n}>{n}</option>{/each}
							</select>
						</div>
						<div class="row">
							<span class="label">Show NULL as</span>
							<div class="seg">
								{#each ['NULL', 'null', '∅', '—'] as t (t)}
									<button class="mono" class:on={p.nullText === t} onclick={() => prefs.set('nullText', t)}>{t}</button>
								{/each}
							</div>
						</div>
						{@render toggle('Restore last session', 'Reopen the connection, tabs and layout on launch', p.restoreSession, (v) =>
							prefs.set('restoreSession', v)
						)}
					{:else if section === 'safety'}
						{@render toggle('Confirm destructive queries', 'DELETE or UPDATE without WHERE, DROP, TRUNCATE', p.confirmDestructive, (v) =>
							prefs.set('confirmDestructive', v)
						)}
						{@render toggle('Confirm before discarding edits', 'When reloading, sorting or filtering with unsaved changes', p.confirmDiscard, (v) =>
							prefs.set('confirmDiscard', v)
						)}
					{:else}
						<div class="about">
							<svg class="logo" viewBox="0 0 64 64" aria-hidden="true">
								<rect x="1" y="1" width="62" height="62" rx="14" />
								<g transform="translate(8 8) scale(2)">
									<path d="M9 17c2 0 2.8-1 2.8-2.8V10c0-2 1-3.3 3.2-3" />
									<path d="M9 11.2h5.7" />
								</g>
							</svg>
							<div>
								<p class="name">TableF <span class="mono muted">v{VERSION}</span></p>
								<p class="muted">Free and open-source database client.</p>
							</div>
						</div>
						<p class="note muted">Preferences are saved on this device.</p>
					{/if}
				</div>
			{/key}
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
		display: grid;
		grid-template-columns: 11.5rem 1fr;
		width: min(44rem, 100%);
		height: min(30rem, calc(100vh - 2rem));
		overflow: hidden;
		background: var(--background);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
	}
	.side {
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 0.75rem 0.5rem 0.6rem;
		background: var(--sidebar);
		border-right: 1px solid var(--sidebar-border);
	}
	.side h2 {
		margin: 0.1rem 0.45rem 0.7rem;
		font-size: var(--text-lg);
		font-weight: 600;
	}
	.side button {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		height: 1.9rem;
		padding: 0 0.45rem;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 13px;
		text-align: left;
	}
	.side button :global(.icon-box) {
		opacity: 0.8;
	}
	.side button:hover {
		background: color-mix(in oklch, var(--sidebar-accent) 70%, transparent);
	}
	.side button.on {
		background: var(--sidebar-accent);
		font-weight: 500;
	}
	.side button.on :global(.icon-box) {
		opacity: 1;
	}
	.spacer {
		flex: 1;
	}
	.side .reset {
		height: 1.7rem;
		color: var(--muted-foreground);
		font-size: var(--text-sm);
	}
	.main {
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}
	header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		height: 2.9rem;
		flex: none;
		padding: 0 0.5rem 0 1.1rem;
		border-bottom: 1px solid var(--border);
	}
	h3 {
		margin: 0;
		font-size: var(--text-base);
		font-weight: 600;
	}
	.content {
		flex: 1;
		overflow: auto;
		padding: 0.4rem 1.1rem 1rem;
		animation: tf-drift var(--dur) var(--ease-out) both;
	}
	.row {
		position: relative;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		min-height: 2.9rem;
		border-bottom: 1px solid color-mix(in oklab, var(--border) 60%, transparent);
	}
	.label {
		display: flex;
		flex-direction: column;
		font-size: var(--text-base);
	}
	.label small {
		font-size: var(--text-xs);
		color: var(--muted-foreground);
	}
	.seg {
		display: flex;
		flex: none;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
	}
	.seg button {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		height: 1.75rem;
		padding: 0 0.65rem;
		border: none;
		border-right: 1px solid var(--border);
		background: transparent;
		color: var(--muted-foreground);
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.seg button:last-child {
		border-right: none;
	}
	.seg button:hover {
		color: var(--foreground);
	}
	.seg button.on {
		background: var(--accent);
		color: var(--foreground);
	}
	.seg button.on :global(svg) {
		color: var(--brand-ink);
	}
	.stepper {
		display: flex;
		align-items: center;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
	}
	.stepper button {
		width: 1.75rem;
		height: 1.75rem;
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: var(--text-lg);
	}
	.stepper button:hover {
		background: var(--accent);
	}
	.stepper span {
		min-width: 3rem;
		text-align: center;
		font-size: var(--text-sm);
		border-left: 1px solid var(--border);
		border-right: 1px solid var(--border);
		line-height: 1.75rem;
	}
	.sel {
		width: 6.5rem;
		height: 1.75rem;
		font-size: var(--text-sm);
	}
	.check input {
		position: absolute;
		opacity: 0;
		pointer-events: none;
	}
	.track {
		position: relative;
		flex: none;
		width: 1.9rem;
		height: 1.1rem;
		border-radius: 1rem;
		background: color-mix(in oklab, var(--foreground) 18%, transparent);
		transition: background 0.15s;
	}
	.thumb {
		position: absolute;
		top: 0.15rem;
		left: 0.15rem;
		width: 0.8rem;
		height: 0.8rem;
		border-radius: 50%;
		background: var(--card);
		box-shadow: 0 1px 2px oklch(0 0 0 / 0.25);
		transition: transform 0.15s;
	}
	.check input:checked + .track {
		background: var(--brand);
	}
	.check input:checked + .track .thumb {
		transform: translateX(0.8rem);
		background: #fff;
	}
	.check input:focus-visible + .track {
		outline: 1px solid color-mix(in oklab, var(--brand) 60%, transparent);
		outline-offset: 2px;
	}
	.about {
		display: flex;
		align-items: center;
		gap: 0.85rem;
		padding: 1rem 0 0.75rem;
	}
	.about p {
		margin: 0;
	}
	.about .name {
		font-size: var(--text-lg);
		font-weight: 600;
	}
	.logo {
		width: 2.6rem;
		height: 2.6rem;
		fill: none;
		stroke: #fff;
		stroke-width: 1.9;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.logo rect {
		fill: var(--brand);
		stroke: none;
	}
	.note {
		margin: 0.5rem 0 0;
		font-size: var(--text-sm);
	}
	@media (max-width: 560px) {
		.dialog {
			grid-template-columns: 1fr;
			grid-template-rows: auto 1fr;
		}
		.side {
			flex-direction: row;
			flex-wrap: wrap;
			border-right: none;
			border-bottom: 1px solid var(--sidebar-border);
		}
		.side h2,
		.spacer,
		.side .reset {
			display: none;
		}
	}
</style>
