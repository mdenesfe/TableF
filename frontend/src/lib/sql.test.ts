import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isDestructive, sourceTable, splitFilter, statementRanges } from './sql.ts';

test('statementRanges splits on semicolons outside strings and comments', () => {
	const sql = "select 1; select ';' -- ; \n; select 3";
	const parts = statementRanges(sql).map((r) => sql.slice(r.from, r.to).trim());
	assert.deepEqual(
		parts,
		['select 1', "select ';' -- ; \n", 'select 3'].map((s) => s.trim())
	);
});

test('splitFilter separates WHERE from ORDER BY', () => {
	assert.deepEqual(splitFilter("status = 'paid' order by id desc"), { where: "status = 'paid'", order: 'id desc' });
	assert.deepEqual(splitFilter("note = 'order by x'"), { where: "note = 'order by x'", order: '' });
});

test('isDestructive flags writes without WHERE and drops', () => {
	assert.equal(isDestructive('delete from users'), true);
	assert.equal(isDestructive('delete from users where id = 1'), false);
	assert.equal(isDestructive('drop table users'), true);
	assert.equal(isDestructive('select * from users'), false);
});

test('sourceTable finds the single table of a plain SELECT', () => {
	assert.deepEqual(sourceTable('SELECT * from foundations'), { schema: '', table: 'foundations' });
	assert.deepEqual(sourceTable('select id from public."Users" u where id > 3 order by id'), { schema: 'public', table: 'Users' });
	assert.deepEqual(sourceTable('select * from `db`.`t` limit 5;'), { schema: 'db', table: 't' });
	for (const q of [
		'select * from a join b on a.id = b.id',
		'select * from a, b',
		'select count(*) from t group by x',
		'select * from (select 1) x',
		'select 1'
	])
		assert.equal(sourceTable(q), null, q);
});

test('statementRanges stays linear on long comment blocks and $-heavy scripts', () => {
	const comments = '-- note\n'.repeat(5000) + 'select 1;';
	const dollars = 'select $1, $2;'.repeat(5000);
	const start = performance.now();
	assert.equal(statementRanges(comments).length, 1);
	assert.equal(statementRanges(dollars).length, 5000);
	assert.ok(performance.now() - start < 500, 'took too long');
});
