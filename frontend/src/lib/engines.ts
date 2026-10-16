// Every database TableF can connect to: how it is labelled, drawn and configured.
import {
	siApachecassandra,
	siClickhouse,
	siCloudflare,
	siCockroachlabs,
	siDuckdb,
	siElasticsearch,
	siGooglebigquery,
	siMariadb,
	siMongodb,
	siMysql,
	siPostgresql,
	siRedis,
	siSnowflake,
	siSqlite,
	siTurso
} from 'simple-icons';

export type EngineId =
	| 'postgres'
	| 'redshift'
	| 'mysql'
	| 'mariadb'
	| 'mssql'
	| 'cassandra'
	| 'clickhouse'
	| 'bigquery'
	| 'dynamodb'
	| 'elasticsearch'
	| 'libsql'
	| 'd1'
	| 'mongodb'
	| 'snowflake'
	| 'redis'
	| 'sqlite'
	| 'duckdb'
	| 'oracle'
	| 'cockroach'
	| 'greenplum'
	| 'vertica';

/** How the connection is reached, which decides the base fields of the form. */
export type EngineKind = 'server' | 'file' | 'cloud';

/** An extra form field. `option` fields are stored in config.options[key]. */
export interface EngineField {
	key: string;
	label: string;
	placeholder?: string;
	type?: 'text' | 'password' | 'select' | 'toggle' | 'file';
	choices?: string[];
	required?: boolean;
	/** Half-width field, paired with the next half field. */
	half?: boolean;
}

export interface Engine {
	id: EngineId;
	label: string;
	kind: EngineKind;
	port?: number;
	/** Official mark (Simple Icons) — engines without one get a two-letter monogram. */
	icon?: { path: string; hex: string };
	mono: string;
	/** Brand color for the logo box. */
	color: string;
	/** Label of the "database" field for server engines, or false to hide it. */
	database?: string | false;
	/** Show the SSL mode select. */
	ssl?: boolean;
	/** Engine-specific fields, shown under the base ones. */
	fields?: EngineField[];
	/** File-picker placeholder for file engines. */
	file?: string;
	/** Cloud engines that still sign in with a user and password. */
	auth?: boolean;
}

const pg = (id: EngineId, label: string, port: number, mono: string, color: string, icon?: Engine['icon']): Engine => ({
	id,
	label,
	kind: 'server',
	port,
	mono,
	color,
	icon,
	ssl: true
});

