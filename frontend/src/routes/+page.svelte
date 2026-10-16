<script lang="ts">
	import Dialog from '#lib/components/Dialog.svelte';
	import { onMount } from 'svelte';
	import { X } from '@lucide/svelte';
	import { prefs } from '#lib/prefs.svelte.ts';
	import Preferences from '#lib/components/Preferences.svelte';
	import { setToken, webNeedsToken } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import Home from '#lib/components/Home.svelte';
	import Workspace from '#lib/components/Workspace.svelte';

	let needToken = $state(false);
	let tokenInput = $state('');

	prefs.apply();

	function keydown(e: KeyboardEvent) {
		if ((e.metaKey || e.ctrlKey) && e.key === ',') {
			e.preventDefault();
			app.prefsOpen = !app.prefsOpen;
		}
	}

	onMount(async () => {
		needToken = await webNeedsToken();
		if (needToken) return (app.restoring = false);
		await app.loadConnections();
		await app.restore();
	});

	function submitToken(e: Event) {
		e.preventDefault();
		setToken(tokenInput.trim());
		needToken = false;
		app.loadConnections().then(() => app.restore());
	}
</script>

{#if needToken}
	<form class="token" onsubmit={submitToken}>
		<h2>Access token</h2>
		<p class="muted">This TableF server requires a token. Start it with <code>-token</code> to set one.</p>
		<input class="input" type="password" bind:value={tokenInput} placeholder="Token" />
		<button class="btn" type="submit">Continue</button>
	</form>
{:else if app.restoring}
	<div class="restoring" aria-busy="true"></div>
{:else if app.session}
	<Workspace />
{:else}
	<Home />
{/if}

<!-- Save the session before the page goes away; saves are otherwise debounced. -->
<svelte:window onkeydown={keydown} onpagehide={() => app.flushPersist()} />
<svelte:document onvisibilitychange={() => document.hidden && app.flushPersist()} />

<Dialog />

{#if app.prefsOpen}<Preferences onclose={() => (app.prefsOpen = false)} />{/if}

{#if app.toast}
	<div class="toast" class:error={app.toast.kind === 'error'} role="status">
		<span>{app.toast.text}</span>
		<button class="btn ghost icon xs" onclick={() => (app.toast = null)} aria-label="Dismiss"><X size={14} /></button>
	</div>
{/if}

<style>
	.restoring {
		height: 100%;
	}
	.token {
		width: min(360px, calc(100% - 32px));
		margin: 18vh auto 0;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.token h2 {
		margin: 0;
	}
	.toast {
		animation: tf-rise var(--dur-slow) var(--ease-out) both;
		position: fixed;
		right: 16px;
		bottom: 16px;
		max-width: min(480px, calc(100vw - 32px));
		display: flex;
		align-items: flex-start;
		gap: 8px;
		padding: 10px 12px;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: 10px;
		box-shadow: var(--shadow);
		font-size: 12.5px;
		z-index: 100;
		user-select: text;
		white-space: pre-wrap;
		word-break: break-word;
	}
	.toast.error {
		color: var(--destructive);
	}
	.toast button {
		width: 22px;
		height: 22px;
		margin: -2px -4px -2px 0;
	}
</style>
