package core

import "strings"

// backslashEscapes reports whether a backslash escapes the next character
// inside quote q in the given dialect family. Standard SQL (PostgreSQL,
// SQLite, SQL Server, Oracle, ...) has no backslash escapes, so 'C:\' is a
// complete string there; MySQL, ClickHouse and BigQuery treat \' as a quote
// inside both string quotes, Snowflake inside single quotes only.
func backslashEscapes(family string, q byte) bool {
	switch family {
	case "mysql", "clickhouse", "bigquery":
		return q == '\'' || q == '"'
	case "snowflake":
		return q == '\''
	}
	return false
}

// SplitStatements splits a SQL script on top-level semicolons. It understands
// quoted strings/identifiers, line and block comments and Postgres dollar
// quotes. family is the engine's dialect (driver.Features.Family); it decides
// whether backslashes escape quotes. "" means standard SQL.
func SplitStatements(script, family string) []string {
	var (
		out   []string
		start int
		i     int
		n     = len(script)
	)
	flush := func(end int) {
		if s := strings.TrimSpace(script[start:end]); s != "" && !onlyComments(s) {
			out = append(out, s)
		}
	}
	for i < n {
		c := script[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(script, i, c, backslashEscapes(family, c))
		case c == '-' && i+1 < n && script[i+1] == '-':
			for i < n && script[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && script[i+1] == '*':
			end := strings.Index(script[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += end + 4
			}
		case c == '$':
			if tag, ok := dollarTag(script[i:]); ok {
				end := strings.Index(script[i+len(tag):], tag)
				if end < 0 {
					i = n
				} else {
					i += len(tag) + end + len(tag)
				}
			} else {
				i++
			}
		case c == ';':
			flush(i)
			i++
			start = i
		default:
			i++
		}
	}
	flush(n)
	return out
}

// skipQuoted returns the index just past the quoted run starting at s[i].
// A doubled quote is always an escaped quote; a backslash is one when
// backslash is true.
func skipQuoted(s string, i int, q byte, backslash bool) int {
	i++
	for i < len(s) {
		if backslash && s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == q {
			if i+1 < len(s) && s[i+1] == q { // doubled quote escape
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

// dollarTag matches $$ or $tag$ at the start of s.
func dollarTag(s string) (string, bool) {
	for j := 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[:j+1], true
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 1 && c >= '0' && c <= '9') {
			return "", false
		}
	}
	return "", false
}

// onlyComments reports whether s holds nothing but -- and /* */ comments.
func onlyComments(s string) bool { return stripLeadingComments(s) == "" }

// rowKeywords start statements that produce a result set.
var rowKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "VALUES": true, "TABLE": true,
	"SHOW": true, "DESCRIBE": true, "DESC": true, "EXPLAIN": true, "PRAGMA": true,
	"CALL": true, "EXEC": true, "EXECUTE": true,
}

// readOnlyKeywords start statements that can't change the schema; anything
// else invalidates cached table metadata.
var readOnlyKeywords = map[string]bool{
	"SELECT": true, "VALUES": true, "TABLE": true, "SHOW": true,
	"DESCRIBE": true, "DESC": true, "EXPLAIN": true,
}

// firstKeyword returns the statement's first word, upper-cased, skipping
// comments and opening parentheses.
func firstKeyword(stmt string) string {
	s := strings.TrimLeft(stripLeadingComments(stmt), " \t\r\n(")
	end := 0
	for end < len(s) && isLetter(s[end]) {
		end++
	}
	return strings.ToUpper(s[:end])
}

// returnsRows guesses whether a statement produces a result set: by its
// first keyword, or a RETURNING (PostgreSQL, SQLite, Oracle) or OUTPUT
// (SQL Server) clause anywhere outside quotes and comments.
func returnsRows(stmt string) bool {
	if rowKeywords[firstKeyword(stmt)] {
		return true
	}
	found := false
	eachWord(stmt, func(w string) bool {
		found = strings.EqualFold(w, "RETURNING") || strings.EqualFold(w, "OUTPUT")
		return !found
	})
	return found
}

// readOnly reports whether a statement can be assumed not to change the schema.
func readOnly(stmt string) bool { return readOnlyKeywords[firstKeyword(stmt)] }

// eachWord calls fn with every bare word of a statement, skipping quoted
// strings, quoted identifiers and comments, until fn returns false.
func eachWord(s string, fn func(string) bool) {
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(s, i, c, false)
		case c == '-' && i+1 < n && s[i+1] == '-':
			for i < n && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && s[i+1] == '*':
			if end := strings.Index(s[i+2:], "*/"); end < 0 {
				i = n
			} else {
				i += end + 4
			}
		case c == '$':
			if tag, ok := dollarTag(s[i:]); ok {
				if end := strings.Index(s[i+len(tag):], tag); end < 0 {
					i = n
				} else {
					i += len(tag) + end + len(tag)
				}
			} else {
				i++
			}
		case isLetter(c) || c == '_':
			j := i
			for j < n && (isLetter(s[j]) || s[j] == '_' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			if !fn(s[i:j]) {
				return
			}
			i = j
		default:
			i++
		}
	}
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func stripLeadingComments(s string) string {
	for {
		s = strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
			} else {
				return ""
			}
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
			} else {
				return ""
			}
		default:
			return s
		}
	}
}
