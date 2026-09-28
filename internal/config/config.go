// Package config discovers, merges, and validates hq-mcp's own configuration
// files, and reads options out of the raw profile tables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Config is hq-mcp's own configuration: the default profile name and the
// raw profile tables. The file layout is conventional, but the files,
// directories, and pyproject section are hq-mcp's own.
type Config struct {
	DefaultProfile string
	Profiles       map[string]map[string]any
}

// Profile returns the raw profile table for name, falling back to
// default_profile when name is empty.
func (c *Config) Profile(name string) (map[string]any, error) {
	if name == "" {
		name = c.DefaultProfile
		if name == "" {
			return nil, fmt.Errorf("no profile specified and no default_profile is set")
		}
	}
	p, ok := c.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q; run list_profiles", name)
	}
	return p, nil
}

// findConfigFiles returns candidate config paths in priority order: home, then
// the user config dir, then cwd, then an explicitly configured path. The last
// item has the highest priority.
func findConfigFiles() ([]string, error) {
	var found []string
	appendDir := func(dir string, names []string) {
		for _, n := range names {
			p := filepath.Join(dir, n)
			if fileExists(p) {
				found = append(found, p)
			}
		}
	}
	dirNames := []string{"pyproject.toml", ".hq-mcp.toml", "hq-mcp.toml"}
	cfgNames := []string{"config.toml", ".hq-mcp.toml", "hq-mcp.toml"}
	if home, err := os.UserHomeDir(); err == nil {
		appendDir(home, dirNames)
	}
	if cfgDir, err := os.UserConfigDir(); err == nil {
		appendDir(filepath.Join(cfgDir, "hq-mcp"), cfgNames)
	}
	if cwd, err := os.Getwd(); err == nil {
		appendDir(cwd, dirNames)
	}
	if env := os.Getenv("HQ_MCP_CONFIG"); env != "" {
		if !fileExists(env) {
			return nil, fmt.Errorf("config file could not be found at specified path: %s", env)
		}
		found = append(found, env)
	}
	return found, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// readConfigFile reads one candidate file. A pyproject.toml only contributes
// its [tool.hq-mcp] section.
func readConfigFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("attempted to load the config file at %s, but encountered an error: %v", path, err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("attempted to load the config file at %s, but encountered an error: %v", path, err)
	}
	if filepath.Base(path) == "pyproject.toml" {
		tool, _ := doc["tool"].(map[string]any)
		if tool == nil {
			return map[string]any{}, nil
		}
		h, _ := tool["hq-mcp"].(map[string]any)
		if h == nil {
			return map[string]any{}, nil
		}
		return h, nil
	}
	return doc, nil
}

// Load discovers, merges, and validates the config files. Merge is
// top-level key replacement: a profiles table from a higher-priority file
// replaces the whole profiles map.
func Load() (*Config, error) {
	paths, err := findConfigFiles()
	if err != nil {
		return nil, err
	}
	merged := map[string]any{}
	for _, p := range paths {
		rel, err := readConfigFile(p)
		if err != nil {
			return nil, err
		}
		for k, v := range rel {
			merged[k] = v
		}
	}
	cfg := &Config{Profiles: map[string]map[string]any{}}
	for k, v := range merged {
		switch k {
		case "default_profile":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("default_profile must be a string")
			}
			cfg.DefaultProfile = s
		case "profiles":
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("profiles must be a TOML table")
			}
			for name, pv := range m {
				pm, ok := pv.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("profile %q must be a TOML table", name)
				}
				cfg.Profiles[name] = pm
			}
		default:
			return nil, fmt.Errorf(
				"hq-mcp config files must only contain default_profile and profiles tables (got %s)", k)
		}
	}
	return cfg, nil
}

// SupportedAdapters is every adapter this server can open, in the order used
// by error messages.
const SupportedAdapters = "postgres, mysql, mssql, odbc, clickhouse, redis"

// adapterKeys lists the profile keys each adapter accepts, including aliases
// (e.g. database is an alias of dbname for postgres/mysql/mssql).
var adapterKeys = map[string][]string{
	"postgres":   {"adapter", "conn_str", "database", "dbname", "host", "password", "port", "sslmode", "user"},
	"mysql":      {"adapter", "conn_str", "database", "dbname", "host", "password", "port", "tls", "user"},
	"mssql":      {"adapter", "conn_str", "database", "dbname", "encrypt", "host", "password", "port", "user"},
	"odbc":       {"adapter", "conn_str"},
	"clickhouse": {"adapter", "conn_str", "database", "host", "password", "port", "protocol", "secure", "user"},
	"redis":      {"adapter", "database", "host", "password", "port", "secure", "separator", "user"},
}

