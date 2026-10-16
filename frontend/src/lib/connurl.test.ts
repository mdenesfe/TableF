import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatURL, parseURL } from './connurl.ts';
import type { ConnectionConfig } from './types.ts';

test('parses common connection URLs', () => {
	assert.deepEqual(parseURL('postgres://u:p%40ss@db.example.com:6543/app?sslmode=require'), {
		driver: 'postgres',
		user: 'u',
		password: 'p@ss',
		host: 'db.example.com',
		port: 6543,
		database: 'app',
		sslMode: 'require'
	});
	assert.equal(parseURL('mysql://root@localhost/shop')?.port, 3306);
	assert.equal(parseURL('sqlserver://sa@localhost/master')?.driver, 'mssql');
	assert.equal(parseURL('redis://localhost:6379/0')?.driver, 'redis');
	assert.deepEqual(parseURL('mongodb+srv://u:p@cluster.mongodb.net/db'), { driver: 'mongodb', options: { uri: 'mongodb+srv://u:p@cluster.mongodb.net/db' } });
	assert.equal(parseURL('not a url'), null);
});

test('parses the SSH form', () => {
	const c = parseURL('postgres+ssh://deploy@bastion:2222/app:secret@10.0.0.5:5432/main');
	assert.equal(c?.sshEnabled, true);
	assert.equal(c?.sshHost, 'bastion');
	assert.equal(c?.sshPort, 2222);
	assert.equal(c?.host, '10.0.0.5');
});

test('formatURL round-trips and hides the password by default', () => {
	const c = { ...parseURL('postgres://u:secret@h:5433/db')!, sshEnabled: false } as ConnectionConfig;
	assert.equal(formatURL(c), 'postgres://u@h:5433/db');
	assert.equal(formatURL(c, true), 'postgres://u:secret@h:5433/db');
});
