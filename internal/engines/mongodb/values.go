package mongodb

import (
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/mdenesfe/TableF/internal/driver"
)

// toValue converts a decoded BSON value into something the core's normalizer
// shows well: scalars stay scalars, ObjectIDs become hex, dates become
// time.Time and embedded documents/arrays become extended JSON text.
func toValue(v any) any {
	switch t := v.(type) {
	case nil, bson.Null, bson.Undefined:
		return nil
	case string, bool, int32, int64, float64, time.Time:
		return t
	case int:
		return int64(t)
	case float32:
		return float64(t)
	case bson.ObjectID:
		return t.Hex()
	case bson.DateTime:
		return t.Time().UTC()
	case bson.Decimal128:
		return t.String()
	case bson.Binary:
		if (t.Subtype == 4 || t.Subtype == 3) && len(t.Data) == 16 {
			h := hex.EncodeToString(t.Data)
			return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
		}
		return "0x" + hex.EncodeToString(t.Data)
	case []byte:
		return "0x" + hex.EncodeToString(t)
	case bson.Timestamp:
		return fmt.Sprintf("Timestamp(%d, %d)", t.T, t.I)
	case bson.Regex:
		return "/" + t.Pattern + "/" + t.Options
	case bson.JavaScript:
		return string(t)
	case bson.Symbol:
		return string(t)
	case bson.MinKey:
		return "MinKey"
	case bson.MaxKey:
		return "MaxKey"
	}
	return extJSON(v)
}

// extJSON renders any BSON value as relaxed extended JSON.
func extJSON(v any) string {
	b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(b)
	// {"v":<value>}
	s = strings.TrimPrefix(s, `{"v":`)
	s = strings.TrimSuffix(s, "}")
	return s
}

// kindOf names the BSON type of v for column inference.
func kindOf(v any) string {
	switch v.(type) {
	case nil, bson.Null, bson.Undefined:
		return "null"
	case string:
		return "string"
	case int32, int64, int:
		return "int"
	case float64, float32:
		return "double"
	case bool:
		return "bool"
	case bson.DateTime, time.Time:
		return "date"
	case bson.ObjectID:
		return "objectId"
	case bson.D, bson.M, map[string]any, bson.Raw:
		return "object"
	case bson.A, []any:
		return "array"
	case bson.Decimal128:
		return "decimal"
	case bson.Binary, []byte:
		return "binData"
	case bson.Timestamp:
		return "timestamp"
	case bson.Regex:
		return "regex"
	}
	return "mixed"
}

