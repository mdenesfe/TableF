import { prefs } from '#lib/prefs.svelte.ts';

// Shared Svelte transitions.

const ease = (t: number) => 1 - Math.pow(1 - t, 3);
const still = () => prefs.still;

/**
 * Opens and closes a side panel by sliding it in from its edge. The panel
 * keeps its full width (so its content never squeezes) while a negative
 * margin pulls it out of the layout, letting neighbours resize smoothly.
 */
export function panel(node: HTMLElement, { side = 'left', duration = 240 }: { side?: 'left' | 'right'; duration?: number } = {}) {
	const w = node.offsetWidth;
	const prop = side === 'left' ? 'margin-left' : 'margin-right';
	return {
		duration: still() ? 0 : duration,
		easing: ease,
		css: (t: number) => `${prop}: ${-(1 - t) * w}px; opacity: ${Math.min(1, t * 1.6)};`
	};
}
