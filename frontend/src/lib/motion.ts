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

/** The loading screen dissolves: a soft fade while it drifts slightly larger. */
export function dissolve(_node: HTMLElement, { duration = 280 }: { duration?: number } = {}) {
	return {
		duration: still() ? 0 : duration,
		easing: ease,
		css: (t: number) => `opacity: ${t}; transform: scale(${1.03 - 0.03 * t});`
	};
}

/** A page entering: fades in while rising a few pixels into place. */
export function arrive(_node: HTMLElement, { duration = 380, delay = 0 }: { duration?: number; delay?: number } = {}) {
	return {
		duration: still() ? 0 : duration,
		delay: still() ? 0 : delay,
		easing: ease,
		css: (t: number) => `opacity: ${t}; transform: translateY(${(1 - t) * 6}px);`
	};
}
