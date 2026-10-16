package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Split returns one command per non-empty line; lines starting with # or //
// are comments.
func (c *conn) Split(script string) []string {
	var out []string
	for _, line := range strings.Split(script, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "//") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// parseArgs splits a command line like redis-cli: whitespace separates
// arguments; "double quotes" support \n \r \t \b \a \\ \" and \xHH escapes;
// 'single quotes' only \'.
func parseArgs(line string) ([]string, error) {
	var args []string
	i, n := 0, len(line)
	for {
		for i < n && isSpace(line[i]) {
			i++
		}
		if i >= n {
			return args, nil
		}
		var b strings.Builder
		inDouble, inSingle, done := false, false, false
		for !done {
			if i >= n {
				if inDouble || inSingle {
					return nil, errors.New("unbalanced quotes")
				}
				break
			}
			ch := line[i]
			switch {
			case inDouble:
				switch {
				case ch == '\\' && i+3 < n && line[i+1] == 'x' && isHex(line[i+2]) && isHex(line[i+3]):
					v, _ := strconv.ParseUint(line[i+2:i+4], 16, 8)
					b.WriteByte(byte(v))
					i += 3
				case ch == '\\' && i+1 < n:
					i++
					switch line[i] {
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case 'b':
						b.WriteByte('\b')
					case 'a':
						b.WriteByte('\a')
					default:
						b.WriteByte(line[i])
					}
				case ch == '"':
					if i+1 < n && !isSpace(line[i+1]) {
						return nil, errors.New("closing quote must be followed by a space")
					}
					done = true
				default:
					b.WriteByte(ch)
				}
			case inSingle:
				switch {
				case ch == '\\' && i+1 < n && line[i+1] == '\'':
					i++
					b.WriteByte('\'')
				case ch == '\'':
					if i+1 < n && !isSpace(line[i+1]) {
						return nil, errors.New("closing quote must be followed by a space")
					}
					done = true
				default:
					b.WriteByte(ch)
				}
			default:
				switch {
				case isSpace(ch):
					done = true
				case ch == '"':
					inDouble = true
				case ch == '\'':
					inSingle = true
				default:
					b.WriteByte(ch)
				}
			}
			i++
		}
		args = append(args, b.String())
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// quoteArg renders an argument the way redis-cli would accept it.
func quoteArg(s string) string {
	plain := s != ""
	for i := 0; i < len(s) && plain; i++ {
		c := s[i]
		if c <= ' ' || c == '"' || c == '\'' || c == '\\' || c >= 0x7f {
			plain = false
		}
	}
	if plain {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < ' ' || c == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func render(args []any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = quoteArg(fmt.Sprint(a))
	}
	return strings.Join(parts, " ")
}

// Commands that would break the shared connection pool or hang.
var refused = map[string]string{
	"SUBSCRIBE": "pub/sub isn't supported in the editor", "PSUBSCRIBE": "pub/sub isn't supported in the editor",
	"SSUBSCRIBE": "pub/sub isn't supported in the editor", "MONITOR": "MONITOR isn't supported in the editor",
	"SELECT": "switch the database from the database picker", "AUTH": "set credentials in the connection settings",
	"HELLO": "the protocol is negotiated by the connection", "QUIT": "close the connection instead",
	"RESET": "RESET isn't supported in the editor",
	"MULTI": "transactions span connections here; use a Lua script (EVAL) instead",
	"EXEC":  "transactions span connections here; use a Lua script (EVAL) instead",
	"WATCH": "transactions span connections here; use a Lua script (EVAL) instead",
}

// Write commands whose integer reply counts affected keys or elements.
var counting = map[string]bool{
	"DEL": true, "UNLINK": true, "HSET": true, "HDEL": true, "HSETNX": true,
	"SADD": true, "SREM": true, "ZADD": true, "ZREM": true, "LPUSH": true, "RPUSH": true,
	"LREM": true, "EXPIRE": true, "PEXPIRE": true, "EXPIREAT": true, "PERSIST": true,
	"SETNX": true, "MSETNX": true, "RENAMENX": true, "TOUCH": true, "COPY": true,
	"SMOVE": true, "XDEL": true, "XTRIM": true, "ZREMRANGEBYSCORE": true,
	"ZREMRANGEBYRANK": true, "ZREMRANGEBYLEX": true,
}

func (c *conn) Execute(ctx context.Context, command string) (driver.Result, error) {
	defer c.forgetKeys() // the command may have added, renamed or removed keys
	args, err := parseArgs(command)
	if err != nil {
		return driver.Result{}, err
	}
	if len(args) == 0 {
		return driver.Result{}, errors.New("empty command")
	}
	name := strings.ToUpper(args[0])
	if why, ok := refused[name]; ok {
		return driver.Result{}, fmt.Errorf("%s: %s", name, why)
	}
	a := make([]any, len(args))
	for i, s := range args {
		a[i] = s
	}
	reply, err := c.c.Do(ctx, a...).Result()
	if errors.Is(err, goredis.Nil) {
		return driver.Result{Columns: []string{"result"}, Rows: [][]any{{nil}}}, nil
	}
	if err != nil {
		return driver.Result{}, err
	}
	res := replyResult(reply)
	if n, ok := reply.(int64); ok && counting[name] {
		res.RowsAffected = n
	}
	return res, nil
}

// replyResult maps a reply onto a grid.
func replyResult(reply any) driver.Result {
	switch t := reply.(type) {
	case []any:
		rows := make([][]any, len(t))
		for i, v := range t {
			rows[i] = []any{scalar(v)}
		}
		return driver.Result{Columns: []string{"value"}, Rows: rows}
	case map[any]any:
		type kv struct {
			k string
			v any
		}
		items := make([]kv, 0, len(t))
		for k, v := range t {
			items = append(items, kv{fmt.Sprint(plain(k)), scalar(v)})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].k < items[j].k })
		rows := make([][]any, len(items))
		for i, it := range items {
			rows[i] = []any{it.k, it.v}
		}
		return driver.Result{Columns: []string{"field", "value"}, Rows: rows}
	case map[string]any:
		m := make(map[any]any, len(t))
		for k, v := range t {
			m[k] = v
		}
		return replyResult(m)
	}
	return driver.Result{Columns: []string{"result"}, Rows: [][]any{{scalar(reply)}}}
}

// scalar renders one reply element; nested arrays and maps become JSON.
func scalar(v any) any {
	switch v.(type) {
	case []any, map[any]any, map[string]any:
		b, err := json.Marshal(plain(v))
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
	return plain(v)
}

// plain turns a reply into JSON-friendly Go values.
func plain(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = plain(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(plain(k))] = plain(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = plain(e)
		}
		return out
	case *big.Int:
		return t.String()
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return strconv.FormatFloat(t, 'g', -1, 64)
		}
		return t
	case goredis.Error:
		return "(error) " + t.Error()
	case error:
		return "(error) " + t.Error()
	}
	return v
}

// ---- Apply ----

func text(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", errors.New("value can't be empty (null); delete the key instead")
	case string:
		return t, nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(t), nil
	case json.Number:
		return t.String(), nil
	}
	return fmt.Sprint(v), nil
}

