// Package redis browses Redis through the driver.Store interface. Keys are
// grouped into "tables" by the prefix before their first ":"; the table "*"
// holds every key.
package redis

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mdenesfe/TableF/internal/driver"
)

func init() { driver.RegisterStore(store{}) }

const (
	// AllKeys is the table that lists every key.
	AllKeys = "*"
	// maxKeys caps how many matching keys a table page or the table list
	// collects. Totals are capped at this number.
	maxKeys = 10000
	// maxScanCalls caps SCAN round trips (COUNT scanCount each), so a very
	// selective pattern on a huge keyspace still returns.
	maxScanCalls = 2000
	scanCount    = 1000
	// maxItems caps how many elements of a list, set, sorted set or hash
	// are shown.
	maxItems = 100
	// maxStringBytes caps how much of a string value is read and shown.
	maxStringBytes = 64 << 10
	// keyCacheTTL is how long a scanned key list is reused, so paging and
	// sorting a table doesn't rescan the keyspace on every request.
	keyCacheTTL = 5 * time.Second
)

type store struct{}

func (store) Name() string { return "redis" }

func (store) Open(ctx context.Context, cfg driver.Config) (driver.StoreConn, error) {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 6379
	}
	db := 0
	if s := strings.TrimSpace(strings.TrimPrefix(cfg.Database, "db")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("redis database must be a number, got %q", cfg.Database)
		}
		db = n
	}
	opt := &goredis.Options{
		Addr:            net.JoinHostPort(host, strconv.Itoa(port)),
		Username:        cfg.User,
		Password:        cfg.Password,
		DB:              db,
		DialTimeout:     10 * time.Second,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		PoolSize:        4,
		DisableIdentity: true,
	}
	if cfg.Opt("tls", "") == "true" {
		opt.TLSConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		if cfg.Opt("tlsInsecure", "") == "true" {
			opt.TLSConfig.InsecureSkipVerify = true
		}
	}
	c := goredis.NewClient(opt)
	if err := c.Ping(ctx).Err(); err != nil {
		c.Close()
		return nil, err
	}
	return &conn{c: c, db: db}, nil
}

type conn struct {
	c  *goredis.Client
	db int

	mu   sync.Mutex
	keys map[keyQuery]scanned // recent scans, see keyCacheTTL
}

// keyQuery identifies a scan: the SCAN pattern and the filter pattern
// applied on top of it ("" for none).
type keyQuery struct{ pattern, filter string }

type scanned struct {
	keys      []string
	truncated bool
	at        time.Time
}

func (c *conn) Close() error { return c.c.Close() }

func (c *conn) Features() driver.Features {
	return driver.Features{Family: "redis", Edit: true}
}

func (c *conn) Version(ctx context.Context) string {
	info, err := c.c.Info(ctx, "server").Result()
	if err != nil {
		return "Redis"
	}
	for _, line := range strings.Split(info, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_version:"); ok {
			return "Redis " + v
		}
	}
	return "Redis"
}

func (c *conn) Databases(ctx context.Context) ([]string, error) {
	n := 16
	if m, err := c.c.ConfigGet(ctx, "databases").Result(); err == nil {
		if v, err := strconv.Atoi(m["databases"]); err == nil && v > 0 {
			n = v
		}
	}
	out := make([]string, n)
	for i := range out {
		out[i] = strconv.Itoa(i)
	}
	return out, nil
}

// scan collects up to maxKeys distinct keys matching pattern (and filter, a
// glob applied client-side, when not ""), sorted. The bool reports whether
// the scan stopped early. Results are reused for keyCacheTTL; callers must
// not modify the returned slice.
func (c *conn) scan(ctx context.Context, pattern, filter string) ([]string, bool, error) {
	q := keyQuery{pattern, filter}
	c.mu.Lock()
	hit, ok := c.keys[q]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < keyCacheTTL {
		return hit.keys, hit.truncated, nil
	}
	keys, truncated, err := c.scanKeys(ctx, pattern, filter)
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	if c.keys == nil {
		c.keys = map[keyQuery]scanned{}
	}
	c.keys[q] = scanned{keys: keys, truncated: truncated, at: time.Now()}
	c.mu.Unlock()
	return keys, truncated, nil
}

// forgetKeys drops cached scans after a write.
func (c *conn) forgetKeys() {
	c.mu.Lock()
	c.keys = nil
	c.mu.Unlock()
}

