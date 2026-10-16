package mongodb

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/mdenesfe/TableF/internal/driver"
)

func (c *conn) Split(script string) []string { return splitScript(script) }

// Execute runs one mongosh-style command.
func (c *conn) Execute(ctx context.Context, command string) (driver.Result, error) {
	defer c.forgetSamples() // the command may have written documents
	call, err := parseShell(command)
	if err != nil {
		return driver.Result{}, err
	}
	switch call.Kind {
	case "command":
		doc, err := jsonDoc(call.Args[0])
		if err != nil {
			return driver.Result{}, err
		}
		return c.runCommand(ctx, c.db, doc)
	case "show":
		return c.show(ctx, call.Method)
	case "db":
		return c.dbMethod(ctx, call)
	}
	return c.collMethod(ctx, call)
}

func docsResult(docs []bson.D) driver.Result {
	cols, rows := tabulate(docs)
	return driver.Result{Columns: cols, Rows: rows}
}

func listResult(col string, vals []any) driver.Result {
	rows := make([][]any, len(vals))
	for i, v := range vals {
		rows[i] = []any{toValue(v)}
	}
	return driver.Result{Columns: []string{col}, Rows: rows}
}

func (c *conn) runCommand(ctx context.Context, db *mongo.Database, cmd bson.D) (driver.Result, error) {
	var res bson.D
	if err := db.RunCommand(ctx, cmd).Decode(&res); err != nil {
		return driver.Result{}, err
	}
	// Commands that return a cursor (find, aggregate, listCollections…) show its first batch.
	for _, e := range res {
		if e.Key != "cursor" {
			continue
		}
		if cur, ok := e.Value.(bson.D); ok {
			for _, ce := range cur {
				if ce.Key == "firstBatch" {
					if a, ok := ce.Value.(bson.A); ok {
						return docsResult(docsOf(a)), nil
					}
				}
			}
		}
	}
	return docsResult([]bson.D{res}), nil
}

func docsOf(a bson.A) []bson.D {
	out := make([]bson.D, 0, len(a))
	for _, v := range a {
		if d := docFields(v); d != nil {
			out = append(out, d)
		} else {
			out = append(out, bson.D{{Key: "value", Value: v}})
		}
	}
	return out
}

func (c *conn) show(ctx context.Context, what string) (driver.Result, error) {
	switch strings.Join(strings.Fields(what), " ") {
	case "show dbs", "show databases":
		dbs, err := c.Databases(ctx)
		if err != nil {
			return driver.Result{}, err
		}
		vals := make([]any, len(dbs))
		for i, d := range dbs {
			vals[i] = d
		}
		return listResult("name", vals), nil
	case "show collections", "show tables":
		return c.collectionNames(ctx)
	}
	if strings.HasPrefix(what, "use ") {
		return driver.Result{}, errors.New("switch the database from the database picker instead of use")
	}
	return driver.Result{}, fmt.Errorf("unsupported command %q", what)
}

func (c *conn) collectionNames(ctx context.Context) (driver.Result, error) {
	names, err := c.db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return driver.Result{}, err
	}
	sort.Strings(names)
	vals := make([]any, len(names))
	for i, n := range names {
		vals[i] = n
	}
	return listResult("name", vals), nil
}

func (c *conn) dbMethod(ctx context.Context, call shellCall) (driver.Result, error) {
	if len(call.Chain) > 0 {
		return driver.Result{}, fmt.Errorf("db.%s() can't be chained", call.Method)
	}
	switch call.Method {
	case "getCollectionNames":
		return c.collectionNames(ctx)
	case "getName":
		return listResult("name", []any{c.dbName}), nil
	case "runCommand", "adminCommand":
		cmd, err := argDoc(call.Args, 0, "command")
		if err != nil {
			return driver.Result{}, err
		}
		if len(cmd) == 0 {
			return driver.Result{}, fmt.Errorf("db.%s needs a command document", call.Method)
		}
		db := c.db
		if call.Method == "adminCommand" {
			db = c.client.Database("admin")
		}
		return c.runCommand(ctx, db, cmd)
	case "stats":
		return c.runCommand(ctx, c.db, bson.D{{Key: "dbStats", Value: 1}})
	case "version":
		return listResult("version", []any{strings.TrimPrefix(c.Version(ctx), "MongoDB ")}), nil
	case "createCollection":
		name, err := argString(call.Args, 0, "name")
		if err != nil {
			return driver.Result{}, err
		}
		if err := c.db.CreateCollection(ctx, name); err != nil {
			return driver.Result{}, err
		}
		return listResult("ok", []any{int32(1)}), nil
	}
	return driver.Result{}, fmt.Errorf("unsupported method db.%s()", call.Method)
}

