// User preferences, stored per device and applied to the document right away.
import { load, remove, save } from './storage.ts';

export type Theme = 'system' | 'light' | 'dark';
export type Density = 'compact' | 'default' | 'comfortable';
export type KeywordCase = 'upper' | 'lower' | 'preserve';

export interface Prefs {
	theme: Theme;
	/** Interface zoom: 0.9 compact, 1 default, 1.1 large. */
	scale: number;
	editorFontSize: number;
	pageSize: number;
	/** How NULL is shown in grids. */
	nullText: string;
	/** Ask before DELETE/UPDATE without WHERE, DROP and TRUNCATE in the query editor. */
	confirmDestructive: boolean;
	/** Ask before throwing away unsaved table edits. */
	confirmDiscard: boolean;
	/** Turns off interface animations, on top of the system setting. */
	reduceMotion: boolean;
	/** The animated 1/0 field behind the header. */
	headerAnimation: boolean;
	/** Grid row height. */
	density: Density;
	/** Alternate row shading in grids. */
	zebra: boolean;
	editorWrap: boolean;
	editorLineNumbers: boolean;
	editorTabSize: number;
	/** Keyword case used by Format SQL and completions. */
	keywordCase: KeywordCase;
	/** Reopen the last connection, tabs and layout on launch. */
	restoreSession: boolean;
}

export const rowHeights: Record<Density, number> = { compact: 24, default: 28, comfortable: 34 };

const KEY = 'tablef.prefs';

export const defaults: Prefs = {
	theme: 'system',
	scale: 1,
	editorFontSize: 13,
	pageSize: 300,
	nullText: 'NULL',
	confirmDestructive: true,
	confirmDiscard: true,
	reduceMotion: false,
	headerAnimation: true,
	density: 'default',
	zebra: true,
	editorWrap: false,
	editorLineNumbers: true,
	editorTabSize: 2,
	keywordCase: 'upper',
	restoreSession: true
};

class PrefsStore {
	value = $state<Prefs>({ ...defaults, ...load<Partial<Prefs>>(KEY, {}) });

	set<K extends keyof Prefs>(key: K, v: Prefs[K]) {
		this.value[key] = v;
		save(KEY, this.value);
		this.apply();
	}

	reset() {
		this.value = { ...defaults };
		remove(KEY);
		this.apply();
	}

	apply() {
		if (typeof document === 'undefined') return;
		const root = document.documentElement;
		if (this.value.theme === 'system') delete root.dataset.theme;
		else root.dataset.theme = this.value.theme;
		root.style.zoom = this.value.scale === 1 ? '' : String(this.value.scale);
		root.style.setProperty('--editor-fs', `${this.value.editorFontSize}px`);
		if (this.value.reduceMotion) root.dataset.motion = 'reduced';
		else delete root.dataset.motion;
	}

	/** True when animations should not run (preference or system setting). */
	get still(): boolean {
		return this.value.reduceMotion || (typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches);
	}
}

export const prefs = new PrefsStore();
