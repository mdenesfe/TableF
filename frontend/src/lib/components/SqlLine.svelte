<script lang="ts">
	// Single-line SQL input (a WHERE condition) with highlighting and column completion.
	import { sql } from '@codemirror/lang-sql';
	import { dialectOf } from '#lib/sqldialect.ts';
	import { isSQL } from '#lib/engines.ts';
	import { autocompletion, closeBrackets, closeCompletion, completionStatus, type CompletionContext } from '@codemirror/autocomplete';
	import { EditorState, Prec } from '@codemirror/state';
	import { EditorView, keymap, placeholder as ph, tooltips } from '@codemirror/view';
	import { HighlightStyle, syntaxHighlighting } from '@codemirror/language';
	import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
	import { tags } from '@lezer/highlight';
	import { onMount } from 'svelte';
	import type { DriverName } from '#lib/types.ts';

	let {
		value = $bindable(''),
		driver,
		columns = [],
		placeholder = '',
		onsubmit,
		oncancel
	}: {
		value?: string;
		driver: DriverName;
		columns?: string[];
		placeholder?: string;
		onsubmit: () => void;
		oncancel?: () => void;
	} = $props();

	let host: HTMLDivElement;
	let view: EditorView | undefined;

	const highlight = HighlightStyle.define([
		{ tag: tags.keyword, color: 'oklch(0.68 0.12 290)', fontWeight: '600' },
		{ tag: [tags.string, tags.special(tags.string)], color: 'oklch(0.68 0.12 150)' },
		{ tag: [tags.number, tags.bool, tags.null], color: 'oklch(0.72 0.12 70)' },
		{ tag: [tags.name, tags.propertyName], color: 'oklch(0.7 0.1 230)' },
		{ tag: tags.operator, color: 'var(--muted-foreground)' },
		{ tag: tags.comment, color: 'var(--muted-foreground)', fontStyle: 'italic' }
	]);

	const theme = EditorView.theme({
		'&': { width: '100%', background: 'transparent', color: 'var(--foreground)', fontSize: '12.5px' },
		'&.cm-focused': { outline: 'none' },
		'.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '20px', overflow: 'hidden' },
		'.cm-content': { padding: '0', caretColor: 'var(--brand-ink)' },
		'.cm-line': { padding: '0' },
		'.cm-placeholder': { color: 'var(--muted-foreground)', opacity: '0.7' },
		'.cm-cursor': { borderLeftColor: 'var(--brand-ink)' },
		'.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': {
			background: 'color-mix(in oklab, var(--brand) 38%, transparent) !important'
		},
		'.cm-tooltip': {
			background: 'var(--popover)',
			border: '1px solid var(--border)',
			borderRadius: '6px',
			boxShadow: 'var(--shadow)',
			overflow: 'hidden'
		},
		'.cm-tooltip-autocomplete ul': { fontFamily: 'var(--font-mono)', fontSize: '12px', maxHeight: '18rem' },
		'.cm-tooltip-autocomplete ul li': { padding: '3px 10px !important', lineHeight: '20px' },
		'.cm-tooltip-autocomplete ul li[aria-selected]': { background: 'var(--accent)', color: 'var(--foreground)' },
		'.cm-completionDetail': { color: 'var(--muted-foreground)', fontStyle: 'normal', marginLeft: '1rem' }
	});

	// Only what makes sense in a filter: this table's columns and condition keywords.
	const KEYWORDS = [
		'AND',
		'OR',
		'NOT',
		'IN',
		'IS NULL',
		'IS NOT NULL',
		'LIKE',
		'ILIKE',
		'BETWEEN',
		'EXISTS',
		'ORDER BY',
		'ASC',
		'DESC',
		'NULLS FIRST',
		'NULLS LAST',
		'TRUE',
		'FALSE',
		'NULL'
	];
	function complete(ctx: CompletionContext) {
		const word = ctx.matchBefore(/[\w.]+/);
		if (!word || (word.from === word.to && !ctx.explicit)) return null;
		return {
			from: word.from,
			validFor: /^[\w.]*$/,
			options: [...columns.map((c) => ({ label: c, type: 'property', boost: 1 })), ...KEYWORDS.map((k) => ({ label: k, type: 'keyword' }))]
		};
	}

	// Enter submits unless a completion is open; newlines are never inserted.
	const submit = () => {
		if (view && completionStatus(view.state) === 'active') return false;
		if (view) closeCompletion(view);
		onsubmit();
		return true;
	};

	onMount(() => {
		view = new EditorView({
			parent: host,
			state: EditorState.create({
				doc: value,
				extensions: [
					tooltips({ parent: document.body }),
					EditorState.transactionFilter.of((tr) => (tr.newDoc.lines > 1 ? [] : tr)),
					Prec.highest(
						keymap.of([
							{ key: 'Enter', run: submit },
							{ key: 'Mod-Enter', run: (v) => (closeCompletion(v), onsubmit(), true) },
							{
								key: 'Escape',
								run: (v) => {
									if (completionStatus(v.state)) return false;
									oncancel?.();
									v.contentDOM.blur();
									return true;
								}
							}
						])
					),
					history(),
					closeBrackets(),
					keymap.of([...defaultKeymap, ...historyKeymap]),
					isSQL(driver) ? sql({ dialect: dialectOf(driver) }) : [],
					autocompletion({ override: [complete], activateOnTyping: true, icons: false }),
					syntaxHighlighting(highlight),
					theme,
					ph(placeholder),
					EditorView.updateListener.of((u) => {
						if (u.docChanged) value = u.state.doc.toString();
					})
				]
			})
		});
		return () => view?.destroy();
	});

	// Follow outside changes (e.g. the clear button).
	$effect(() => {
		const v = value;
		if (view && v !== view.state.doc.toString()) {
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
		}
	});

	export function focus() {
		view?.focus();
		if (view) view.dispatch({ selection: { anchor: 0, head: view.state.doc.length } });
	}
</script>

<div class="line" bind:this={host}></div>

<style>
	.line {
		flex: 1;
		min-width: 0;
		user-select: text;
	}
</style>
