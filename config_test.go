package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	toml "github.com/pelletier/go-toml/v2"
)

func TestParseODBC(t *testing.T) {
	cases := []struct {
		name string
		conn string
		want odbcConn
	}{
		{
			name: "mssql driver string",
			conn: "Driver={ODBC Driver 18 for SQL Server};Server=127.0.0.1,1433;Database=dev_db;Uid=sa;Pwd=pw;Encrypt=no;",
			want: odbcConn{
				driver:   "ODBC Driver 18 for SQL Server",
				host:     "127.0.0.1",
				port:     1433,
				database: "dev_db",
				user:     "sa",
				password: "pw",
				encrypt:  "disable",
			},
		},
		{
			name: "server without port defaults to 1433",
			conn: "Server=127.0.0.1;Database=dev_db;",
			want: odbcConn{host: "127.0.0.1", port: 1433, database: "dev_db"},
		},
		{
			name: "encrypt mandatory maps to true",
			conn: "Server=127.0.0.1;Encrypt=yes;",
			want: odbcConn{host: "127.0.0.1", port: 1433, encrypt: "true"},
		},
		{
			name: "encrypt strict stays strict",
			conn: "Server=127.0.0.1;Encrypt=strict;",
			want: odbcConn{host: "127.0.0.1", port: 1433, encrypt: "strict"},
		},
		{
			name: "braced value keeps semicolons out of the split",
			conn: "Driver={ODBC;Driver 18};Server=127.0.0.1;",
			want: odbcConn{driver: "ODBC;Driver 18", host: "127.0.0.1", port: 1433},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseODBC(tc.conn)
			if err != nil {
				t.Fatalf("parseODBC(%q): %v", tc.conn, err)
			}
			if got != tc.want {
				t.Errorf("parseODBC(%q) = %+v, want %+v", tc.conn, got, tc.want)
			}
		})
	}
}

func TestParseODBCMissingServer(t *testing.T) {
	if _, err := parseODBC("Driver={X};Database=db;"); err == nil {
		t.Error("expected an error when the ODBC string has no Server")
	}
}

