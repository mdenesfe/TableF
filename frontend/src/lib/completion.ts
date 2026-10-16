// Completion popups in TableF's style: each option gets the same brand icon
// box used across the app (white glyph on brand blue) instead of CodeMirror's
// default emoji and letters, and matched characters are bold instead of underlined.
import { autocompletion, type Completion } from '@codemirror/autocomplete';
import { EditorView } from '@codemirror/view';

// Lucide glyphs (ISC license), as path data on a 24x24 grid.
const glyphs: Record<string, string> = {
	keyword: '<path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/>',
	table: '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/>',
	view: '<path d="M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0"/><circle cx="12" cy="12" r="3"/>',
	column: '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18M15 3v18"/>',
	function: '<path d="M9 17c2 0 2.8-1 2.8-2.8V10c0-2 1-3.3 3.2-3"/><path d="M9 11.2h5.7"/>',
	type: '<path d="M4 7V4h16v3"/><path d="M9 20h6"/><path d="M12 4v16"/>',
	schema: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/><path d="M3 12a9 3 0 0 0 18 0"/>',
	other: '<circle cx="12" cy="12" r="3"/>'
};

// CodeMirror completion types (lang-sql and our own sources) → glyph.
const byType: Record<string, keyof typeof glyphs> = {
	keyword: 'keyword',
	type: 'table', // lang-sql uses "type" for tables (see icon())
	table: 'table',
	view: 'view',
	property: 'column',
	variable: 'column',
	column: 'column',
	function: 'function',
	method: 'function',
	namespace: 'schema',
	schema: 'schema',
	class: 'type',
	constant: 'type'
};

function icon(c: Completion): Node {
	const box = document.createElement('span');
	box.className = 'tf-complete-icon';
	box.setAttribute('aria-hidden', 'true');
	// lang-sql marks both tables and built-in data types (INTEGER, VARCHAR) as
	// "type"; the built-ins are the all-caps ones.
	const kind = c.type === 'type' && /^[A-Z][A-Z0-9 _]*$/.test(c.label) ? 'type' : (byType[c.type ?? ''] ?? 'other');
	box.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">${glyphs[kind]}</svg>`;
	return box;
}

/** Completion behaviour and look shared by the SQL editor and the filter line. */
export const completionLook = [
	autocompletion({ icons: false, addToOptions: [{ render: icon, position: 20 }] }),
	EditorView.theme({
		'.cm-tooltip.cm-tooltip-autocomplete': { padding: '4px' },
		'.cm-tooltip-autocomplete ul': { fontFamily: 'var(--font-mono)', fontSize: '12px', maxHeight: '18rem' },
		'.cm-tooltip-autocomplete ul li': {
			display: 'flex',
			alignItems: 'center',
			gap: '8px',
			padding: '3px 8px !important',
			borderRadius: '4px',
			lineHeight: '20px'
		},
		'.cm-tooltip-autocomplete ul li[aria-selected]': { background: 'var(--accent)', color: 'var(--foreground)' },
		'.tf-complete-icon': {
			display: 'inline-grid',
			placeItems: 'center',
			flex: 'none',
			width: '16px',
			height: '16px',
			borderRadius: '4px',
			background: 'var(--brand)',
			color: '#fff'
		},
		'.tf-complete-icon svg': { width: '10px', height: '10px' },
		'.cm-completionLabel': { flex: '1', minWidth: '0' },
		'.cm-completionMatchedText': { textDecoration: 'none', fontWeight: '700', color: 'var(--brand-ink)' },
		'.cm-completionDetail': { marginLeft: 'auto', paddingLeft: '1rem', fontStyle: 'normal', color: 'var(--muted-foreground)' }
	})
];
