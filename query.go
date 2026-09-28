package main

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"
	"unicode/utf8"
)

// jsonValue converts a driver or Redis value into a JSON-safe value:
// nil, bool, int64, float64, string, or a composite of those.
func jsonValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case bool, int64, float64, string:
		return t
	case int:
		return int64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		return int64(t)
	case float32:
		return float64(t)
	case []byte:
		if utf8.Valid(t) {
			return string(t)
		}
		return "b64:" + base64.StdEncoding.EncodeToString(t)
	case time.Time:
		return t.Format(time.RFC3339)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = jsonValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = jsonValue(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprintf("%v", k)] = jsonValue(e)
		}
		return out
	default:
		return fmt.Sprintf("%v", t)
	}
}

// rowScan holds the reusable buffer set for scanning one result row.
type rowScan struct {
	dest []any
	ptrs []any
}

// newRowScan allocates a scan buffer for n columns.
func newRowScan(n int) *rowScan {
	return &rowScan{
		dest: make([]any, n),
		ptrs: make([]any, n),
	}
}

// row scans the current row and returns the JSON-safe values.
func (s *rowScan) row(rows *sql.Rows) ([]any, error) {
	for i := range s.dest {
		s.ptrs[i] = &s.dest[i]
	}
	if err := rows.Scan(s.ptrs...); err != nil {
		return nil, err
	}
	out := make([]any, len(s.dest))
	for i, v := range s.dest {
		out[i] = jsonValue(v)
	}
	return out, nil
}

// queryResult is the shared shape of run_query and export_query streaming.
type queryResult struct {
	Columns   []string
	Rows      [][]any
	RowCount  int
	Truncated bool
}

// runQuery screens and executes a read-only SQL query, collecting at most limit
// rows (limit <= 0 means unlimited).
func runQuery(conn *sql.DB, sqlText string, limit int) (*queryResult, error) {
	if violation := readOnlyViolation(sqlText); violation != "" {
		return nil, fmt.Errorf("Refused (read-only server): %s", violation)
	}
	rows, err := conn.Query(sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &queryResult{Columns: columns}
	scan := newRowScan(len(columns))
	for rows.Next() {
		if limit > 0 && res.RowCount >= limit {
			res.Truncated = true
			break
		}
		row, err := scan.row(rows)
		if err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, row)
		res.RowCount++
	}
	return res, rows.Err()
}
