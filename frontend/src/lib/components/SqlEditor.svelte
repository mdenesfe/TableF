<script lang="ts">
	import { sql } from '@codemirror/lang-sql';
	import { dialectOf } from '#lib/sqldialect.ts';
	import { isSQL } from '#lib/engines.ts';
	import { completionLook } from '#lib/completion.ts';
	import { Compartment, EditorState, Prec } from '@codemirror/state';
	import { EditorView, keymap, tooltips } from '@codemirror/view';
	import { basicSetup } from 'codemirror';
	import { HighlightStyle, indentUnit, syntaxHighlighting } from '@codemirror/language';
	import { prefs } from '#lib/prefs.svelte.ts';
	import { tags } from '@lezer/highlight';
	import { onMount } from 'svelte';
	import type { DriverName } from '#lib/types.ts';
	import { statementRanges } from '#lib/sql.ts';

	let {
		value = $bindable(''),
		driver,
		schema = {},
		onrun
	}: {
		value?: string;
		driver: DriverName;
		schema?: Record<string, string[]>;
		onrun: (sql: string) => void;
	} = $props();

	let host: HTMLDivElement;
	// Settings from Preferences that can change while the editor is open.
	const options = new Compartment();
	function optionExtensions() {
		const p = prefs.value;
		return [
			EditorState.tabSize.of(p.editorTabSize),
			indentUnit.of(' '.repeat(p.editorTabSize)),
			p.editorWrap ? EditorView.lineWrapping : [],
			p.editorLineNumbers ? [] : EditorView.theme({ '.cm-gutters': { display: 'none' } }),
			isSQL(driver) ? sql({ dialect: dialectOf(driver), schema, upperCaseKeywords: p.keywordCase !== 'lower' }) : []
		];
	}
	let view: EditorView | undefined;

	const highlight = HighlightStyle.define([
		{ tag: tags.keyword, color: 'var(--foreground)', fontWeight: '600' },
		{ tag: [tags.string, tags.special(tags.string)], color: 'oklch(0.68 0.12 150)' },
		{ tag: [tags.number, tags.bool, tags.null], color: 'oklch(0.7 0.12 60)' },
		{ tag: tags.comment, color: 'var(--muted-foreground)', fontStyle: 'italic' },
		{ tag: [tags.typeName, tags.standard(tags.name)], color: 'oklch(0.68 0.1 240)' },
		{ tag: tags.operator, color: 'var(--muted-foreground)' }
	]);

	const theme = EditorView.theme({
		'&': { height: '100%', background: 'var(--card)', color: 'var(--foreground)', fontSize: 'var(--editor-fs)' },
		'.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.65' },
		'.cm-content': { caretColor: 'var(--brand-ink)', padding: '12px 0' },
		'.cm-gutters': { background: 'var(--card)', color: 'var(--muted-foreground)', border: 'none', paddingLeft: '8px' },
		'.cm-activeLine': { background: 'color-mix(in oklch, var(--accent) 50%, transparent)' },
		'.cm-activeLineGutter': { background: 'transparent', color: 'var(--foreground)' },
		'.cm-foldGutter, .cm-foldPlaceholder': { display: 'none !important' },
		'.cm-cursor': { borderLeftColor: 'var(--brand-ink)' },
		'&.cm-focused': { outline: 'none' },
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
		'.cm-matchingBracket': { background: 'color-mix(in oklab, var(--brand) 22%, transparent)', outline: 'none' }
	});

	/** Selection if any, otherwise the statement under the cursor. */
	export function currentStatement(): string {
		if (!view) return '';
		const { from, to, head } = view.state.selection.main;
		if (from !== to) return view.state.sliceDoc(from, to);
		const doc = view.state.doc.toString();
		const ranges = statementRanges(doc);
		// The cursor belongs to the statement it is in, or the one just before it.
		const hit = ranges.find((r) => head >= r.from && head <= r.to) ?? ranges.filter((r) => r.to <= head).pop() ?? ranges[0];
		return hit ? doc.slice(hit.from, hit.to) : '';
	}

	export function allStatements(): string {
		return view?.state.doc.toString() ?? '';
	}

	export function focus() {
		view?.focus();
	}

	const runCurrent = () => (onrun(currentStatement()), true);
	const runAll = () => (onrun(allStatements()), true);

	onMount(() => {
		view = new EditorView({
			parent: host,
			state: EditorState.create({
				doc: value,
				extensions: [
					Prec.highest(
						keymap.of([
							{ key: 'Mod-Enter', run: runCurrent },
							{ key: 'Shift-Mod-Enter', run: runAll }
						])
					),
					basicSetup,
					completionLook,
					// Tooltips (autocomplete) live on <body> so the editor pane never clips them.
					// Popups live on <body> so panes can't clip them. Absolute positioning lets
					// CodeMirror correct for the interface zoom (it measures the parent's scale).
					tooltips({ parent: document.body, position: 'absolute' }),
					options.of(optionExtensions()),
					syntaxHighlighting(highlight),
					theme,
					EditorView.updateListener.of((u) => {
						if (u.docChanged) value = emitted = u.state.doc.toString();
					})
				]
			})
		});
		view.focus();
		return () => view?.destroy();
	});

	$effect(() => {
		(void prefs.value.editorTabSize, prefs.value.editorWrap, prefs.value.editorLineNumbers, prefs.value.keywordCase);
		view?.dispatch({ effects: options.reconfigure(optionExtensions()) });
	});

	// Follow outside changes (e.g. Format SQL). Values the editor emitted itself
	// are skipped without re-reading the whole document.
	let emitted: string | undefined;
	$effect(() => {
		const v = value;
		if (view && v !== emitted && v !== view.state.doc.toString()) {
			emitted = v;
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
		}
	});
</script>

<div class="editor" bind:this={host}></div>

<style>
	.editor {
		height: 100%;
		overflow: hidden;
		user-select: text;
	}
	.editor :global(.cm-editor) {
		height: 100%;
	}
</style>
