// localStorage that never throws: private windows, quotas and disabled storage
// just fall back to defaults.

export function load<T>(key: string, fallback: T): T {
	try {
		const v = localStorage.getItem(key);
		return v === null ? fallback : (JSON.parse(v) as T);
	} catch {
		return fallback;
	}
}

export function save(key: string, value: unknown) {
	try {
		localStorage.setItem(key, JSON.stringify(value));
	} catch {
		/* storage unavailable */
	}
}

export function remove(key: string) {
	try {
		localStorage.removeItem(key);
	} catch {
		/* storage unavailable */
	}
}
