// One API for both builds: Wails bindings on desktop, HTTP RPC in the browser.
import type {
	ChangeSet,
	Column,
	ConnectionConfig,
	QueryResult,
	Routine,
	SessionInfo,
	TestResult,
	Table,
	TableData,
	TableDataRequest,
	GroupRequest,
	GroupResult,
	Catalog,
	Principal,
	InfoField,
	SavedQuery,
	Index,
	RowPage,
	ForeignKey,
	HistoryEntry
} from './types';

type Bound = Record<string, (...args: unknown[]) => Promise<unknown>>;

declare global {
	interface Window {
		go?: { core?: { Service?: Bound }; main?: { Desktop?: Bound; Updater?: Bound } };
		/** Wails' window runtime, present in the desktop app. */
		runtime?: {
			WindowMinimise(): void;
			WindowToggleMaximise(): void;
			WindowIsMaximised(): Promise<boolean>;
			WindowIsFullscreen(): Promise<boolean>;
			WindowUnfullscreen(): void;
			Quit(): void;
			/** Subscribes to a backend event; returns the unsubscribe function. */
			EventsOn(name: string, cb: (...data: unknown[]) => void): () => void;
			/** Opens a URL in the system browser. */
			BrowserOpenURL(url: string): void;
		};
	}
}

export const isDesktop = typeof window !== 'undefined' && !!window.go?.core?.Service;

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/i.test(navigator.userAgent);

/** The desktop app on macOS: the header leaves room for the traffic lights. */
export const isMacDesktop = isDesktop && isMac;

/** Opens a link in the system browser (desktop) or a new tab (web). */
export function openExternal(url: string) {
	if (window.runtime?.BrowserOpenURL) window.runtime.BrowserOpenURL(url);
	else window.open(url, '_blank', 'noopener');
}

/** The desktop app on Windows/Linux: the window is frameless and the header draws its own controls. */
export const hasWindowControls = isDesktop && !isMac;

/** Label for the main modifier key. */
export const modLabel = isMac ? '⌘' : 'Ctrl';

/**
 * Label for shortcuts the browser keeps for itself on macOS (⌘T, ⌘W, ⌘1–9):
 * a web page never receives them, so there the app listens for ⌃ instead.
 */
export const tabModLabel = isMac && !isDesktop ? '⌃' : modLabel;

const TOKEN_KEY = 'tablef.token';

function token(): string {
	try {
		return localStorage.getItem(TOKEN_KEY) ?? '';
	} catch {
		return '';
	}
}

export function setToken(t: string) {
	try {
		localStorage.setItem(TOKEN_KEY, t);
	} catch {
		/* storage unavailable */
	}
}

export class UnauthorizedError extends Error {}

