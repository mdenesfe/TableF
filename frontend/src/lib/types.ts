import type { EngineId } from './engines.ts';

export type DriverName = EngineId;

export interface ConnectionConfig {
	id: string;
	name: string;
	driver: DriverName;
	host: string;
	port: number;
	user: string;
	password: string;
	database: string;
	sslMode: string;
	filePath: string;
	color: string;
	sshEnabled: boolean;
	sshHost: string;
	sshPort: number;
	sshUser: string;
	sshAuth: 'password' | 'key' | 'agent';
	sshPassword: string;
	sshKeyFile: string;
	sshPassphrase: string;
	/** Free-form labels, e.g. local, staging, production. */
	tags: string[];
	/** Engine-specific settings (see Engine.fields in engines.ts). */
	options: Record<string, string>;
	/**
	 * Secrets the server holds but never sends back, e.g. "password",
	 * "sshPassword", "options.apiToken". Leaving such a field empty keeps it.
	 */
	secrets?: string[];
}

export interface TestResult {
	version: string;
	latencyMs: number;
}

export interface SessionInfo {
	id: string;
	driver: DriverName;
	database: string;
	databases: string[];
	version: string;
	features: Features;
}

/** What the connected engine supports; mirrors driver.Features in Go. */
export interface Features {
	family: string;
	schemas: boolean;
	createSchema: boolean;
	routines: boolean;
	foreignKeys: boolean;
	groups: boolean;
	edit: boolean;
	transactions: boolean;
}

export interface Table {
	schema: string;
	name: string;
	type: 'table' | 'view';
}

export interface Routine {
	id: string;
	schema: string;
	name: string;
	kind: 'function' | 'procedure';
	arguments: string;
	returnType: string;
}

export interface Column {
	name: string;
	type: string;
	nullable: boolean;
	default: string | null;
	primaryKey: boolean;
}

export type Cell = string | number | boolean | null;

export interface QueryResult {
	statement: string;
	columns: string[];
	columnTypes: string[];
	rows: Cell[][];
	rowsAffected: number;
	durationMs: number;
	truncated: boolean;
	error?: string;
}

export interface TableDataRequest {
	schema: string;
	table: string;
	limit: number;
	offset: number;
	orderBy: string;
	orderDir: 'asc' | 'desc' | '';
	where: string;
	orderSql?: string;
	match?: ColumnMatch[];
}

export interface ColumnMatch {
	column: string;
	value: Cell;
}

export interface GroupRequest {
	schema: string;
	table: string;
	where: string;
	match: ColumnMatch[];
	column: string;
	limit?: number;
}

export interface GroupResult {
	groups: { value: Cell; count: number }[];
	total: number;
	rows: number;
}

export interface ForeignKey {
	name: string;
	schema: string;
	table: string;
	column: string;
	refSchema: string;
	refTable: string;
	refColumn: string;
}

export interface TableData {
	columns: Column[];
	rows: Cell[][];
	total: number;
	sql: string;
	durationMs: number;
	foreignKeys: ForeignKey[];
	referencedBy: ForeignKey[];
}

export interface RowUpdate {
	keys: Record<string, Cell>;
	values: Record<string, Cell>;
}

export interface ChangeSet {
	schema: string;
	table: string;
	updates: RowUpdate[];
	inserts: Record<string, Cell>[];
	deletes: Record<string, Cell>[];
}
