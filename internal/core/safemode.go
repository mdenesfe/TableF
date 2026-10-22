package core

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Read-only connections refuse anything that could change data or schema.
// The check errs on the side of refusing: a statement passes only when it
// starts with a reading keyword and contains no writing keyword anywhere
// (outside strings and comments), so CTEs that write, EXPLAIN ANALYZE of a
// DELETE or SELECT ... INTO are caught. PostgreSQL and MySQL connections are
// also opened read-only on the server (see driver.Config.ReadOnly).

// readKeywords may start a read-only statement.
var readKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "VALUES": true, "TABLE": true, "SHOW": true,
	"DESCRIBE": true, "DESC": true, "EXPLAIN": true, "PRAGMA": true, "USE": true,
}

// writeWords may not appear anywhere in a read-only statement. Statements
// that start with any other keyword are refused already, so this only has to
// catch what can write from inside a reading statement: data-changing CTEs,
// EXPLAIN ANALYZE of a write, SELECT ... INTO and procedure calls. Words that
// are also common function or column names (replace, comment, set) stay out.
var writeWords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "UPSERT": true,
	"INTO": true, "CREATE": true, "DROP": true, "ALTER": true, "TRUNCATE": true,
	"GRANT": true, "REVOKE": true, "CALL": true, "EXECUTE": true,
}

// errReadOnly explains why a statement was refused.
func errReadOnly(stmt string) error {
	first := strings.TrimSpace(stmt)
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if len(first) > 80 {
		first = first[:80] + "…"
	}
	return fmt.Errorf("this connection is read-only: %q could change data. Switch the safe mode (the lock in the header) to run it", first)
}

// sqlReadOnly reports whether a SQL statement only reads.
func sqlReadOnly(stmt string) bool {
	if !readKeywords[firstKeyword(stmt)] {
		return false
	}
	// PRAGMA name = value changes settings; PRAGMA name reads them.
	if firstKeyword(stmt) == "PRAGMA" && strings.Contains(stmt, "=") {
		return false
	}
	ok := true
	eachWord(stmt, func(w string) bool {
		if writeWords[strings.ToUpper(w)] {
			ok = false
		}
		return ok
	})
	return ok
}

// redisReads are the Redis commands a read-only connection may run.
var redisReads = map[string]bool{
	"GET": true, "MGET": true, "GETRANGE": true, "STRLEN": true, "EXISTS": true, "TYPE": true,
	"TTL": true, "PTTL": true, "KEYS": true, "SCAN": true, "RANDOMKEY": true, "DBSIZE": true,
	"HGET": true, "HMGET": true, "HGETALL": true, "HKEYS": true, "HVALS": true, "HLEN": true,
	"HEXISTS": true, "HSCAN": true, "HSTRLEN": true, "LRANGE": true, "LLEN": true, "LINDEX": true,
	"LPOS": true, "SMEMBERS": true, "SCARD": true, "SISMEMBER": true, "SMISMEMBER": true,
	"SRANDMEMBER": true, "SSCAN": true, "SINTER": true, "SUNION": true, "SDIFF": true,
	"ZRANGE": true, "ZRANGEBYSCORE": true, "ZRANGEBYLEX": true, "ZREVRANGE": true,
	"ZREVRANGEBYSCORE": true, "ZCARD": true, "ZSCORE": true, "ZMSCORE": true, "ZRANK": true,
	"ZREVRANK": true, "ZCOUNT": true, "ZLEXCOUNT": true, "ZSCAN": true, "XRANGE": true,
	"XREVRANGE": true, "XLEN": true, "XINFO": true, "BITCOUNT": true, "BITPOS": true,
	"GETBIT": true, "PFCOUNT": true, "GEOPOS": true, "GEODIST": true, "GEOHASH": true,
	"GEOSEARCH": true, "INFO": true, "PING": true, "ECHO": true, "TIME": true, "MEMORY": true,
	"OBJECT": true, "DUMP": true, "LASTSAVE": true, "SELECT": true,
}

// mongoWrites are mongo shell methods that change data or settings.
var mongoWrites = regexp.MustCompile(`(?i)\.\s*(insert\w*|update\w*|replace\w*|delete\w*|remove\w*|drop\w*|rename\w*|create\w*|bulkWrite|findOneAnd\w*|findAndModify|save|runCommand|adminCommand|eval|grant\w*|revoke\w*)\s*\(|\$(out|merge)\b`)

// storeReadOnly reports whether a command of a non-SQL engine only reads.
func storeReadOnly(family, cmd string) bool {
	switch family {
	case "redis":
		f := strings.Fields(cmd)
		return len(f) > 0 && redisReads[strings.ToUpper(f[0])]
	case "mongodb":
		return !mongoWrites.MatchString(cmd)
	default:
		return sqlReadOnly(cmd)
	}
}

// checkReadOnly refuses a script on a read-only connection if any of its
// statements could change something; nothing runs then.
func checkReadOnly(sess *session, stmts []string) error {
	if !sess.cfg.ReadOnly() {
		return nil
	}
	for _, st := range stmts {
		ok := false
		if sess.store != nil {
			ok = storeReadOnly(sess.feat.Family, st)
		} else {
			ok = sqlReadOnly(st)
		}
		if !ok {
			return errReadOnly(st)
		}
	}
	return nil
}

var errReadOnlyEdit = errors.New("this connection is read-only: editing is turned off. Switch the safe mode (the lock in the header) to edit data")
