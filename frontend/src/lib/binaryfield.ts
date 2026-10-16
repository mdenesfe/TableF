// Animated fields of binary digits drawn on a canvas (the header and the home
// screen). The expensive parts are done once, not per frame:
//   - "0" and "1" are pre-rendered into small sprites and stamped with drawImage
//     (much cheaper than fillText for thousands of glyphs);
//   - each cell's random seed is computed on resize, not on every frame;
//   - digits too faint to see are skipped;
//   - the loop is capped to a low frame rate and pauses while the canvas is
//     off screen or the page is hidden.

export interface FieldSpec {
	/** Cell size in CSS pixels. */
	cell: { w: number; h: number };
	/** CSS font for the digits, e.g. "600 8px monospace". */
	font: string;
	/** Frames per second while animating. */
	fps: number;
	/** Paints anything under the digits (background, glow). Optional. */
	underlay?: (ctx: CanvasRenderingContext2D, w: number, h: number, t: number) => void;
	/**
	 * Called once per frame before the digits (dt: seconds since the last
	 * frame). Returns the per-cell function, which writes the opacity (0..1)
	 * and flip speed of the cell centred at x, y into `out` — one reused object,
	 * so a frame allocates nothing per cell.
	 */
	frame: (w: number, h: number, t: number, dt: number) => (x: number, y: number, out: Cell) => void;
}

export interface Cell {
	alpha: number;
	speed: number;
}

export interface Field {
	/** Re-renders the glyph sprites in a new color. */
	setColor(color: string): void;
	/** Draws one frame now (e.g. after a change while paused). */
	redraw(): void;
	destroy(): void;
}

const MIN_ALPHA = 0.02;

export function binaryField(canvas: HTMLCanvasElement, spec: FieldSpec, color: string, still: boolean): Field {
	const ctx = canvas.getContext('2d')!;
	let w = 0;
	let h = 0;
	let dpr = 1;
	let cols = 0;
	let rows = 0;
	let seeds = new Float32Array(0);
	let sprites: [HTMLCanvasElement, HTMLCanvasElement] | null = null;
	let currentColor = color;
	let t = Math.random() * 100;
	let raf = 0;
	let last = 0;
	let visible = true;

	function makeSprites() {
		const sw = Math.ceil(spec.cell.w * dpr);
		const sh = Math.ceil(spec.cell.h * dpr);
		sprites = ['0', '1'].map((ch) => {
			const c = document.createElement('canvas');
			c.width = sw;
			c.height = sh;
			const g = c.getContext('2d')!;
			g.scale(dpr, dpr);
			g.font = spec.font;
			g.fillStyle = currentColor;
			g.textAlign = 'center';
			g.textBaseline = 'middle';
			g.fillText(ch, spec.cell.w / 2, spec.cell.h / 2);
			return c;
		}) as [HTMLCanvasElement, HTMLCanvasElement];
	}

	function resize() {
		dpr = devicePixelRatio || 1;
		w = canvas.clientWidth;
		h = canvas.clientHeight;
		canvas.width = Math.round(w * dpr);
		canvas.height = Math.round(h * dpr);
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		cols = Math.ceil(w / spec.cell.w) + 1;
		rows = Math.ceil(h / spec.cell.h) + 1;
		seeds = new Float32Array(cols * rows);
		for (let k = 0; k < seeds.length; k++) {
			const n = Math.sin((k % cols) * 127.1 + Math.floor(k / cols) * 311.7) * 43758.5453;
			seeds[k] = n - Math.floor(n);
		}
		makeSprites();
		draw();
	}

	const out: Cell = { alpha: 0, speed: 1 };

	function draw(dt = 0) {
		if (!sprites || !w || !h) return;
		ctx.globalAlpha = 1;
		ctx.clearRect(0, 0, w, h);
		spec.underlay?.(ctx, w, h, t);
		const at = spec.frame(w, h, t, dt);
		const { w: cw, h: ch } = spec.cell;
		for (let j = 0; j < rows; j++) {
			const y = j * ch;
			for (let i = 0; i < cols; i++) {
				const x = i * cw;
				at(x + cw / 2, y + ch / 2, out);
				if (out.alpha < MIN_ALPHA) continue;
				const r = seeds[j * cols + i];
				// Each digit flips at its own pace.
				const bit = (Math.floor(r * 10 + t * (0.15 + r) * out.speed) + (r < 0.5 ? 0 : 1)) & 1;
				ctx.globalAlpha = out.alpha;
				ctx.drawImage(sprites[bit], x, y, cw, ch);
			}
		}
		ctx.globalAlpha = 1;
	}

	function loop(now: number) {
		raf = requestAnimationFrame(loop);
		if (now - last < 1000 / spec.fps) return;
		const dt = Math.min(0.1, (now - (last || now)) / 1000);
		last = now;
		t += dt;
		draw(dt);
	}

	function start() {
		if (still || raf || !visible || document.hidden) return;
		last = 0;
		raf = requestAnimationFrame(loop);
	}

	function stop() {
		cancelAnimationFrame(raf);
		raf = 0;
	}

	const ro = new ResizeObserver(resize);
	ro.observe(canvas);
	const io = new IntersectionObserver(([e]) => {
		visible = e.isIntersecting;
		if (visible) start();
		else stop();
	});
	io.observe(canvas);
	const onVisibility = () => (document.hidden ? stop() : start());
	document.addEventListener('visibilitychange', onVisibility);

	resize();
	start();

	return {
		setColor(c) {
			if (c === currentColor) return;
			currentColor = c;
			makeSprites();
			draw();
		},
		redraw: () => draw(),
		destroy() {
			stop();
			ro.disconnect();
			io.disconnect();
			document.removeEventListener('visibilitychange', onVisibility);
		}
	};
}

/** Smooth 0..1 step between a and b. */
export function smooth(a: number, b: number, x: number): number {
	const k = Math.min(1, Math.max(0, (x - a) / (b - a)));
	return k * k * (3 - 2 * k);
}
