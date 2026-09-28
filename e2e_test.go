package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// e2eSession is a client session against the built hq-mcp binary, configured
// with the loopback docker fixtures.
type e2eSession struct {
	t    *testing.T
	sess *mcp.ClientSession
}

// startE2E builds the binary, writes the dev profile config into a temp dir,
// and connects a client to it with that dir as the working directory.
func startE2E(t *testing.T) *e2eSession {
	t.Helper()
	if os.Getenv("HQ_MCP_E2E") != "1" {
		t.Skip("set HQ_MCP_E2E=1 and run bash docs/plans/fixtures.sh up first")
	}
	bin := filepath.Join(t.TempDir(), "hq-mcp")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build hq-mcp: %v", err)
	}
	dir := t.TempDir()
	cfg, err := os.ReadFile(filepath.Join("docs", "plans", "dev.hq-mcp.toml"))
	if err != nil {
		t.Fatalf("read dev.hq-mcp.toml: %v", err)
	}
	cfgPath := filepath.Join(dir, "hq-mcp.toml")
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HQ_MCP_CONFIG="+cfgPath)
	client := mcp.NewClient(&mcp.Implementation{Name: "hq-mcp-e2e", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to hq-mcp: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return &e2eSession{t: t, sess: sess}
}

// call runs one tool and fails the test on a protocol-level error.
func (s *e2eSession) call(name string, args map[string]any) *mcp.CallToolResult {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		s.t.Fatalf("%s: %v", name, err)
	}
	return res
}

// ok calls a tool, fails on IsError, and decodes the structured output.
func (s *e2eSession) ok(name string, args map[string]any, out any) {
	s.t.Helper()
	res := s.call(name, args)
	if res.IsError {
		s.t.Fatalf("%s returned an error: %s", name, textOf(res))
	}
	if out == nil {
		return
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		s.t.Fatalf("marshal %s output: %v", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		s.t.Fatalf("decode %s output %s: %v", name, raw, err)
	}
}

// errText calls a tool expecting a refusal and returns the message.
func (s *e2eSession) errText(name string, args map[string]any) string {
	s.t.Helper()
	res := s.call(name, args)
	if !res.IsError {
		s.t.Fatalf("%s should have failed, got %s", name, textOf(res))
	}
	return textOf(res)
}

// textOf concatenates the text content blocks of a tool result.
func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestE2EListProfiles(t *testing.T) {
	s := startE2E(t)
	var out listProfilesOut
	s.ok("list_profiles", map[string]any{}, &out)
	got := map[string]ProfileInfo{}
	for _, p := range out.Profiles {
		got[p.Name] = p
	}
	for _, w := range []string{"pg-dev", "my-dev", "ms-dev", "ch-dev", "redis-dev"} {
		if _, ok := got[w]; !ok {
			t.Errorf("profile %q missing from %+v", w, out.Profiles)
		}
	}
	if p := got["pg-dev"]; !p.IsDefault || p.Adapter != "postgres" {
		t.Errorf("pg-dev = %+v, want the default postgres profile", p)
	}
}

func TestE2ERunQueryAdapters(t *testing.T) {
	s := startE2E(t)
	for _, profile := range []string{"pg-dev", "my-dev", "ms-dev", "ch-dev", "ch-native-dev", "ms-odbc-dev"} {
		var out runQueryOut
		s.ok("run_query", map[string]any{"profile": profile, "sql": "SELECT 1 AS one"}, &out)
		if len(out.Rows) != 1 || len(out.Rows[0]) != 1 {
			t.Fatalf("%s: rows = %+v, want [[1]]", profile, out.Rows)
		}
		if out.Rows[0][0] != float64(1) {
			t.Errorf("%s: row = %+v, want [1]", profile, out.Rows[0])
		}
		if out.Columns[0] != "one" {
			t.Errorf("%s: columns = %v, want [one]", profile, out.Columns)
		}
	}
}

func TestE2ERunQueryClickHouseVersion(t *testing.T) {
	s := startE2E(t)
	for _, profile := range []string{"ch-dev", "ch-native-dev"} {
		var out runQueryOut
		s.ok("run_query", map[string]any{"profile": profile, "sql": "SELECT version()"}, &out)
		if len(out.Rows) != 1 {
			t.Fatalf("%s: rows = %+v, want one row", profile, out.Rows)
		}
		if v, ok := out.Rows[0][0].(string); !ok || v == "" {
			t.Errorf("%s: version = %#v, want a non-empty string", profile, out.Rows[0][0])
		}
	}
}

func TestE2ERunQueryLimit(t *testing.T) {
	s := startE2E(t)
	var out runQueryOut
	s.ok("run_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT id FROM widgets ORDER BY id", "limit": 2,
	}, &out)
	if out.RowCount != 2 || !out.Truncated {
		t.Errorf("row_count = %d, truncated = %v; want 2 and true", out.RowCount, out.Truncated)
	}
}

