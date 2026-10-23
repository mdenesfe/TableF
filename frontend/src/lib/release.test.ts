import { test } from 'node:test';
import assert from 'node:assert/strict';
import { highlights, newer } from './release.ts';

test('newer compares like the Go updater', () => {
	assert.equal(newer('0.1.4', '0.1.3'), true);
	assert.equal(newer('v0.1.10', '0.1.9'), true);
	assert.equal(newer('0.2', '0.1.9'), true);
	assert.equal(newer('0.1.3', '0.1.3'), false);
	assert.equal(newer('0.1.2', '0.1.3'), false);
	assert.equal(newer('1.0.0', '1.0.0-beta'), true);
	assert.equal(newer('1.0.0-beta', '1.0.0'), false);
});

test('highlights takes the first bullets as plain text, skipping downloads', () => {
	const notes = [
		'**Downloads**',
		'- macOS 13+: `TableF-macos-universal.dmg`',
		'- Windows 10/11 (x64): `TableF-windows-amd64.zip`',
		'**What is new**',
		'- **Safe modes**, like [TablePlus](https://tableplus.com)',
		'  - a nested detail that is left out',
		'- See who you are connected as:',
		'* Query history',
		'- A fourth one'
	].join('\n');
	assert.deepEqual(highlights(notes), ['Safe modes, like TablePlus', 'See who you are connected as', 'Query history']);
});
