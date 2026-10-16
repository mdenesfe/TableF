package bigquery

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
)

// ---------- result values: BigQuery -> JSON-friendly Go ----------

// ratString formats NUMERIC (scale 9) / BIGNUMERIC (scale 38) exactly,
// without trailing zeros.
func ratString(r *big.Rat, scale int) string {
	s := r.FloatString(scale)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func ratScale(t bigquery.FieldType) int {
	if t == bigquery.BigNumericFieldType {
		return 38
	}
	return 9
}

// cell converts one value of field f into int64, float64, bool, string,
// time.Time or nil. Arrays and structs become JSON text.
func cell(v bigquery.Value, f *bigquery.FieldSchema) any {
	if v == nil {
		return nil
	}
	if _, isArr := v.([]bigquery.Value); isArr || isStruct(f) {
		b, err := json.Marshal(plain(v, f))
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
	return scalar(v, f)
}

func isStruct(f *bigquery.FieldSchema) bool {
	return f != nil && (f.Type == bigquery.RecordFieldType || len(f.Schema) > 0)
}

func scalar(v bigquery.Value, f *bigquery.FieldSchema) any {
	switch t := v.(type) {
	case nil:
		return nil
	case int64, float64, bool, string:
		return t
	case time.Time:
		return t.UTC()
	case []byte:
		return base64.StdEncoding.EncodeToString(t)
	case civil.Date:
		return t.String()
	case civil.Time:
		return bigquery.CivilTimeString(t)
	case civil.DateTime:
		return bigquery.CivilDateTimeString(t)
	case *big.Rat:
		if t == nil {
			return nil
		}
		scale := 9
		if f != nil {
			scale = ratScale(f.Type)
		}
		return ratString(t, scale)
	case *bigquery.IntervalValue:
		if t == nil {
			return nil
		}
		return t.String()
	case *bigquery.RangeValue:
		if t == nil {
			return nil
		}
		return "[" + rangeEnd(t.Start) + ", " + rangeEnd(t.End) + ")"
	case fmt.Stringer:
		return t.String()
	}
	return fmt.Sprint(v)
}

func rangeEnd(v bigquery.Value) string {
	if v == nil {
		return "UNBOUNDED"
	}
	return fmt.Sprint(scalar(v, nil))
}

// plain builds a JSON-encodable tree: arrays as slices, structs as objects
// keyed by field name, scalars as in scalar (large ints and FLOAT64 specials
// as strings so the JSON is valid and exact).
func plain(v bigquery.Value, f *bigquery.FieldSchema) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []bigquery.Value:
		if f != nil && f.Repeated {
			elem := *f
			elem.Repeated = false
			out := make([]any, len(t))
			for i, e := range t {
				out[i] = plain(e, &elem)
			}
			return out
		}
		if isStruct(f) {
			obj := make(map[string]any, len(t))
			for i, e := range t {
				if i < len(f.Schema) {
					obj[f.Schema[i].Name] = plain(e, f.Schema[i])
				}
			}
			return obj
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = plain(e, nil)
		}
		return out
	case map[string]bigquery.Value:
		obj := make(map[string]any, len(t))
		for k, e := range t {
			var sub *bigquery.FieldSchema
			if f != nil {
				for _, s := range f.Schema {
					if s.Name == k {
						sub = s
					}
				}
			}
			obj[k] = plain(e, sub)
		}
		return obj
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return strconv.FormatFloat(t, 'g', -1, 64)
		}
		return t
	case int64:
		if t > 1<<53 || t < -(1<<53) {
			return strconv.FormatInt(t, 10)
		}
		return t
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	}
	return scalar(v, f)
}

