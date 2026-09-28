package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// maxSchemaChars caps the marshaled get_schema payload; larger results are
// refused so the caller drills down instead of dumping a whole catalog.
const maxSchemaChars = 60000

// maxSchemaNodes caps how many nodes one level returns.
const maxSchemaNodes = 500

// Redis schema scanning limits.
const (
	redisScanCount   = 500
	redisMaxKeys     = 10000
	redisTypeSamples = 25
)

// SchemaNode is one entry of a get_schema level.
type SchemaNode struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	Type  string `json:"type,omitempty"`
}

// Column describes one column of a table.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable"`
}

// SchemaOut is the get_schema response.
type SchemaOut struct {
	Nodes     []SchemaNode `json:"nodes"`
	Columns   []Column     `json:"columns,omitempty"`
	Truncated bool         `json:"truncated"`
}

// ColumnsOut is the get_columns response.
type ColumnsOut struct {
	Table   string   `json:"table"`
	Columns []Column `json:"columns"`
}

const sharedTablesQuery = `SELECT table_name, table_type FROM information_schema.tables ` +
	`WHERE table_schema = ? AND lower(table_name) LIKE lower(?) ORDER BY table_name`

const columnsQuery = `SELECT column_name, data_type, is_nullable FROM information_schema.columns ` +
	`WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`

// systemSchemas are the built-in schemas hidden from get_schema.
var systemSchemas = map[string][]string{
	"postgres": {"pg_catalog", "information_schema"},
	"mysql":    {"mysql", "information_schema", "performance_schema", "sys"},
	"mssql":    {"sys", "INFORMATION_SCHEMA", "guest"},
}

// getSchema returns the children of the level addressed by path.
func getSchema(ctx context.Context, conn DB, path []string, nameFilter string, includeColumns bool) (*SchemaOut, error) {
	var (
		nodes     []SchemaNode
		truncated bool
		err       error
	)
	switch c := conn.(type) {
	case *redisDB:
		nodes, truncated, err = redisSchemaNodes(ctx, c, path, nameFilter, includeColumns)
	case *sqlDB:
		nodes, err = sqlSchemaNodes(ctx, c, path, nameFilter)
	default:
		return nil, fmt.Errorf("get_schema is not available for %s", conn.Kind())
	}
	if err != nil {
		return nil, err
	}
	out := &SchemaOut{Nodes: nodes, Truncated: truncated}
	if b, err := json.Marshal(out); err == nil && len(b) > maxSchemaChars {
		return nil, fmt.Errorf("result too large; drill down using path")
	}
	return out, nil
}

// sqlSchemaNodes dispatches the per-adapter catalog queries.
func sqlSchemaNodes(ctx context.Context, conn *sqlDB, path []string, nameFilter string) ([]SchemaNode, error) {
	switch conn.Kind() {
	case "postgres", "mysql", "mssql":
		return relationalSchema(ctx, conn, path, nameFilter)
	case "clickhouse":
		return clickhouseSchema(ctx, conn, path, nameFilter)
	default:
		return nil, fmt.Errorf("get_schema is not available for %s", conn.Kind())
	}
}

// rebindPlaceholders rewrites '?' placeholders into the form the driver
// expects: pgx wants $1, $2, ... while mysql, mssql, and clickhouse accept '?'.
func rebindPlaceholders(query string, kind string) string {
	if kind != "postgres" {
		return query
	}
	var sb strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			sb.WriteString("$" + strconv.Itoa(n))
			continue
		}
		sb.WriteByte(query[i])
	}
	return sb.String()
}

