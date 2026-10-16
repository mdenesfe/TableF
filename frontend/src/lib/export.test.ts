import { test } from 'node:test';
import assert from 'node:assert/strict';
import { toCSV, toJSON, toMarkdown, toXLSX } from './export.ts';

const grid = {
	columns: ['id', 'name'],
	rows: [
		[1, 'a|b'],
		[2, 'x,"y"\nz'],
		[3, null]
	]
};

test('JSON keeps column names and nulls', () => {
	assert.deepEqual(JSON.parse(toJSON(grid)), [
		{ id: 1, name: 'a|b' },
		{ id: 2, name: 'x,"y"\nz' },
		{ id: 3, name: null }
	]);
});

test('Markdown escapes pipes and newlines', () => {
	assert.equal(toMarkdown(grid).split('\n')[2], '| 1 | a\\|b |');
});

test('CSV quotes fields that need it and starts with a BOM', () => {
	const csv = toCSV(grid);
	assert.ok(csv.startsWith('﻿id,name\r\n'));
	assert.ok(csv.includes('2,"x,""y""\nz"'));
});

test('XLSX is a zip with the expected parts', () => {
	const bytes = toXLSX(grid);
	assert.deepEqual([...bytes.subarray(0, 4)], [0x50, 0x4b, 0x03, 0x04]); // PK\x03\x04
	const text = new TextDecoder().decode(bytes);
	for (const part of ['[Content_Types].xml', 'xl/workbook.xml', 'xl/worksheets/sheet1.xml']) assert.ok(text.includes(part), part);
});
