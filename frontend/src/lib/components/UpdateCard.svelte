<script lang="ts">
	// A newer TableF: what's in it, the download as it goes, and a button to
	// restart into it. "Later" hides the card until the next version.
	import { ArrowUpRight, Download, LoaderCircle, RefreshCw, SquareFunction, X } from '@lucide/svelte';
	import { openExternal } from '#lib/api.ts';
	import { bytes } from '#lib/format.ts';
	import { highlights } from '#lib/release.ts';
	import { updates } from '#lib/updates.svelte.ts';
	import { VERSION } from '#lib/version.ts';

	const r = $derived(updates.release);
	const points = $derived(r ? highlights(r.notes) : []);
	const pct = $derived(Math.round(updates.progress * 100));
</script>

{#if updates.visible && r}
	<aside class="card" role="status" aria-live="polite">
		<header>
			<span class="icon-box"><SquareFunction /></span>
			<div class="titles">
				<b>
					{#if updates.phase === 'ready' || updates.phase === 'installing'}TableF {r.version} is ready{:else}TableF {r.version} is available{/if}
				</b>
				<span class="muted mono">you have v{VERSION}</span>
			</div>
			<button class="close" onclick={() => updates.dismiss()} title="Later" aria-label="Remind me later"><X size={14} /></button>
		</header>

		{#if points.length}
			<ul>
				{#each points as p (p)}<li>{p}</li>{/each}
			</ul>
		{/if}

		{#if updates.phase === 'downloading'}
			<div class="progress" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
				<span style:width="{pct}%"></span>
			</div>
			<p class="muted small">
				Downloading… {pct}%{#if updates.total}&nbsp;· {bytes(updates.progress * updates.total)} of {bytes(updates.total)}{/if}
			</p>
		{:else if updates.phase === 'error'}
			<p class="err small" title={updates.error}>{updates.error}</p>
		{/if}

		<div class="actions">
			<button class="link" onclick={() => openExternal(r.url)}>Release notes <ArrowUpRight size={12} /></button>
			<span class="grow"></span>
			{#if updates.phase === 'ready'}
				<button class="btn sm ghost" onclick={() => updates.dismiss()}>Later</button>
				<button class="btn sm" onclick={() => updates.install()}><RefreshCw size={13} /> Restart to update</button>
			{:else if updates.phase === 'installing'}
				<button class="btn sm" disabled><LoaderCircle size={13} class="spin" /> Installing…</button>
			{:else if updates.phase === 'downloading'}
				<button class="btn sm ghost" onclick={() => updates.dismiss()}>Hide</button>
			{:else if updates.canInstall}
				<button class="btn sm ghost" onclick={() => updates.dismiss()}>Later</button>
				<button class="btn sm" onclick={() => updates.download()}>
					{#if updates.phase === 'error'}<RefreshCw size={13} /> Try again{:else}<Download size={13} /> Download{/if}
				</button>
			{:else}
				<button class="btn sm ghost" onclick={() => updates.dismiss()}>Later</button>
				<button class="btn sm" onclick={() => openExternal(r.url)}><Download size={13} /> Download</button>
			{/if}
		</div>
	</aside>
{/if}

<style>
	.card {
		position: fixed;
		left: 1rem;
		bottom: 3rem;
		z-index: 900;
		width: 21rem;
		padding: 0.75rem 0.8rem 0.65rem;
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
		animation: tf-rise var(--dur-slow) var(--ease-out) both;
	}
	header {
		display: flex;
		align-items: flex-start;
		gap: 0.6rem;
	}
	.titles {
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
		flex: 1;
		min-width: 0;
	}
	.titles b {
		font-size: 13px;
		font-weight: 600;
	}
	.titles span {
		font-size: 11px;
	}
	.close {
		display: grid;
		place-items: center;
		width: 1.5rem;
		height: 1.5rem;
		padding: 0;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--muted-foreground);
	}
	.close:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	li {
		position: relative;
		padding-left: 0.75rem; /* room for the dot, inside the clipped box */
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}
	li::before {
		content: '';
		position: absolute;
		left: 0;
		top: 0.55em;
		width: 4px;
		height: 4px;
		border-radius: 50%;
		background: var(--brand-ink);
	}
	ul {
		list-style: none;
		margin: 0;
		padding: 0 0 0 1.2rem;
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
		font-size: 12px;
		color: color-mix(in oklab, var(--foreground) 85%, transparent);
	}
	.progress {
		height: 0.4rem;
		border-radius: 999px;
		background: var(--muted);
		overflow: hidden;
	}
	.progress span {
		display: block;
		height: 100%;
		border-radius: 999px;
		background: var(--brand-ink);
		transition: width 0.2s linear;
	}
	.small {
		margin: 0;
		font-size: 11px;
	}
	.err {
		color: var(--destructive);
		overflow: hidden;
		text-overflow: ellipsis;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 0.35rem;
	}
	.grow {
		flex: 1;
	}
	.link {
		white-space: nowrap;
		display: inline-flex;
		align-items: center;
		gap: 0.2rem;
		padding: 0;
		border: none;
		background: transparent;
		color: var(--muted-foreground);
		font-size: 12px;
	}
	.link:hover {
		color: var(--foreground);
	}
	.card :global(.spin) {
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