func (c *conn) scanKeys(ctx context.Context, pattern, filter string) ([]string, bool, error) {
	seen := map[string]struct{}{}
	var cursor uint64
	for calls := 0; ; calls++ {
		if calls >= maxScanCalls {
			return sortedSet(seen), true, nil
		}
		keys, next, err := c.c.Scan(ctx, cursor, pattern, scanCount).Result()
		if err != nil {
			return nil, false, err
		}
		for _, k := range keys {
			if filter != "" && !globMatch(filter, k) {
				continue
			}
			seen[k] = struct{}{}
			if len(seen) >= maxKeys {
				return sortedSet(seen), true, nil
			}
		}
		if next == 0 {
			return sortedSet(seen), false, nil
		}
		cursor = next
	}
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *conn) Tables(ctx context.Context) ([]driver.Table, error) {
	keys, _, err := c.scan(ctx, "*", "")
	if err != nil {
		return nil, err
	}
	groups := map[string]struct{}{}
	for _, k := range keys {
		if i := strings.IndexByte(k, ':'); i > 0 {
			groups[k[:i]] = struct{}{}
		}
	}
	out := []driver.Table{{Name: AllKeys, Type: "table"}}
	for _, g := range sortedSet(groups) {
		out = append(out, driver.Table{Name: g, Type: "table"})
	}
	return out, nil
}

var columns = []driver.Column{
	{Name: "key", Type: "string", PrimaryKey: true},
	{Name: "type", Type: "string"},
	{Name: "ttl", Type: "integer", Nullable: true},
	{Name: "value", Type: "string", Nullable: true},
}

func (c *conn) Columns(ctx context.Context, table string) ([]driver.Column, error) {
	return append([]driver.Column(nil), columns...), nil
}

// escapeGlob makes s match literally in a Redis pattern.
func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[]\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

// filterPattern turns the filter line into a key pattern: glob patterns are
// used as typed, anything else matches as a substring.
func filterPattern(f string) string {
	f = strings.TrimSpace(f)
	if f == "" {
		return ""
	}
	if hasGlob(f) {
		return f
	}
	return "*" + escapeGlob(f) + "*"
}

// tablePattern is the SCAN pattern of a table.
func tablePattern(table string) string {
	if table == AllKeys || table == "" {
		return "*"
	}
	return escapeGlob(table) + ":*"
}

// keyInfo is one row of a table page.
type keyInfo struct {
	key   string
	typ   string
	ttl   any // nil = no expiry, else seconds
	value any
}

func (c *conn) meta(ctx context.Context, keys []string) ([]keyInfo, error) {
	out := make([]keyInfo, len(keys))
	for start := 0; start < len(keys); start += 500 {
		end := min(start+500, len(keys))
		pipe := c.c.Pipeline()
		types := make([]*goredis.StatusCmd, 0, end-start)
		ttls := make([]*goredis.Cmd, 0, end-start)
		for _, k := range keys[start:end] {
			types = append(types, pipe.Type(ctx, k))
			ttls = append(ttls, pipe.Do(ctx, "TTL", k))
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
			return nil, err
		}
		for i, k := range keys[start:end] {
			info := keyInfo{key: k, typ: types[i].Val()}
			if n, err := ttls[i].Int64(); err == nil && n >= 0 {
				info.ttl = n
			}
			out[start+i] = info
		}
	}
	return out, nil
}

// values fills in the value of each key, by type. Like collections (first
// maxItems elements), strings are read only up to maxStringBytes.
func (c *conn) values(ctx context.Context, rows []keyInfo) error {
	pipe := c.c.Pipeline()
	cmds := make([]goredis.Cmder, len(rows))
	lens := make([]*goredis.IntCmd, len(rows)) // full string lengths
	for i, r := range rows {
		switch r.typ {
		case "string":
			cmds[i] = pipe.GetRange(ctx, r.key, 0, maxStringBytes-1)
			lens[i] = pipe.StrLen(ctx, r.key)
		case "hash":
			cmds[i] = pipe.HScan(ctx, r.key, 0, "*", maxItems)
		case "list":
			cmds[i] = pipe.LRange(ctx, r.key, 0, maxItems-1)
		case "set":
			cmds[i] = pipe.SScan(ctx, r.key, 0, "*", maxItems)
		case "zset":
			cmds[i] = pipe.ZRangeWithScores(ctx, r.key, 0, maxItems-1)
		case "stream":
			cmds[i] = pipe.XLen(ctx, r.key)
		}
	}
	if len(rows) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
			// Per-command errors (a key that changed type, a deleted key) are
			// shown per row below; only connection errors abort.
			var re goredis.Error
			if !errors.As(err, &re) {
				return err
			}
		}
	}
	for i := range rows {
		rows[i].value = renderValue(cmds[i], rows[i].typ)
		if s, ok := rows[i].value.(string); ok && lens[i] != nil && lens[i].Val() > int64(len(s)) {
			rows[i].value = truncatedText(s, lens[i].Val())
		}
	}
	return nil
}