// relationalSchema walks databases, then schemas, then tables.
func relationalSchema(ctx context.Context, conn *sqlDB, path []string, nameFilter string) ([]SchemaNode, error) {
	kind := conn.Kind()
	tables := func(schema string) ([]SchemaNode, error) {
		rows, err := conn.db.QueryContext(ctx, rebindPlaceholders(sharedTablesQuery, kind), schema, likeFilter(nameFilter))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanTables(rows)
	}
	if kind == "mysql" {
		// mysql has no schema level: path is [database] then [database, table].
		switch len(path) {
		case 0:
			excluded := systemSchemas[kind]
			return scanOneColumn(ctx, conn, `SELECT schema_name FROM information_schema.schemata `+
				`WHERE schema_name NOT IN (`+placeholders(len(excluded))+`) ORDER BY schema_name`,
				"database", toArgs(excluded)...)
		case 1:
			return tables(path[0])
		default:
			return nil, fmt.Errorf("path %q is too deep for mysql; expected [database] or [database, table]",
				strings.Join(path, "."))
		}
	}
	// postgres and mssql serve one database per connection.
	switch len(path) {
	case 0:
		q := `SELECT current_database()`
		if kind == "mssql" {
			q = `SELECT DB_NAME()`
		}
		var name string
		if err := conn.db.QueryRowContext(ctx, q).Scan(&name); err != nil {
			return nil, err
		}
		return []SchemaNode{{Name: name, Kind: "database"}}, nil
	case 1:
		excluded := systemSchemas[kind]
		return scanOneColumn(ctx, conn, `SELECT schema_name FROM information_schema.schemata `+
			`WHERE schema_name NOT IN (`+placeholders(len(excluded))+`) ORDER BY schema_name`,
			"schema", toArgs(excluded)...)
	case 2:
		return tables(path[1])
	default:
		return nil, fmt.Errorf("path %q is too deep for %s; expected [database], [database, schema], or [database, schema, table]",
			strings.Join(path, "."), kind)
	}
}

// clickhouseSchema walks databases, then tables (ClickHouse has no schemas).
func clickhouseSchema(ctx context.Context, conn *sqlDB, path []string, nameFilter string) ([]SchemaNode, error) {
	switch len(path) {
	case 0:
		return scanOneColumn(ctx, conn, `SELECT name FROM system.databases ORDER BY name`, "database")
	case 1:
		rows, err := conn.db.QueryContext(ctx,
			`SELECT name, engine FROM system.tables WHERE database = ? AND positionCaseInsensitive(name, ?) > 0 ORDER BY name`,
			path[0], nameFilter)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanTables(rows)
	default:
		return nil, fmt.Errorf("path %q is too deep for clickhouse; expected [database] or [database, table]",
			strings.Join(path, "."))
	}
}

