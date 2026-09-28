// Package query screens and runs one read-only SQL statement, collecting its
// rows as JSON-safe values.
package query

import (
	"database/sql"
	"fmt"

	"hq-mcp/internal/readonly"
	"hq-mcp/internal/sqlrows"
)

// Result is the shared shape of run_query and export_query streaming.
type Result struct {
	Columns   []string
	Rows      [][]any
	RowCount  int
	Truncated bool
}

// Run screens and executes a read-only SQL query, collecting at most limit
// rows (limit <= 0 means unlimited).
func Run(conn *sql.DB, sqlText string, limit int) (*Result, error) {
	if violation := readonly.Violation(sqlText); violation != "" {
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
	res := &Result{Columns: columns}
	scan := sqlrows.NewScan(len(columns))
	for rows.Next() {
		if limit > 0 && res.RowCount >= limit {
			res.Truncated = true
			break
		}
		row, err := scan.Row(rows)
		if err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, row)
		res.RowCount++
	}
	return res, rows.Err()
}