// elem renders a JSON element as a Redis string: strings as-is, the rest as JSON.
func elem(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// writeCmds returns the commands that replace key's value with v, for its type.
func writeCmds(key, typ string, v any) ([][]any, error) {
	s, err := text(v)
	if err != nil {
		return nil, err
	}
	if typ == "string" {
		return [][]any{{"SET", key, s}}, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%s: a %s value must be JSON: %w", key, typ, err)
	}
	num := func(x any) any {
		if n, ok := x.(json.Number); ok {
			return n.String()
		}
		return x
	}
	switch typ {
	case "hash":
		obj, ok := parsed.(map[string]any)
		if !ok || len(obj) == 0 {
			return nil, fmt.Errorf("%s: a hash value must be a non-empty JSON object", key)
		}
		cmd := []any{"HSET", key}
		for _, f := range slices.Sorted(maps.Keys(obj)) {
			cmd = append(cmd, f, elem(num(obj[f])))
		}
		return [][]any{{"DEL", key}, cmd}, nil
	case "list", "set":
		arr, ok := parsed.([]any)
		if !ok || len(arr) == 0 {
			return nil, fmt.Errorf("%s: a %s value must be a non-empty JSON array", key, typ)
		}
		op := "RPUSH"
		if typ == "set" {
			op = "SADD"
		}
		cmd := []any{op, key}
		for _, e := range arr {
			cmd = append(cmd, elem(num(e)))
		}
		return [][]any{{"DEL", key}, cmd}, nil
	case "zset":
		arr, ok := parsed.([]any)
		if !ok || len(arr) == 0 {
			return nil, fmt.Errorf(`%s: a zset value must be a non-empty JSON array of {"member": …, "score": n}`, key)
		}
		cmd := []any{"ZADD", key}
		for _, e := range arr {
			o, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf(`%s: zset items look like {"member": "a", "score": 1}`, key)
			}
			score, ok := o["score"].(json.Number)
			if !ok {
				return nil, fmt.Errorf("%s: each zset item needs a numeric score", key)
			}
			if _, err := score.Float64(); err != nil {
				return nil, fmt.Errorf("%s: bad score %s", key, score)
			}
			cmd = append(cmd, score.String(), elem(num(o["member"])))
		}
		return [][]any{{"DEL", key}, cmd}, nil
	}
	return nil, fmt.Errorf("%s: editing %s values isn't supported", key, typ)
}

