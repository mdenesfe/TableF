// Connection URLs in the formats developers already use:
//   postgres://user:pass@host:5432/db?sslmode=require   (also postgresql://)
//   mysql://user:pass@host:3306/db                       (also mariadb://, user:pass@tcp(host:3306)/db)
//   sqlite:///abs/path.db   sqlite:rel.db   file:path.db
// and the TablePlus-style SSH form:
//   postgres+ssh://sshuser@bastion:22/user:pass@10.0.0.5:5432/db
import { engine } from './engines.ts';
import type { ConnectionConfig, DriverName } from './types';

const SCHEMES: Record<string, DriverName> = {
	postgres: 'postgres',
	postgresql: 'postgres',
	pg: 'postgres',
	mysql: 'mysql',
	mariadb: 'mariadb',
	singlestore: 'mariadb',
	sqlite: 'sqlite',
	sqlite3: 'sqlite',
	file: 'sqlite',
	duckdb: 'duckdb',
	redshift: 'redshift',
	cockroach: 'cockroach',
	cockroachdb: 'cockroach',
	greenplum: 'greenplum',
	sqlserver: 'mssql',
	mssql: 'mssql',
	oracle: 'oracle',
	clickhouse: 'clickhouse',
	vertica: 'vertica',
	cassandra: 'cassandra',
	mongodb: 'mongodb',
	redis: 'redis',
	rediss: 'redis',
	elasticsearch: 'elasticsearch',
	libsql: 'libsql'
};

const defaultPort = (d: DriverName) => engine(d).port ?? 0;
// Schemes written back by formatURL.
const SCHEME_OF: Partial<Record<DriverName, string>> = { mariadb: 'mariadb', mssql: 'sqlserver' };

export type ParsedURL = Partial<ConnectionConfig> & { driver: DriverName };

const dec = (s: string) => {
	try {
		return decodeURIComponent(s);
	} catch {
		return s;
	}
};

/** userinfo@host:port, any part optional. */
function authority(s: string) {
	const at = s.lastIndexOf('@');
	const info = at >= 0 ? s.slice(0, at) : '';
	const hostport = at >= 0 ? s.slice(at + 1) : s;
	const colon = info.indexOf(':');
	const user = dec(colon >= 0 ? info.slice(0, colon) : info);
	const password = colon >= 0 ? dec(info.slice(colon + 1)) : undefined;
	const m = /^(\[[^\]]+\]|[^:]*)(?::(\d+))?$/.exec(hostport);
	return {
		user,
		password,
		host: (m?.[1] ?? hostport).replace(/^\[|\]$/g, ''),
		port: m?.[2] ? Number(m[2]) : undefined
	};
}

export function looksLikeURL(text: string): boolean {
	return parseURL(text) !== null;
}

