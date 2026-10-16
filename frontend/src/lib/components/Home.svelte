<script lang="ts">
	import { Check, CopyPlus, Link, LoaderCircle, Pencil, Plus, Search, Settings, Trash2 } from '@lucide/svelte';
	import { formatURL, parseURL } from '#lib/connurl.ts';
	import { api, modLabel, targetIn } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import type { ConnectionConfig } from '#lib/types.ts';
	import ConnectionForm from './ConnectionForm.svelte';
	import { dialogs } from '#lib/dialog.svelte.ts';
	import { VERSION } from '#lib/version.ts';
	import { engine } from '#lib/engines.ts';
	import DbLogo from './DbLogo.svelte';
	import HeroField from './HeroField.svelte';

	let editing = $state<Partial<ConnectionConfig> | null | undefined>(undefined); // undefined = closed
	let copied = $state('');
	let search = $state('');
	let connecting = $state('');
	let cursor = $state(0);

	let tag = $state('');
	const allTags = $derived([...new Set(app.connections.flatMap((c) => c.tags ?? []))].sort());
	const filtered = $derived(
		app.connections.filter(
			(c) =>
				(!tag || c.tags?.includes(tag)) &&
				`${c.name} ${c.host} ${c.database} ${c.filePath} ${(c.tags ?? []).join(' ')}`.toLowerCase().includes(search.toLowerCase())
		)
	);

	function address(c: ConnectionConfig) {
		const e = engine(c.driver);
		if (e.kind === 'file') return c.filePath || 'in-memory';
		if (c.driver === 'mongodb' && c.options?.uri) return c.options.uri.replace(/:\/\/([^:@/]+):[^@/]*@/, '://$1@');
		if (e.kind === 'cloud')
			return [e.label, c.options?.account || c.options?.projectId || c.options?.region || c.options?.databaseId || c.options?.url].filter(Boolean).join(' · ');
		const user = c.user ? `${c.user}@` : '';
		const db = c.database ? `/${c.database}` : '';
		return `${user}${c.host}:${c.port}${db}`;
	}

	async function open(c: ConnectionConfig) {
		if (app.busy) return;
		connecting = c.id;
		await app.connect(c);
		connecting = '';
	}

	// One click edits, a double click connects. The edit waits a moment so a
	// double click never opens the form on its way to connecting.
	let clickTimer: ReturnType<typeof setTimeout> | undefined;
	function rowClick(c: ConnectionConfig) {
		clearTimeout(clickTimer);
		clickTimer = setTimeout(() => (editing = c), 230);
	}
	function rowDouble(c: ConnectionConfig) {
		clearTimeout(clickTimer);
		open(c);
	}

	// The tagline types itself out once.
	const TAGLINE = 'open-source database client';
	let typed = $state(matchMedia('(prefers-reduced-motion: reduce)').matches ? TAGLINE.length : 0);
	$effect(() => {
		if (typed >= TAGLINE.length) return;
		const id = setTimeout(() => typed++, typed === 0 ? 450 : 22);
		return () => clearTimeout(id);
	});

	async function remove(c: ConnectionConfig) {
		const ok = await dialogs.confirm({
			title: 'Delete connection?',
			message: `"${c.name}" will be removed from this device.`,
			confirmLabel: 'Delete',
			danger: true
		});
		if (!ok) return;
		try {
			await api.deleteConnection(c.id);
			await app.loadConnections();
		} catch (e) {
			app.fail(e);
		}
	}

	async function copyURL(c: ConnectionConfig) {
		await navigator.clipboard?.writeText(formatURL(c, true)).catch(() => {});
		copied = c.id;
		setTimeout(() => copied === c.id && (copied = ''), 1200);
	}

	function duplicate(c: ConnectionConfig) {
		editing = { ...c, id: '', name: `${c.name} copy` };
	}

	// Pasting a connection URL anywhere outside an input starts a new connection from it.
	function paste(e: ClipboardEvent) {
		if (editing !== undefined || targetIn(e, 'input, textarea')) return;
		const parsed = parseURL(e.clipboardData?.getData('text') ?? '');
		if (!parsed) return;
		e.preventDefault();
		editing = parsed;
	}

	async function closed(saved?: ConnectionConfig) {
		editing = undefined;
		if (saved) await app.loadConnections();
	}

	function keydown(e: KeyboardEvent) {
		if (editing !== undefined || !filtered.length) return;
		if (e.key === 'ArrowDown') cursor = Math.min(filtered.length - 1, cursor + 1);
		else if (e.key === 'ArrowUp') cursor = Math.max(0, cursor - 1);
		else if (e.key === 'Enter') open(filtered[cursor]);
		else return;
		e.preventDefault();
	}
</script>

<svelte:window onkeydown={keydown} onpaste={paste} />