// truncatedText marks a string value that was only partly read.
func truncatedText(s string, size int64) string {
	// GETRANGE counts bytes; don't end on half a UTF-8 sequence.
	for len(s) > 0 && !utf8.ValidString(s[max(0, len(s)-utf8.UTFMax):]) {
		s = s[:len(s)-1]
	}
	return fmt.Sprintf("%s… (%d bytes, truncated)", s, size)
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func renderValue(cmd goredis.Cmder, typ string) any {
	if cmd == nil {
		if typ == "none" {
			return nil
		}
		return "(" + typ + ")"
	}
	if err := cmd.Err(); err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil
		}
		return "(error: " + err.Error() + ")"
	}
	switch t := cmd.(type) {
	case *goredis.StringCmd:
		return t.Val()
	case *goredis.MapStringStringCmd:
		return jsonText(t.Val())
	case *goredis.StringSliceCmd:
		return jsonText(t.Val())
	case *goredis.ScanCmd:
		members, _ := t.Val()
		if typ == "hash" { // HSCAN: field, value, field, value, ...
			fields := make(map[string]string, len(members)/2)
			names := make([]string, 0, len(members)/2)
			for j := 0; j+1 < len(members); j += 2 {
				if _, dup := fields[members[j]]; !dup {
					names = append(names, members[j])
				}
				fields[members[j]] = members[j+1]
			}
			sort.Strings(names)
			for _, n := range names[min(len(names), maxItems):] {
				delete(fields, n)
			}
			return jsonText(fields)
		}
		sort.Strings(members)
		if len(members) > maxItems {
			members = members[:maxItems]
		}
		return jsonText(members)
	case *goredis.ZSliceCmd:
		type item struct {
			Member any     `json:"member"`
			Score  float64 `json:"score"`
		}
		items := make([]item, 0, len(t.Val()))
		for _, z := range t.Val() {
			items = append(items, item{Member: z.Member, Score: z.Score})
		}
		return jsonText(items)
	case *goredis.IntCmd:
		return fmt.Sprintf("stream (%d entries)", t.Val())
	}
	return nil
}

func (c *conn) Rows(ctx context.Context, req driver.RowsRequest) (driver.RowsResult, error) {
	pattern := tablePattern(req.Table)
	keep := ""
	fp := filterPattern(req.Filter)
	if fp != "" {
		if pattern == "*" {
			pattern = fp
		} else {
			keep = fp
		}
	}
	query := "SCAN 0 MATCH " + quoteArg(pattern) + fmt.Sprintf(" COUNT %d", scanCount)
	if keep != "" {
		query += "  -- keys matching " + quoteArg(fp)
	}
	keys, _, err := c.scan(ctx, pattern, keep)
	if err != nil {
		return driver.RowsResult{}, err
	}
	// The scan result is shared through the cache; filter and reverse a copy.
	keys = slices.Clone(keys)

	// Exact matches and sorting on key alone don't need to look up anything.
	needMeta := false
	needValues := false
	for _, m := range req.Match {
		switch m.Column {
		case "key":
		case "value":
			needMeta, needValues = true, true
		default:
			needMeta = true
		}
	}
	if req.OrderBy == "type" || req.OrderBy == "ttl" {
		needMeta = true
	}
	if req.OrderBy == "value" {
		needMeta, needValues = true, true
	}
	desc := strings.EqualFold(req.OrderDir, "desc")

	var rows []keyInfo
	if !needMeta {
		keys = filterKeys(keys, req.Match)
		if desc && req.OrderBy == "key" {
			for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
		total := len(keys)
		keys = page(keys, req.Offset, req.Limit)
		if rows, err = c.meta(ctx, keys); err != nil {
			return driver.RowsResult{}, err
		}
		if err := c.values(ctx, rows); err != nil {
			return driver.RowsResult{}, err
		}
		return result(rows, total, query), nil
	}

	if rows, err = c.meta(ctx, keys); err != nil {
		return driver.RowsResult{}, err
	}
	if needValues {
		if err := c.values(ctx, rows); err != nil {
			return driver.RowsResult{}, err
		}
	}
	rows = filterRows(rows, req.Match)
	sortRows(rows, req.OrderBy, desc)
	total := len(rows)
	rows = page(rows, req.Offset, req.Limit)
	if !needValues {
		if err := c.values(ctx, rows); err != nil {
			return driver.RowsResult{}, err
		}
	}
	return result(rows, total, query), nil
}

func page[T any](s []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(s) {
		return nil
	}
	s = s[offset:]
	if limit > 0 && limit < len(s) {
		s = s[:limit]
	}
	return s
}

func result(rows []keyInfo, total int, query string) driver.RowsResult {
	out := make([][]any, len(rows))
	for i, r := range rows {
		out[i] = []any{r.key, r.typ, r.ttl, r.value}
	}
	return driver.RowsResult{
		Columns: append([]driver.Column(nil), columns...),
		Rows:    out, Total: int64(total), Query: query,
	}
}

func filterKeys(keys []string, match []driver.Match) []string {
	for _, m := range match {
		if m.Column != "key" {
			continue
		}
		var out []string
		for _, k := range keys {
			if m.Value != nil && k == fmt.Sprint(m.Value) {
				out = append(out, k)
			}
		}
		keys = out
	}
	return keys
}

func cell(r keyInfo, col string) any {
	switch col {
	case "key":
		return r.key
	case "type":
		return r.typ
	case "ttl":
		return r.ttl
	case "value":
		return r.value
	}
	return nil
}

func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			return fa == fb
		}
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func filterRows(rows []keyInfo, match []driver.Match) []keyInfo {
	if len(match) == 0 {
		return rows
	}
	out := rows[:0:0]
outer:
	for _, r := range rows {
		for _, m := range match {
			if !sameValue(cell(r, m.Column), m.Value) {
				continue outer
			}
		}
		out = append(out, r)
	}
	return out
}

