// Package sqlrows scans and streams database/sql result rows as JSON-safe
// values.
package sqlrows

import (
	"database/sql"

	"hq-mcp/internal/values"
)

// Scan holds the reusable buffer set for scanning one result row.
type Scan struct {
	dest []any
	ptrs []any
}

// NewScan allocates a scan buffer for n columns.
func NewScan(n int) *Scan {
	return &Scan{
		dest: make([]any, n),
		ptrs: make([]any, n),
	}
}

// Row scans the current row and returns the JSON-safe values.
func (s *Scan) Row(rows *sql.Rows) ([]any, error) {
	for i := range s.dest {
		s.ptrs[i] = &s.dest[i]
	}
	if err := rows.Scan(s.ptrs...); err != nil {
		return nil, err
	}
	out := make([]any, len(s.dest))
	for i, v := range s.dest {
		out[i] = values.JSON(v)
	}
	return out, nil
}

// Stream reads rows while handing them to emit, stopping after limit rows
// when limit > 0.
func Stream(rows *sql.Rows, columns []string, limit int, emit func([]any) error) (int, error) {
	scan := NewScan(len(columns))
	n := 0
	for rows.Next() {
		if limit > 0 && n >= limit {
			break
		}
		row, err := scan.Row(rows)
		if err != nil {
			return n, err
		}
		if err := emit(row); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// Object maps a positional row onto its column names.
func Object(columns []string, row []any) map[string]any {
	obj := make(map[string]any, len(row))
	for i, v := range row {
		if i < len(columns) {
			obj[columns[i]] = v
		}
	}
	return obj
}