export const engines: Engine[] = [
	pg('postgres', 'PostgreSQL', 5432, 'Pg', '#336791', siPostgresql),
	pg('redshift', 'Amazon Redshift', 5439, 'Rs', '#2f5b9b'),
	{ id: 'mysql', label: 'MySQL', kind: 'server', port: 3306, mono: 'Ms', color: '#e48e00', icon: siMysql, ssl: true },
	{ id: 'mariadb', label: 'MariaDB & SingleStore', kind: 'server', port: 3306, mono: 'Ma', color: '#3d8f9b', icon: siMariadb, ssl: true },
	{
		id: 'mssql',
		label: 'Microsoft SQL Server',
		kind: 'server',
		port: 1433,
		mono: 'Ss',
		color: '#a1a9b4',
		ssl: true,
		fields: [{ key: 'trustServerCertificate', label: 'Trust server certificate', type: 'toggle' }]
	},
	{
		id: 'cassandra',
		label: 'Cassandra',
		kind: 'server',
		port: 9042,
		mono: 'Cs',
		color: '#2b2b2b',
		icon: siApachecassandra,
		database: 'Keyspace',
		fields: [{ key: 'datacenter', label: 'Datacenter', placeholder: 'datacenter1' }]
	},
	{
		id: 'clickhouse',
		label: 'ClickHouse',
		kind: 'server',
		port: 9000,
		mono: 'Ch',
		color: '#d4b11c',
		icon: siClickhouse,
		fields: [{ key: 'secure', label: 'Use TLS', type: 'toggle' }]
	},
	{
		id: 'bigquery',
		label: 'BigQuery',
		kind: 'cloud',
		mono: 'Bq',
		color: '#4f7bdc',
		icon: siGooglebigquery,
		database: 'Default dataset',
		fields: [
			{ key: 'projectId', label: 'Project ID', placeholder: 'my-project', required: true, half: true },
			{ key: 'location', label: 'Location', placeholder: 'US', half: true },
			{ key: 'credentialsFile', label: 'Service account key', placeholder: '/path/to/key.json (empty = default credentials)', type: 'file' }
		]
	},
	{
		id: 'dynamodb',
		label: 'DynamoDB',
		kind: 'cloud',
		mono: 'Dn',
		color: '#2a4f8f',
		database: false,
		fields: [
			{ key: 'region', label: 'Region', placeholder: 'eu-central-1', required: true, half: true },
			{ key: 'endpoint', label: 'Endpoint', placeholder: 'optional, e.g. http://localhost:8000', half: true },
			{ key: 'accessKeyId', label: 'Access key ID', half: true },
			{ key: 'secretAccessKey', label: 'Secret access key', type: 'password', half: true }
		]
	},
	{
		id: 'elasticsearch',
		label: 'Elasticsearch',
		kind: 'server',
		port: 9200,
		mono: 'Es',
		color: '#3ab7b0',
		icon: siElasticsearch,
		database: false,
		fields: [
			{ key: 'scheme', label: 'Protocol', type: 'select', choices: ['http', 'https'], half: true },
			{ key: 'apiKey', label: 'API key', type: 'password', placeholder: 'optional', half: true }
		]
	},
	{
		id: 'libsql',
		label: 'LibSQL',
		kind: 'cloud',
		mono: 'Ls',
		color: '#4aa27e',
		icon: siTurso,
		database: false,
		fields: [
			{ key: 'url', label: 'Database URL', placeholder: 'libsql://my-db.turso.io', required: true },
			{ key: 'authToken', label: 'Auth token', type: 'password' }
		]
	},
	{
		id: 'd1',
		label: 'Cloudflare D1',
		kind: 'cloud',
		mono: 'D1',
		color: '#de8a4a',
		icon: siCloudflare,
		database: false,
		fields: [
			{ key: 'accountId', label: 'Account ID', required: true, half: true },
			{ key: 'databaseId', label: 'Database ID', required: true, half: true },
			{ key: 'apiToken', label: 'API token', type: 'password', required: true }
		]
	},
	{
		id: 'mongodb',
		label: 'MongoDB',
		kind: 'server',
		port: 27017,
		mono: 'Mg',
		color: '#2f6b46',
		icon: siMongodb,
		fields: [
			{ key: 'uri', label: 'Connection string', placeholder: 'optional, mongodb+srv://… (overrides the fields above)' },
			{ key: 'authSource', label: 'Auth database', placeholder: 'admin', half: true },
			{ key: 'tls', label: 'Use TLS', type: 'toggle', half: true }
		]
	},
	{
		id: 'snowflake',
		label: 'Snowflake',
		kind: 'cloud',
		mono: 'Nf',
		color: '#4aa8de',
		icon: siSnowflake,
		auth: true,
		database: 'Database',
		fields: [
			{ key: 'account', label: 'Account', placeholder: 'xy12345.eu-central-1', required: true },
			{ key: 'warehouse', label: 'Warehouse', half: true },
			{ key: 'role', label: 'Role', half: true },
			{ key: 'schema', label: 'Schema', placeholder: 'PUBLIC', half: true }
		]
	},
	{
		id: 'redis',
		label: 'Redis',
		kind: 'server',
		port: 6379,
		mono: 'Re',
		color: '#8f2a1b',
		icon: siRedis,
		database: 'DB index',
		fields: [{ key: 'tls', label: 'Use TLS', type: 'toggle' }]
	},
	{ id: 'sqlite', label: 'SQLite', kind: 'file', mono: 'Sl', color: '#7a2fe0', icon: siSqlite, file: '/path/to/database.db' },
	{ id: 'duckdb', label: 'DuckDB', kind: 'file', mono: 'Du', color: '#111111', icon: siDuckdb, file: '/path/to/file.duckdb (empty = in-memory)' },
	{ id: 'oracle', label: 'Oracle', kind: 'server', port: 1521, mono: 'Oc', color: '#d9342b', database: 'Service name' },
	pg('cockroach', 'Cockroach', 26257, 'Cr', '#5aa832', siCockroachlabs),
	pg('greenplum', 'Greenplum', 5432, 'Gp', '#8bb03a'),
	{ id: 'vertica', label: 'Vertica', kind: 'server', port: 5433, mono: 'Ve', color: '#1c2f4d' }
];

const byId = new Map(engines.map((e) => [e.id, e]));

/** The engine for an id; unknown ids fall back to PostgreSQL. */
export const engine = (id: string): Engine => byId.get(id as EngineId) ?? engines[0];

/** Engines reached over the network can tunnel through SSH. */
export const canTunnel = (id: string) => engine(id).kind === 'server';

/**
 * The query language an engine speaks, matching driver.Features.Family on the
 * server; used before connecting and for editor highlighting and formatting.
 */
const families: Partial<Record<EngineId, string>> = {
	redshift: 'postgres',
	cockroach: 'postgres',
	greenplum: 'postgres',
	mariadb: 'mysql',
	libsql: 'sqlite',
	d1: 'sqlite',
	dynamodb: 'partiql'
};
export const familyOf = (id: string): string => families[id as EngineId] ?? id;

/** Engines whose editor takes native commands instead of SQL. */
export const isSQL = (id: string) => !['mongodb', 'redis'].includes(familyOf(id));

/** sql-formatter language per family ('sql' is its generic dialect). */
const formatters: Record<string, string> = {
	postgres: 'postgresql',
	mysql: 'mysql',
	sqlite: 'sqlite',
	mssql: 'tsql',
	oracle: 'plsql',
	bigquery: 'bigquery',
	snowflake: 'snowflake',
	duckdb: 'duckdb',
	clickhouse: 'clickhouse',
	redshift: 'redshift'
};
export const formatterOf = (id: string) => (id === 'redshift' ? 'redshift' : (formatters[familyOf(id)] ?? 'sql'));
