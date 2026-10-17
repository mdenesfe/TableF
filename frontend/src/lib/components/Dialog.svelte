<script lang="ts">
	import { Info, TextCursorInput, TriangleAlert, X } from '@lucide/svelte';
	import { dialogs } from '#lib/dialog.svelte.ts';

	const d = $derived(dialogs.current);
	let value = $state('');

	$effect(() => {
		value = d?.value ?? '';
	});

	function ok() {
		if (!d) return;
		if (d.kind === 'prompt') {
			if (!value.trim()) return;
			dialogs.close(value.trim());
		} else dialogs.close(true);
	}

	function keydown(e: KeyboardEvent) {
		if (!d) return;
		if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			dialogs.close(null);
		} else if (e.key === 'Enter' && !e.isComposing) {
			e.preventDefault();
			ok();
		}
	}

	const focus = (el: HTMLElement) => el.focus();
</script>

<svelte:window onkeydowncapture={keydown} />

{#if d}
	<div class="overlay" role="presentation">
		<div class="dialog" role="dialog" aria-modal="true" aria-label={d.title}>
			<header>
				<span class="icon-box" class:danger={d.danger}>
					{#if d.danger}<TriangleAlert />{:else if d.kind === 'prompt'}<TextCursorInput />{:else}<Info />{/if}
				</span>
				<h2>{d.title}</h2>
				<button class="btn ghost icon xs close" onclick={() => dialogs.close(null)} aria-label="Close"><X size={15} /></button>
			</header>
			{#if d.message || d.detail || d.kind === 'prompt'}
				<div class="body">
					{#if d.message}<p>{d.message}</p>{/if}
					{#if d.detail}<pre class="mono">{d.detail}</pre>{/if}
					{#if d.kind === 'prompt'}
						<input class="input mono" use:focus bind:value placeholder={d.placeholder} spellcheck="false" />
					{/if}
				</div>
			{/if}
			<footer>
				<button class="btn ghost" onclick={() => dialogs.close(null)}>{d.cancelLabel ?? 'Cancel'}</button>
				{#if d.kind === 'confirm'}
					<button class="btn ok" class:danger={d.danger} use:focus onclick={ok}>{d.confirmLabel ?? 'OK'}</button>
				{:else}
					<button class="btn ok" disabled={!value.trim()} onclick={ok}>{d.confirmLabel ?? 'OK'}</button>
				{/if}
			</footer>
		</div>
	</div>
{/if}

<style>
	.overlay {
		position: fixed;
		inset: 0;
		z-index: 1100;
		display: grid;
		place-items: center;
		padding: 1rem;
		background: var(--overlay);
	}
	.dialog {
		width: min(26rem, 100%);
		background: var(--background);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
	}
	header {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		height: 2.75rem;
		padding: 0 0.5rem 0 1rem;
		border-bottom: 1px solid var(--border);
	}
	.icon-box.danger {
		background: var(--destructive);
	}
	h2 {
		flex: 1;
		min-width: 0;
		margin: 0;
		font-size: var(--text-lg);
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.body {
		padding: 0.85rem 1rem;
	}
	p {
		margin: 0;
		font-size: var(--text-base);
		color: var(--muted-foreground);
		line-height: 1.45;
	}
	pre {
		margin: 0.6rem 0 0;
		max-height: 9rem;
		overflow: auto;
		padding: 0.5rem 0.6rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		font-size: 11.5px;
		white-space: pre-wrap;
		user-select: text;
	}
	.input {
		width: 100%;
		margin-top: 0.65rem;
		height: 2rem;
	}
	footer {
		display: flex;
		justify-content: flex-end;
		gap: 0.4rem;
		padding: 0.65rem 0.75rem;
		border-top: 1px solid var(--border);
	}
	.ok {
		background: var(--brand);
		border-color: var(--brand);
		color: #fff;
	}
	.ok:hover:not(:disabled) {
		filter: brightness(1.15);
	}
	.ok.danger {
		background: var(--destructive);
		border-color: var(--destructive);
	}
</style>