func sortRows(rows []keyInfo, by string, desc bool) {
	less := func(a, b keyInfo) bool {
		switch by {
		case "type":
			if a.typ != b.typ {
				return a.typ < b.typ
			}
		case "ttl":
			// No expiry sorts after any expiry.
			at, aok := a.ttl.(int64)
			bt, bok := b.ttl.(int64)
			if aok != bok {
				return aok
			}
			if at != bt {
				return at < bt
			}
		case "value":
			av, bv := fmt.Sprint(a.value), fmt.Sprint(b.value)
			if av != bv {
				return av < bv
			}
		}
		return a.key < b.key
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if desc {
			return less(rows[j], rows[i])
		}
		return less(rows[i], rows[j])
	})
}

// globMatch reports whether s matches a Redis glob pattern (* ? [abc] [^a]
// [a-z] and \ escapes), like Redis' stringmatchlen. It runs in
// O(len(pattern) * len(s)): on a mismatch it only retries from the last '*'
// instead of recursing at every star.
func globMatch(pattern, s string) bool {
	p, r := []rune(pattern), []rune(s)
	pi, si := 0, 0
	star, mark := -1, 0 // last '*' in p, and the position in r it matched up to
	for si < len(r) {
		if pi < len(p) && p[pi] == '*' {
			for pi < len(p) && p[pi] == '*' {
				pi++
			}
			star, mark = pi, si
			continue
		}
		if pi < len(p) {
			if n, ok := matchOne(p[pi:], r[si]); ok {
				pi += n
				si++
				continue
			}
		}
		if star < 0 {
			return false
		}
		// Let the last star absorb one more rune and retry after it.
		mark++
		pi, si = star, mark
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// matchOne matches the single-rune token at the start of p (a literal, ?,
// an escape or a [class]) against c, returning the token's length.
func matchOne(p []rune, c rune) (int, bool) {
	switch p[0] {
	case '?':
		return 1, true
	case '\\':
		if len(p) >= 2 {
			return 2, p[1] == c
		}
		return 1, c == '\\'
	case '[':
		i := 1
		not := i < len(p) && p[i] == '^'
		if not {
			i++
		}
		matched := false
		for i < len(p) && p[i] != ']' {
			switch {
			case p[i] == '\\' && i+1 < len(p):
				if p[i+1] == c {
					matched = true
				}
				i += 2
			case i+2 < len(p) && p[i+1] == '-' && p[i+2] != ']':
				lo, hi := p[i], p[i+2]
				if lo > hi {
					lo, hi = hi, lo
				}
				if c >= lo && c <= hi {
					matched = true
				}
				i += 3
			default:
				if p[i] == c {
					matched = true
				}
				i++
			}
		}
		if i < len(p) {
			i++ // ]
		}
		return i, matched != not
	}
	return 1, p[0] == c
}
