const DOLLAR_TAG = /\$[A-Za-z_]?[A-Za-z0-9_]*\$/y;
const ORDER_BY = /order\s+by\b/iy;

/** True when sql[from, to) holds nothing but whitespace and line comments. Linear time. */
function onlyComments(sql: string, from: number, to: number): boolean {
	let i = from;
	while (i < to) {
		const c = sql[i];
		if (c === '-' && sql[i + 1] === '-') {
			while (i < to && sql[i] !== '\n') i++;
		} else if (c === ' ' || c === '\t' || c === '\n' || c === '\r') i++;
		else return false;
	}
	return true;
}

/**
 * Splits a SQL script into statement ranges on top-level semicolons, skipping
 * quotes, comments and Postgres dollar quotes. Mirrors core.SplitStatements.
 * Ranges are trimmed and exclude the terminating semicolon.
 */
export function statementRanges(sql: string): { from: number; to: number }[] {
	const out: { from: number; to: number }[] = [];
	let start = 0;
	let i = 0;
	const n = sql.length;

	const push = (end: number) => {
		let a = start;
		let b = end;
		while (a < b && /\s/.test(sql[a])) a++;
		while (b > a && /\s/.test(sql[b - 1])) b--;
		if (b > a && !onlyComments(sql, a, b)) out.push({ from: a, to: b });
	};

	while (i < n) {
		const c = sql[i];
		if (c === "'" || c === '"' || c === '`') {
			i++;
			while (i < n && sql[i] !== c) i += sql[i] === '\\' && c === "'" ? 2 : 1;
			i++;
		} else if (c === '-' && sql[i + 1] === '-') {
			while (i < n && sql[i] !== '\n') i++;
		} else if (c === '/' && sql[i + 1] === '*') {
			const end = sql.indexOf('*/', i + 2);
			i = end < 0 ? n : end + 2;
		} else if (c === '$') {
			// Sticky match at i: no copy of the rest of the script per "$".
			DOLLAR_TAG.lastIndex = i;
			const m = DOLLAR_TAG.exec(sql);
			if (m) {
				const end = sql.indexOf(m[0], i + m[0].length);
				i = end < 0 ? n : end + m[0].length;
			} else i++;
		} else if (c === ';') {
			push(i);
			start = ++i;
		} else i++;
	}
	push(n);
	return out;
}

/**
 * Splits a free-form filter line into its condition and ORDER BY parts, e.g.
 * "WHERE paid = false ORDER BY amount DESC" → { where: "paid = false", order: "amount DESC" }.
 * Keywords inside quotes, comments or parentheses are ignored.
 */
export function splitFilter(text: string): { where: string; order: string } {
	let depth = 0;
	let i = 0;
	const n = text.length;
	while (i < n) {
		const c = text[i];
		if (c === "'" || c === '"' || c === '`') {
			i++;
			while (i < n && text[i] !== c) i++;
			i++;
		} else if (c === '(') {
			depth++;
			i++;
		} else if (c === ')') {
			depth--;
			i++;
		} else if (depth === 0 && (c === 'o' || c === 'O') && (i === 0 || /\s/.test(text[i - 1])) && ((ORDER_BY.lastIndex = i), ORDER_BY.test(text))) {
			const where = text.slice(0, i);
			const order = text.slice(i).replace(/^order\s+by\s*/i, '');
			return { where: stripWhere(where), order: order.trim().replace(/;$/, '') };
		} else i++;
	}
	return { where: stripWhere(text), order: '' };
}

const stripWhere = (s: string) =>
	s
		.trim()
		.replace(/^where\b\s*/i, '')
		.replace(/;$/, '')
		.trim();

const DESTRUCTIVE = /^\s*(drop|truncate)\b|^\s*(delete|update)\b(?![\s\S]*\bwhere\b)/i;

/** Statements that remove or rewrite data wholesale. */
export function isDestructive(statement: string): boolean {
	return DESTRUCTIVE.test(statement.replace(/^\s*(--[^\n]*\n|\/\*[\s\S]*?\*\/)*/g, ''));
}

const IDENT = String.raw`(?:"(?:[^"]|"")+"|\x60(?:[^\x60]|\x60\x60)+\x60|\[[^\]]+\]|[\p{L}_][\p{L}\p{N}_$]*)`;
const unquote = (s: string) =>
	/^".*"$/s.test(s)
		? s.slice(1, -1).replaceAll('""', '"')
		: /^`.*`$/s.test(s)
			? s.slice(1, -1).replaceAll('``', '`')
			: /^\[.*\]$/s.test(s)
				? s.slice(1, -1)
				: s;

/**
 * The one table a plain SELECT reads from, so its rows can be edited in place.
 * Joins, unions, grouping, DISTINCT, CTEs and subqueries in FROM return null.
 */
export function sourceTable(sql: string): { schema: string; table: string } | null {
	const s = sql
		.replace(/--[^\n]*/g, ' ')
		.replace(/\/\*[\s\S]*?\*\//g, ' ')
		.trim()
		.replace(/;\s*$/, '');
	if (!/^select\b/i.test(s)) return null;
	if (/\b(join|union|intersect|except|group\s+by|distinct|having|with)\b/i.test(s)) return null;
	const m = new RegExp(
		String.raw`\bfrom\s+(${IDENT})(?:\s*\.\s*(${IDENT}))?(?:\s+(?:as\s+)?${IDENT})?\s*(?:$|\bwhere\b|\border\b|\blimit\b|\boffset\b|\bfetch\b)`,
		'iu'
	).exec(s);
	if (!m) return null;
	return m[2] ? { schema: unquote(m[1]), table: unquote(m[2]) } : { schema: '', table: unquote(m[1]) };
}