<div class="home">
	<header class="hero drag" aria-hidden="true">
		<HeroField />
	</header>

	<main class="body">
		<div class="column">
			<div class="intro">
				<svg class="logo" viewBox="0 0 64 64" aria-hidden="true">
					<rect x="1" y="1" width="62" height="62" rx="14" />
					<g transform="translate(8 8) scale(2)">
						<path d="M9 17c2 0 2.8-1 2.8-2.8V10c0-2 1-3.3 3.2-3" />
						<path d="M9 11.2h5.7" />
					</g>
				</svg>
				<div class="title">
					<span class="word">TableF <span class="ver mono">v{VERSION}</span></span>
					<span class="tagline mono"><span class="prompt">$</span> {TAGLINE.slice(0, typed)}<span class="caret"></span></span>
				</div>
				<button class="btn ghost icon prefs" onclick={() => (app.prefsOpen = true)} title="Preferences ({modLabel},)"><Settings size={15} /></button>
			</div>
			<div class="toolbar">
				<label class="search">
					<Search size={13} />
					<input placeholder="Search" bind:value={search} oninput={() => (cursor = 0)} spellcheck="false" />
				</label>
				<button class="btn new" onclick={() => (editing = null)}><Plus size={14} /> New connection</button>
			</div>

			{#if allTags.length}
				<div class="tagbar" role="radiogroup" aria-label="Filter by tag">
					<button class="tagchip" class:on={!tag} onclick={() => ((tag = ''), (cursor = 0))}>All</button>
					{#each allTags as t (t)}
						<button class="tagchip mono" class:on={tag === t} onclick={() => ((tag = tag === t ? '' : t), (cursor = 0))}>{t}</button>
					{/each}
				</div>
			{/if}

			<div class="frame">
				<ul class="list tf-stagger">
					{#each filtered as c, i (c.id)}
						<li class:on={i === cursor} style:--i={i} onmouseenter={() => (cursor = i)}>
							<button class="row" onclick={() => rowClick(c)} ondblclick={() => rowDouble(c)} disabled={app.busy}>
								<DbLogo driver={c.driver} color={c.color} size={30} />
								<span class="text">
									<span class="name">
										{c.name}
										{#each c.tags ?? [] as t (t)}<span class="ctag mono">{t}</span>{/each}
									</span>
									<span class="addr mono">
										{#if c.sshEnabled}<span class="tag">ssh {c.sshHost}</span>{/if}
										{address(c)}
									</span>
								</span>
								{#if connecting === c.id}<LoaderCircle size={14} class="spin" />{/if}
							</button>
							<div class="tools">
								<button class="btn ghost icon xs" onclick={() => copyURL(c)} title="Copy URL">
									{#if copied === c.id}<Check size={13} />{:else}<Link size={13} />{/if}
								</button>
								<button class="btn ghost icon xs" onclick={() => duplicate(c)} title="Duplicate"><CopyPlus size={13} /></button>
								<button class="btn ghost icon xs" onclick={() => (editing = c)} title="Edit"><Pencil size={13} /></button>
								<button class="btn ghost icon xs" onclick={() => remove(c)} title="Delete"><Trash2 size={13} /></button>
							</div>
						</li>
					{:else}
						<li class="none mono">
							{#if app.connections.length}
								<span class="muted">no matches for</span> "{search}"
							{:else}
								<span class="muted">No connections yet</span>
							{/if}
						</li>
					{/each}
				</ul>
			</div>

			<p class="hints">Click to edit · Double-click to connect · Paste a URL with <kbd>{modLabel}V</kbd></p>
		</div>
	</main>
</div>

{#if editing !== undefined}
	<ConnectionForm initial={editing} onclose={closed} />
{/if}

<style>
	.home {
		display: grid;
		grid-template-rows: auto 1fr;
		height: 100%;
		background-color: var(--background);
		/* Engineering paper: a faint dot grid under everything. */
		background-image: radial-gradient(color-mix(in oklab, var(--foreground) 9%, transparent) 1px, transparent 1.2px);
		background-size: 24px 24px;
		background-position: 12px 12px;
		overflow: hidden;
		isolation: isolate;
	}

	/* ---------- hero ---------- */
	.hero {
		position: relative;
		height: clamp(5rem, 14vh, 7.5rem);
		overflow: hidden;
		border-bottom: 1px solid var(--border);
	}
	.intro {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		margin-bottom: 1.5rem;
		animation: tf-drift var(--dur-slow) var(--ease-out) both;
	}
	.logo {
		width: 2.25rem;
		height: 2.25rem;
		flex: none;
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
	.title {
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
		min-width: 0;
	}
	.word {
		font-size: var(--text-lg);
		font-weight: 600;
		letter-spacing: -0.01em;
	}
	.ver {
		margin-left: 0.3rem;
		font-size: 11px;
		font-weight: 400;
		color: var(--muted-foreground);
	}
	.tagline {
		font-size: 11.5px;
		color: var(--muted-foreground);
	}
	.prefs {
		margin-left: auto;
	}
	.prompt {
		color: var(--brand-ink);
		font-weight: 600;
	}
	.caret {
		display: inline-block;
		width: 0.5em;
		height: 1.05em;
		margin-left: 2px;
		vertical-align: -0.18em;
		background: currentColor;
		animation: blink 1.05s steps(1) infinite;
	}
	@keyframes blink {
		50% {
			opacity: 0;
		}
	}

	/* ---------- connections ---------- */
	.body {
		min-height: 0;
		overflow: auto;
		padding: 1.5rem 1.25rem 2rem;
	}
	.column {
		position: relative;
		max-width: 46rem;
		margin: 0 auto;
	}
	.column::before {
		left: max(1.25rem, calc(50% - 23rem - 1rem));
	}
	.column::after {
		right: max(1.25rem, calc(50% - 23rem - 1rem));
	}
	.toolbar {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		margin-bottom: 0.75rem;
	}
	.search {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		flex: 1;
		height: 1.9rem;
		padding: 0 0.55rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--muted-foreground);
	}
	.search:focus-within {
		border-color: color-mix(in oklab, var(--brand) 55%, var(--border));
	}
	.search input {
		flex: 1;
		min-width: 0;
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		outline: none;
		user-select: text;
	}
	.new {
		height: 1.9rem;
		background: var(--brand);
		border-color: var(--brand);
		color: #fff;
	}
	.new:hover {
		filter: brightness(1.15);
	}

	.frame {
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--card);
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 0.3rem;
	}
	li {
		position: relative;
		display: flex;
		align-items: center;
		border-radius: var(--radius);
	}
	li + li {
		margin-top: 1px;
	}
	li.on {
		background: var(--accent);
	}
	.row {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		flex: 1;
		min-width: 0;
		height: 3.1rem;
		padding: 0 0.6rem;
		border: none;
		background: transparent;
		color: var(--foreground);
		text-align: left;
	}
	.row :global(.db-logo) {
		transition: transform var(--dur) var(--ease-out);
	}
	li.on .row :global(.db-logo) {
		transform: scale(1.06);
	}
	.text {
		display: flex;
		flex-direction: column;
		min-width: 0;
		flex: 1;
		line-height: 1.35;
	}
	.name {
		font-size: 13.5px;
		font-weight: 500;
	}
	.tagbar {
		display: flex;
		flex-wrap: wrap;
		gap: 0.3rem;
		margin-bottom: 0.6rem;
	}
	.tagchip {
		height: 1.5rem;
		padding: 0 0.6rem;
		border: 1px solid var(--border);
		border-radius: 999px;
		background: transparent;
		color: var(--muted-foreground);
		font-size: 11.5px;
	}
	.tagchip:hover {
		color: var(--foreground);
	}
	.tagchip.on {
		background: var(--brand);
		border-color: var(--brand);
		color: #fff;
	}
	.name {
		display: flex;
		align-items: center;
		gap: 0.35rem;
	}
	.ctag {
		padding: 0 0.35rem;
		border: 1px solid var(--border);
		border-radius: 999px;
		font-size: 10px;
		font-weight: 400;
		line-height: 1.4;
		color: var(--muted-foreground);
	}
	.addr {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 11.5px;
		color: var(--muted-foreground);
	}
	.tag {
		margin-right: 0.35rem;
		padding: 0 0.3rem;
		border: 1px solid var(--border);
		border-radius: 0.25rem;
		font-size: 10.5px;
	}
	/* Tools keep their own space at the end of the row, so they never cover the text. */
	.tools {
		flex: none;
		padding-right: 0.4rem;
		display: flex;
		gap: 0.1rem;
		opacity: 0;
		transform: translateX(4px);
		transition:
			opacity var(--dur) var(--ease-out),
			transform var(--dur) var(--ease-out);
		pointer-events: none;
	}
	li.on .tools,
	li:focus-within .tools {
		opacity: 1;
		transform: none;
		pointer-events: auto;
	}
	.none {
		display: flex;
		align-items: center;
		gap: 0.25rem;
		height: 3.1rem;
		padding: 0 0.75rem;
		font-size: 12px;
	}
	.hints {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem 1.1rem;
		margin: 0.85rem 0 0;
		font-size: 11px;
		color: var(--muted-foreground);
		animation: tf-fade 0.5s 0.3s ease-out both;
	}
	.hints kbd {
		margin: 0 0.15rem;
	}

	.row :global(.spin) {
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (max-width: 720px) {
		.toolbar {
			flex-wrap: wrap;
		}
		.search {
			order: 3;
			max-width: none;
			flex-basis: 100%;
		}
	}
</style>
