<script lang="ts">
	// Who the session acts as, in the header. Click to switch to another role
	// (PostgreSQL SET ROLE) or sign in as another user for this session.
	import { Check, LoaderCircle, LogIn, ShieldAlert, User, UserCog } from '@lucide/svelte';
	import { api } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';

	let open = $state(false);
	let root = $state<HTMLDivElement>();
	let roles = $state.raw<string[] | null>(null);
	let rolesError = $state('');
	let signIn = $state(false);
	let user = $state('');
	let password = $state('');
	let pending = $state(false);

	const s = $derived(app.session);
	// The login whose roles are listed: the session user unless a role is active.
	const login = $derived(s?.login || s?.user || '');

	async function toggle() {
		open = !open;
		if (!open || !s) return;
		signIn = false;
		user = s.login || s.user || '';
		password = '';
		if (!s.canSwitchRole) return;
		roles = null;
		rolesError = '';
		try {
			roles = await api.listRoles(s.id);
		} catch (e) {
			rolesError = (e as Error).message;
			roles = [];
		}
	}

	async function pick(role: string) {
		open = false;
		// The login's own role means "no SET ROLE".
		await app.useRole(role === login ? '' : role);
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		pending = true;
		const ok = await app.connectAs(user, password);
		pending = false;
		password = '';
		if (ok) open = false;
	}
</script>

<svelte:window
	onpointerdown={(e) => open && !root?.contains(e.target as Node) && (open = false)}
	onkeydown={(e) => open && e.key === 'Escape' && (open = false)}
/>

{#if s?.user}
	<div class="who" bind:this={root}>
		<button
			class="chip"
			class:super={s.superuser}
			onclick={toggle}
			aria-haspopup="menu"
			aria-expanded={open}
			title={`Connected as ${s.user}${s.login ? ` (signed in as ${s.login})` : ''}${s.superuser ? ' · superuser' : ''}`}
		>
			{#if s.superuser}<ShieldAlert size={13} />{:else}<User size={13} />{/if}
			<span class="name mono">{s.user}</span>
			{#if s.login}<span class="via mono">← {s.login}</span>{/if}
			{#if s.superuser}<span class="tag">superuser</span>{/if}
		</button>

		{#if open}
			<div class="menu" role="menu">
				<p class="head">
					Connected as <b class="mono">{s.user}</b>{#if s.login}, signed in as <b class="mono">{s.login}</b>{/if}
				</p>

				{#if s.canSwitchRole}
					<p class="section">Act as role</p>
					{#if roles === null}
						<p class="note"><LoaderCircle size={12} class="spin" /> Loading roles…</p>
					{:else if rolesError}
						<p class="note err">{rolesError}</p>
					{:else}
						<div class="roles">
							{#each roles as r (r)}
								{@const active = r === s.user}
								<button role="menuitemradio" aria-checked={active} class:on={active} onclick={() => pick(r)} disabled={app.busy}>
									<span class="check"
										>{#if active}<Check size={13} />{/if}</span
									>
									<span class="mono">{r}</span>
									{#if r === login}<span class="hint">login</span>{/if}
								</button>
							{/each}
						</div>
					{/if}
				{/if}

				<span class="sep"></span>
				{#if !signIn}
					<button class="item" onclick={() => (signIn = true)}><LogIn size={14} /> Sign in as another user…</button>
				{:else}
					<form class="signin" onsubmit={submit}>
						<p class="section"><UserCog size={12} /> Another user, for this session only</p>
						<input class="input mono" placeholder="User" bind:value={user} autocomplete="username" spellcheck="false" required />
						<input
							class="input mono"
							type="password"
							placeholder="Password (empty: keep the current one)"
							bind:value={password}
							autocomplete="current-password"
						/>
						<div class="buttons">
							<button type="button" class="btn sm ghost" onclick={() => (signIn = false)}>Cancel</button>
							<button type="submit" class="btn sm" disabled={pending || !user.trim()}>
								{#if pending}<LoaderCircle size={13} class="spin" />{/if} Connect
							</button>
						</div>
						<p class="note">The saved connection keeps its own user and password.</p>
					</form>
				{/if}
			</div>
		{/if}
	</div>
{/if}

<style>
	.who {
		position: relative;
		min-width: 0;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		max-width: 18rem;
		height: 1.6rem;
		padding: 0 0.55rem;
		border: 1px solid color-mix(in oklch, var(--foreground) 22%, transparent);
		border-radius: 999px;
		background: color-mix(in oklch, var(--background) 30%, transparent);
		color: color-mix(in oklab, var(--foreground) 88%, transparent);
		font-size: 11.5px;
		white-space: nowrap;
	}
	.chip:hover {
		background: color-mix(in oklch, var(--background) 50%, transparent);
	}
	.chip.super {
		border-color: var(--warning);
	}
	.chip.super :global(svg) {
		color: var(--warning);
	}
	.name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		font-weight: 500;
	}
	.via {
		color: var(--muted-foreground);
	}
	.tag {
		padding: 0 0.3rem;
		border-radius: 0.25rem;
		background: color-mix(in oklch, var(--warning) 22%, transparent);
		color: var(--warning);
		font-size: 10px;
		font-weight: 600;
		line-height: 1.1rem;
	}
	.menu {
		position: absolute;
		top: calc(100% + 0.35rem);
		right: 0;
		z-index: 700;
		width: 19rem;
		padding: 0.3rem;
		display: flex;
		flex-direction: column;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
		animation: tf-pop var(--dur-fast) var(--ease-out) both;
	}
	.head {
		margin: 0;
		padding: 0.4rem 0.5rem 0.5rem;
		font-size: 11.5px;
		color: var(--muted-foreground);
	}
	.head b {
		color: var(--foreground);
		font-weight: 500;
	}
	.section {
		display: flex;
		align-items: center;
		gap: 0.3rem;
		margin: 0;
		padding: 0.3rem 0.5rem 0.2rem;
		font-size: 10.5px;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--muted-foreground);
	}
	.roles {
		display: flex;
		flex-direction: column;
		max-height: 14rem;
		overflow: auto;
	}
	.roles button,
	.item {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		height: 1.85rem;
		padding: 0 0.5rem;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 12.5px;
		text-align: left;
	}
	.roles button:hover:not(:disabled),
	.item:hover {
		background: var(--accent);
	}
	.roles button.on {
		background: var(--brand-soft);
	}
	.check {
		display: grid;
		place-items: center;
		width: 0.9rem;
		color: var(--brand-ink);
	}
	.hint {
		margin-left: auto;
		font-size: 10.5px;
		color: var(--muted-foreground);
	}
	.sep {
		height: 1px;
		margin: 0.3rem 0.4rem;
		background: var(--border);
	}
	.signin {
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
		padding: 0 0.3rem 0.3rem;
	}
	.signin .input {
		height: 1.85rem;
		font-size: 12px;
	}
	.buttons {
		display: flex;
		justify-content: flex-end;
		gap: 0.3rem;
	}
	.note {
		display: flex;
		align-items: center;
		gap: 0.3rem;
		margin: 0;
		padding: 0.2rem 0.5rem;
		font-size: 11px;
		color: var(--muted-foreground);
	}
	.note.err {
		color: var(--destructive);
	}
	.menu :global(.spin) {
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
