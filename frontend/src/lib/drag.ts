// Pointer drags for resize handles and dividers.

/**
 * Follows a drag that starts with `e` (a pointerdown on the handle). The
 * pointer is captured by the handle, so events keep arriving outside it and
 * every listener lives on the handle itself: nothing leaks if the component
 * goes away mid-drag. Moves are coalesced to one call per animation frame;
 * `end` runs once, on release, cancel or lost capture.
 */
export function drag(e: PointerEvent, move: (ev: PointerEvent) => void, end?: () => void) {
	e.preventDefault();
	e.stopPropagation();
	const el = e.currentTarget as HTMLElement;
	el.setPointerCapture(e.pointerId);
	let frame = 0;
	let last: PointerEvent | null = null;
	let done = false;

	const onMove = (ev: PointerEvent) => {
		last = ev;
		frame ||= requestAnimationFrame(() => {
			frame = 0;
			if (last) move(last);
		});
	};
	const finish = () => {
		if (done) return;
		done = true;
		cancelAnimationFrame(frame);
		if (last) move(last);
		el.removeEventListener('pointermove', onMove);
		el.removeEventListener('pointerup', finish);
		el.removeEventListener('pointercancel', finish);
		el.removeEventListener('lostpointercapture', finish);
		end?.();
	};
	el.addEventListener('pointermove', onMove);
	el.addEventListener('pointerup', finish);
	el.addEventListener('pointercancel', finish);
	el.addEventListener('lostpointercapture', finish);
}
