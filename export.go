package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// exportFormats are the file formats export_query can write.
var exportFormats = []string{"csv", "json", "ndjson"}

// inferFormat guesses the format from the file extension.
func inferFormat(destPath string) (string, error) {
	switch strings.ToLower(filepath.Ext(destPath)) {
	case ".csv", ".tsv":
		return "csv", nil
	case ".json":
		return "json", nil
	case ".ndjson":
		return "ndjson", nil
	}
	return "", fmt.Errorf("cannot infer format from extension %q; pass format explicitly", filepath.Ext(destPath))
}

// exportQuery streams a read-only query into destPath without materializing
// all rows. limit <= 0 means unlimited.
func exportQuery(conn *sql.DB, sqlText, destPath, format string, limit int) (int, error) {
	if violation := readOnlyViolation(sqlText); violation != "" {
		return 0, fmt.Errorf("Refused (read-only server): %s", violation)
	}
	dir := filepath.Dir(destPath)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return 0, fmt.Errorf("parent directory does not exist: %s", dir)
	}
	rows, err := conn.Query(sqlText)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	var written int
	var writeErr error
	switch format {
	case "csv":
		written, writeErr = writeCSV(f, rows, columns, limit)
	case "json":
		written, writeErr = writeJSONRows(f, rows, columns, limit)
	case "ndjson":
		written, writeErr = writeNDJSON(f, rows, columns, limit)
	default:
		writeErr = fmt.Errorf("format must be one of %s (got %q)", strings.Join(exportFormats, ", "), format)
	}
	if cerr := f.Close(); writeErr == nil {
		writeErr = cerr
	}
	return written, writeErr
}

// writeCSV writes a header row and one line per row, stringifying every value.
func writeCSV(f *os.File, rows *sql.Rows, columns []string, limit int) (int, error) {
	w := csv.NewWriter(f)
	if err := w.Write(columns); err != nil {
		return 0, err
	}
	n, err := streamRows(rows, columns, limit, func(row []any) error {
		rec := make([]string, len(row))
		for i, v := range row {
			rec[i] = stringify(v)
		}
		return w.Write(rec)
	})
	w.Flush()
	if err != nil {
		return n, err
	}
	return n, w.Error()
}

// writeJSONRows writes a streamed JSON array of row objects.
func writeJSONRows(f *os.File, rows *sql.Rows, columns []string, limit int) (int, error) {
	if _, err := f.WriteString("["); err != nil {
		return 0, err
	}
	enc := json.NewEncoder(f)
	first := true
	n, err := streamRows(rows, columns, limit, func(row []any) error {
		if !first {
			if _, err := f.WriteString(","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(rowObject(columns, row))
	})
	if err != nil {
		return n, err
	}
	_, err = f.WriteString("]\n")
	return n, err
}

// writeNDJSON writes one row object per line.
func writeNDJSON(f *os.File, rows *sql.Rows, columns []string, limit int) (int, error) {
	enc := json.NewEncoder(f)
	return streamRows(rows, columns, limit, func(row []any) error {
		return enc.Encode(rowObject(columns, row))
	})
}

// rowObject maps a positional row onto its column names.
func rowObject(columns []string, row []any) map[string]any {
	obj := make(map[string]any, len(row))
	for i, v := range row {
		if i < len(columns) {
			obj[columns[i]] = v
		}
	}
	return obj
}

// streamRows reads rows while writing them, stopping after limit rows when
// limit > 0.
func streamRows(rows *sql.Rows, columns []string, limit int, emit func([]any) error) (int, error) {
	scan := newRowScan(len(columns))
	n := 0
	for rows.Next() {
		if limit > 0 && n >= limit {
			break
		}
		row, err := scan.row(rows)
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

// stringify renders a converted value for CSV output.
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
