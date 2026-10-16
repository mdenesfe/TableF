<script lang="ts">
	// Calendar popover for date, time and timestamp cells. It edits the same
	// text the field shows, so typing and picking always agree.
	import { ChevronLeft, ChevronRight } from '@lucide/svelte';
	import { fmtDate, joinMoment, now, parseMoment, type CellKind } from '#lib/celltype.ts';

	let {
		kind,
		value,
		x,
		y,
		nullable = true,
		onpick,
		onclose
	}: {
		kind: CellKind;
		value: string;
		/** Viewport position of the anchor's bottom-left corner. */
		x: number;
		y: number;
		nullable?: boolean;
		onpick: (v: string | null) => void;
		onclose: () => void;
	} = $props();

	// svelte-ignore state_referenced_locally
	const parsed = parseMoment(value) ?? { date: '', time: '', zone: '' };
	// The picker edits a copy of the value it opened with.
	// svelte-ignore state_referenced_locally
	let date = $state(parsed.date || (kind === 'time' ? '' : fmtDate(new Date())));
	// svelte-ignore state_referenced_locally
	let time = $state(parsed.time || (kind === 'date' ? '' : '00:00:00'));
	const zone = parsed.zone;

	// The month on screen.
	const start = parsed.date ? new Date(`${parsed.date}T00:00:00`) : new Date();
	let year = $state(start.getFullYear());
	let month = $state(start.getMonth());

	const monthLabel = $derived(new Date(year, month, 1).toLocaleDateString([], { month: 'long', year: 'numeric' }));
	const weekdays = Array.from({ length: 7 }, (_, i) => new Date(2024, 0, i + 1).toLocaleDateString([], { weekday: 'narrow' })); // Jan 1 2024 is a Monday
	const days = $derived.by(() => {
		const first = new Date(year, month, 1);
		const offset = (first.getDay() + 6) % 7; // weeks start on Monday
		return Array.from({ length: 42 }, (_, i) => new Date(year, month, 1 - offset + i));
	});

	function step(n: number) {
		const d = new Date(year, month + n, 1);
		year = d.getFullYear();
		month = d.getMonth();
	}

	function pickDay(d: Date) {
		date = fmtDate(d);
		if (kind === 'date') onpick(date);
	}

	function apply() {
		onpick(joinMoment(kind, date, time, zone));
	}

	function setNow() {
		const p = parseMoment(now(kind))!;
		date = p.date;
		time = p.time;
		onpick(joinMoment(kind, date, time, zone));
	}

	let root: HTMLDivElement;
	const left = $derived(Math.max(8, Math.min(x, window.innerWidth - 268)));
	const top = $derived(y + 330 > window.innerHeight ? Math.max(8, y - 340) : y);
</script>

<svelte:window
	onpointerdown={(e) => !root.contains(e.target as Node) && onclose()}
	onkeydown={(e) => {
		if (e.key === 'Escape') {
			e.stopPropagation();
			onclose();
		}
	}}
/>

<div class="picker" bind:this={root} style:left="{left}px" style:top="{top}px" role="dialog" aria-label="Pick a date">
	{#if kind !== 'time'}
		<div class="head">
			<button class="nav" onclick={() => step(-1)} aria-label="Previous month"><ChevronLeft size={14} /></button>
			<span class="month">{monthLabel}</span>
			<button class="nav" onclick={() => step(1)} aria-label="Next month"><ChevronRight size={14} /></button>
		</div>
		<div class="days">
			{#each weekdays as w, i (i)}<span class="wd">{w}</span>{/each}
			{#each days as d (d.getTime())}
				{@const iso = fmtDate(d)}
				<button class="day mono" class:out={d.getMonth() !== month} class:on={iso === date} class:today={iso === fmtDate(new Date())} onclick={() => pickDay(d)}
					>{d.getDate()}</button
				>
			{/each}
		</div>
	{/if}
	{#if kind !== 'date'}
		<label class="time">
			<span>Time</span>
			<input class="input mono" type="time" step="1" bind:value={time} />
			{#if zone}<span class="zone mono">{zone}</span>{/if}
		</label>
	{/if}
	<div class="foot">
		<button class="link" onclick={setNow}>{kind === 'date' ? 'Today' : 'Now'}</button>
		{#if nullable}<button class="link mono" onclick={() => onpick(null)}>NULL</button>{/if}
		<span class="spacer"></span>
		{#if kind !== 'date'}<button class="btn sm ok" onclick={apply}>Set</button>{/if}
	</div>
</div>

<style>
	.picker {
		position: fixed;
		z-index: 500;
		width: 16.25rem;
		padding: 0.5rem;
		background: var(--popover);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow);
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 0.35rem;
	}
	.month {
		font-size: 12.5px;
		font-weight: 600;
		text-transform: capitalize;
	}
	.nav {
		display: grid;
		place-items: center;
		width: 1.6rem;
		height: 1.6rem;
		padding: 0;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--muted-foreground);
	}
	.nav:hover {
		background: var(--accent);
		color: var(--foreground);
	}
	.days {
		display: grid;
		grid-template-columns: repeat(7, 1fr);
		gap: 1px;
	}
	.wd {
		height: 1.5rem;
		display: grid;
		place-items: center;
		font-size: 10.5px;
		color: var(--muted-foreground);
	}
	.day {
		height: 1.9rem;
		padding: 0;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-size: 12px;
	}
	.day:hover {
		background: var(--accent);
	}
	.day.out {
		color: var(--muted-foreground);
		opacity: 0.55;
	}
	.day.today {
		box-shadow: inset 0 0 0 1px var(--border);
	}
	.day.on {
		background: var(--brand);
		color: #fff;
		box-shadow: none;
	}
	.time {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin-top: 0.5rem;
		font-size: 12px;
		color: var(--muted-foreground);
	}
	.time input {
		flex: 1;
		height: 1.8rem;
	}
	.zone {
		font-size: 11px;
	}
	.foot {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		margin-top: 0.5rem;
		padding-top: 0.45rem;
		border-top: 1px solid var(--border);
	}
	.spacer {
		flex: 1;
	}
	.link {
		padding: 0;
		border: none;
		background: transparent;
		color: var(--foreground);
		font-size: 12px;
		text-decoration: underline;
		text-underline-offset: 2px;
	}
	.ok {
		background: var(--brand);
		border-color: var(--brand);
		color: #fff;
	}
</style>