// argDoc decodes argument i as a document; a missing argument is {}.
func argDoc(args []string, i int, what string) (bson.D, error) {
	if i >= len(args) {
		return bson.D{}, nil
	}
	v, err := jsonValue(args[i])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	switch d := v.(type) {
	case bson.D:
		return d, nil
	case nil:
		return bson.D{}, nil
	}
	return nil, fmt.Errorf("%s must be a document", what)
}

func argValue(args []string, i int) (any, error) {
	if i >= len(args) {
		return nil, nil
	}
	return jsonValue(args[i])
}

func argString(args []string, i int, what string) (string, error) {
	v, err := argValue(args, i)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", what)
	}
	return s, nil
}

func argInt(args []string, i int, what string) (int64, error) {
	v, err := argValue(args, i)
	if err != nil {
		return 0, err
	}
	switch n := v.(type) {
	case int32:
		return int64(n), nil
	case int64:
		return n, nil
	case float64:
		if n == math.Trunc(n) {
			return int64(n), nil
		}
	}
	return 0, fmt.Errorf("%s must be a whole number", what)
}

// argDocs decodes argument i as an array of documents.
func argDocs(args []string, i int, what string) (bson.A, error) {
	v, err := argValue(args, i)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	a, ok := v.(bson.A)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", what)
	}
	return a, nil
}

func optBool(d bson.D, key string) bool {
	for _, e := range d {
		if e.Key == key {
			b, _ := e.Value.(bool)
			return b
		}
	}
	return false
}

