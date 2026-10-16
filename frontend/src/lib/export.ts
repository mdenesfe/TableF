// Turns a grid (column names + rows) into the formats offered by Export menus.

export type ExportKind = 'markdown' | 'json' | 'csv' | 'xlsx';
export type Grid = { columns: string[]; rows: unknown[][] };

const pad2 = (n: number) => String(n).padStart(2, '0');

/** A file name for an export: what was exported plus when, e.g. "customers_2026-10-16_22-55-03". */
export function exportFileName(base: string, at = new Date()): string {
	const safe =
		base
			.trim()
			.replace(/[^\p{L}\p{N}._-]+/gu, '_')
			.replace(/^_+|_+$/g, '') || 'export';
	const stamp = `${at.getFullYear()}-${pad2(at.getMonth() + 1)}-${pad2(at.getDate())}_${pad2(at.getHours())}-${pad2(at.getMinutes())}-${pad2(at.getSeconds())}`;
	return `${safe}_${stamp}`;
}

const text = (v: unknown) => (v === null || v === undefined ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v));

export function toJSON({ columns, rows }: Grid): string {
	return JSON.stringify(
		rows.map((r) => Object.fromEntries(columns.map((c, i) => [c, r[i] ?? null]))),
		null,
		2
	);
}

export function toMarkdown({ columns, rows }: Grid): string {
	const esc = (v: unknown) => text(v).replaceAll('|', '\\|').replaceAll('\n', ' ');
	return [
		`| ${columns.map(esc).join(' | ')} |`,
		`| ${columns.map(() => '---').join(' | ')} |`,
		...rows.map((r) => `| ${columns.map((_, i) => esc(r[i])).join(' | ')} |`)
	].join('\n');
}

export function toCSV({ columns, rows }: Grid): string {
	const q = (v: unknown) => {
		const t = text(v);
		return /[",\r\n]/.test(t) ? `"${t.replaceAll('"', '""')}"` : t;
	};
	// The BOM makes Excel read UTF-8 correctly.
	return '\ufeff' + [columns.map(q).join(','), ...rows.map((r) => columns.map((_, i) => q(r[i])).join(','))].join('\r\n');
}

// ---------- xlsx ----------

const xml = (s: string) =>
	s
		.replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '')
		.replaceAll('&', '&amp;')
		.replaceAll('<', '&lt;')
		.replaceAll('>', '&gt;')
		.replaceAll('"', '&quot;');

function colName(i: number): string {
	let s = '';
	for (i++; i > 0; i = Math.floor((i - 1) / 26)) s = String.fromCharCode(65 + ((i - 1) % 26)) + s;
	return s;
}

function cell(v: unknown, ref: string, style = 0): string {
	const s = style ? ` s="${style}"` : '';
	if (v === null || v === undefined) return '';
	if (typeof v === 'number' && Number.isFinite(v)) return `<c r="${ref}"${s}><v>${v}</v></c>`;
	if (typeof v === 'boolean') return `<c r="${ref}" t="b"${s}><v>${v ? 1 : 0}</v></c>`;
	return `<c r="${ref}" t="inlineStr"${s}><is><t xml:space="preserve">${xml(text(v))}</t></is></c>`;
}

/** A single-sheet Excel workbook with a bold, frozen header row. */
export function toXLSX({ columns, rows }: Grid, sheet = 'Sheet1'): Uint8Array {
	const letters = columns.map((_, i) => colName(i)); // once per column, not per cell
	const header = `<row r="1">${columns.map((c, i) => cell(c, `${letters[i]}1`, 1)).join('')}</row>`;
	const body = rows.map((r, j) => `<row r="${j + 2}">${letters.map((l, i) => cell(r[i], `${l}${j + 2}`)).join('')}</row>`).join('');
	const files: [string, string][] = [
		[
			'[Content_Types].xml',
			'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>'
		],
		[
			'_rels/.rels',
			'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>'
		],
		[
			'xl/workbook.xml',
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="${xml(sheet.slice(0, 31))}" sheetId="1" r:id="rId1"/></sheets></workbook>`
		],
		[
			'xl/_rels/workbook.xml.rels',
			'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>'
		],
		[
			'xl/styles.xml',
			'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts><fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf/></cellStyleXfs><cellXfs count="2"><xf fontId="0"/><xf fontId="1" applyFont="1"/></cellXfs></styleSheet>'
		],
		[
			'xl/worksheets/sheet1.xml',
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><sheetData>${header}${body}</sheetData></worksheet>`
		]
	];
	return zip(files.map(([name, data]) => ({ name, data: new TextEncoder().encode(data) })));
}

// A minimal ZIP writer (stored, no compression) — enough for xlsx.
const crcTable = (() => {
	const t = new Uint32Array(256);
	for (let n = 0; n < 256; n++) {
		let c = n;
		for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
		t[n] = c >>> 0;
	}
	return t;
})();

function crc32(b: Uint8Array): number {
	let c = 0xffffffff;
	for (let i = 0; i < b.length; i++) c = crcTable[(c ^ b[i]) & 0xff] ^ (c >>> 8);
	return (c ^ 0xffffffff) >>> 0;
}

function zip(entries: { name: string; data: Uint8Array }[]): Uint8Array {
	const enc = new TextEncoder();
	const local: Uint8Array[] = [];
	const central: Uint8Array[] = [];
	let offset = 0;
	for (const e of entries) {
		const name = enc.encode(e.name);
		const crc = crc32(e.data);
		const head = new DataView(new ArrayBuffer(30));
		head.setUint32(0, 0x04034b50, true);
		head.setUint16(4, 20, true);
		head.setUint32(14, crc, true);
		head.setUint32(18, e.data.length, true);
		head.setUint32(22, e.data.length, true);
		head.setUint16(26, name.length, true);
		local.push(new Uint8Array(head.buffer), name, e.data);

		const dir = new DataView(new ArrayBuffer(46));
		dir.setUint32(0, 0x02014b50, true);
		dir.setUint16(4, 20, true);
		dir.setUint16(6, 20, true);
		dir.setUint32(16, crc, true);
		dir.setUint32(20, e.data.length, true);
		dir.setUint32(24, e.data.length, true);
		dir.setUint16(28, name.length, true);
		dir.setUint32(42, offset, true);
		central.push(new Uint8Array(dir.buffer), name);
		offset += 30 + name.length + e.data.length;
	}
	const size = central.reduce((n, b) => n + b.length, 0);
	const end = new DataView(new ArrayBuffer(22));
	end.setUint32(0, 0x06054b50, true);
	end.setUint16(8, entries.length, true);
	end.setUint16(10, entries.length, true);
	end.setUint32(12, size, true);
	end.setUint32(16, offset, true);
	const parts = [...local, ...central, new Uint8Array(end.buffer)];
	const out = new Uint8Array(parts.reduce((n, b) => n + b.length, 0));
	let at = 0;
	for (const p of parts) {
		out.set(p, at);
		at += p.length;
	}
	return out;
}
