import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compareCells, nextSort } from './sort.ts';

test('compareCells: numbers, natural strings, NULLs last both ways', () => {
	const rows = [10, null, 2, 'item10', 'item2'];
	const asc = [...rows].sort((a, b) => compareCells(a, b, 1));
	const desc = [...rows].sort((a, b) => compareCells(a, b, -1));
	assert.equal(asc.at(-1), null);
	assert.equal(desc.at(-1), null);
	assert.deepEqual(
		['item10', 'item2'].sort((a, b) => compareCells(a, b, 1)),
		['item2', 'item10']
	);
});

test('nextSort cycles asc → desc → none', () => {
	let s = nextSort({ column: '', dir: '' }, 'id');
	assert.deepEqual(s, { column: 'id', dir: 'asc' });
	s = nextSort(s, 'id');
	assert.equal(s.dir, 'desc');
	s = nextSort(s, 'id');
	assert.equal(s.dir, '');
	assert.deepEqual(nextSort(s, 'name'), { column: 'name', dir: 'asc' });
});