func readCursor(ctx context.Context, cur *mongo.Cursor, max int) ([]bson.D, error) {
	defer cur.Close(ctx)
	var docs []bson.D
	for len(docs) < max && cur.Next(ctx) {
		var d bson.D
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, cur.Err()
}

func (c *conn) collMethod(ctx context.Context, call shellCall) (driver.Result, error) {
	coll := c.db.Collection(call.Coll)
	a := call.Args
	switch call.Method {
	case "find":
		return c.find(ctx, coll, call)
	case "findOne":
		filter, err := argDoc(a, 0, "filter")
		if err != nil {
			return driver.Result{}, err
		}
		fo := options.FindOne()
		if len(a) > 1 {
			proj, err := argDoc(a, 1, "projection")
			if err != nil {
				return driver.Result{}, err
			}
			fo.SetProjection(proj)
		}
		var d bson.D
		err = coll.FindOne(ctx, filter, fo).Decode(&d)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return driver.Result{Columns: []string{}}, nil
		}
		if err != nil {
			return driver.Result{}, err
		}
		return docsResult([]bson.D{d}), nil
	case "countDocuments", "count":
		filter, err := argDoc(a, 0, "filter")
		if err != nil {
			return driver.Result{}, err
		}
		n, err := coll.CountDocuments(ctx, filter)
		if err != nil {
			return driver.Result{}, err
		}
		return listResult("count", []any{n}), nil
	case "estimatedDocumentCount":
		n, err := coll.EstimatedDocumentCount(ctx)
		if err != nil {
			return driver.Result{}, err
		}
		return listResult("count", []any{n}), nil
	case "aggregate":
		var pipeline bson.A
		if v, err := argValue(a, 0); err != nil {
			return driver.Result{}, fmt.Errorf("pipeline: %w", err)
		} else if arr, ok := v.(bson.A); ok {
			pipeline = arr
		} else {
			// aggregate(stage1, stage2, …)
			for i := range a {
				st, err := argDoc(a, i, "stage")
				if err != nil {
					return driver.Result{}, err
				}
				pipeline = append(pipeline, st)
			}
		}
		if pipeline == nil {
			pipeline = bson.A{}
		}
		cur, err := coll.Aggregate(ctx, pipeline)
		if err != nil {
			return driver.Result{}, err
		}
		docs, err := readCursor(ctx, cur, maxShellRows)
		if err != nil {
			return driver.Result{}, err
		}
		return docsResult(docs), nil
	case "distinct":
		field, err := argString(a, 0, "field")
		if err != nil {
			return driver.Result{}, err
		}
		filter, err := argDoc(a, 1, "filter")
		if err != nil {
			return driver.Result{}, err
		}
		var vals bson.A
		if err := coll.Distinct(ctx, field, filter).Decode(&vals); err != nil {
			return driver.Result{}, err
		}
		return listResult(field, vals), nil
	case "insertOne":
		doc, err := argDoc(a, 0, "document")
		if err != nil {
			return driver.Result{}, err
		}
		res, err := coll.InsertOne(ctx, doc)
		if err != nil {
			return driver.Result{}, err
		}
		r := listResult("insertedId", []any{res.InsertedID})
		r.RowsAffected = 1
		return r, nil
	case "insertMany":
		docs, err := argDocs(a, 0, "documents")
		if err != nil {
			return driver.Result{}, err
		}
		res, err := coll.InsertMany(ctx, docs)
		if err != nil {
			return driver.Result{}, err
		}
		r := listResult("insertedId", res.InsertedIDs)
		r.RowsAffected = int64(len(res.InsertedIDs))
		return r, nil
	case "updateOne", "updateMany", "replaceOne":
		filter, err := argDoc(a, 0, "filter")
		if err != nil {
			return driver.Result{}, err
		}
		update, err := argValue(a, 1)
		if err != nil {
			return driver.Result{}, fmt.Errorf("update: %w", err)
		}
		if update == nil {
			return driver.Result{}, fmt.Errorf("%s needs an update document", call.Method)
		}
		opt, err := argDoc(a, 2, "options")
		if err != nil {
			return driver.Result{}, err
		}
		upsert := optBool(opt, "upsert")
		var res *mongo.UpdateResult
		switch call.Method {
		case "updateOne":
			res, err = coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(upsert))
		case "updateMany":
			res, err = coll.UpdateMany(ctx, filter, update, options.UpdateMany().SetUpsert(upsert))
		default:
			res, err = coll.ReplaceOne(ctx, filter, update, options.Replace().SetUpsert(upsert))
		}
		if err != nil {
			return driver.Result{}, err
		}
		return driver.Result{
			Columns:      []string{"matchedCount", "modifiedCount", "upsertedId"},
			Rows:         [][]any{{res.MatchedCount, res.ModifiedCount, toValue(res.UpsertedID)}},
			RowsAffected: res.ModifiedCount + res.UpsertedCount,
		}, nil
	case "deleteOne", "deleteMany":
		filter, err := argDoc(a, 0, "filter")
		if err != nil {
			return driver.Result{}, err
		}
		var res *mongo.DeleteResult
		if call.Method == "deleteOne" {
			res, err = coll.DeleteOne(ctx, filter)
		} else {
			res, err = coll.DeleteMany(ctx, filter)
		}
		if err != nil {
			return driver.Result{}, err
		}
		r := listResult("deletedCount", []any{res.DeletedCount})
		r.RowsAffected = res.DeletedCount
		return r, nil
	case "getIndexes":
		cur, err := coll.Indexes().List(ctx)
		if err != nil {
			return driver.Result{}, err
		}
		docs, err := readCursor(ctx, cur, maxShellRows)
		if err != nil {
			return driver.Result{}, err
		}
		return docsResult(docs), nil
	case "drop":
		if err := coll.Drop(ctx); err != nil {
			return driver.Result{}, err
		}
		return listResult("ok", []any{int32(1)}), nil
	}
	return driver.Result{}, fmt.Errorf("unsupported method %s.%s()", collRef(call.Coll), call.Method)
}

