import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isNumber, joinMoment, kindOf, parseMoment, shortType } from './celltype.ts';

test('kindOf maps database types to editors', () => {
	assert.equal(kindOf('boolean'), 'bool');
	assert.equal(kindOf('timestamptz'), 'datetime');
	assert.equal(kindOf('date'), 'date');
	assert.equal(kindOf('time'), 'time');
	assert.equal(kindOf('jsonb'), 'json');
	assert.equal(kindOf('uuid'), 'uuid');
	assert.equal(kindOf('numeric(10,2)'), 'number');
	assert.equal(kindOf('varchar'), 'text');
});

test('isNumber accepts numbers only', () => {
	for (const s of ['1', '-2.5', '.5', '1e10', ' 3 ']) assert.equal(isNumber(s), true, s);
	for (const s of ['', 'abc', '1.2.3', '1e', '--1']) assert.equal(isNumber(s), false, s);
});

test('moments keep their zone when edited', () => {
	const m = parseMoment('2026-10-16 14:16:35+03:00');
	assert.deepEqual(m, { date: '2026-10-16', time: '14:16:35', zone: '+03:00' });
	assert.equal(joinMoment('datetime', '2026-10-17', '09:00:00', m!.zone), '2026-10-17 09:00:00+03:00');
	assert.equal(parseMoment('nope'), null);
});

test('shortType abbreviates long SQL type names', () => {
	assert.equal(shortType('timestamp with time zone'), 'timestamptz');
	assert.equal(shortType('timestamp(3) without time zone'), 'timestamp');
	assert.equal(shortType('character varying(255)'), 'varchar(255)');
	assert.equal(shortType('USER-DEFINED'), 'enum');
	assert.equal(shortType('numeric'), 'numeric');
});
