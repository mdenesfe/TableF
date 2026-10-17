package core

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxSafeInt is the largest integer a JavaScript number holds exactly (2^53).
const maxSafeInt = 1 << 53

// readRows reads a result set. The first skip rows are discarded without
// being scanned (for engines that can't OFFSET); after that at most limit
// rows are kept (0 = no limit) and truncated reports whether more followed.
func readRows(rows *sql.Rows, limit, skip int) (cols []string, out [][]any, truncated bool, err error) {
	defer rows.Close()
	if cols, err = rows.Columns(); err != nil {
		return nil, nil, false, err
	}
	for skip > 0 && rows.Next() {
		skip-- // discarded without Scan: nothing is copied or converted
	}
	out, truncated, err = scanRows(rows, len(cols), limit, false)
	return cols, out, truncated, err
}

// scanRows reads up to limit rows (0 = no limit). pending says rows is already
// positioned on a row that hasn't been scanned. more reports whether another
// row follows; rows is then left positioned on it (pending for the next call).
func scanRows(rows *sql.Rows, ncols, limit int, pending bool) (out [][]any, more bool, err error) {
	out = [][]any{}
	// One scan buffer for the whole chunk; each kept row gets its own slice.
	buf := make([]any, ncols)
	ptrs := make([]any, ncols)
	for i := range buf {
		ptrs[i] = &buf[i]
	}
	for limit <= 0 || len(out) < limit {
		if !pending && !rows.Next() {
			return out, false, rows.Err()
		}
		pending = false
		if err := rows.Scan(ptrs...); err != nil {
			return nil, false, err
		}
		row := make([]any, ncols)
		for i, v := range buf {
			row[i] = normalize(v)
		}
		out = append(out, row)
	}
	if rows.Next() {
		return out, true, nil
	}
	return out, false, rows.Err()
}

// columnTypes returns the database type name of each result column ("" when unknown).
func columnTypes(rows *sql.Rows) []string {
	types, err := rows.ColumnTypes()
	if err != nil {
		return []string{}
	}
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = strings.ToLower(t.DatabaseTypeName())
	}
	return out
}

// normalize converts driver values into JSON-friendly ones: text instead of
// bytes when valid UTF-8, strings for integers JavaScript can't hold exactly
// and for the floats JSON can't encode.
func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(t) {
			return string(t)
		}
		return "0x" + hex.EncodeToString(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05.999999Z07:00")
	case int64:
		if t > maxSafeInt || t < -maxSafeInt {
			return strconv.FormatInt(t, 10)
		}
		return t
	case int:
		if t > maxSafeInt || t < -maxSafeInt {
			return strconv.Itoa(t)
		}
		return t
	case uint64:
		if t > maxSafeInt {
			return strconv.FormatUint(t, 10)
		}
		return t
	case uint:
		if t > maxSafeInt {
			return strconv.FormatUint(uint64(t), 10)
		}
		return t
	case float64:
		return finite(t, t)
	case float32:
		return finite(float64(t), t)
	case [16]byte: // uuid
		h := hex.EncodeToString(t[:])
		return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	case string, bool, int32, int16, int8, uint32, uint16, uint8:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		if b, err := json.Marshal(t); err == nil {
			return string(b)
		}
		return fmt.Sprint(t)
	}
}

// finite returns v, or the name of f when JSON has no number for it.
func finite(f float64, v any) any {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return v
}
