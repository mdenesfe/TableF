import { test } from 'node:test';
import assert from 'node:assert/strict';
import { needsConfirm, onlyReads } from './safemode.ts';

test('onlyReads matches the backend rule', () => {
	for (const q of [
		'SELECT * FROM users',
		"select replace(name, 'a', 'b'), comment from t order by id desc",
		'WITH x AS (SELECT 1) SELECT * FROM x',
		'SHOW TABLES',
		"SELECT 'DELETE FROM users' AS s -- UPDATE in a comment"
	])
		assert.equal(onlyReads(q), true, q);
	for (const q of [
		'INSERT INTO users VALUES (1)',
		'WITH gone AS (DELETE FROM users RETURNING *) SELECT * FROM gone',
		'EXPLAIN ANALYZE DELETE FROM users',
		'SELECT * INTO backup FROM users',
		'PRAGMA foreign_keys = OFF',
		'DROP TABLE users'
	])
		assert.equal(onlyReads(q), false, q);
});

test('needsConfirm follows the mode', () => {
	assert.equal(needsConfirm('silent', ['DELETE FROM t']), false);
	assert.equal(needsConfirm('alert-changes', ['SELECT 1']), false);
	assert.equal(needsConfirm('alert-changes', ['SELECT 1', 'UPDATE t SET x = 1']), true);
	assert.equal(needsConfirm('alert-all', ['SELECT 1']), true);
	assert.equal(needsConfirm('read-only', ['DELETE FROM t']), false);
});
