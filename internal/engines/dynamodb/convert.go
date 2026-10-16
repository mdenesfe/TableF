package dynamodb

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ---------- AttributeValue -> JSON-friendly Go ----------

// numberValue keeps DynamoDB numbers exact: int64 within JavaScript's safe
// range, float64 when it round-trips, otherwise the decimal string.
func numberValue(s string) any {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 1<<53 && n >= -(1<<53) {
			return n
		}
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return s
	}
	exact, ok1 := new(big.Rat).SetString(s)
	short, ok2 := new(big.Rat).SetString(strconv.FormatFloat(f, 'g', -1, 64))
	if ok1 && ok2 && exact.Cmp(short) == 0 {
		return f
	}
	return s
}

// plain converts an attribute into a value encoding/json renders naturally
// (used inside lists and maps).
func plain(av types.AttributeValue) any {
	switch v := av.(type) {
	case nil:
		return nil
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return json.Number(v.Value)
	case *types.AttributeValueMemberBOOL:
		return v.Value
	case *types.AttributeValueMemberNULL:
		return nil
	case *types.AttributeValueMemberB:
		return base64.StdEncoding.EncodeToString(v.Value)
	case *types.AttributeValueMemberSS:
		return v.Value
	case *types.AttributeValueMemberNS:
		out := make([]json.Number, len(v.Value))
		for i, n := range v.Value {
			out[i] = json.Number(n)
		}
		return out
	case *types.AttributeValueMemberBS:
		out := make([]string, len(v.Value))
		for i, b := range v.Value {
			out[i] = base64.StdEncoding.EncodeToString(b)
		}
		return out
	case *types.AttributeValueMemberL:
		out := make([]any, len(v.Value))
		for i, e := range v.Value {
			out[i] = plain(e)
		}
		return out
	case *types.AttributeValueMemberM:
		out := make(map[string]any, len(v.Value))
		for k, e := range v.Value {
			out[k] = plain(e)
		}
		return out
	}
	return fmt.Sprint(av)
}

// toValue converts an attribute into a cell value: scalars stay scalars,
// L/M/SS/NS/BS become JSON text, B becomes base64.
func toValue(av types.AttributeValue) any {
	switch v := av.(type) {
	case nil, *types.AttributeValueMemberNULL:
		return nil
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return numberValue(v.Value)
	case *types.AttributeValueMemberBOOL:
		return v.Value
	case *types.AttributeValueMemberB:
		return base64.StdEncoding.EncodeToString(v.Value)
	}
	b, err := json.Marshal(plain(av))
	if err != nil {
		return fmt.Sprint(av)
	}
	return string(b)
}

// kind is the DynamoDB type letter of an attribute (S, N, B, BOOL, NULL, M, L, SS, NS, BS).
func kind(av types.AttributeValue) string {
	switch av.(type) {
	case *types.AttributeValueMemberS:
		return "S"
	case *types.AttributeValueMemberN:
		return "N"
	case *types.AttributeValueMemberB:
		return "B"
	case *types.AttributeValueMemberBOOL:
		return "BOOL"
	case *types.AttributeValueMemberNULL:
		return "NULL"
	case *types.AttributeValueMemberM:
		return "M"
	case *types.AttributeValueMemberL:
		return "L"
	case *types.AttributeValueMemberSS:
		return "SS"
	case *types.AttributeValueMemberNS:
		return "NS"
	case *types.AttributeValueMemberBS:
		return "BS"
	}
	return ""
}

// kinds records which attribute types were seen for each attribute name.
type kinds map[string]map[string]bool

func (k kinds) add(name, t string) {
	if t == "" {
		return
	}
	if k[name] == nil {
		k[name] = map[string]bool{}
	}
	k[name][t] = true
}

// typeOf joins the seen types, e.g. "S" or "N|S". NULL alone counts only
// when nothing else was seen.
func (k kinds) typeOf(name string) string {
	var out []string
	for t := range k[name] {
		if t != "NULL" {
			out = append(out, t)
		}
	}
	if len(out) == 0 && k[name]["NULL"] {
		return "NULL"
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

// ---------- Go bind values -> AttributeValue ----------

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// toAttribute converts a bind argument (JSON-shaped values from the UI, or
// plain Go values) into an AttributeValue.
func toAttribute(v any) (types.AttributeValue, error) {
	switch t := v.(type) {
	case nil:
		return &types.AttributeValueMemberNULL{Value: true}, nil
	case types.AttributeValue:
		return t, nil
	case string:
		return &types.AttributeValueMemberS{Value: t}, nil
	case bool:
		return &types.AttributeValueMemberBOOL{Value: t}, nil
	case json.Number:
		return &types.AttributeValueMemberN{Value: t.String()}, nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil, fmt.Errorf("dynamodb: cannot store %v", t)
		}
		return &types.AttributeValueMemberN{Value: formatNumber(t)}, nil
	case float32:
		return toAttribute(float64(t))
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return &types.AttributeValueMemberN{Value: fmt.Sprint(t)}, nil
	case *big.Int:
		return &types.AttributeValueMemberN{Value: t.String()}, nil
	case *big.Rat:
		return &types.AttributeValueMemberN{Value: t.FloatString(20)}, nil
	case []byte:
		return &types.AttributeValueMemberB{Value: t}, nil
	case time.Time:
		return &types.AttributeValueMemberS{Value: t.UTC().Format(time.RFC3339Nano)}, nil
	case []string:
		return &types.AttributeValueMemberSS{Value: t}, nil
	case []any:
		out := make([]types.AttributeValue, len(t))
		for i, e := range t {
			a, err := toAttribute(e)
			if err != nil {
				return nil, err
			}
			out[i] = a
		}
		return &types.AttributeValueMemberL{Value: out}, nil
	case map[string]any:
		out := make(map[string]types.AttributeValue, len(t))
		for k, e := range t {
			a, err := toAttribute(e)
			if err != nil {
				return nil, err
			}
			out[k] = a
		}
		return &types.AttributeValueMemberM{Value: out}, nil
	}
	// Other slices/maps via reflection.
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return toAttribute(nil)
		}
		return toAttribute(rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return toAttribute(out)
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			out := make(map[string]any, rv.Len())
			it := rv.MapRange()
			for it.Next() {
				out[it.Key().String()] = it.Value().Interface()
			}
			return toAttribute(out)
		}
	}
	return nil, fmt.Errorf("dynamodb: unsupported parameter type %T", v)
}
