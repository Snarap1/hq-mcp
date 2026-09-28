package config

import (
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestOptConnStrListForm(t *testing.T) {
	// Real profiles use the list form; elements join with a single space.
	p := map[string]any{"conn_str": []any{"Driver={ODBC Driver 18 for SQL Server}", "Server=127.0.0.1,1433"}}
	got, err := OptConnStr(p)
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
	got, err := FillFromURL(p, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"host": "127.0.0.1", "user": "u", "password": "p", "dbname": "d",
	}
	for k, want := range checks {
		v, err := OptStr(got, k)
		if err != nil {
			t.Fatal(err)
		}
		if v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if port, ok, _ := OptInt(got, "port"); !ok || port != 5432 {
		t.Errorf("port = %d (ok=%v), want 5432", port, ok)
	}
}

func TestFillFromURLExplicitKeyWins(t *testing.T) {
	p := map[string]any{"conn_str": "postgres://u:p@127.0.0.1:5432/d", "host": "explicit"}
	got, err := FillFromURL(p, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := OptStr(got, "host"); h != "explicit" {
		t.Errorf("host = %q, want explicit", h)
	}
}

func TestFillFromURLClickHouseDatabase(t *testing.T) {
	p := map[string]any{"conn_str": "clickhouse://hq:hq@127.0.0.1:8123/hqdb"}
	got, err := FillFromURL(p, "clickhouse")
	if err != nil {
		t.Fatal(err)
	}
	if db, _ := OptStr(got, "database"); db != "hqdb" {
		t.Errorf("database = %q, want hqdb", db)
	}
}

func TestValidateUnknownKey(t *testing.T) {
	p := map[string]any{"adapter": "postgres", "host": "127.0.0.1", "foo": int64(1)}
	_, err := Validate(p)
	if err == nil {
		t.Fatal("expected an error for an unknown profile key")
	}
	want := `unknown option "foo" for adapter "postgres"; valid options: adapter, conn_str, database, dbname, host, password, port, sslmode, user`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestValidateUnsupportedAdapter(t *testing.T) {
	_, err := Validate(map[string]any{"adapter": "duckdb"})
	if err == nil {
		t.Fatal("expected an error for the duckdb adapter")
	}
	want := `adapter "duckdb" is not supported; supported: postgres, mysql, mssql, odbc, clickhouse, redis`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
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

	cfg, err := Load()
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
	if _, err := Load(); err == nil {
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

	cfg, err := Load()
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
	p, err := cfg.Profile("")
	if err != nil {
		t.Fatal(err)
	}
	if p["adapter"] != "redis" {
		t.Errorf("adapter = %v, want redis", p["adapter"])
	}
	if _, err := cfg.Profile("nope"); err == nil {
		t.Error("expected an error for an unknown profile")
	} else if err.Error() != `unknown profile "nope"; run list_profiles` {
		t.Errorf("unexpected message: %q", err)
	}
	empty := &Config{Profiles: map[string]map[string]any{}}
	if _, err := empty.Profile(""); err == nil ||
		err.Error() != "no profile specified and no default_profile is set" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTomlUnmarshalSanity(t *testing.T) {
	// Guards that profile values arrive as the types the accessors expect.
	var doc map[string]any
	if err := toml.Unmarshal([]byte("[profiles.p]\nadapter=\"redis\"\nport=6380\nsecure=true\n"), &doc); err != nil {
		t.Fatal(err)
	}
	p := doc["profiles"].(map[string]any)["p"].(map[string]any)
	if port, ok, err := OptInt(p, "port"); err != nil || !ok || port != 6380 {
		t.Errorf("port = %d (ok=%v, err=%v), want 6380", port, ok, err)
	}
	if secure, err := OptBool(p, "secure"); err != nil || !secure {
		t.Errorf("secure = %v (err=%v), want true", secure, err)
	}
}