// ttlCmd returns EXPIRE/PERSIST for an edited ttl; nil, "" or -1 mean no expiry.
func ttlCmd(key string, v any) ([]any, error) {
	var n float64
	switch t := v.(type) {
	case nil:
		return []any{"PERSIST", key}, nil
	case float64:
		n = t
	case int64:
		n = float64(t)
	case int:
		n = float64(t)
	case string:
		s := strings.TrimSpace(t)
		if s == "" || strings.EqualFold(s, "null") {
			return []any{"PERSIST", key}, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: ttl must be a number of seconds", key)
		}
		n = f
	default:
		return nil, fmt.Errorf("%s: ttl must be a number of seconds", key)
	}
	if n == -1 {
		return []any{"PERSIST", key}, nil
	}
	if n <= 0 || n != math.Trunc(n) {
		return nil, fmt.Errorf("%s: ttl must be a positive whole number of seconds (empty for no expiry)", key)
	}
	return []any{"EXPIRE", key, int64(n)}, nil
}

// exec runs cmds atomically (MULTI/EXEC) and returns their results.
func (c *conn) exec(ctx context.Context, cmds [][]any) ([]*goredis.Cmd, error) {
	pipe := c.c.TxPipeline()
	out := make([]*goredis.Cmd, len(cmds))
	for i, cmd := range cmds {
		out[i] = pipe.Do(ctx, cmd...)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, goredis.Nil) {
		return out, err
	}
	return out, nil
}