func TestE2ERunQueryRefusesWrite(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("run_query", map[string]any{"profile": "pg-dev", "sql": "DELETE FROM widgets"})
	if !strings.HasPrefix(msg, "Refused (read-only server): ") {
		t.Errorf("message = %q, want the read-only refusal prefix", msg)
	}
}

func TestE2ERunQueryOnRedisProfileFails(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("run_query", map[string]any{"profile": "redis-dev", "sql": "SELECT 1"})
	if !strings.Contains(msg, "run_query is not available for redis") {
		t.Errorf("message = %q", msg)
	}
}

func TestE2EGetSchemaPostgres(t *testing.T) {
	s := startE2E(t)
	var root SchemaOut
	s.ok("get_schema", map[string]any{"profile": "pg-dev", "path": []string{}}, &root)
	if len(root.Nodes) != 1 || root.Nodes[0].Name != "hqdb" || root.Nodes[0].Kind != "database" {
		t.Fatalf("root nodes = %+v, want the hqdb database", root.Nodes)
	}
	var schemas SchemaOut
	s.ok("get_schema", map[string]any{"profile": "pg-dev", "path": []string{"hqdb"}}, &schemas)
	names := nodeNames(schemas.Nodes)
	if !names["analytics"] || !names["public"] {
		t.Errorf("schema nodes = %+v, want analytics and public", schemas.Nodes)
	}
	var tables SchemaOut
	s.ok("get_schema", map[string]any{"profile": "pg-dev", "path": []string{"hqdb", "public"}}, &tables)
	if len(tables.Nodes) != 1 || tables.Nodes[0].Name != "widgets" || tables.Nodes[0].Kind != "table" {
		t.Errorf("table nodes = %+v, want widgets (table)", tables.Nodes)
	}
	var filtered SchemaOut
	s.ok("get_schema", map[string]any{
		"profile": "pg-dev", "path": []string{"hqdb", "analytics"}, "name_filter": "wid",
	}, &filtered)
	if !nodeNames(filtered.Nodes)["widgets_extra"] {
		t.Errorf("filtered nodes = %+v, want widgets_extra", filtered.Nodes)
	}
}

func TestE2EGetColumns(t *testing.T) {
	s := startE2E(t)
	var out ColumnsOut
	s.ok("get_columns", map[string]any{
		"profile": "pg-dev", "path": []string{"hqdb", "public", "widgets"},
	}, &out)
	if out.Table != "public.widgets" {
		t.Errorf("table = %q, want public.widgets", out.Table)
	}
	cols := map[string]Column{}
	for _, c := range out.Columns {
		cols[c.Name] = c
	}
	if _, ok := cols["price"]; !ok {
		t.Errorf("columns = %+v, want a price column", out.Columns)
	}
	if c := cols["name"]; c.Nullable != "YES" {
		t.Errorf("widgets.name nullable = %q, want YES", c.Nullable)
	}
}

func TestE2EGetColumnsClickHouseNullability(t *testing.T) {
	s := startE2E(t)
	var out ColumnsOut
	s.ok("get_columns", map[string]any{
		"profile": "ch-dev", "path": []string{"hqdb", "widgets"},
	}, &out)
	cols := map[string]Column{}
	for _, c := range out.Columns {
		cols[c.Name] = c
	}
	if c := cols["id"]; c.Nullable != "NO" {
		t.Errorf("id column = %+v, want nullable NO", c)
	}
	if c := cols["note"]; c.Nullable != "YES" {
		t.Errorf("note column = %+v, want nullable YES", c)
	}
}

func TestE2EGetSchemaRedis(t *testing.T) {
	s := startE2E(t)
	var out SchemaOut
	s.ok("get_schema", map[string]any{
		"profile": "redis-dev", "path": []string{}, "include_columns": true,
	}, &out)
	nodes := map[string]SchemaNode{}
	for _, n := range out.Nodes {
		nodes[n.Name] = n
	}
	if u, ok := nodes["user"]; !ok || u.Kind != "namespace" || u.Count != 2 {
		t.Errorf("user node = %+v, want a namespace with 2 keys", u)
	}
	if p, ok := nodes["plainkey"]; !ok || p.Kind != "key" || p.Type != "string" {
		t.Errorf("plainkey node = %+v, want a string key", p)
	}
}

func TestE2EGetSchemaClickHouse(t *testing.T) {
	s := startE2E(t)
	var root SchemaOut
	s.ok("get_schema", map[string]any{"profile": "ch-dev", "path": []string{}}, &root)
	if !nodeNames(root.Nodes)["hqdb"] {
		t.Errorf("root nodes = %+v, want hqdb", root.Nodes)
	}
	var tables SchemaOut
	s.ok("get_schema", map[string]any{"profile": "ch-dev", "path": []string{"hqdb"}}, &tables)
	if !nodeNames(tables.Nodes)["widgets"] {
		t.Errorf("table nodes = %+v, want widgets", tables.Nodes)
	}
}

