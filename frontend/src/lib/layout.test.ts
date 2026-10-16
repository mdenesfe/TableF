import { test } from 'node:test';
import assert from 'node:assert/strict';
import { groupsOf, leaf, measure, removeLeaf, resizeAt, splitLeaf } from './layout.ts';

test('splitting and removing panes', () => {
	let root = splitLeaf(leaf(0), 0, 1, 'right');
	root = splitLeaf(root, 1, 2, 'right'); // same direction: a sibling, not nested
	assert.deepEqual(groupsOf(root), [0, 1, 2]);
	assert.equal(root.kind === 'split' && root.children.length, 3);
	root = removeLeaf(root, 1);
	assert.deepEqual(groupsOf(root), [0, 2]);
	assert.deepEqual(removeLeaf(removeLeaf(root, 0), 2), leaf(0));
});

test('measure and resize keep sizes in bounds', () => {
	const root = splitLeaf(leaf(0), 0, 1, 'bottom');
	const { rects, dividers } = measure(root);
	assert.deepEqual(rects.get(1), { x: 0, y: 0.5, w: 1, h: 0.5 });
	const resized = resizeAt(root, dividers[0], 0.99);
	assert.ok(resized.kind === 'split' && resized.sizes[1] >= 0.1 - 1e-9);
});