func (c *conn) find(ctx context.Context, coll *mongo.Collection, call shellCall) (driver.Result, error) {
	filter, err := argDoc(call.Args, 0, "filter")
	if err != nil {
		return driver.Result{}, err
	}
	fo := options.Find()
	if len(call.Args) > 1 {
		proj, err := argDoc(call.Args, 1, "projection")
		if err != nil {
			return driver.Result{}, err
		}
		fo.SetProjection(proj)
	}
	limit := int64(maxShellRows)
	for _, ch := range call.Chain {
		switch ch.Method {
		case "sort":
			s, err := argDoc(ch.Args, 0, "sort")
			if err != nil {
				return driver.Result{}, err
			}
			fo.SetSort(s)
		case "limit":
			n, err := argInt(ch.Args, 0, "limit")
			if err != nil {
				return driver.Result{}, err
			}
			if n < 0 {
				n = -n
			}
			if n > 0 && n < limit {
				limit = n
			}
		case "skip":
			n, err := argInt(ch.Args, 0, "skip")
			if err != nil {
				return driver.Result{}, err
			}
			fo.SetSkip(n)
		case "projection", "project":
			p, err := argDoc(ch.Args, 0, "projection")
			if err != nil {
				return driver.Result{}, err
			}
			fo.SetProjection(p)
		case "count", "countDocuments", "size":
			n, err := coll.CountDocuments(ctx, filter)
			if err != nil {
				return driver.Result{}, err
			}
			return listResult("count", []any{n}), nil
		case "toArray", "pretty", "batchSize", "maxTimeMS", "allowDiskUse":
		default:
			return driver.Result{}, fmt.Errorf("unsupported cursor method .%s()", ch.Method)
		}
	}
	fo.SetLimit(limit)
	cur, err := coll.Find(ctx, filter, fo)
	if err != nil {
		return driver.Result{}, err
	}
	docs, err := readCursor(ctx, cur, int(limit))
	if err != nil {
		return driver.Result{}, err
	}
	return docsResult(docs), nil
}

// ---- Apply ----

// idCandidates lists what a grid key value may have been stored as: hex
// strings may be ObjectIDs, UUID strings may be binary UUIDs.
func idCandidates(v any) []any {
	s, ok := v.(string)
	if !ok {
		return []any{v}
	}
	var out []any
	if oid, err := bson.ObjectIDFromHex(s); err == nil {
		out = append(out, oid)
	}
	if len(s) == 36 && strings.Count(s, "-") == 4 {
		if b, err := hex.DecodeString(strings.ReplaceAll(s, "-", "")); err == nil && len(b) == 16 {
			out = append(out, bson.Binary{Subtype: 4, Data: b}, bson.Binary{Subtype: 3, Data: b})
		}
	}
	return append(out, s)
}

// resolveID finds the stored _id for a grid key and returns it with the document.
func resolveID(ctx context.Context, coll *mongo.Collection, keys map[string]any) (any, bson.D, error) {
	raw, ok := keys["_id"]
	if !ok {
		return nil, nil, errors.New("the row has no _id")
	}
	cands := idCandidates(raw)
	for _, id := range cands {
		var d bson.D
		err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&d)
		if err == nil {
			return id, d, nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil, err
		}
	}
	return nil, nil, fmt.Errorf("document with _id %v not found", raw)
}