// sizeOK refuses to rewrite values that are shown truncated.
func (c *conn) sizeOK(ctx context.Context, key, typ string) error {
	var n int64
	var err error
	switch typ {
	case "string":
		if n, err = c.c.StrLen(ctx, key).Result(); err == nil && n > maxStringBytes {
			return fmt.Errorf("%s: this string has %d bytes and only %d are shown, so it can't be edited here; use the editor", key, n, maxStringBytes)
		}
		return err
	case "hash":
		n, err = c.c.HLen(ctx, key).Result()
	case "list":
		n, err = c.c.LLen(ctx, key).Result()
	case "set":
		n, err = c.c.SCard(ctx, key).Result()
	case "zset":
		n, err = c.c.ZCard(ctx, key).Result()
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if n > maxItems {
		return fmt.Errorf("%s: this %s has %d elements and only %d are shown, so it can't be edited here; use the editor", key, typ, n, maxItems)
	}
	return nil
}

func keyOf(keys map[string]any) (string, error) {
	k, ok := keys["key"]
	if !ok || k == nil {
		return "", errors.New("the row has no key")
	}
	return fmt.Sprint(k), nil
}

func (c *conn) Apply(ctx context.Context, table string, ch driver.Changes) ([]string, error) {
	defer c.forgetKeys()
	var log []string
	run := func(cmds [][]any) ([]*goredis.Cmd, error) {
		res, err := c.exec(ctx, cmds)
		if err != nil {
			return res, err
		}
		for _, cmd := range cmds {
			log = append(log, render(cmd))
		}
		return res, nil
	}

	for _, u := range ch.Updates {
		key, err := keyOf(u.Keys)
		if err != nil {
			return log, err
		}
		typ, err := c.c.Type(ctx, key).Result()
		if err != nil {
			return log, err
		}
		if typ == "none" {
			return log, fmt.Errorf("key %s no longer exists", key)
		}
		if t, ok := u.Values["type"]; ok && fmt.Sprint(t) != typ {
			return log, fmt.Errorf("%s: the type can't be changed", key)
		}
		var cmds [][]any
		if v, ok := u.Values["value"]; ok {
			if err := c.sizeOK(ctx, key, typ); err != nil {
				return log, err
			}
			w, err := writeCmds(key, typ, v)
			if err != nil {
				return log, err
			}
			cmds = append(cmds, w...)
			// Rewriting the value drops the expiry; keep it unless ttl is edited too.
			if _, editsTTL := u.Values["ttl"]; !editsTTL {
				if ms, err := c.c.PTTL(ctx, key).Result(); err == nil && ms > 0 {
					cmds = append(cmds, []any{"PEXPIRE", key, ms.Milliseconds()})
				}
			}
		}
		if v, ok := u.Values["ttl"]; ok {
			t, err := ttlCmd(key, v)
			if err != nil {
				return log, err
			}
			cmds = append(cmds, t)
		}
		renameIdx := -1
		if nk, ok := u.Values["key"]; ok && nk != nil && fmt.Sprint(nk) != key {
			newKey := fmt.Sprint(nk)
			if newKey == "" {
				return log, errors.New("the key can't be empty")
			}
			if n, err := c.c.Exists(ctx, newKey).Result(); err != nil {
				return log, err
			} else if n > 0 {
				return log, fmt.Errorf("can't rename %s: %s already exists", key, newKey)
			}
			renameIdx = len(cmds)
			cmds = append(cmds, []any{"RENAMENX", key, newKey})
		}
		if len(cmds) == 0 {
			continue
		}
		res, err := run(cmds)
		if err != nil {
			return log, err
		}
		if renameIdx >= 0 {
			if n, _ := res[renameIdx].Int64(); n == 0 {
				return log, fmt.Errorf("can't rename %s: the new key already exists", key)
			}
		}
	}

	for _, row := range ch.Inserts {
		key, err := keyOf(row)
		if err != nil || key == "" {
			return log, errors.New("a new row needs a key")
		}
		typ := "string"
		if t, ok := row["type"]; ok && t != nil && fmt.Sprint(t) != "" {
			typ = strings.ToLower(fmt.Sprint(t))
		}
		var ttl []any
		if v, ok := row["ttl"]; ok && v != nil && v != "" {
			if ttl, err = ttlCmd(key, v); err != nil {
				return log, err
			}
			if ttl[0] == "PERSIST" {
				ttl = nil
			}
		}
		val := row["value"]
		if val == nil && typ == "string" {
			val = ""
		}
		if typ == "string" {
			s, err := text(val)
			if err != nil {
				return log, err
			}
			cmd := []any{"SET", key, s, "NX"}
			if ttl != nil {
				cmd = append(cmd, "EX", ttl[2])
			}
			res, err := c.c.Do(ctx, cmd...).Result()
			if errors.Is(err, goredis.Nil) || (err == nil && res == nil) {
				return log, fmt.Errorf("key %s already exists", key)
			}
			if err != nil {
				return log, err
			}
			log = append(log, render(cmd))
			continue
		}
		if n, err := c.c.Exists(ctx, key).Result(); err != nil {
			return log, err
		} else if n > 0 {
			return log, fmt.Errorf("key %s already exists", key)
		}
		w, err := writeCmds(key, typ, val)
		if err != nil {
			return log, err
		}
		w = w[1:] // no DEL for a new key
		if ttl != nil {
			w = append(w, ttl)
		}
		if _, err := run(w); err != nil {
			return log, err
		}
	}

	if len(ch.Deletes) > 0 {
		cmd := []any{"DEL"}
		for _, keys := range ch.Deletes {
			key, err := keyOf(keys)
			if err != nil {
				return log, err
			}
			cmd = append(cmd, key)
		}
		if _, err := run([][]any{cmd}); err != nil {
			return log, err
		}
	}
	return log, nil
}
