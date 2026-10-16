// CodeMirror SQL dialect for an engine.
import { Cassandra, MSSQL, MariaSQL, MySQL, PLSQL, PostgreSQL, SQLite, StandardSQL, type SQLDialect } from '@codemirror/lang-sql';
import { familyOf } from './engines.ts';

const byFamily: Record<string, SQLDialect> = {
	postgres: PostgreSQL,
	mysql: MySQL,
	sqlite: SQLite,
	duckdb: PostgreSQL,
	mssql: MSSQL,
	oracle: PLSQL,
	cassandra: Cassandra
};

export const dialectOf = (engine: string): SQLDialect => (engine === 'mariadb' ? MariaSQL : (byFamily[familyOf(engine)] ?? StandardSQL));
