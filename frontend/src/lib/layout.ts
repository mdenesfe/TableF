// Split-pane layout as a tree: leaves are tab groups, splits arrange children in a row or column.

export type LayoutNode = { kind: 'leaf'; group: number } | { kind: 'split'; dir: 'row' | 'col'; children: LayoutNode[]; sizes: number[] };

export type Side = 'left' | 'right' | 'top' | 'bottom';

export interface Rect {
	x: number;
	y: number;
	w: number;
	h: number;
}

/** A draggable line between two children of a split; path addresses the split node. */
export interface Divider {
	path: number[];
	index: number;
	dir: 'row' | 'col';
	x: number;
	y: number;
	len: number;
	/** Extent of the parent split along its axis, for converting pointer moves to sizes. */
	span: number;
	start: number;
}

export const leaf = (group: number): LayoutNode => ({ kind: 'leaf', group });

export function groupsOf(node: LayoutNode): number[] {
	return node.kind === 'leaf' ? [node.group] : node.children.flatMap(groupsOf);
}

/** Splits the leaf for `target`, putting a new leaf `group` on `side` of it. */
export function splitLeaf(root: LayoutNode, target: number, group: number, side: Side): LayoutNode {
	const dir = side === 'left' || side === 'right' ? 'row' : 'col';
	const before = side === 'left' || side === 'top';
	const walk = (node: LayoutNode): LayoutNode => {
		if (node.kind === 'leaf') {
			if (node.group !== target) return node;
			const children = before ? [leaf(group), node] : [node, leaf(group)];
			return { kind: 'split', dir, children, sizes: [0.5, 0.5] };
		}
		// Same direction: add a sibling instead of nesting, sharing the target's space.
		const i = node.children.findIndex((c) => c.kind === 'leaf' && c.group === target);
		if (i >= 0 && node.dir === dir) {
			const children = [...node.children];
			const sizes = [...node.sizes];
			const half = sizes[i] / 2;
			const at = before ? i : i + 1;
			children.splice(at, 0, leaf(group));
			sizes.splice(i, 1, half, half);
			return { ...node, children, sizes };
		}
		return { ...node, children: node.children.map(walk) };
	};
	return walk(root);
}

/** Removes a group's leaf; splits left with one child collapse into it. */
export function removeLeaf(root: LayoutNode, group: number): LayoutNode {
	const walk = (node: LayoutNode): LayoutNode | null => {
		if (node.kind === 'leaf') return node.group === group ? null : node;
		const kept: LayoutNode[] = [];
		const sizes: number[] = [];
		node.children.forEach((c, i) => {
			const n = walk(c);
			if (n) {
				kept.push(n);
				sizes.push(node.sizes[i]);
			}
		});
		if (!kept.length) return null;
		if (kept.length === 1) return kept[0];
		const total = sizes.reduce((a, b) => a + b, 0);
		return { ...node, children: kept, sizes: sizes.map((s) => s / total) };
	};
	return walk(root) ?? leaf(0);
}

/** Fractional rectangles for every group, plus the dividers between siblings. */
export function measure(root: LayoutNode): { rects: Map<number, Rect>; dividers: Divider[] } {
	const rects = new Map<number, Rect>();
	const dividers: Divider[] = [];
	const walk = (node: LayoutNode, r: Rect, path: number[]) => {
		if (node.kind === 'leaf') {
			rects.set(node.group, r);
			return;
		}
		let offset = node.dir === 'row' ? r.x : r.y;
		const span = node.dir === 'row' ? r.w : r.h;
		const start = offset;
		node.children.forEach((c, i) => {
			const size = node.sizes[i] * span;
			const cr = node.dir === 'row' ? { x: offset, y: r.y, w: size, h: r.h } : { x: r.x, y: offset, w: r.w, h: size };
			walk(c, cr, [...path, i]);
			offset += size;
			if (i < node.children.length - 1) {
				dividers.push(
					node.dir === 'row'
						? { path, index: i, dir: 'row', x: offset, y: r.y, len: r.h, span, start }
						: { path, index: i, dir: 'col', x: r.x, y: offset, len: r.w, span, start }
				);
			}
		});
	};
	walk(root, { x: 0, y: 0, w: 1, h: 1 }, []);
	return { rects, dividers };
}

/** Moves the divider at `index` of the split at `path` to fraction `pos` of the whole canvas. */
export function resizeAt(root: LayoutNode, d: Divider, pos: number): LayoutNode {
	const walk = (node: LayoutNode, depth: number): LayoutNode => {
		if (node.kind === 'leaf') return node;
		if (depth < d.path.length) {
			const i = d.path[depth];
			return { ...node, children: node.children.map((c, j) => (j === i ? walk(c, depth + 1) : c)) };
		}
		const sizes = [...node.sizes];
		const before = sizes.slice(0, d.index).reduce((a, b) => a + b, 0);
		const pair = sizes[d.index] + sizes[d.index + 1];
		const min = 0.1;
		const local = (pos - d.start) / d.span - before;
		const a = Math.min(pair - min, Math.max(min, local));
		sizes[d.index] = a;
		sizes[d.index + 1] = pair - a;
		return { ...node, sizes };
	};
	return walk(root, 0);
}
