package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverVersion is reported in the MCP implementation handshake.
const serverVersion = "0.1.0"

// openProfile resolves a profile name (or the default profile), opens a
// connection, and closes it when the returned function is called.
func openProfile(name string) (DB, func(), error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	profile, err := cfg.profile(name)
	if err != nil {
		return nil, nil, err
	}
	conn, err := openDB(profile)
	if err != nil {
		return nil, nil, err
	}
	return conn, conn.Close, nil
}

// ProfileInfo is one entry of the list_profiles response; credentials are
// never included.
type ProfileInfo struct {
	Name      string `json:"name"`
	Adapter   string `json:"adapter"`
	IsDefault bool   `json:"is_default"`
}

type listProfilesIn struct{}

type listProfilesOut struct {
	Profiles []ProfileInfo `json:"profiles"`
}

func listProfiles(ctx context.Context, req *mcp.CallToolRequest, in listProfilesIn) (*mcp.CallToolResult, listProfilesOut, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, listProfilesOut{}, err
	}
	out := listProfilesOut{Profiles: []ProfileInfo{}}
	for _, name := range sortedProfileNames(cfg) {
		adapter, err := validateProfile(cfg.Profiles[name])
		if err != nil {
			adapter = adapterName(cfg.Profiles[name])
		}
		out.Profiles = append(out.Profiles, ProfileInfo{
			Name:      name,
			Adapter:   adapter,
			IsDefault: name == cfg.DefaultProfile,
		})
	}
	return nil, out, nil
}

type getSchemaIn struct {
	Profile        string   `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Path           []string `json:"path" jsonschema:"path segments drilled into, e.g. [] or [\"hqdb\"] or [\"hqdb\",\"public\"]"`
	NameFilter     string   `json:"name_filter,omitempty" jsonschema:"case-insensitive substring filter on node names"`
	IncludeColumns bool     `json:"include_columns,omitempty" jsonschema:"sample key types when the level contains redis keys"`
}

func getSchemaTool(ctx context.Context, req *mcp.CallToolRequest, in getSchemaIn) (*mcp.CallToolResult, SchemaOut, error) {
	conn, closeConn, err := openProfile(in.Profile)
	if err != nil {
		return nil, SchemaOut{}, err
	}
	defer closeConn()
	out, err := getSchema(ctx, conn, in.Path, in.NameFilter, in.IncludeColumns)
	if err != nil {
		return nil, SchemaOut{}, err
	}
	return nil, *out, nil
}

type getColumnsIn struct {
	Profile string   `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Path    []string `json:"path" jsonschema:"path ending with the table, e.g. [\"hqdb\",\"public\",\"widgets\"]"`
}

func getColumnsTool(ctx context.Context, req *mcp.CallToolRequest, in getColumnsIn) (*mcp.CallToolResult, ColumnsOut, error) {
	conn, closeConn, err := openProfile(in.Profile)
	if err != nil {
		return nil, ColumnsOut{}, err
	}
	defer closeConn()
	out, err := getColumns(ctx, conn, in.Path)
	if err != nil {
		return nil, ColumnsOut{}, err
	}
	return nil, *out, nil
}

type runQueryIn struct {
	Profile string `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	SQL     string `json:"sql" jsonschema:"one read-only SQL statement"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum rows to return; 0 means unlimited (default 50)"`
}

type runQueryOut struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"row_count"`
	Truncated bool     `json:"truncated"`
}

func runQueryTool(ctx context.Context, req *mcp.CallToolRequest, in runQueryIn) (*mcp.CallToolResult, runQueryOut, error) {
	conn, closeConn, err := openProfile(in.Profile)
	if err != nil {
		return nil, runQueryOut{}, err
	}
	defer closeConn()
	sqlConn, ok := conn.(*sqlDB)
	if !ok {
		return nil, runQueryOut{}, errNotSQL(conn.Kind(), "run_query")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	res, err := runQuery(sqlConn.SQL(), in.SQL, limit)
	if err != nil {
		return nil, runQueryOut{}, err
	}
	return nil, runQueryOut{
		Columns:   res.Columns,
		Rows:      res.Rows,
		RowCount:  res.RowCount,
		Truncated: res.Truncated,
	}, nil
}

type runRedisIn struct {
	Profile string   `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Command string   `json:"command" jsonschema:"a read-only redis command with any inline arguments, e.g. \"GET user:1000\""`
	Args    []string `json:"args,omitempty" jsonschema:"further command arguments, e.g. [\"MATCH\",\"user:*\"]"`
}