// docFields returns the top-level fields of a decoded document in order.
func docFields(doc any) bson.D {
	switch d := doc.(type) {
	case bson.D:
		return d
	case bson.M:
		keys := make([]string, 0, len(d))
		for k := range d {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(bson.D, len(keys))
		for i, k := range keys {
			out[i] = bson.E{Key: k, Value: d[k]}
		}
		return out
	}
	return nil
}

// fieldSet collects the union of top-level fields, _id first, in first-seen order.
type fieldSet struct {
	names []string
	kinds map[string]string
}

func newFieldSet() *fieldSet { return &fieldSet{kinds: map[string]string{}} }

// clone returns an independent copy.
func (f *fieldSet) clone() *fieldSet {
	kinds := make(map[string]string, len(f.kinds))
	for k, v := range f.kinds {
		kinds[k] = v
	}
	return &fieldSet{names: slices.Clone(f.names), kinds: kinds}
}

func (f *fieldSet) add(doc bson.D) {
	for _, e := range doc {
		k := kindOf(e.Value)
		old, seen := f.kinds[e.Key]
		if !seen {
			f.names = append(f.names, e.Key)
			f.kinds[e.Key] = k
			continue
		}
		switch {
		case old == k || k == "null":
		case old == "null":
			f.kinds[e.Key] = k
		case (old == "int" && k == "double") || (old == "double" && k == "int"):
			f.kinds[e.Key] = "double"
		default:
			f.kinds[e.Key] = "mixed"
		}
	}
}

// names with _id first.
func (f *fieldSet) ordered() []string {
	out := make([]string, 0, len(f.names)+1)
	out = append(out, "_id")
	for _, n := range f.names {
		if n != "_id" {
			out = append(out, n)
		}
	}
	return out
}

func (f *fieldSet) columns() []driver.Column {
	names := f.ordered()
	cols := make([]driver.Column, len(names))
	for i, n := range names {
		t := f.kinds[n]
		if t == "" {
			t = "objectId"
		}
		cols[i] = driver.Column{Name: n, Type: t, Nullable: true, PrimaryKey: n == "_id"}
	}
	return cols
}

// tabulate aligns documents to the union of their fields.
func tabulate(docs []bson.D) ([]string, [][]any) {
	fs := newFieldSet()
	hasID := false
	for _, d := range docs {
		fs.add(d)
		for _, e := range d {
			if e.Key == "_id" {
				hasID = true
			}
		}
	}
	names := fs.ordered()
	if !hasID {
		names = names[1:]
	}
	return names, alignRows(names, docs)
}

func alignRows(names []string, docs []bson.D) [][]any {
	idx := make(map[string]int, len(names))
	for i, n := range names {
		idx[n] = i
	}
	rows := make([][]any, len(docs))
	for r, d := range docs {
		row := make([]any, len(names))
		for _, e := range d {
			if i, ok := idx[e.Key]; ok {
				row[i] = toValue(e.Value)
			}
		}
		rows[r] = row
	}
	return rows
}

// shell renders a BSON value in mongosh syntax for logs and query previews.
func shell(v any) string {
	var b strings.Builder
	writeShell(&b, v)
	return b.String()
}

func writeShell(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil, bson.Null, bson.Undefined:
		b.WriteString("null")
	case string:
		b.WriteString(jsonString(t))
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case int32:
		b.WriteString(strconv.FormatInt(int64(t), 10))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		if t > math.MaxInt32 || t < math.MinInt32 {
			fmt.Fprintf(b, "NumberLong(%d)", t)
		} else {
			b.WriteString(strconv.FormatInt(t, 10))
		}
	case float64:
		switch {
		case math.IsInf(t, 1):
			b.WriteString("Infinity")
		case math.IsInf(t, -1):
			b.WriteString("-Infinity")
		case math.IsNaN(t):
			b.WriteString("NaN")
		default:
			b.WriteString(strconv.FormatFloat(t, 'g', -1, 64))
		}
	case bson.ObjectID:
		fmt.Fprintf(b, "ObjectId(%q)", t.Hex())
	case bson.DateTime:
		fmt.Fprintf(b, "ISODate(%q)", t.Time().UTC().Format("2006-01-02T15:04:05.000Z"))
	case time.Time:
		fmt.Fprintf(b, "ISODate(%q)", t.UTC().Format("2006-01-02T15:04:05.000Z"))
	case bson.Decimal128:
		fmt.Fprintf(b, "NumberDecimal(%q)", t.String())
	case bson.Regex:
		b.WriteString("/" + strings.ReplaceAll(t.Pattern, "/", `\/`) + "/" + t.Options)
	case bson.Timestamp:
		fmt.Fprintf(b, "Timestamp(%d, %d)", t.T, t.I)
	case bson.MinKey:
		b.WriteString("MinKey")
	case bson.MaxKey:
		b.WriteString("MaxKey")
	case bson.D:
		b.WriteByte('{')
		for i, e := range t {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(jsonString(e.Key))
			b.WriteString(": ")
			writeShell(b, e.Value)
		}
		b.WriteByte('}')
	case bson.M:
		writeShell(b, docFields(t))
	case map[string]any:
		writeShell(b, docFields(bson.M(t)))
	case bson.A:
		writeShellArray(b, []any(t))
	case []any:
		writeShellArray(b, t)
	case []bson.D:
		a := make([]any, len(t))
		for i := range t {
			a[i] = t[i]
		}
		writeShellArray(b, a)
	default:
		b.WriteString(extJSON(v))
	}
}

func writeShellArray(b *strings.Builder, a []any) {
	b.WriteByte('[')
	for i, e := range a {
		if i > 0 {
			b.WriteString(", ")
		}
		writeShell(b, e)
	}
	b.WriteByte(']')
}

// parseDoc parses relaxed shell syntax into a document.
func parseDoc(src string) (bson.D, error) {
	if strings.TrimSpace(src) == "" {
		return bson.D{}, nil
	}
	js, err := RelaxedToJSON(src)
	if err != nil {
		return nil, err
	}
	return jsonDoc(js)
}

// jsonDoc decodes extended JSON text that must be a document.
func jsonDoc(js string) (bson.D, error) {
	v, err := jsonValue(js)
	if err != nil {
		return nil, err
	}
	d, ok := v.(bson.D)
	if !ok {
		return nil, fmt.Errorf("expected a document, got %s", kindOf(v))
	}
	return d, nil
}

// jsonValue decodes any extended JSON value (document, array or scalar).
func jsonValue(js string) (any, error) {
	var holder bson.D
	if err := bson.UnmarshalExtJSON([]byte(`{"v":`+js+`}`), false, &holder); err != nil {
		return nil, err
	}
	if len(holder) != 1 {
		return nil, fmt.Errorf("bad value")
	}
	return holder[0].Value, nil
}

// parseValue parses one relaxed shell value of any type.
func parseValue(src string) (any, error) {
	js, err := RelaxedToJSON(src)
	if err != nil {
		return nil, err
	}
	return jsonValue(js)
}

// collRef is how a collection is addressed in mongosh.
func collRef(name string) string {
	ok := name != ""
	for i := 0; i < len(name); i++ {
		if !isIdentPart(name[i]) || (i == 0 && name[i] >= '0' && name[i] <= '9') {
			ok = false
			break
		}
	}
	if ok && !shellReserved[name] {
		return "db." + name
	}
	return "db.getCollection(" + jsonString(name) + ")"
}

// Names that collide with db methods/properties must go through getCollection.
var shellReserved = map[string]bool{
	"getCollection": true, "getCollectionNames": true, "runCommand": true,
	"getName": true, "stats": true, "adminCommand": true, "getSiblingDB": true,
}