// AdapterName returns the profile's adapter, defaulting to duckdb like the
// reference CLI does (list_profiles shows that default).
func AdapterName(p map[string]any) string {
	if s, ok := p["adapter"].(string); ok {
		return s
	}
	return "duckdb"
}

// Validate checks the adapter is supported and every profile key is
// valid for that adapter, returning the adapter name.
func Validate(p map[string]any) (string, error) {
	adapter := AdapterName(p)
	keys, ok := adapterKeys[adapter]
	if !ok {
		return "", fmt.Errorf("adapter %q is not supported; supported: %s", adapter, SupportedAdapters)
	}
	valid := make(map[string]bool, len(keys))
	for _, k := range keys {
		valid[k] = true
	}
	for k := range p {
		if !valid[k] {
			return "", fmt.Errorf("unknown option %q for adapter %q; valid options: %s",
				k, adapter, strings.Join(keys, ", "))
		}
	}
	return adapter, nil
}

// OptStr reads a string option; missing keys are "".
func OptStr(p map[string]any, key string) (string, error) {
	v, ok := p[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("option %q must be a string", key)
	}
	return s, nil
}

// OptInt reads an integer option; missing keys are (0, false).
func OptInt(p map[string]any, key string) (int, bool, error) {
	v, ok := p[key]
	if !ok || v == nil {
		return 0, false, nil
	}
	switch t := v.(type) {
	case int64:
		return int(t), true, nil
	case int:
		return t, true, nil
	default:
		return 0, true, fmt.Errorf("option %q must be an integer", key)
	}
}

// OptBool reads a boolean option; missing keys are false.
func OptBool(p map[string]any, key string) (bool, error) {
	v, ok := p[key]
	if !ok || v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("option %q must be a boolean", key)
	}
	return b, nil
}

// OptConnStr reads conn_str, accepting either a plain string or a list of
// strings (the form shared ODBC profiles use).
// List elements are joined with a single space.
func OptConnStr(p map[string]any) (string, error) {
	v, ok := p["conn_str"]
	if !ok || v == nil {
		return "", nil
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return "", fmt.Errorf("option \"conn_str\" must be a string or a list of strings")
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " "), nil
	default:
		return "", fmt.Errorf("option \"conn_str\" must be a string or a list of strings")
	}
}

// DBOpt reads the database name option, where database is an alias of dbname.
func DBOpt(p map[string]any) (string, error) {
	name, err := OptStr(p, "dbname")
	if err != nil {
		return "", err
	}
	if name == "" {
		if v, ok := p["database"]; ok && v != nil {
			name, err = OptStr(p, "database")
			if err != nil {
				return "", err
			}
		}
	}
	return name, nil
}

// urlSchemes are the conn_str forms parsed as URLs.
var urlSchemes = []string{"postgres://", "mysql://", "sqlserver://", "http://", "https://", "clickhouse://"}

// FillFromURL returns a copy of p with connection keys filled in from a
// URL-form conn_str. Explicit keys in the profile take precedence over URL
// parts, so URL fields only fill keys that are absent.
func FillFromURL(p map[string]any, adapter string) (map[string]any, error) {
	cs, err := OptConnStr(p)
	if err != nil || cs == "" {
		return p, err
	}
	isURL := false
	for _, s := range urlSchemes {
		if strings.HasPrefix(cs, s) {
			isURL = true
			break
		}
	}
	if !isURL {
		return p, nil
	}
	u, err := url.Parse(cs)
	if err != nil {
		return nil, fmt.Errorf("could not parse conn_str %q: %v", cs, err)
	}
	q := make(map[string]any, len(p))
	for k, v := range p {
		q[k] = v
	}
	set := func(key, val string) {
		if val != "" {
			if _, ok := q[key]; !ok {
				q[key] = val
			}
		}
	}
	if adapter == "clickhouse" {
		set("database", strings.TrimPrefix(u.Path, "/"))
	} else {
		set("dbname", strings.TrimPrefix(u.Path, "/"))
	}
	set("host", u.Hostname())
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err == nil {
			if _, ok := q["port"]; !ok {
				q["port"] = int64(n)
			}
		}
	}
	if u.User != nil {
		set("user", u.User.Username())
		if pw, ok := u.User.Password(); ok {
			set("password", pw)
		}
	}
	return q, nil
}

// SortedProfileNames returns profile names in stable order.
func SortedProfileNames(c *Config) []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