// scanOneColumn reads a one-column result set into nodes of the given kind.
func scanOneColumn(ctx context.Context, conn *sqlDB, query string, kind string, args ...any) ([]SchemaNode, error) {
	rows, err := conn.db.QueryContext(ctx, rebindPlaceholders(query, conn.Kind()), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []SchemaNode
	for rows.Next() {
		var name sql.NullString
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !name.Valid {
			continue
		}
		nodes = append(nodes, SchemaNode{Name: name.String, Kind: kind})
	}
	return nodes, rows.Err()
}

// scanTables reads a name/table_type (or name/engine) result set into nodes.
func scanTables(rows *sql.Rows) ([]SchemaNode, error) {
	var nodes []SchemaNode
	for rows.Next() {
		var name, detail string
		if err := rows.Scan(&name, &detail); err != nil {
			return nil, err
		}
		kind := "table"
		if strings.Contains(strings.ToUpper(detail), "VIEW") {
			kind = "view"
		}
		nodes = append(nodes, SchemaNode{Name: name, Kind: kind})
	}
	return nodes, rows.Err()
}

// likeFilter turns a name filter into a LIKE pattern.
func likeFilter(nameFilter string) string {
	if nameFilter == "" {
		return "%%"
	}
	return "%" + nameFilter + "%"
}

// placeholders builds "?, ?, ..." for an IN list.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// toArgs adapts strings for a variadic query argument list.
func toArgs(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

// getColumns returns the columns of the table addressed by the trailing two
// path segments.
func getColumns(ctx context.Context, conn DB, path []string) (*ColumnsOut, error) {
	if conn.Kind() == "redis" {
		return nil, fmt.Errorf("get_columns is not available for redis; use get_schema")
	}
	sqlConn, ok := conn.(*sqlDB)
	if !ok {
		return nil, fmt.Errorf("get_columns is not available for %s", conn.Kind())
	}
	if len(path) < 2 {
		return nil, fmt.Errorf("path needs at least 2 segments, e.g. %s", columnsUsage(sqlConn.Kind()))
	}
	schema, table := path[len(path)-2], path[len(path)-1]
	out := &ColumnsOut{Table: schema + "." + table}
	if sqlConn.Kind() == "clickhouse" {
		rows, err := sqlConn.db.QueryContext(ctx,
			`SELECT name, type FROM system.columns WHERE database = ? AND table = ? ORDER BY position`,
			schema, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var name, typ string
			if err := rows.Scan(&name, &typ); err != nil {
				return nil, err
			}
			nullable := "NO"
			if strings.Contains(typ, "Nullable(") {
				nullable = "YES"
			}
			out.Columns = append(out.Columns, Column{Name: name, Type: typ, Nullable: nullable})
		}
		return out, rows.Err()
	}
	rows, err := sqlConn.db.QueryContext(ctx, rebindPlaceholders(columnsQuery, sqlConn.Kind()), schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ, nullable string
		if err := rows.Scan(&name, &typ, &nullable); err != nil {
			return nil, err
		}
		out.Columns = append(out.Columns, Column{Name: name, Type: typ, Nullable: nullable})
	}
	return out, rows.Err()
}

// columnsUsage is the per-adapter path example in the too-short-path error.
func columnsUsage(kind string) string {
	if kind == "postgres" || kind == "mssql" {
		return `["hqdb", "public", "widgets"]`
	}
	return `["hqdb", "widgets"]`
}

// redisSchemaNodes groups SCAN results into namespace and key nodes by the
// segment following the path prefix.
func redisSchemaNodes(ctx context.Context, conn *redisDB, path []string, nameFilter string, includeColumns bool) ([]SchemaNode, bool, error) {
	sep := conn.sep
	prefix := strings.Join(path, sep)
	prefixWithSep := prefix
	if len(path) > 0 {
		prefixWithSep = prefix + sep
	}
	pattern := "*"
	if len(path) > 0 {
		pattern = prefixWithSep + "*"
	}
	if nameFilter != "" {
		pattern = "*" + nameFilter + "*"
		if len(path) > 0 {
			pattern = prefixWithSep + "*" + nameFilter + "*"
		}
	}
	keys, err := redisScanKeys(ctx, conn, pattern)
	if err != nil {
		return nil, false, err
	}
	type group struct {
		count int
		key   bool
	}
	groups := map[string]*group{}
	for _, k := range keys {
		rest := k
		if len(path) > 0 {
			if !strings.HasPrefix(k, prefixWithSep) {
				continue
			}
			rest = k[len(prefixWithSep):]
		}
		name, _, more := strings.Cut(rest, sep)
		if name == "" {
			continue
		}
		g, ok := groups[name]
		if !ok {
			g = &group{}
			groups[name] = g
		}
		g.count++
		g.key = g.key || !more
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	truncated := len(names) > maxSchemaNodes
	if truncated {
		names = names[:maxSchemaNodes]
	}
	nodes := make([]SchemaNode, 0, len(names))
	var keyIndexes []int
	for _, n := range names {
		g := groups[n]
		kind := "namespace"
		if g.key {
			kind = "key"
			keyIndexes = append(keyIndexes, len(nodes))
		}
		nodes = append(nodes, SchemaNode{Name: n, Kind: kind, Count: g.count})
	}
	if includeColumns {
		for i, idx := range keyIndexes {
			if i >= redisTypeSamples {
				break
			}
			typ, err := conn.client.Type(ctx, prefixWithSep+nodes[idx].Name).Result()
			if err != nil {
				return nil, false, err
			}
			nodes[idx].Type = typ
		}
	}
	return nodes, truncated, nil
}

// redisScanKeys iterates SCAN until the cursor returns to 0 or the key cap is hit.
func redisScanKeys(ctx context.Context, conn *redisDB, pattern string) ([]string, error) {
	var (
		keys   []string
		cursor uint64
	)
	for {
		batch, next, err := conn.client.Scan(ctx, cursor, pattern, redisScanCount).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 || len(keys) >= redisMaxKeys {
			return keys, nil
		}
	}
}
