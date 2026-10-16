import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bytes } from './format.ts';

test('bytes picks a unit and drops trailing zeros', () => {
	assert.equal(bytes(512), '512 B');
	assert.equal(bytes(4096), '4 KB');
	assert.equal(bytes(1536), '1.5 KB');
	assert.equal(bytes(80 * 1024 * 1024), '80 MB');
});
