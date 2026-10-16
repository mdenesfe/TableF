// In-app replacements for window.confirm / window.prompt, rendered by Dialog.svelte.

export interface DialogRequest {
	kind: 'confirm' | 'prompt';
	title: string;
	message?: string;
	/** Monospace detail under the message, e.g. the statements about to run. */
	detail?: string;
	confirmLabel?: string;
	cancelLabel?: string;
	/** Paints the confirm button as destructive. */
	danger?: boolean;
	placeholder?: string;
	value?: string;
	resolve: (v: string | boolean | null) => void;
}

class Dialogs {
	current = $state<DialogRequest | null>(null);

	confirm(opts: Omit<DialogRequest, 'kind' | 'resolve'>): Promise<boolean> {
		return new Promise((resolve) => this.open({ ...opts, kind: 'confirm', resolve: (v) => resolve(v === true) }));
	}

	prompt(opts: Omit<DialogRequest, 'kind' | 'resolve'>): Promise<string | null> {
		return new Promise((resolve) => this.open({ ...opts, kind: 'prompt', resolve: (v) => resolve(typeof v === 'string' ? v : null) }));
	}

	private open(req: DialogRequest) {
		// A new dialog cancels whatever was open.
		this.current?.resolve(null);
		this.current = req;
	}

	close(value: string | boolean | null) {
		const c = this.current;
		this.current = null;
		c?.resolve(value);
	}
}

export const dialogs = new Dialogs();
