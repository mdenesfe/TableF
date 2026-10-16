package cassandra

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"gopkg.in/inf.v0"
)

// ---------- result values: CQL -> JSON-friendly Go ----------

// toJSONValue turns a decoded CQL value into int64, float64, bool, string,
// time.Time or nil. Collections, UDTs and other composites become JSON text.
func toJSONValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string, bool, int64, float64, time.Time:
		return t
	case int:
		return int64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case float32:
		return float64(t)
	case []byte:
		return "0x" + hex.EncodeToString(t)
	case gocql.UUID:
		return t.String()
	case *big.Int:
		if t == nil {
			return nil
		}
		return t.String()
	case big.Int:
		return t.String()
	case *inf.Dec:
		if t == nil {
			return nil
		}
		return t.String()
	case inf.Dec:
		return t.String()
	case net.IP:
		return t.String()
	case time.Duration:
		return t.String()
	case gocql.Duration:
		return fmt.Sprintf("%dmo%dd%dns", t.Months, t.Days, t.Nanoseconds)
	}
	b, err := json.Marshal(jsonable(v))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// jsonable rewrites nested values so encoding/json can handle them: maps with
// non-string keys get string keys, and leaf values go through toJSONValue.
func jsonable(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return jsonable(rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return toJSONValue(v)
		}
		if rv.Kind() == reflect.Array && rv.Type() == reflect.TypeOf(gocql.UUID{}) {
			return toJSONValue(v)
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = jsonable(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		it := rv.MapRange()
		for it.Next() {
			out[fmt.Sprint(jsonable(it.Key().Interface()))] = jsonable(it.Value().Interface())
		}
		return out
	}
	return toJSONValue(v)
}

// ---------- bind arguments: Go (from the UI) -> CQL ----------

// flexArg defers conversion of a bind value until gocql knows the column's CQL
// type. Values from the UI are JSON-shaped (float64 numbers, strings), which
// gocql would otherwise refuse for int, uuid, timestamp, decimal, ...
type flexArg struct{ v any }

func (a flexArg) MarshalCQL(info gocql.TypeInfo) ([]byte, error) {
	v, err := coerce(info, a.v)
	if err != nil {
		return nil, err
	}
	return gocql.Marshal(info, v)
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02",
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as a timestamp", s)
}

func asInt(v any) (int64, bool, error) {
	switch t := v.(type) {
	case float64:
		if t != math.Trunc(t) {
			return 0, true, fmt.Errorf("%v is not an integer", t)
		}
		return int64(t), true, nil
	case float32:
		return asInt(float64(t))
	case json.Number:
		n, err := t.Int64()
		return n, true, err
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, true, err
	case bool:
		if t {
			return 1, true, nil
		}
		return 0, true, nil
	}
	return 0, false, nil
}

func coerce(info gocql.TypeInfo, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch info.Type() {
	case gocql.TypeInt, gocql.TypeBigInt, gocql.TypeCounter, gocql.TypeSmallInt, gocql.TypeTinyInt:
		n, ok, err := asInt(v)
		if !ok {
			return v, nil
		}
		if err != nil {
			return nil, err
		}
		switch info.Type() {
		case gocql.TypeInt:
			if n > math.MaxInt32 || n < math.MinInt32 {
				return nil, fmt.Errorf("%d out of range for int", n)
			}
			return int32(n), nil
		case gocql.TypeSmallInt:
			if n > math.MaxInt16 || n < math.MinInt16 {
				return nil, fmt.Errorf("%d out of range for smallint", n)
			}
			return int16(n), nil
		case gocql.TypeTinyInt:
			if n > math.MaxInt8 || n < math.MinInt8 {
				return nil, fmt.Errorf("%d out of range for tinyint", n)
			}
			return int8(n), nil
		}
		return n, nil
	case gocql.TypeVarint:
		switch t := v.(type) {
		case string:
			b, ok := new(big.Int).SetString(strings.TrimSpace(t), 10)
			if !ok {
				return nil, fmt.Errorf("cannot parse %q as varint", t)
			}
			return *b, nil
		case float64:
			n, _, err := asInt(t)
			return n, err
		}
	case gocql.TypeDouble, gocql.TypeFloat:
		var f float64
		switch t := v.(type) {
		case float64:
			f = t
		case int64:
			f = float64(t)
		case int:
			f = float64(t)
		case string:
			var err error
			if f, err = strconv.ParseFloat(strings.TrimSpace(t), 64); err != nil {
				return nil, err
			}
		default:
			return v, nil
		}
		if info.Type() == gocql.TypeFloat {
			return float32(f), nil
		}
		return f, nil
	case gocql.TypeDecimal:
		var s string
		switch t := v.(type) {
		case string:
			s = strings.TrimSpace(t)
		case float64:
			s = strconv.FormatFloat(t, 'f', -1, 64)
		case int64, int:
			s = fmt.Sprint(t)
		default:
			return v, nil
		}
		d, ok := new(inf.Dec).SetString(s)
		if !ok {
			return nil, fmt.Errorf("cannot parse %q as decimal", s)
		}
		return *d, nil
	case gocql.TypeBoolean:
		if s, ok := v.(string); ok {
			return strconv.ParseBool(strings.TrimSpace(s))
		}
	case gocql.TypeTimestamp:
		switch t := v.(type) {
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
				return n, nil // epoch milliseconds
			}
			return parseTime(t)
		case float64:
			return int64(t), nil
		}
	case gocql.TypeDate:
		if s, ok := v.(string); ok {
			t, err := parseTime(s)
			if err != nil {
				return nil, err
			}
			return t, nil
		}
	case gocql.TypeTime:
		switch t := v.(type) {
		case string:
			if d, err := time.Parse("15:04:05.999999999", strings.TrimSpace(t)); err == nil {
				return time.Duration(d.Hour())*time.Hour + time.Duration(d.Minute())*time.Minute +
					time.Duration(d.Second())*time.Second + time.Duration(d.Nanosecond()), nil
			}
			return time.ParseDuration(strings.TrimSpace(t))
		case float64:
			return int64(t), nil
		}
	case gocql.TypeBlob:
		if s, ok := v.(string); ok {
			if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
				return hex.DecodeString(s[2:])
			}
			if b, err := base64.StdEncoding.DecodeString(s); err == nil && s != "" {
				return b, nil
			}
			return []byte(s), nil
		}
	case gocql.TypeInet:
		if s, ok := v.(string); ok {
			ip := net.ParseIP(strings.TrimSpace(s))
			if ip == nil {
				return nil, fmt.Errorf("cannot parse %q as inet", s)
			}
			return ip, nil
		}
	case gocql.TypeList, gocql.TypeSet, gocql.TypeMap:
		ct, ok := info.(gocql.CollectionType)
		if !ok {
			return v, nil
		}
		if s, ok := v.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err != nil {
				return nil, fmt.Errorf("expected a JSON %s: %w", strings.ToLower(typeName(info)), err)
			}
			v = parsed
		}
		switch t := v.(type) {
		case []any:
			out := make([]any, len(t))
			for i, e := range t {
				c, err := coerce(ct.Elem, e)
				if err != nil {
					return nil, err
				}
				out[i] = flexArgValue{info: ct.Elem, v: c}
			}
			return out, nil
		case map[string]any:
			if info.Type() != gocql.TypeMap {
				return v, nil
			}
			out := make(map[any]any, len(t))
			for k, e := range t {
				ck, err := coerce(ct.Key, k)
				if err != nil {
					return nil, err
				}
				if ck != nil && !reflect.TypeOf(ck).Comparable() {
					return nil, fmt.Errorf("unsupported map key type %T", ck)
				}
				ce, err := coerce(ct.Elem, e)
				if err != nil {
					return nil, err
				}
				out[flexArgValue{info: ct.Key, v: ck}] = flexArgValue{info: ct.Elem, v: ce}
			}
			return out, nil
		}
	case gocql.TypeUDT, gocql.TypeTuple:
		if s, ok := v.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				if m, ok := parsed.(map[string]any); ok && info.Type() == gocql.TypeUDT {
					if udt, ok := info.(gocql.UDTTypeInfo); ok {
						out := make(map[string]any, len(m))
						for _, f := range udt.Elements {
							if e, ok := m[f.Name]; ok {
								c, err := coerce(f.Type, e)
								if err != nil {
									return nil, err
								}
								out[f.Name] = flexArgValue{info: f.Type, v: c}
							}
						}
						return out, nil
					}
				}
				return parsed, nil
			}
		}
	}
	return v, nil
}

// flexArgValue is an already coerced element of a collection; it marshals
// itself so gocql's reflection-based collection marshaling accepts []any.
type flexArgValue struct {
	info gocql.TypeInfo
	v    any
}

func (f flexArgValue) MarshalCQL(info gocql.TypeInfo) ([]byte, error) {
	return gocql.Marshal(info, f.v)
}
