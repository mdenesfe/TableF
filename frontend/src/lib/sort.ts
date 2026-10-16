// Client-side sorting shared by the table and query views.

export type SortDir = 'asc' | 'desc' | '';
export interface Sort {
	column: string;
	dir: SortDir;
}

// One collator for every comparison: creating one per call (as
// localeCompare with options does) is an order of magnitude slower.
export const collator = new Intl.Collator(undefined, { numeric: true });

/** Compares two cells in the given direction; NULLs sort last either way. */
export function compareCells(x: unknown, y: unknown, dir: 1 | -1): number {
	if (x === y) return 0;
	if (x === null || x === undefined) return 1;
	if (y === null || y === undefined) return -1;
	if (typeof x === 'number' && typeof y === 'number') return (x - y) * dir;
	return collator.compare(String(x), String(y)) * dir;
}

/** Header clicks cycle a column through ascending, descending and unsorted. */
export function nextSort(sort: Sort, column: string): Sort {
	if (sort.column !== column) return { column, dir: 'asc' };
	return { column, dir: sort.dir === 'asc' ? 'desc' : sort.dir === 'desc' ? '' : 'asc' };
}