func TestOptConnStrListForm(t *testing.T) {
	// Real profiles use the list form; elements join with a single space.
	p := map[string]any{"conn_str": []any{"Driver={ODBC Driver 18 for SQL Server}", "Server=127.0.0.1,1433"}}
	got, err := optConnStr(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "Driver={ODBC Driver 18 for SQL Server} Server=127.0.0.1,1433"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFillFromURL(t *testing.T) {
	p := map[string]any{"conn_str": "postgres://u:p@127.0.0.1:5432/d"}
	got, err := fillFromURL(p, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"host": "127.0.0.1", "user": "u", "password": "p", "dbname": "d",
	}
	for k, want := range checks {
		v, err := optStr(got, k)
		if err != nil {
			t.Fatal(err)
		}
		if v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if port, ok, _ := optInt(got, "port"); !ok || port != 5432 {
		t.Errorf("port = %d (ok=%v), want 5432", port, ok)
	}
}

func TestFillFromURLExplicitKeyWins(t *testing.T) {
	p := map[string]any{"conn_str": "postgres://u:p@127.0.0.1:5432/d", "host": "explicit"}
	got, err := fillFromURL(p, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := optStr(got, "host"); h != "explicit" {
		t.Errorf("host = %q, want explicit", h)
	}
}

func TestFillFromURLClickHouseDatabase(t *testing.T) {
	p := map[string]any{"conn_str": "clickhouse://hq:hq@127.0.0.1:8123/hqdb"}
	got, err := fillFromURL(p, "clickhouse")
	if err != nil {
		t.Fatal(err)
	}
	if db, _ := optStr(got, "database"); db != "hqdb" {
		t.Errorf("database = %q, want hqdb", db)
	}
}

func TestValidateProfileUnknownKey(t *testing.T) {
	p := map[string]any{"adapter": "postgres", "host": "127.0.0.1", "foo": int64(1)}
	_, err := validateProfile(p)
	if err == nil {
		t.Fatal("expected an error for an unknown profile key")
	}
	want := `unknown option "foo" for adapter "postgres"; valid options: adapter, conn_str, database, dbname, host, password, port, sslmode, user`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestValidateProfileUnsupportedAdapter(t *testing.T) {
	_, err := validateProfile(map[string]any{"adapter": "duckdb"})
	if err == nil {
		t.Fatal("expected an error for the duckdb adapter")
	}
	want := `adapter "duckdb" is not supported; supported: postgres, mysql, mssql, odbc, clickhouse, redis`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestClickHouseProtocolDefault(t *testing.T) {
	cases := []struct {
		port int
		want clickhouse.Protocol
	}{
		{8123, clickhouse.HTTP},
		{8443, clickhouse.HTTP},
		{9000, clickhouse.Native},
		{59000, clickhouse.Native},
	}
	for _, tc := range cases {
		if got := defaultClickHouseProtocol(tc.port); got != tc.want {
			t.Errorf("port %d: protocol = %v, want %v", tc.port, got, tc.want)
		}
	}
}

func TestLoadConfigMerge(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HQ_MCP_CONFIG", "")

	// A higher-priority file replaces the whole profiles table.
	if err := os.WriteFile(filepath.Join(home, ".hq-mcp.toml"),
		[]byte("default_profile = \"one\"\n[profiles.one]\nadapter = \"postgres\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "hq-mcp.toml"),
		[]byte("default_profile = \"two\"\n[profiles.two]\nadapter = \"redis\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(cwd, "hq-mcp.toml")) })

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProfile != "two" {
		t.Errorf("default_profile = %q, want two (cwd wins over home)", cfg.DefaultProfile)
	}
	if _, ok := cfg.Profiles["one"]; ok {
		t.Error("profile one should be gone: the higher-priority file replaces the whole profiles table")
	}
	if _, ok := cfg.Profiles["two"]; !ok {
		t.Error("profile two is missing")
	}
}

func TestReadConfigFilePyproject(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pyproject.toml")
	if err := os.WriteFile(p, []byte("[project]\nname = \"x\"\n\n[tool.hq-mcp]\ndefault_profile = \"p\"\n[tool.hq-mcp.profiles.p]\nadapter = \"postgres\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := readConfigFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["project"]; ok {
		t.Error("the top-level [project] table must not leak in")
	}
	if doc["default_profile"] != "p" {
		t.Errorf("default_profile = %v, want p", doc["default_profile"])
	}
}

func TestLoadConfigRejectsUnknownTopLevelKey(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HQ_MCP_CONFIG", "")

	if err := os.WriteFile(filepath.Join(home, ".hq-mcp.toml"), []byte("proflies = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Error("expected an error for an unknown top-level key")
	}
}

func TestLoadConfigIgnoresOtherToolConfigs(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "other-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HQ_MCP_CONFIG", "")

	// A foreign CLI config (keymaps included) is invisible to hq-mcp.
	if err := os.WriteFile(filepath.Join(home, ".config", "other-cli", "config.toml"),
		[]byte("default_profile = \"hq\"\n[keymaps.global]\nquit = \"ctrl+q\"\n[profiles.hq]\nadapter = \"postgres\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// hq-mcp's own config dir is searched.
	if err := os.MkdirAll(filepath.Join(home, ".config", "hq-mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "hq-mcp", "config.toml"),
		[]byte("default_profile = \"mine\"\n[profiles.mine]\nadapter = \"redis\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProfile != "mine" {
		t.Errorf("default_profile = %q, want mine", cfg.DefaultProfile)
	}
	if _, ok := cfg.Profiles["hq"]; ok {
		t.Error("profile from the foreign CLI config must be ignored")
	}
	if _, ok := cfg.Profiles["mine"]; !ok {
		t.Error("profile mine is missing")
	}
}

func TestProfileFallback(t *testing.T) {
	cfg := &Config{DefaultProfile: "def", Profiles: map[string]map[string]any{
		"def": {"adapter": "redis"},
	}}
	p, err := cfg.profile("")
	if err != nil {
		t.Fatal(err)
	}
	if p["adapter"] != "redis" {
		t.Errorf("adapter = %v, want redis", p["adapter"])
	}
	if _, err := cfg.profile("nope"); err == nil {
		t.Error("expected an error for an unknown profile")
	} else if err.Error() != `unknown profile "nope"; run list_profiles` {
		t.Errorf("unexpected message: %q", err)
	}
	empty := &Config{Profiles: map[string]map[string]any{}}
	if _, err := empty.profile(""); err == nil ||
		err.Error() != "no profile specified and no default_profile is set" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInferFormat(t *testing.T) {
	cases := map[string]string{
		"/tmp/out.csv":    "csv",
		"/tmp/out.tsv":    "csv",
		"/tmp/out.json":   "json",
		"/tmp/out.ndjson": "ndjson",
	}
	for path, want := range cases {
		got, err := inferFormat(path)
		if err != nil {
			t.Fatalf("inferFormat(%q): %v", path, err)
		}
		if got != want {
			t.Errorf("inferFormat(%q) = %q, want %q", path, got, want)
		}
	}
	if _, err := inferFormat("/tmp/out.txt"); err == nil ||
		err.Error() != `cannot infer format from extension ".txt"; pass format explicitly` {
		t.Errorf("unexpected error for an unknown extension: %v", err)
	}
}

func TestTomlUnmarshalSanity(t *testing.T) {
	// Guards that profile values arrive as the types the accessors expect.
	var doc map[string]any
	if err := toml.Unmarshal([]byte("[profiles.p]\nadapter=\"redis\"\nport=6380\nsecure=true\n"), &doc); err != nil {
		t.Fatal(err)
	}
	prof := doc["profiles"].(map[string]any)["p"].(map[string]any)
	p := prof
	if port, ok, err := optInt(p, "port"); err != nil || !ok || port != 6380 {
		t.Errorf("port = %d (ok=%v, err=%v), want 6380", port, ok, err)
	}
	if secure, err := optBool(p, "secure"); err != nil || !secure {
		t.Errorf("secure = %v (err=%v), want true", secure, err)
	}
}
