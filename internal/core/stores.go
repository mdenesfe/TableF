package core

import (
	"context"
	"time"

	"github.com/mdenesfe/TableF/internal/driver"
)

// storeTableData loads a page of a non-SQL table.
func storeTableData(ctx context.Context, st driver.StoreConn, req TableDataRequest) (TableData, error) {
	match := make([]driver.Match, len(req.Match))
	for i, m := range req.Match {
		match[i] = driver.Match{Column: m.Column, Value: m.Value}
	}
	start := time.Now()
	res, err := st.Rows(ctx, driver.RowsRequest{
		Table: req.Table, Limit: req.Limit, Offset: req.Offset,
		OrderBy: req.OrderBy, OrderDir: req.OrderDir, Filter: req.Where, Match: match,
	})
	if err != nil {
		return TableData{}, err
	}
	for _, r := range res.Rows {
		for i, v := range r {
			r[i] = normalize(v)
		}
	}
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	return TableData{
		Columns: res.Columns, Rows: res.Rows, Total: res.Total, SQL: res.Query,
		DurationMs:  float64(time.Since(start).Microseconds()) / 1000,
		ForeignKeys: []driver.ForeignKey{}, ReferencedBy: []driver.ForeignKey{},
	}, nil
}

// storeExecute runs native commands one by one, stopping at the first error.
func storeExecute(ctx context.Context, st driver.StoreConn, cmds []string) []QueryResult {
	results := make([]QueryResult, 0, len(cmds))
	for _, c := range cmds {
		res := QueryResult{Statement: c, Columns: []string{}, ColumnTypes: []string{}, Rows: [][]any{}}
		start := time.Now()
		r, err := st.Execute(ctx, c)
		res.DurationMs = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			break
		}
		if r.Columns != nil {
			res.Columns = r.Columns
		}
		for _, row := range r.Rows {
			if len(res.Rows) >= MaxQueryRows {
				res.Truncated = true
				break
			}
			for i, v := range row {
				row[i] = normalize(v)
			}
			res.Rows = append(res.Rows, row)
		}
		res.RowsAffected = r.RowsAffected
		results = append(results, res)
	}
	return results
}

func rowChanges(us []RowUpdate) []driver.RowChange {
	out := make([]driver.RowChange, len(us))
	for i, u := range us {
		out[i] = driver.RowChange{Keys: u.Keys, Values: u.Values}
	}
	return out
}