func TestE2EGetSchemaMySQL(t *testing.T) {
	s := startE2E(t)
	var root SchemaOut
	s.ok("get_schema", map[string]any{"profile": "my-dev", "path": []string{}}, &root)
	if !nodeNames(root.Nodes)["hqdb2"] {
		t.Errorf("root nodes = %+v, want hqdb2", root.Nodes)
	}
	var tables SchemaOut
	s.ok("get_schema", map[string]any{"profile": "my-dev", "path": []string{"hqdb"}}, &tables)
	if !nodeNames(tables.Nodes)["widgets"] {
		t.Errorf("table nodes = %+v, want widgets", tables.Nodes)
	}
}

func TestE2EExportQuery(t *testing.T) {
	s := startE2E(t)
	dest := filepath.Join(t.TempDir(), "out.csv")
	var out exportQueryOut
	s.ok("export_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT 1 AS one", "dest_path": dest,
	}, &out)
	if out.Path != dest || out.Format != "csv" || out.RowCount != 1 {
		t.Errorf("export result = %+v", out)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "one\n1" {
		t.Errorf("file content = %q, want header one and row 1", got)
	}
}

func TestE2EExportQueryJSONAndNDJSON(t *testing.T) {
	s := startE2E(t)
	dir := t.TempDir()
	var out exportQueryOut
	s.ok("export_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT 1 AS one", "dest_path": filepath.Join(dir, "out.json"),
	}, &out)
	if out.Format != "json" {
		t.Errorf("format = %q, want json", out.Format)
	}
	data, err := os.ReadFile(out.Path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("json export %s: %v", data, err)
	}
	if len(rows) != 1 || rows[0]["one"] != float64(1) {
		t.Errorf("json rows = %+v", rows)
	}
	s.ok("export_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT 1 AS one", "dest_path": filepath.Join(dir, "out.ndjson"),
	}, &out)
	data, err = os.ReadFile(out.Path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n"); lines != 0 {
		t.Errorf("ndjson content = %q, want a single line", data)
	}
	if !strings.Contains(string(data), `"one":1`) {
		t.Errorf("ndjson content = %q, want the one column", data)
	}
}

func TestE2EExportQueryRefusesWrite(t *testing.T) {
	s := startE2E(t)
	dest := filepath.Join(t.TempDir(), "out.csv")
	msg := s.errText("export_query", map[string]any{
		"profile": "pg-dev", "sql": "TRUNCATE widgets", "dest_path": dest,
	})
	if !strings.HasPrefix(msg, "Refused (read-only server): ") {
		t.Errorf("message = %q", msg)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("a refused export must not create the destination file")
	}
}

func TestE2ERunRedis(t *testing.T) {
	s := startE2E(t)
	var got runRedisOut
	s.ok("run_redis", map[string]any{
		"profile": "redis-dev", "command": "GET user:1000",
	}, &got)
	if got.Result != `{"id":1000}` {
		t.Errorf("result = %#v, want the user JSON", got.Result)
	}
	var scan runRedisOut
	s.ok("run_redis", map[string]any{
		"profile": "redis-dev", "command": "SCAN", "args": []string{"0", "MATCH", "user:*", "COUNT", "100"},
	}, &scan)
	// SCAN replies with a [cursor, keys] pair.
	pair, ok := scan.Result.([]any)
	if !ok || len(pair) != 2 {
		t.Fatalf("scan result = %#v, want a [cursor, keys] pair", scan.Result)
	}
	keyList, ok := pair[1].([]any)
	if !ok {
		t.Fatalf("scan keys = %#v, want an array", pair[1])
	}
	found := map[string]bool{}
	for _, k := range keyList {
		found[fmt.Sprint(k)] = true
	}
	if !found["user:1000"] || !found["user:1000:roles"] {
		t.Errorf("scan keys = %v, want user:1000 and user:1000:roles", keyList)
	}
	msg := s.errText("run_redis", map[string]any{
		"profile": "redis-dev", "command": "DEL", "args": []string{"user:1000"},
	})
	if !strings.Contains(msg, "this server permits read-only Redis commands only") {
		t.Errorf("message = %q", msg)
	}
}

func TestE2EGetColumnsRedisFails(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("get_columns", map[string]any{"profile": "redis-dev", "path": []string{"plainkey"}})
	if !strings.Contains(msg, "get_columns is not available for redis") {
		t.Errorf("message = %q", msg)
	}
}

func TestE2EUnknownProfile(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("run_query", map[string]any{"profile": "nope", "sql": "SELECT 1"})
	if !strings.Contains(msg, `unknown profile "nope"; run list_profiles`) {
		t.Errorf("message = %q", msg)
	}
}

// nodeNames indexes node names for assertions.
func nodeNames(nodes []SchemaNode) map[string]bool {
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		out[n.Name] = true
	}
	return out
}
