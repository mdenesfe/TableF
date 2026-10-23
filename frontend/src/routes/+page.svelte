<script lang="ts">
	import Dialog from '#lib/components/Dialog.svelte';
	import WindowControls from '#lib/components/WindowControls.svelte';
	import Splash from '#lib/components/Splash.svelte';
	import { arrive } from '#lib/motion.ts';
	import { onMount } from 'svelte';
	import { X } from '@lucide/svelte';
	import { prefs } from '#lib/prefs.svelte.ts';
	import Preferences from '#lib/components/Preferences.svelte';
	import UpdateCard from '#lib/components/UpdateCard.svelte';
	import { updates } from '#lib/updates.svelte.ts';
	import { setToken, webNeedsToken, hasWindowControls, isMacDesktop } from '#lib/api.ts';
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

	// In fullscreen the header draws its own traffic lights (FullscreenLights).
	function watchFullscreen() {
		const sync = async () => {
			const full = (await window.runtime?.WindowIsFullscreen()) ?? false;
			if (full) document.documentElement.dataset.fullscreen = '';
			else delete document.documentElement.dataset.fullscreen;
		};
		sync();
		addEventListener('resize', sync);
	}

	// Full-page start-up animation: until connections (and a saved session) are
	// loaded, and at least long enough to be seen rather than flash.
	let booting = $state(true);
	const bootStarted = performance.now();
	const finishBoot = () => setTimeout(() => (booting = false), Math.max(0, 600 - (performance.now() - bootStarted)));

	onMount(async () => {
		updates.start();
		// Lets CSS size the header to the platform's window chrome.
		if (isMacDesktop) {
			document.documentElement.dataset.platform = 'mac';
			watchFullscreen();
		} else if (hasWindowControls) document.documentElement.dataset.platform = 'framed';
		try {
			needToken = await webNeedsToken();
			if (needToken) return (app.restoring = false);
			await app.loadConnections();
			await app.restore();
		} finally {
			finishBoot();
		}
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
{:else}
	<!-- Each view arrives with a short settle-in, after the splash and when switching. -->
	{#key !!app.session}
		<div class="view" in:arrive={{ delay: booting ? 0 : 60 }}>
			{#if app.session}<Workspace />{:else}<Home />{/if}
		</div>
	{/key}
{/if}

<!-- Save the session before the page goes away; saves are otherwise debounced. -->
<svelte:window onkeydown={keydown} onpagehide={() => app.flushPersist()} />
<svelte:document onvisibilitychange={() => document.hidden && app.flushPersist()} />

<Dialog />

{#if booting}
	<Splash instant message={app.connecting ? `reopening ${app.connecting.name}` : 'starting'} />
{:else if app.connecting}
	<Splash message={`connecting to ${app.connecting.name}`} color={app.connecting.color} />
{/if}

{#if hasWindowControls}<WindowControls onBrand={!app.session} />{/if}

{#if app.prefsOpen}<Preferences onclose={() => (app.prefsOpen = false)} />{/if}
{#if !booting}<UpdateCard />{/if}

{#if app.toast}
	<div class="toast" class:error={app.toast.kind === 'error'} role="status">
		<span>{app.toast.text}</span>
		<button class="btn ghost icon xs" onclick={() => (app.toast = null)} aria-label="Dismiss"><X size={14} /></button>
	</div>
{/if}

<style>
	.restoring,
	.view {
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