async function call<T>(method: string, ...args: unknown[]): Promise<T> {
	const svc = window.go?.core?.Service;
	if (svc) {
		try {
			return (await svc[method](...args)) as T;
		} catch (e) {
			throw new Error(typeof e === 'string' ? e : String((e as Error)?.message ?? e));
		}
	}
	const res = await fetch(`/api/rpc/${method}`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token()}` },
		body: JSON.stringify(args)
	});
	if (res.status === 401) throw new UnauthorizedError('unauthorized');
	const body = await res.json().catch(() => ({ error: res.statusText }));
	if (body.error) throw new Error(body.error);
	return body.result as T;
}

export async function webNeedsToken(): Promise<boolean> {
	if (isDesktop) return false;
	try {
		const info = await (await fetch('/api/info')).json();
		return !!info.auth && !token();
	} catch {
		return false;
	}
}

export const api = {
	listConnections: () => call<ConnectionConfig[]>('ListConnections'),
	/** Engine ids this build supports (some, like DuckDB, are optional). */
	engines: () => call<string[]>('Engines'),
	saveConnection: (c: ConnectionConfig) => call<ConnectionConfig>('SaveConnection', c),
	deleteConnection: (id: string) => call<void>('DeleteConnection', id),
	testConnection: (c: ConnectionConfig) => call<TestResult>('TestConnection', c),
	/** Records the SSH server's host key as trusted; returns its fingerprint. */
	trustHostKey: (c: ConnectionConfig) => call<string>('TrustHostKey', c),
	connect: (id: string) => call<SessionInfo>('Connect', id),
	disconnect: (id: string) => call<void>('Disconnect', id),
	useDatabase: (id: string, db: string) => call<SessionInfo>('UseDatabase', id, db),
	listTables: (id: string) => call<Table[]>('ListTables', id),
	listSchemas: (id: string) => call<string[]>('ListSchemas', id),
	createSchema: (id: string, name: string) => call<void>('CreateSchema', id, name),
	listRoutines: (id: string) => call<Routine[]>('ListRoutines', id),
	/** Tables, views, routines and schemas of any database on the server ('' = current). */
	getCatalog: (id: string, database: string) => call<Catalog>('GetCatalog', id, database),
	/** Users and roles of a database ('' = current). */
	listPrincipals: (id: string, database: string) => call<Principal[]>('ListPrincipals', id, database),
	listSavedQueries: () => call<SavedQuery[]>('ListSavedQueries'),
	saveQuery: (q: SavedQuery) => call<SavedQuery>('SaveQuery', q),
	deleteSavedQuery: (id: string) => call<void>('DeleteSavedQuery', id),
	getRelations: (id: string, schema: string, table: string) =>
		call<{ foreignKeys: ForeignKey[]; referencedBy: ForeignKey[] }>('GetRelations', id, schema, table),
	listRoles: (id: string) => call<string[]>('ListRoles', id),
	useRole: (id: string, role: string) => call<SessionInfo>('UseRole', id, role),
	connectAs: (id: string, user: string, password: string) => call<SessionInfo>('ConnectAs', id, user, password),
	listHistory: (connectionId: string, query: string, limit: number) => call<HistoryEntry[]>('ListHistory', connectionId, query, limit),
	clearHistory: (connectionId: string) => call<void>('ClearHistory', connectionId),
	fetchRows: (id: string, cursor: string, limit: number) => call<RowPage>('FetchRows', id, cursor, limit),
	closeCursor: (id: string, cursor: string) => call<void>('CloseCursor', id, cursor),
	getIndexes: (id: string, schema: string, table: string) => call<Index[]>('GetIndexes', id, schema, table),
	getTableInfo: (id: string, schema: string, table: string) => call<InfoField[]>('GetTableInfo', id, schema, table),
	getRoutineDefinition: (id: string, r: Routine) => call<string>('GetRoutineDefinition', id, r),
	getColumns: (id: string, schema: string, table: string) => call<Column[]>('GetColumns', id, schema, table),
	getTableData: (id: string, req: TableDataRequest) => call<TableData>('GetTableData', id, req),
	getGroups: (id: string, req: GroupRequest) => call<GroupResult>('GetGroups', id, req),
	executeQuery: (id: string, sql: string) => call<QueryResult[]>('ExecuteQuery', id, sql),
	cancelQuery: (id: string) => call<void>('CancelQuery', id),
	applyChanges: (id: string, cs: ChangeSet) => call<string[]>('ApplyChanges', id, cs),
	/** Desktop only: native file picker. Returns '' when unavailable or cancelled. */
	pickSQLiteFile: async (): Promise<string> => {
		const d = window.go?.main?.Desktop;
		return d ? ((await d.PickSQLiteFile()) as string) : '';
	},
	/** Saves text to a file: a native Save dialog on desktop, a download in the browser. */
	saveFile: async (name: string, content: string | Uint8Array, type = 'text/csv;charset=utf-8'): Promise<boolean> => {
		const d = window.go?.main?.Desktop;
		if (d) {
			if (typeof content === 'string') return (await d.SaveTextFile(name, content)) as boolean;
			let bin = '';
			for (let i = 0; i < content.length; i += 0x8000) bin += String.fromCharCode(...content.subarray(i, i + 0x8000));
			return (await d.SaveBinaryFile(name, btoa(bin))) as boolean;
		}
		const url = URL.createObjectURL(new Blob([content as BlobPart], { type }));
		const a = Object.assign(document.createElement('a'), { href: url, download: name });
		a.click();
		setTimeout(() => URL.revokeObjectURL(url), 1000);
		return true;
	},
	/** Desktop only: native picker for an SSH private key. */
	pickKeyFile: async (): Promise<string> => {
		const d = window.go?.main?.Desktop;
		return d ? ((await d.PickKeyFile()) as string) : '';
	}
};

/** True when the event target sits inside an element matching selector. */
export function targetIn(e: Event, selector: string): boolean {
	return e.target instanceof Element && !!e.target.closest(selector);
}
