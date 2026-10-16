package dynamodb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The core generates standard SQL for browsing and editing. PartiQL for
// DynamoDB differs in three places, which this file bridges:
//
//   - paging: PartiQL has no LIMIT/OFFSET. Paginate appends a trailing
//     "-- tablef:limit N" comment; the driver strips it and stops reading after
//     N rows (the core then drops the first offset rows).
//   - totals: PartiQL has no COUNT(*). The exact shape
//     SELECT COUNT(*) FROM "t" [WHERE ...] is answered by the driver itself.
//   - inserts: INSERT INTO "t" ("a", "b") VALUES (?, ?) becomes
//     INSERT INTO "t" VALUE {'a': ?, 'b': ?}.

const limitMarker = "-- tablef:limit "

var limitRe = regexp.MustCompile(`(?s)\s*--\s*tablef:limit\s+(\d+)\s*$`)

// withLimit tags q so the driver returns at most n rows.
func withLimit(q string, n int) string {
	return q + "\n" + limitMarker + strconv.Itoa(n)
}

// stripLimit removes the paging tag; limit is 0 when there was none.
func stripLimit(q string) (string, int) {
	m := limitRe.FindStringSubmatchIndex(q)
	if m == nil {
		return q, 0
	}
	n, _ := strconv.Atoi(q[m[2]:m[3]])
	return q[:m[0]], n
}

// ident matches a double-quoted identifier ("a""b" escapes a quote) or a bare one.
const ident = `(?:"(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_.\-]*)`

var (
	countRe  = regexp.MustCompile(`(?is)^\s*SELECT\s+COUNT\s*\(\s*\*\s*\)\s+FROM\s+(` + ident + `)\s*(?:WHERE\s+(.*?))?\s*;?\s*$`)
	insertRe = regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+(` + ident + `)\s*\((.*)\)\s*VALUES\s*\((.*)\)\s*;?\s*$`)
	fromRe   = regexp.MustCompile(`(?is)\bFROM\s+(` + ident + `)`)
	selectRe = regexp.MustCompile(`(?is)^\s*SELECT\s+(.*?)\s+FROM\s`)
	intoRe   = regexp.MustCompile(`(?is)^\s*(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(` + ident + `)`)
)

// unquote turns "a""b" into a"b; bare identifiers are returned as is.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

// tableName returns the base table of a statement: a "t"."index" reference
// (querying a secondary index) yields "t".
func tableName(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, `"`) {
		// "t" or "t"."idx"
		end := 1
		for end < len(ref) {
			if ref[end] == '"' {
				if end+1 < len(ref) && ref[end+1] == '"' {
					end += 2
					continue
				}
				break
			}
			end++
		}
		return unquote(ref[:min(end+1, len(ref))])
	}
	if i := strings.IndexByte(ref, '.'); i > 0 {
		return ref[:i]
	}
	return ref
}

// countQuery recognizes SELECT COUNT(*) FROM t [WHERE cond].
func countQuery(q string) (table, where string, ok bool) {
	m := countRe.FindStringSubmatch(q)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}

// splitList splits a comma separated list, ignoring commas inside quotes,
// brackets, braces and parentheses.
func splitList(s string) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				if i+1 < len(s) && s[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" || len(out) > 0 {
		out = append(out, rest)
	}
	return out
}

// translateInsert rewrites the SQL INSERT shape into PartiQL's
// INSERT INTO t VALUE {...}. Statements of any other shape are returned as is.
func translateInsert(q string) (string, error) {
	m := insertRe.FindStringSubmatch(q)
	if m == nil {
		return q, nil
	}
	cols, vals := splitList(m[2]), splitList(m[3])
	if len(cols) == 0 || len(cols) != len(vals) {
		return "", fmt.Errorf("dynamodb: INSERT has %d columns but %d values", len(cols), len(vals))
	}
	pairs := make([]string, len(cols))
	for i, c := range cols {
		name := unquote(c)
		pairs[i] = "'" + strings.ReplaceAll(name, "'", "''") + "': " + vals[i]
	}
	return "INSERT INTO " + m[1] + " VALUE {" + strings.Join(pairs, ", ") + "}", nil
}

// projection returns the plain attribute names of SELECT a, "b" FROM ...;
// nil for SELECT * or anything more complex (paths, functions).
func projection(q string) []string {
	m := selectRe.FindStringSubmatch(q)
	if m == nil {
		return nil
	}
	list := strings.TrimSpace(m[1])
	if list == "*" {
		return nil
	}
	var out []string
	for _, p := range splitList(list) {
		if p == "" || strings.ContainsAny(p, "()[]*") {
			return nil
		}
		if !strings.HasPrefix(p, `"`) && strings.ContainsAny(p, ". ") {
			return nil
		}
		name := unquote(p)
		if strings.HasPrefix(p, `"`) && !strings.HasSuffix(p, `"`) {
			return nil
		}
		out = append(out, name)
	}
	return out
}

// statementTable names the table a SELECT/INSERT/UPDATE/DELETE works on.
func statementTable(q string) string {
	if m := intoRe.FindStringSubmatch(q); m != nil {
		return tableName(m[1])
	}
	if m := fromRe.FindStringSubmatch(q); m != nil {
		return tableName(m[1])
	}
	return ""
}
