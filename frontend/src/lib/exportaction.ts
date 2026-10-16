// Runs an export: copies Markdown/JSON to the clipboard or saves CSV/Excel.
import { api } from './api.ts';
import { exportFileName, toCSV, toJSON, toMarkdown, toXLSX, type ExportKind, type Grid } from './export.ts';
import { app } from './state.svelte.ts';

/** Exports g as `kind`; `single` treats it as one record (JSON object, field/value table). */
export async function exportGrid(kind: ExportKind, g: Grid, name: string, single = false) {
	const file = exportFileName(name);
	const n = g.rows.length;
	const what = single ? 'row' : `${n.toLocaleString()} row${n === 1 ? '' : 's'}`;
	if (kind === 'json' || kind === 'markdown') {
		let out: string;
		if (single && kind === 'json') out = JSON.stringify(Object.fromEntries(g.columns.map((c, i) => [c, g.rows[0]?.[i] ?? null])), null, 2);
		else if (single) out = toMarkdown({ columns: ['field', 'value'], rows: g.columns.map((c, i) => [c, g.rows[0]?.[i]]) });
		else out = kind === 'json' ? toJSON(g) : toMarkdown(g);
		await navigator.clipboard?.writeText(out).catch(() => {});
		app.notify('info', `Copied ${what} as ${kind === 'json' ? 'JSON' : 'Markdown'}`);
	} else if (kind === 'csv') {
		if (await api.saveFile(`${file}.csv`, toCSV(g))) app.notify('info', `Saved ${what} as CSV`);
	} else if (await api.saveFile(`${file}.xlsx`, toXLSX(g, name), 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet')) {
		app.notify('info', `Saved ${what} as Excel`);
	}
}
