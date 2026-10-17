<script lang="ts">
	import { Check, CircleAlert, FolderOpen, Link, LoaderCircle, Search, X } from '@lucide/svelte';
	import { api, isDesktop } from '#lib/api.ts';
	import { app } from '#lib/state.svelte.ts';
	import { formatURL, parseURL } from '#lib/connurl.ts';
	import { canTunnel, engine, engines, type EngineField } from '#lib/engines.ts';
	import type { ConnectionConfig, DriverName } from '#lib/types.ts';
	import DbLogo from './DbLogo.svelte';

	let {
		initial,
		onclose
	}: {
		/** An existing connection to edit, a partial one to prefill (e.g. from a pasted URL), or null. */
		initial: Partial<ConnectionConfig> | null;
		onclose: (saved?: ConnectionConfig) => void;
	} = $props();

	const colors = ['', '#3b82f6', '#22c55e', '#eab308', '#f97316', '#ef4444', '#a855f7'];
	const defaults: ConnectionConfig = {
		id: '',
		name: '',
		driver: 'postgres',
		host: '127.0.0.1',
		port: 5432,
		user: '',
		password: '',
		database: '',
		sslMode: 'prefer',
		filePath: '',
		color: '',
		sshEnabled: false,
		sshHost: '',
		sshPort: 22,
		sshUser: '',
		sshAuth: 'agent',
		sshPassword: '',
		sshKeyFile: '',
		sshPassphrase: '',
		tags: [],
		options: {}
	};

	// svelte-ignore state_referenced_locally
	const editingExisting = !!initial?.id;
	// svelte-ignore state_referenced_locally
	let form = $state<ConnectionConfig>({ ...defaults, ...(initial ?? {}), tags: [...(initial?.tags ?? [])], options: { ...(initial?.options ?? {}) } });
	// New connections start by choosing the database, unless a pasted URL already did.
	// svelte-ignore state_referenced_locally
	let step = $state<'pick' | 'form'>(initial?.driver ? 'form' : 'pick');
	let pickQuery = $state('');
	let testing = $state(false);
	let test = $state<{ ok: boolean; text: string } | null>(null);

	const e = $derived(engine(form.driver));
	const tunnel = $derived(canTunnel(form.driver));
	const shownEngines = $derived(engines.filter((x) => (!app.engineIds || app.engineIds.has(x.id)) && x.label.toLowerCase().includes(pickQuery.toLowerCase())));
	const hasURL = $derived(e.kind === 'server' || form.driver === 'libsql' || e.kind === 'file');

	// URL bar: mirrors the form unless the user is typing in it.
	let urlFocused = $state(false);
	let urlText = $state('');
	let urlInvalid = $state(false);
	$effect(() => {
		const u = formatURL(form);
		if (!urlFocused) urlText = u;
	});

	function onURLInput() {
		const text = urlText.trim();
		if (!text) {
			urlInvalid = false;
			return;
		}
		const parsed = parseURL(text);
		urlInvalid = !parsed;
		if (!parsed) return;
		const next = { ...form, ...parsed, options: { ...form.options, ...(parsed.options ?? {}) } };
		if (parsed.driver !== form.driver && parsed.port === undefined) next.port = engine(parsed.driver).port ?? 0;
		if (!parsed.sshEnabled && /^[a-z]+:\/\//i.test(text)) next.sshEnabled = false;
		form = next;
		test = null;
	}

	function setDriver(d: DriverName) {
		const prev = engine(form.driver);
		if (form.port === prev.port || !form.port) form.port = engine(d).port ?? 0;
		form.driver = d;
		if (!canTunnel(d)) form.sshEnabled = false;
		if (d === 'redis' && !form.database) form.database = '0';
		test = null;
		step = 'form';
	}

	function autoName() {
		if (form.name.trim()) return form.name;
		if (e.kind === 'file') return form.filePath.split(/[/\\]/).pop() || e.label;
		if (e.kind === 'cloud') return form.options.databaseId || form.options.projectId || form.options.account || form.database || e.label;
		return form.database ? `${form.database}@${form.host}` : form.host || e.label;
	}

	// ---------- tags ----------
	let tagInput = $state('');
	const knownTags = $derived([...new Set(app.connections.flatMap((c) => c.tags ?? []))].filter((t) => !form.tags.includes(t)).sort());
	function addTag(raw: string) {
		const t = raw.trim().toLowerCase().replace(/\s+/g, '-');
		if (t && !form.tags.includes(t)) form.tags = [...form.tags, t];
		tagInput = '';
	}
	function tagKey(ev: KeyboardEvent) {
		if (ev.key === 'Enter' || ev.key === ',') {
			if (!tagInput.trim()) return;
			ev.preventDefault();
			addTag(tagInput);
		} else if (ev.key === 'Backspace' && !tagInput && form.tags.length) {
			form.tags = form.tags.slice(0, -1);
		}
	}

	// ---------- engine options ----------
	const opt = (f: EngineField) => form.options[f.key] ?? '';
	const setOpt = (f: EngineField, v: string) => (form.options = { ...form.options, [f.key]: v });
	/** Pairs half-width fields into rows. */
	const fieldRows = $derived.by(() => {
		const rows: EngineField[][] = [];
		for (const f of e.fields ?? []) {
			const last = rows.at(-1);
			if (f.half && last?.length === 1 && last[0].half) last.push(f);
			else rows.push([f]);
		}
		return rows;
	});

	// Secrets stay on the server; their fields show that one is saved.
	const saved = (field: string) => (form.secrets?.includes(field) ? '•••••• saved — leave empty to keep' : undefined);

	async function runTest() {
		testing = true;
		test = null;
		try {
			const cfg = $state.snapshot(form);
			const r = await app.withHostTrust(cfg, () => api.testConnection(cfg));
			const version = r.version
				.split(/[\s,]+/)
				.slice(0, 2)
				.join(' ');
			test = { ok: true, text: `${version} · ${r.latencyMs.toFixed(0)} ms` };
		} catch (err) {
			test = { ok: false, text: (err as Error).message };
		} finally {
			testing = false;
		}
	}

	// Both Save and Connect submit the form, so required fields are checked either way.
	async function save(ev: SubmitEvent) {
		ev.preventDefault();
		const andConnect = (ev.submitter as HTMLElement | null)?.dataset.connect !== undefined;
		if (tagInput.trim()) addTag(tagInput);
		form.name = autoName();
		try {
			const saved = await api.saveConnection($state.snapshot(form));
			onclose(saved);
			if (andConnect) await app.connect(saved);
		} catch (err) {
			app.fail(err);
		}
	}

	function keydown(ev: KeyboardEvent) {
		if (ev.key === 'Escape') onclose();
	}
</script>

<svelte:window onkeydown={keydown} />

{#snippet field(f: EngineField)}
	<div class="field grow">
		{#if f.type === 'toggle'}
			<span class="label">&nbsp;</span>
			<label class="switch inline-switch">
				<input type="checkbox" checked={opt(f) === 'true'} onchange={(ev) => setOpt(f, ev.currentTarget.checked ? 'true' : '')} />
				<span class="track"><span class="thumb"></span></span>
				<span>{f.label}</span>
			</label>
		{:else}
			<label for="o-{f.key}">{f.label}</label>
			{#if f.type === 'select'}
				<select id="o-{f.key}" class="input mono" value={opt(f) || f.choices?.[0]} onchange={(ev) => setOpt(f, ev.currentTarget.value)}>
					{#each f.choices ?? [] as c (c)}<option value={c}>{c}</option>{/each}
				</select>
			{:else if f.type === 'file'}
				<div class="inline">
					<input
						id="o-{f.key}"
						class="input mono"
						value={opt(f)}
						oninput={(ev) => setOpt(f, ev.currentTarget.value)}
						placeholder={f.placeholder}
						spellcheck="false"
					/>
					{#if isDesktop}
						<button type="button" class="btn outline icon pick" onclick={async () => setOpt(f, (await api.pickKeyFile()) || opt(f))} title="Browse"
							><FolderOpen size={14} /></button
						>
					{/if}
				</div>
			{:else}
				<input
					id="o-{f.key}"
					class="input mono"
					type={f.type === 'password' ? 'password' : 'text'}
					value={opt(f)}
					oninput={(ev) => setOpt(f, ev.currentTarget.value)}
					placeholder={saved(`options.${f.key}`) ?? f.placeholder}
					required={f.required && !saved(`options.${f.key}`)}
					autocomplete={f.type === 'password' ? 'new-password' : 'off'}
					spellcheck="false"
				/>
			{/if}
		{/if}
	</div>
{/snippet}

<div class="overlay" role="presentation">
	{#if step === 'pick'}
		<div class="dialog picker-dialog" role="dialog" aria-label="Choose a database">
			<header>
				<h2>New connection</h2>
				<button type="button" class="btn ghost icon xs" onclick={() => onclose()} aria-label="Close"><X size={15} /></button>
			</header>
			<label class="url pick-search">
				<Search size={13} />
				<!-- svelte-ignore a11y_autofocus -->
				<input bind:value={pickQuery} placeholder="Search databases" spellcheck="false" autofocus />
			</label>
			<div class="grid tf-stagger">
				{#each shownEngines as x, i (x.id)}
					<button type="button" class="tile" class:on={x.id === form.driver} style:--i={i} onclick={() => setDriver(x.id)}>
						<DbLogo driver={x.id} size={36} />
						<span>{x.label}</span>
					</button>
				{:else}
					<p class="hint none-found">No database matches “{pickQuery}”</p>
				{/each}
			</div>
		</div>
	{:else}
		<!-- Any edit invalidates the last test result (and its green/red fields). -->
		<form class="dialog" onsubmit={save} oninput={() => test && (test = null)} onchange={() => test && (test = null)}>
			<header>
				<div class="title">
					{#if editingExisting}
						<DbLogo driver={form.driver} size={22} />
					{:else}
						<!-- The logo reopens the database grid, to switch engines. -->
						<button type="button" class="logo-btn" onclick={() => (step = 'pick')} title="Choose another database" aria-label="Choose another database">
							<DbLogo driver={form.driver} size={22} />
						</button>
					{/if}
					<h2>{editingExisting ? 'Edit connection' : `New ${e.label} connection`}</h2>
				</div>
				<button type="button" class="btn ghost icon xs" onclick={() => onclose()} aria-label="Close"><X size={15} /></button>
			</header>

			{#if hasURL}
				<label class="url mono" class:invalid={urlInvalid}>
					<Link size={13} />
					<input
						bind:value={urlText}
						oninput={onURLInput}
						onfocus={() => (urlFocused = true)}
						onblur={() => {
							urlFocused = false;
							urlInvalid = false;
						}}
						placeholder={e.kind === 'file' ? `${form.driver}:///path/to/file` : `${form.driver}://user:pass@host:${e.port ?? ''}/db`}
						spellcheck="false"
						autocomplete="off"
						aria-label="Connection URL"
					/>
				</label>
			{/if}

			<div class="fields" class:tested-ok={test?.ok} class:tested-err={test && !test.ok}>
				<div class="row">
					<div class="field grow">
						<label for="f-name">Name</label>
						<input id="f-name" class="input" bind:value={form.name} placeholder={autoName() || 'production'} />
					</div>
					<div class="field">
						<span class="label">Color</span>
						<div class="colors">
							{#each colors as c (c)}
								<button
									type="button"
									class="swatch"
									class:on={form.color === c}
									class:none={!c}
									style:--c={c || 'transparent'}
									aria-label={c ? `Color ${c}` : 'No color'}
									onclick={() => (form.color = c)}
								></button>
							{/each}
						</div>
					</div>
				</div>

				<div class="field">
					<label for="f-tags">Tags</label>
					<div class="tags input">
						{#each form.tags as t (t)}
							<span class="tag mono"
								>{t}<button type="button" onclick={() => (form.tags = form.tags.filter((x) => x !== t))} aria-label={`Remove ${t}`}><X size={11} /></button
								></span
							>
						{/each}
						<input
							id="f-tags"
							bind:value={tagInput}
							onkeydown={tagKey}
							onblur={() => tagInput.trim() && addTag(tagInput)}
							placeholder={form.tags.length ? '' : 'local, staging, production…'}
							spellcheck="false"
						/>
					</div>
					{#if knownTags.length}
						<div class="suggest">
							{#each knownTags.slice(0, 8) as t (t)}<button type="button" class="tag mono ghost" onclick={() => addTag(t)}>+ {t}</button>{/each}
						</div>
					{/if}
				</div>

				{#if e.kind === 'file'}
					<div class="field">
						<label for="f-file">File</label>
						<div class="inline">
							<input id="f-file" class="input mono" bind:value={form.filePath} placeholder={e.file} required={form.driver === 'sqlite'} />
							{#if isDesktop}
								<button
									type="button"
									class="btn outline icon pick"
									onclick={async () => (form.filePath = (await api.pickSQLiteFile()) || form.filePath)}
									title="Browse"
								>
									<FolderOpen size={14} />
								</button>
							{/if}
						</div>
					</div>
				{:else}
					{#if e.kind === 'server'}
						<div class="row">
							<div class="field grow">
								<label for="f-host">{form.driver === 'cassandra' ? 'Hosts' : 'Host'}</label>
								<input id="f-host" class="input mono" bind:value={form.host} required={!(form.driver === 'mongodb' && form.options.uri)} spellcheck="false" />
							</div>
							<div class="field port">
								<label for="f-port">Port</label>
								<input id="f-port" class="input mono" type="number" bind:value={form.port} />
							</div>
						</div>
					{/if}
					{#if e.kind === 'server' || e.auth}
						<div class="row">
							<div class="field grow">
								<label for="f-user">User</label>
								<input id="f-user" class="input mono" bind:value={form.user} autocomplete="off" spellcheck="false" />
							</div>
							<div class="field grow">
								<label for="f-pass">Password</label>
								<input id="f-pass" class="input mono" type="password" bind:value={form.password} placeholder={saved('password')} autocomplete="new-password" />
							</div>
						</div>
					{/if}
					{#if e.database !== false || e.ssl}
						<div class="row">
							{#if e.database !== false}
								<div class="field grow">
									<label for="f-db">{e.database ?? 'Database'}</label>
									<input id="f-db" class="input mono" bind:value={form.database} spellcheck="false" />
								</div>
							{/if}
							{#if e.ssl}
								<div class="field grow">
									<label for="f-ssl">SSL mode</label>
									<select id="f-ssl" class="input mono" bind:value={form.sslMode}>
										<option value="disable">disable</option>
										<option value="prefer">prefer</option>
										<option value="require">require</option>
										<option value="verify-full">verify-full</option>
									</select>
								</div>
							{/if}
						</div>
					{/if}
				{/if}

				{#each fieldRows as r (r[0].key)}
					<div class="row">
						{#each r as f (f.key)}{@render field(f)}{/each}
					</div>
				{/each}

				{#if tunnel}
					<div class="field">
						<span class="label">Connect</span>
						<div class="seg small" role="radiogroup" aria-label="Connection method">
							<button type="button" role="radio" aria-checked={!form.sshEnabled} class:on={!form.sshEnabled} onclick={() => (form.sshEnabled = false)}
								>Direct</button
							>
							<button type="button" role="radio" aria-checked={form.sshEnabled} class:on={form.sshEnabled} onclick={() => (form.sshEnabled = true)}
								>Over SSH</button
							>
						</div>
					</div>

					{#if form.sshEnabled}
						<div class="ssh">
							<div class="row">
								<div class="field grow">
									<label for="s-host">SSH host</label>
									<input id="s-host" class="input mono" bind:value={form.sshHost} placeholder="bastion.example.com" spellcheck="false" required />
								</div>
								<div class="field port">
									<label for="s-port">Port</label>
									<input id="s-port" class="input mono" type="number" bind:value={form.sshPort} />
								</div>
							</div>
							<div class="row">
								<div class="field grow">
									<label for="s-user">SSH user</label>
									<input id="s-user" class="input mono" bind:value={form.sshUser} placeholder="$USER" spellcheck="false" />
								</div>
								<div class="field grow">
									<span class="label">Authentication</span>
									<div class="seg small" role="radiogroup">
										{#each [['agent', 'Agent'], ['key', 'Key file'], ['password', 'Password']] as [v, label] (v)}
											<button
												type="button"
												role="radio"
												aria-checked={form.sshAuth === v}
												class:on={form.sshAuth === v}
												onclick={() => (form.sshAuth = v as ConnectionConfig['sshAuth'])}>{label}</button
											>
										{/each}
									</div>
								</div>
							</div>
							{#if form.sshAuth === 'key'}
								<div class="row">
									<div class="field grow">
										<label for="s-key">Private key</label>
										<div class="inline">
											<input id="s-key" class="input mono" bind:value={form.sshKeyFile} placeholder="~/.ssh/id_ed25519" spellcheck="false" />
											{#if isDesktop}
												<button
													type="button"
													class="btn outline icon pick"
													onclick={async () => (form.sshKeyFile = (await api.pickKeyFile()) || form.sshKeyFile)}
													title="Browse"
												>
													<FolderOpen size={14} />
												</button>
											{/if}
										</div>
									</div>
									<div class="field grow">
										<label for="s-pp">Passphrase</label>
										<input
											id="s-pp"
											class="input mono"
											type="password"
											bind:value={form.sshPassphrase}
											placeholder={saved('sshPassphrase') ?? 'optional'}
											autocomplete="new-password"
										/>
									</div>
								</div>
							{:else if form.sshAuth === 'password'}
								<div class="field">
									<label for="s-pass">SSH password</label>
									<input
										id="s-pass"
										class="input mono"
										type="password"
										bind:value={form.sshPassword}
										placeholder={saved('sshPassword')}
										autocomplete="new-password"
									/>
								</div>
							{:else}
								<p class="hint mono">Uses SSH_AUTH_SOCK, then ~/.ssh/id_ed25519, id_ecdsa, id_rsa.</p>
							{/if}
						</div>
					{/if}
				{/if}
			</div>

			<footer>
				<div class="result mono" class:ok={test?.ok} class:err={test && !test.ok} title={test?.text}>
					{#if testing}<LoaderCircle size={13} class="spin" /> connecting…
					{:else if test?.ok}<Check size={13} /> {test.text}
					{:else if test}<CircleAlert size={13} /> {test.text}{/if}
				</div>
				<button type="button" class="btn outline sm" onclick={runTest} disabled={testing}>Test</button>
				<button type="submit" class="btn outline sm">{editingExisting ? 'Save' : 'Create'}</button>
				<button type="submit" class="btn sm primary" data-connect disabled={app.busy}>{editingExisting ? 'Save & connect' : 'Connect'}</button>
			</footer>
		</form>
	{/if}
</div>

<style>
	.logo-btn {
		display: grid;
		padding: 0;
		border: none;
		border-radius: 0.3rem;
		background: transparent;
	}
	.logo-btn:hover :global(.db-logo) {
		filter: brightness(1.15);
	}
	/* After Test, the fields that make up the connection turn green (or red), like TablePlus. */
	.fields :is([id^='f-host'], [id^='f-port'], [id^='f-user'], [id^='f-pass'], [id^='f-db'], [id^='f-ssl'], [id^='f-file'], [id^='o-'], [id^='s-']) {
		transition:
			border-color var(--dur) ease-out,
			background-color var(--dur) ease-out;
	}
	.tested-ok :is([id^='f-host'], [id^='f-port'], [id^='f-user'], [id^='f-pass'], [id^='f-db'], [id^='f-ssl'], [id^='f-file'], [id^='o-'], [id^='s-']) {
		border-color: color-mix(in oklab, var(--success) 70%, var(--border));
		background: color-mix(in oklab, var(--success) 12%, var(--card));
	}
	.tested-err :is([id^='f-host'], [id^='f-port'], [id^='f-user'], [id^='f-pass'], [id^='f-db'], [id^='f-ssl'], [id^='f-file'], [id^='o-'], [id^='s-']) {
		border-color: color-mix(in oklab, var(--destructive) 70%, var(--border));
		background: color-mix(in oklab, var(--destructive) 10%, var(--card));
	}
	.title {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
	}
	.picker-dialog {
		width: min(46rem, 100%);
	}
	.pick-search {
		margin-bottom: 0.25rem;
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(6.6rem, 1fr));
		gap: 0.25rem;
		padding: 0.6rem 0.75rem 0.9rem;
		overflow: auto;
	}
	.tile {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.5rem;
		min-height: 5.6rem;
		padding: 0.7rem 0.35rem 0.55rem;
		border: 1px solid transparent;
		border-radius: var(--radius-lg);
		background: transparent;
		color: var(--foreground);
		font-size: 12px;
		line-height: 1.25;
		text-align: center;
	}
	.tile:hover {
		background: var(--accent);
	}
	.tile.on {
		border-color: color-mix(in oklab, var(--brand) 60%, var(--border));
		background: color-mix(in oklab, var(--brand) 10%, transparent);
	}
	.tile :global(.db-logo) {
		transition: transform var(--dur) var(--ease-out);
	}
	.tile:hover :global(.db-logo) {
		transform: translateY(-2px);
	}
	.none-found {
		grid-column: 1 / -1;
		padding: 1rem 0.25rem;
	}
	.tags {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.3rem;
		height: auto;
		min-height: 2rem;
		padding: 0.2rem 0.4rem;
	}
	.tags input {
		flex: 1;
		min-width: 6rem;
		height: 1.5rem;
		border: none;
		background: transparent;
		color: var(--foreground);
		font: inherit;
		outline: none;
	}
	.tag {
		display: inline-flex;
		align-items: center;
		gap: 0.2rem;
		height: 1.35rem;
		padding: 0 0.25rem 0 0.45rem;
		border: 1px solid var(--border);
		border-radius: 999px;
		background: var(--accent);
		color: var(--foreground);
		font-size: 11px;
	}
	.tag button {
		display: grid;
		place-items: center;
		width: 1rem;
		height: 1rem;
		padding: 0;
		border: none;
		border-radius: 50%;
		background: transparent;
		color: var(--muted-foreground);
	}
	.tag button:hover {
		color: var(--foreground);
	}
	.tag.ghost {
		padding: 0 0.45rem;
		background: transparent;
		color: var(--muted-foreground);
	}
	.tag.ghost:hover {
		color: var(--foreground);
		background: var(--accent);
	}
	.suggest {
		display: flex;
		flex-wrap: wrap;
		gap: 0.3rem;
	}
	.ssh {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		padding: 0.75rem;
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--card);
		animation: tf-drift var(--dur) var(--ease-out) both;
	}
	.inline-switch {
		height: 2rem;
		font-size: var(--text-sm);
	}
	.primary {
		background: var(--brand);
		border-color: var(--brand);
		color: #fff;
	}
	.primary:hover {
		filter: brightness(1.15);
	}

	.overlay {
		position: fixed;
		inset: 0;
		z-index: 1000;
		background: var(--overlay);
		display: grid;
		place-items: center;
		padding: 1rem;
	}
	.dialog {
		width: min(36rem, 100%);
		max-height: calc(100vh - 2rem);
		overflow: auto;
		display: flex;
		flex-direction: column;
		background: var(--background);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
	}
	header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		height: 2.75rem;
		padding: 0 0.5rem 0 1rem;
	}
	h2 {
		margin: 0;
		font-size: var(--text-lg);
		font-weight: 600;
	}
	.url {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		height: 2.25rem;
		margin: 0 1rem;
		padding: 0 0.7rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--card);
		color: var(--muted-foreground);
	}
	.url:focus-within {
		border-color: color-mix(in oklch, var(--foreground) 35%, var(--border));
	}
	.url.invalid {
		border-color: color-mix(in oklch, var(--destructive) 55%, var(--border));
	}
	.url input {
		flex: 1;
		min-width: 0;
		height: 100%;
		border: none;
		background: transparent;
		color: var(--foreground);
		font: inherit;
		outline: none;
		user-select: text;
	}
	.url input::placeholder {
		color: var(--muted-foreground);
		opacity: 0.7;
	}
	.seg {
		display: flex;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
	}
	.seg button {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		height: 1.75rem;
		padding: 0 0.7rem;
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
	.seg button:hover:not(:disabled) {
		color: var(--foreground);
	}
	.seg button.on {
		background: var(--accent);
		color: var(--foreground);
	}
	.seg.small {
		height: 2.25rem;
		align-items: stretch;
	}
	.seg.small button {
		flex: 1;
		justify-content: center;
		height: auto;
	}
	.fields {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.fields {
		padding: 0.9rem 1rem 1rem;
	}
	.field {
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
		min-width: 0;
	}
	label:not(.switch):not(.url),
	.label {
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--muted-foreground);
	}
	.input {
		height: 2rem;
		font-size: var(--code);
	}
	.row {
		display: flex;
		gap: 0.6rem;
	}
	.grow {
		flex: 1;
	}
	.port {
		width: 5.5rem;
	}
	.inline {
		display: flex;
		gap: 0.4rem;
	}
	.pick {
		width: 2rem;
		height: 2rem;
		flex: none;
	}
	.colors {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		height: 2rem;
	}
	.swatch {
		width: 1rem;
		height: 1rem;
		padding: 0;
		border-radius: 50%;
		border: 1px solid color-mix(in oklab, var(--c) 70%, var(--border));
		background: var(--c);
	}
	.swatch.none {
		border-color: var(--input);
		background: linear-gradient(135deg, transparent 44%, var(--input) 44% 56%, transparent 56%);
	}
	.swatch.on {
		outline: 1.5px solid var(--foreground);
		outline-offset: 2px;
	}
	.switch {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		font-weight: 500;
	}
	.switch input {
		position: absolute;
		opacity: 0;
		pointer-events: none;
	}
	.track {
		position: relative;
		width: 1.85rem;
		height: 1.05rem;
		border-radius: 1rem;
		background: color-mix(in oklch, var(--foreground) 18%, transparent);
		transition: background 0.15s;
	}
	.thumb {
		position: absolute;
		top: 0.15rem;
		left: 0.15rem;
		width: 0.75rem;
		height: 0.75rem;
		border-radius: 50%;
		background: var(--card);
		box-shadow: 0 1px 2px oklch(0 0 0 / 0.25);
		transition: transform 0.15s;
	}
	.switch input:checked + .track {
		background: var(--brand);
	}
	.switch input:checked + .track .thumb {
		transform: translateX(0.8rem);
		background: #fff;
	}
	.switch input:focus-visible + .track {
		outline: 2px solid color-mix(in oklch, var(--foreground) 30%, transparent);
		outline-offset: 2px;
	}
	.hint {
		margin: 0;
		font-size: 11px;
		color: var(--muted-foreground);
	}
	footer {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		padding: 0.65rem 1rem;
		border-top: 1px solid var(--border);
	}
	.result {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		flex: 1;
		min-width: 0;
		font-size: 11px;
		color: var(--muted-foreground);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		user-select: text;
	}
	.result :global(svg) {
		flex: none;
	}
	.result.ok :global(svg) {
		color: var(--success);
	}
	.result.ok {
		color: var(--foreground);
	}
	.result.err {
		color: var(--destructive);
	}
	.result :global(.spin) {
		animation: spin 0.8s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