export function parseURL(input: string): ParsedURL | null {
	const text = input.trim();

	// Go MySQL DSN: user:pass@tcp(host:3306)/db?params
	const dsn = /^([^@/]*)@tcp\(([^)]*)\)\/([^?]*)(?:\?(.*))?$/.exec(text);
	if (dsn) {
		const a = authority(`${dsn[1]}@${dsn[2]}`);
		return { driver: 'mysql', user: a.user, password: a.password, host: a.host, port: a.port ?? 3306, database: dec(dsn[3]) };
	}

	const m = /^([a-z0-9]+)(\+srv)?(\+ssh)?:(.*)$/i.exec(text);
	if (!m) return null;
	const driver = SCHEMES[m[1].toLowerCase()];
	if (!driver) return null;
	let rest = m[4];
	const sshForm = !!m[3];

	// Atlas-style SRV URIs and libsql URLs are kept whole; their engines read them as is.
	if (m[2] && driver === 'mongodb') return { driver, options: { uri: text } };
	if (driver === 'libsql') return { driver, options: { url: text.replace(/\?authToken=([^&]*)/, '') }, ...tokenOf(text) };

	if (engine(driver).kind === 'file') {
		const path = dec(
			rest
				.replace(/^\/\/(?=\/)/, '')
				.replace(/^\/\/localhost(?=\/)/, '')
				.replace(/\?.*$/, '')
		);
		if (!path || path === '//') return null;
		return { driver, filePath: path };
	}

	if (!rest.startsWith('//')) return null;
	rest = rest.slice(2);
	const [main, query = ''] = rest.split(/\?(.*)/s);
	const params = new URLSearchParams(query);
	const out: ParsedURL = { driver };

	let dbPart = main;
	if (sshForm) {
		// sshuser@sshhost:port/user:pass@dbhost:port/db
		const slash = main.indexOf('/');
		if (slash < 0) return null;
		const ssh = authority(main.slice(0, slash));
		Object.assign(out, {
			sshEnabled: true,
			sshHost: ssh.host,
			sshPort: ssh.port ?? 22,
			sshUser: ssh.user,
			...(ssh.password !== undefined ? { sshAuth: 'password' as const, sshPassword: ssh.password } : {})
		});
		dbPart = main.slice(slash + 1);
	}

	const slash = dbPart.indexOf('/');
	const auth = authority(slash >= 0 ? dbPart.slice(0, slash) : dbPart);
	Object.assign(out, {
		user: auth.user,
		host: auth.host || '127.0.0.1',
		port: auth.port ?? defaultPort(driver),
		database: slash >= 0 ? dec(dbPart.slice(slash + 1)) : ''
	});
	if (auth.password !== undefined) out.password = auth.password;

	if (m[1].toLowerCase() === 'rediss') out.options = { tls: 'true' };
	const ssl = params.get('sslmode') ?? params.get('ssl-mode') ?? params.get('tls');
	if (ssl) {
		const v = ssl.toLowerCase();
		out.sslMode = v === 'true' || v === 'required' ? 'require' : v === 'false' || v === 'disabled' ? 'disable' : v.replace('_', '-');
	}
	return out;
}

/** Formats a connection as a URL. The password is only included when asked. */
export function formatURL(c: ConnectionConfig, withPassword = false): string {
	const enc = encodeURIComponent;
	const e = engine(c.driver);
	if (e.kind === 'file') return c.filePath ? `${c.driver}://${c.filePath.startsWith('/') ? '' : '/'}${c.filePath}` : '';
	if (c.driver === 'mongodb' && c.options?.uri) return withPassword ? c.options.uri : c.options.uri.replace(/:\/\/([^:@/]+):[^@/]*@/, '://$1@');
	if (c.driver === 'libsql') return c.options?.url ?? '';
	if (e.kind !== 'server') return '';
	const scheme = SCHEME_OF[c.driver] ?? c.driver;
	const pw = withPassword && c.password ? `:${enc(c.password)}` : '';
	const user = c.user ? `${enc(c.user)}${pw}@` : '';
	const host = c.host.includes(':') ? `[${c.host}]` : c.host;
	const port = c.port && c.port !== defaultPort(c.driver) ? `:${c.port}` : '';
	const db = c.database ? `/${enc(c.database)}` : '';
	const q = c.sslMode && c.sslMode !== 'prefer' ? `?sslmode=${c.sslMode}` : '';
	if (c.sshEnabled && c.sshHost) {
		const sshPw = withPassword && c.sshAuth === 'password' && c.sshPassword ? `:${enc(c.sshPassword)}` : '';
		const sshUser = c.sshUser ? `${enc(c.sshUser)}${sshPw}@` : '';
		const sshPort = c.sshPort && c.sshPort !== 22 ? `:${c.sshPort}` : '';
		return `${scheme}+ssh://${sshUser}${c.sshHost}${sshPort}/${user}${host}${port}${db || '/'}${q}`;
	}
	return `${scheme}://${user}${host}${port}${db}${q}`;
}

function tokenOf(text: string): { options: Record<string, string> } | object {
	const t = /[?&]authToken=([^&]*)/.exec(text)?.[1];
	return t ? { options: { url: text.replace(/\?.*$/, ''), authToken: dec(t) } } : {};
}