type runRedisOut struct {
	Command string `json:"command"`
	Result  any    `json:"result"`
}

func runRedisTool(ctx context.Context, req *mcp.CallToolRequest, in runRedisIn) (*mcp.CallToolResult, runRedisOut, error) {
	conn, closeConn, err := openProfile(in.Profile)
	if err != nil {
		return nil, runRedisOut{}, err
	}
	defer closeConn()
	redisConn, ok := conn.(*redisDB)
	if !ok {
		return nil, runRedisOut{}, errNotSQL(conn.Kind(), "run_redis")
	}
	result, err := runRedisCommand(ctx, redisConn, in.Command, in.Args)
	if err != nil {
		return nil, runRedisOut{}, err
	}
	return nil, runRedisOut{Command: strings.TrimSpace(in.Command), Result: result}, nil
}

type exportQueryIn struct {
	Profile  string `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	SQL      string `json:"sql" jsonschema:"one read-only SQL statement"`
	DestPath string `json:"dest_path" jsonschema:"destination file path; its parent directory must exist"`
	Format   string `json:"format,omitempty" jsonschema:"csv, json, or ndjson; inferred from the file extension when omitted"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum rows to write; 0 means unlimited"`
}

type exportQueryOut struct {
	Path     string `json:"path"`
	Format   string `json:"format"`
	RowCount int    `json:"row_count"`
}

func exportQueryTool(ctx context.Context, req *mcp.CallToolRequest, in exportQueryIn) (*mcp.CallToolResult, exportQueryOut, error) {
	conn, closeConn, err := openProfile(in.Profile)
	if err != nil {
		return nil, exportQueryOut{}, err
	}
	defer closeConn()
	sqlConn, ok := conn.(*sqlDB)
	if !ok {
		return nil, exportQueryOut{}, errNotSQL(conn.Kind(), "export_query")
	}
	format := in.Format
	if format == "" {
		if format, err = inferFormat(in.DestPath); err != nil {
			return nil, exportQueryOut{}, err
		}
	}
	n, err := exportQuery(sqlConn.SQL(), in.SQL, in.DestPath, format, in.Limit)
	if err != nil {
		return nil, exportQueryOut{}, err
	}
	return nil, exportQueryOut{Path: in.DestPath, Format: format, RowCount: n}, nil
}

// registerTools adds every tool to the server.
func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_profiles",
		Description: "List the database profiles from the hq-mcp config files, with their adapter " +
			"and which one is the default. Never includes passwords.",
	}, listProfiles)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_schema",
		Description: "List database objects one level at a time. Start with an empty path for the " +
			"databases, then drill down: [\"hqdb\"] lists schemas (postgres, mysql, mssql) or tables " +
			"(clickhouse), and [\"hqdb\",\"public\"] lists tables. On redis, keys are grouped by the " +
			"segment after the path prefix, reported as namespace or key nodes.",
	}, getSchemaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_columns",
		Description: "List the columns of one table: name, type, and nullability. The path's last two segments address the table, e.g. [\"hqdb\",\"public\",\"widgets\"] on postgres, [\"hqdb\",\"widgets\"] on clickhouse.",
	}, getColumnsTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "Run one read-only SQL statement and return rows as JSON. The statement is " +
			"screened first: it must start with SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / " +
			"INSERT / UPDATE, must not stack statements, and must not contain write keywords " +
			"(delete, drop, truncate, alter, create, call, do, ...). The SQL is sent to the database " +
			"unmodified; no LIMIT is injected, so use the limit argument to bound the response.",
	}, runQueryTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_redis",
		Description: "Run one read-only Redis command against a redis profile. Only allowlisted " +
			"read commands are permitted (get, mget, hgetall, smembers, scan, ttl, type, info, " +
			"zrange, xrange, geosearch, ...); writes, blocking commands, eval, subscribe, and " +
			"config are refused.",
	}, runRedisTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "export_query",
		Description: "Run one read-only SQL statement and stream the results into a csv, json, or " +
			"ndjson file. The format is inferred from the file extension when not given. The " +
			"destination's parent directory must exist; it is not created.",
	}, exportQueryTool)
}

func main() {
	// stdout is the MCP protocol channel: every diagnostic goes to stderr.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	server := mcp.NewServer(&mcp.Implementation{Name: "hq-mcp", Version: serverVersion}, nil)
	registerTools(server)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("hq-mcp: %v", err)
		os.Exit(1)
	}
}