// typeName renders a field's type in GoogleSQL terms, e.g. ARRAY<STRING>, STRUCT.
func typeName(f *bigquery.FieldSchema) string {
	if f == nil {
		return ""
	}
	t := string(f.Type)
	switch f.Type {
	case bigquery.RecordFieldType:
		t = "STRUCT"
	case bigquery.IntegerFieldType:
		t = "INT64"
	case bigquery.FloatFieldType:
		t = "FLOAT64"
	case bigquery.BooleanFieldType:
		t = "BOOL"
	case bigquery.NumericFieldType:
		t = "NUMERIC"
	case bigquery.BigNumericFieldType:
		t = "BIGNUMERIC"
	}
	if f.Repeated {
		return "ARRAY<" + t + ">"
	}
	return t
}

// ---------- bind arguments ----------

// placeholders returns the byte offsets of the positional ? parameters in q,
// skipping string literals ('…', "…", triple-quoted, raw/bytes prefixes),
// quoted identifiers (`…`) and comments (--, #, /* */).
func placeholders(q string) []int {
	var out []int
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '?':
			out = append(out, i)
		case c == '-' && i+1 < len(q) && q[i+1] == '-', c == '#':
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += end + 3
		case c == '`':
			end := strings.IndexByte(q[i+1:], '`')
			if end < 0 {
				return out
			}
			i += end + 1
		case c == '\'' || c == '"':
			raw := i > 0 && (q[i-1] == 'r' || q[i-1] == 'R') && (i < 2 || !isIdentChar(q[i-2]) || q[i-2] == 'b' || q[i-2] == 'B')
			if strings.HasPrefix(q[i:], strings.Repeat(string(c), 3)) {
				end := strings.Index(q[i+3:], strings.Repeat(string(c), 3))
				if end < 0 {
					return out
				}
				i += end + 5
				continue
			}
			j := i + 1
			for j < len(q) && q[j] != c {
				if q[j] == '\\' && !raw {
					j++
				}
				j++
			}
			i = j
		}
	}
	return out
}

func isIdentChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// bind maps database/sql arguments onto positional BigQuery parameters. NULL
// arguments are inlined as NULL literals because a typed NULL parameter would
// not coerce to every column type.
func bind(q string, args []any) (string, []bigquery.QueryParameter, error) {
	if len(args) == 0 {
		return q, nil, nil
	}
	pos := placeholders(q)
	if len(pos) != len(args) {
		return "", nil, fmt.Errorf("bigquery: statement has %d ? parameters but %d arguments were given", len(pos), len(args))
	}
	var b strings.Builder
	var params []bigquery.QueryParameter
	last := 0
	for i, p := range pos {
		v, isNull := paramValue(args[i])
		if !isNull {
			params = append(params, bigquery.QueryParameter{Value: v})
			continue
		}
		b.WriteString(q[last:p])
		b.WriteString("NULL")
		last = p + 1
	}
	b.WriteString(q[last:])
	return b.String(), params, nil
}

// paramValue adapts JSON-shaped UI values: integral float64 becomes INT64
// (which coerces to FLOAT64/NUMERIC columns), short decimals become NUMERIC
// (which coerces to FLOAT64/BIGNUMERIC), other floats stay FLOAT64. Strings
// stay STRING, which BigQuery coerces to DATE/TIME/DATETIME/TIMESTAMP.
func paramValue(v any) (any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1<<53 {
			return int64(t), false
		}
		if s := strconv.FormatFloat(t, 'f', -1, 64); !math.IsInf(t, 0) && !math.IsNaN(t) {
			if dot := strings.IndexByte(s, '.'); dot >= 0 && len(s)-dot-1 <= 9 && math.Abs(t) < 1e29 {
				if r, ok := new(big.Rat).SetString(s); ok {
					return r, false
				}
			}
		}
		return t, false
	case float32:
		return paramValue(float64(t))
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, false
		}
		if r, ok := new(big.Rat).SetString(t.String()); ok {
			return r, false
		}
		return t.String(), false
	case int:
		return int64(t), false
	case uint, uint64:
		return fmt.Sprint(t), false
	case int64, int32, int16, int8, uint32, uint16, uint8, bool, string, []byte, time.Time, *big.Rat,
		civil.Date, civil.Time, civil.DateTime:
		return t, false
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t), false
		}
		return string(b), false
	}
	return v, false
}
