// Safe modes, like TablePlus's: per connection, how careful TableF is before
// running anything. Alert modes ask first (here, in the UI); read-only is
// enforced by the backend, which refuses any statement that could write.

export type SafeMode = 'silent' | 'alert-changes' | 'alert-all' | 'read-only';

export const SAFE_MODES: { value: SafeMode; label: string; hint: string }[] = [
	{ value: 'silent', label: 'Silent', hint: 'Runs everything without asking.' },
	{ value: 'alert-changes', label: 'Alert on changes', hint: 'Asks before anything that changes data or schema, and before commits.' },
	{ value: 'alert-all', label: 'Alert on every query', hint: 'Asks before every query and commit.' },
	{ value: 'read-only', label: 'Read-only', hint: 'Refuses anything that could change data. Editing is turned off.' }
];

export const modeOf = (m: string | undefined): SafeMode => (SAFE_MODES.some((x) => x.value === m) ? (m as SafeMode) : 'silent');

const READ_START = new Set(['SELECT', 'WITH', 'VALUES', 'TABLE', 'SHOW', 'DESCRIBE', 'DESC', 'EXPLAIN', 'PRAGMA', 'USE']);
const WRITE_WORDS = new Set([
	'INSERT',
	'UPDATE',
	'DELETE',
	'MERGE',
	'UPSERT',
	'INTO',
	'CREATE',
	'DROP',
	'ALTER',
	'TRUNCATE',
	'GRANT',
	'REVOKE',
	'CALL',
	'EXECUTE'
]);

/** The statement's bare words, without strings, quoted names and comments. */
function words(stmt: string): string[] {
	const bare = stmt
		.replace(/--[^\n]*/g, ' ')
		.replace(/\/\*[\s\S]*?\*\//g, ' ')
		.replace(/'(?:[^']|'')*'|"(?:[^"]|"")*"|`(?:[^`]|``)*`|\[[^\]]*\]/g, ' ');
	return bare.match(/[A-Za-z_][A-Za-z0-9_$]*/g) ?? [];
}

/**
 * Whether a SQL statement only reads (the same rule the backend uses for
 * read-only connections, so alerts and refusals agree).
 */
export function onlyReads(stmt: string): boolean {
	const w = words(stmt).map((x) => x.toUpperCase());
	if (!w.length || !READ_START.has(w[0])) return false;
	if (w[0] === 'PRAGMA' && stmt.includes('=')) return false;
	return !w.some((x) => WRITE_WORDS.has(x));
}

/** Whether a mode wants confirmation before running these statements. */
export function needsConfirm(mode: SafeMode, statements: string[]): boolean {
	if (mode === 'alert-all') return statements.length > 0;
	if (mode === 'alert-changes') return statements.some((s) => !onlyReads(s));
	return false;
}
