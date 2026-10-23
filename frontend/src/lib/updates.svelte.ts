// Keeps TableF current. The desktop app asks the Go updater (GitHub releases,
// verified downloads, install and restart); the web version looks at GitHub
// itself and links to the release.
import { VERSION } from './version.ts';
import { prefs } from './prefs.svelte.ts';
import { load, save } from './storage.ts';
import { newer } from './release.ts';

export interface ReleaseInfo {
	version: string;
	name: string;
	/** Markdown release notes. */
	notes: string;
	/** The release page. */
	url: string;
	publishedAt: string;
}

type Phase = 'idle' | 'checking' | 'current' | 'available' | 'downloading' | 'ready' | 'installing' | 'error';

const REPO = 'mdenesfe/TableF';
const DISMISSED_KEY = 'tablef.update.dismissed';
const EVERY = 6 * 60 * 60 * 1000;

const updater = () => (typeof window === 'undefined' ? undefined : window.go?.main?.Updater);

class Updates {
	phase = $state<Phase>('idle');
	release = $state.raw<ReleaseInfo | null>(null);
	/** Download progress, 0–1. */
	progress = $state(0);
	total = $state(0);
	error = $state('');
	/** This build can install updates itself (desktop on macOS and Windows). */
	canInstall = $state(false);
	/** The version the user chose to be reminded about later. */
	dismissed = $state(load<string>(DISMISSED_KEY, ''));
	lastChecked = $state<Date | null>(null);
	private timer: ReturnType<typeof setInterval> | undefined;

	/** The update card shows for a newer release the user hasn't put off. */
	get visible() {
		return !!this.release && this.release.version !== this.dismissed && ['available', 'downloading', 'ready', 'installing', 'error'].includes(this.phase);
	}

	/** Starts the periodic check (once, on app start). */
	start() {
		if (this.timer || typeof window === 'undefined') return;
		window.runtime?.EventsOn?.('update:progress', (d) => {
			const { done, total } = d as { done: number; total: number };
			this.total = total;
			this.progress = total > 0 ? done / total : 0;
		});
		setTimeout(() => prefs.value.checkUpdates && this.check(), 4000);
		this.timer = setInterval(() => prefs.value.checkUpdates && this.check(), EVERY);
	}

	/** Looks for a newer release; manual checks also show the card again. */
	async check(manual = false) {
		if (this.phase === 'checking' || this.phase === 'downloading' || this.phase === 'installing') return;
		this.phase = 'checking';
		this.error = '';
		try {
			const u = updater();
			let release: ReleaseInfo | null = null;
			let downloaded = false;
			if (u) {
				const r = (await u.CheckUpdate(VERSION)) as { available: boolean; release: ReleaseInfo | null; canInstall: boolean; downloaded: boolean };
				this.canInstall = r.canInstall;
				release = r.available ? r.release : null;
				downloaded = r.downloaded;
			} else {
				const res = await fetch(`https://api.github.com/repos/${REPO}/releases/latest`, { headers: { Accept: 'application/vnd.github+json' } });
				if (!res.ok) throw new Error(`GitHub answered ${res.status}`);
				const gh = (await res.json()) as { tag_name: string; name: string; body: string; html_url: string; published_at: string };
				const version = gh.tag_name.replace(/^v/, '');
				if (newer(version, VERSION)) release = { version, name: gh.name, notes: gh.body ?? '', url: gh.html_url, publishedAt: gh.published_at };
			}
			this.lastChecked = new Date();
			this.release = release;
			if (!release) {
				this.phase = 'current';
				return;
			}
			if (manual) this.dismiss(false);
			this.phase = downloaded ? 'ready' : 'available';
			if (!downloaded && this.canInstall && prefs.value.downloadUpdates) await this.download();
		} catch (e) {
			this.phase = this.release ? 'error' : 'idle';
			this.error = (e as Error).message ?? String(e);
			if (manual && !this.release) this.phase = 'error';
		}
	}

	/** Downloads the release (desktop) and checks it; then it's ready to install. */
	async download() {
		const u = updater();
		if (!u || !this.release) return;
		this.phase = 'downloading';
		this.progress = 0;
		this.error = '';
		try {
			await u.DownloadUpdate();
			this.phase = 'ready';
		} catch (e) {
			this.phase = 'error';
			this.error = (e as Error).message ?? String(e);
		}
	}

	/** Installs the downloaded update and restarts TableF. */
	async install() {
		const u = updater();
		if (!u) return;
		this.phase = 'installing';
		try {
			await u.InstallUpdate(); // quits on success
		} catch (e) {
			this.phase = 'error';
			this.error = (e as Error).message ?? String(e);
		}
	}

	/** Hides the card for this version (or shows it again with false). */
	dismiss(hide = true) {
		this.dismissed = hide && this.release ? this.release.version : '';
		save(DISMISSED_KEY, this.dismissed);
	}
}

export const updates = new Updates();