func (c *conn) Apply(ctx context.Context, table string, ch driver.Changes) ([]string, error) {
	defer c.forgetSamples()
	coll := c.db.Collection(table)
	ref := collRef(table)
	var log []string

	for _, u := range ch.Updates {
		if _, ok := u.Values["_id"]; ok {
			return log, errors.New("_id can't be changed; insert a new document instead")
		}
		if len(u.Values) == 0 {
			continue
		}
		id, doc, err := resolveID(ctx, coll, u.Keys)
		if err != nil {
			return log, err
		}
		old := map[string]any{}
		for _, e := range doc {
			old[e.Key] = e.Value
		}
		set := bson.D{}
		for _, k := range slices.Sorted(maps.Keys(u.Values)) {
			v, err := coerce(k, u.Values[k], kindOf(old[k]), old[k])
			if err != nil {
				return log, err
			}
			set = append(set, bson.E{Key: k, Value: v})
		}
		filter := bson.D{{Key: "_id", Value: id}}
		update := bson.D{{Key: "$set", Value: set}}
		stmt := ref + ".updateOne(" + shell(filter) + ", " + shell(update) + ")"
		if _, err := coll.UpdateOne(ctx, filter, update); err != nil {
			return log, fmt.Errorf("%s: %w", stmt, err)
		}
		log = append(log, stmt)
	}

	if len(ch.Inserts) > 0 {
		// Inserted values take the type the field has elsewhere in the collection.
		fs, err := c.sampleFields(ctx, table)
		if err != nil {
			fs = newFieldSet()
		}
		for _, row := range ch.Inserts {
			doc := bson.D{}
			if id, ok := row["_id"]; ok && id != nil && id != "" {
				v := id
				if s, ok := id.(string); ok {
					if oid, err := bson.ObjectIDFromHex(s); err == nil {
						v = oid
					}
				}
				doc = append(doc, bson.E{Key: "_id", Value: v})
			}
			for _, k := range slices.Sorted(maps.Keys(row)) {
				if k == "_id" || row[k] == nil {
					continue
				}
				v, err := coerce(k, row[k], fs.kinds[k], nil)
				if err != nil {
					return log, err
				}
				doc = append(doc, bson.E{Key: k, Value: v})
			}
			stmt := ref + ".insertOne(" + shell(doc) + ")"
			if _, err := coll.InsertOne(ctx, doc); err != nil {
				return log, fmt.Errorf("%s: %w", stmt, err)
			}
			log = append(log, stmt)
		}
	}

	for _, keys := range ch.Deletes {
		id, _, err := resolveID(ctx, coll, keys)
		if err != nil {
			return log, err
		}
		filter := bson.D{{Key: "_id", Value: id}}
		stmt := ref + ".deleteOne(" + shell(filter) + ")"
		if _, err := coll.DeleteOne(ctx, filter); err != nil {
			return log, fmt.Errorf("%s: %w", stmt, err)
		}
		log = append(log, stmt)
	}
	return log, nil
}

var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02",
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func looksJSON(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 2 && (s[0] == '{' && s[len(s)-1] == '}' || s[0] == '[' && s[len(s)-1] == ']')
}

// coerce converts a value edited in the grid back to the field's BSON type.
// kind is the field's current type (from the document or a sample); like is
// the current value when known. Text keeps being text when it doesn't parse
// as the field's type, except objects and arrays, which must be valid
// (relaxed) JSON. For fields without a single known type, text that looks
// like a JSON object or array is parsed back into one.
func coerce(field string, v any, kind string, like any) (any, error) {
	if v == nil {
		return nil, nil
	}
	f, isFloat := v.(float64)
	s, isStr := v.(string)
	switch kind {
	case "string":
		if isStr {
			return s, nil
		}
	case "objectId":
		if isStr {
			if oid, err := bson.ObjectIDFromHex(strings.TrimSpace(s)); err == nil {
				return oid, nil
			}
		}
	case "date":
		if isStr {
			if t, ok := parseDate(s); ok {
				return bson.NewDateTimeFromTime(t), nil
			}
		}
	case "int":
		var n int64
		ok := false
		if isStr {
			if x, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				n, ok = x, true
			}
		} else if isFloat && f == math.Trunc(f) && math.Abs(f) < 1<<63 {
			n, ok = int64(f), true
		}
		if ok {
			if _, is64 := like.(int64); !is64 && n >= math.MinInt32 && n <= math.MaxInt32 {
				return int32(n), nil
			}
			return n, nil
		}
	case "double":
		if isStr {
			if x, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				return x, nil
			}
		}
	case "bool":
		if isStr {
			if b, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
				return b, nil
			}
		}
	case "decimal":
		if isStr {
			if d, err := bson.ParseDecimal128(strings.TrimSpace(s)); err == nil {
				return d, nil
			}
		} else if isFloat {
			if d, err := bson.ParseDecimal128(strconv.FormatFloat(f, 'f', -1, 64)); err == nil {
				return d, nil
			}
		}
	case "object", "array":
		if isStr {
			pv, err := parseValue(s)
			if err != nil {
				return nil, fmt.Errorf("%s: not a valid %s: %w", field, kind, err)
			}
			return pv, nil
		}
	default:
		if isStr && looksJSON(s) {
			if pv, err := parseValue(s); err == nil {
				return pv, nil
			}
		}
	}
	if isFloat && f == math.Trunc(f) && math.Abs(f) <= 1<<53 && kind != "double" {
		if f >= math.MinInt32 && f <= math.MaxInt32 {
			return int32(f), nil
		}
		return int64(f), nil
	}
	return v, nil
}
