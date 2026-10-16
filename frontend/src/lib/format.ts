// Number formatting shared by the details pane and the indexes view.

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

/** 1536 → "1.5 KB"; trailing zeros are dropped. */
export function bytes(n: number): string {
	let i = 0;
	while (Math.abs(n) >= 1024 && i < UNITS.length - 1) {
		n /= 1024;
		i++;
	}
	return `${i === 0 ? n : +n.toFixed(n < 10 ? 2 : n < 100 ? 1 : 0)} ${UNITS[i]}`;
}

const compactFormat = new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 });

/** 1234567 → "1.2M"; numbers under 10,000 keep their digits. */
export function compact(n: number): string {
	return n < 10_000 ? n.toLocaleString() : compactFormat.format(n);
}
