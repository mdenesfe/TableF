// Pure helpers for release versions and notes (shared by the updater UI and tests).

/** Whether version a is newer than b (same rules as internal/update.Newer). */
export function newer(a: string, b: string): boolean {
	const parse = (v: string) => {
		const [main, pre = ''] = v.trim().replace(/^v/, '').split('-', 2);
		const n = main.split('.').map((x) => Number.parseInt(x, 10) || 0);
		return { n: [n[0] ?? 0, n[1] ?? 0, n[2] ?? 0], pre };
	};
	const pa = parse(a);
	const pb = parse(b);
	for (let i = 0; i < 3; i++) if (pa.n[i] !== pb.n[i]) return pa.n[i] > pb.n[i];
	return (pa.pre === '' && pb.pre !== '') || (pa.pre !== '' && pb.pre !== '' && pa.pre > pb.pre);
}

/** The first few top-level bullet points of release notes, as plain text. */
export function highlights(notes: string, max = 3): string[] {
	return notes
		.split('\n')
		.filter((l) => /^[-*] /.test(l)) // top-level bullets only, not nested ones
		.map((l) => l.trim())
		.map(
			(l) =>
				l
					.replace(/^[-*] /, '')
					.replace(/\*\*|`/g, '')
					.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
					.replace(/:$/, '') // a bullet that introduces nested ones
		)
		.filter((l) => !/^(macOS|Windows) /.test(l))
		.slice(0, max);
}
