// What kind of editor a column gets, from its database type name.

export type CellKind = 'bool' | 'date' | 'datetime' | 'time' | 'json' | 'uuid' | 'number' | 'text';

export function kindOf(type = ''): CellKind {
	const t = type.toLowerCase();
	if (/^(bool|boolean|bit)$/.test(t) || t === 'tinyint(1)') return 'bool';
	if (/json/.test(t)) return 'json';
	if (/uuid|uniqueidentifier/.test(t)) return 'uuid';
	if (/^time(?!stamp)/.test(t) || t === 'timetz') return 'time';
	if (/^date$/.test(t)) return 'date';
	if (/timestamp|datetime|smalldatetime|^date/.test(t)) return 'datetime';
	if (/int|numeric|decimal|real|double|float|serial|money|number/.test(t)) return 'number';
	return 'text';
}

const pad = (n: number, w = 2) => String(n).padStart(w, '0');

export const fmtDate = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export const fmtTime = (d: Date) => `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;

/** The current moment in the column's own format. */
export function now(kind: CellKind): string {
	const d = new Date();
	if (kind === 'date') return fmtDate(d);
	if (kind === 'time') return fmtTime(d);
	return `${fmtDate(d)} ${fmtTime(d)}`;
}

/**
 * Splits a date/time string into its parts. The zone suffix (Z, +03:00) is
 * kept as written, so editing never shifts a stored value.
 */
export function parseMoment(v: string): { date: string; time: string; zone: string } | null {
	const m = /^\s*(\d{4}-\d{2}-\d{2})?(?:[ T]?(\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?))?\s*(Z|[+-]\d{2}(?::?\d{2})?)?\s*$/.exec(v);
	if (!m || (!m[1] && !m[2])) return null;
	return { date: m[1] ?? '', time: m[2] ?? '', zone: m[3] ?? '' };
}

export function joinMoment(kind: CellKind, date: string, time: string, zone: string): string {
	if (kind === 'date') return date;
	if (kind === 'time') return `${time}${zone}`;
	return `${date}${time ? ` ${time}` : ''}${zone}`;
}

export const uuid = () => crypto.randomUUID();

/** Pretty-prints JSON text, or returns null when it doesn't parse. */
export function prettyJSON(v: string): string | null {
	try {
		return JSON.stringify(JSON.parse(v), null, 2);
	} catch {
		return null;
	}
}

/** A complete number as a database accepts it (sign, decimals, exponent). */
export const isNumber = (s: string) => /^\s*[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?\s*$/.test(s);

/** beforeinput guard for numeric fields: only characters a number can contain get in. */
export function numericOnly(e: InputEvent) {
	if (e.data && !/^[\d.eE+-]+$/.test(e.data)) e.preventDefault();
}

/** How a cell reads as a boolean: 'true', 'false' or 'null' (also for 't'/'f', 1/0). */
export function boolText(v: unknown): 'true' | 'false' | 'null' {
	if (v === null || v === undefined) return 'null';
	if (v === true || v === 1 || v === 't' || v === 'true' || v === '1') return 'true';
	if (v === false || v === 0 || v === 'f' || v === 'false' || v === '0') return 'false';
	return 'null';
}
