package main

import (
	"encoding/base64"
	"reflect"
	"testing"
	"time"
)

// mustTime parses an RFC3339 timestamp or fails the test.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

// equalJSON compares two converted values structurally.
func equalJSON(t *testing.T, got, want any) bool {
	t.Helper()
	return reflect.DeepEqual(got, want)
}

func TestRunQueryRefusesWrites(t *testing.T) {
	// The screening happens before any connection is used, so a nil handle is fine.
	_, err := runQuery(nil, "DELETE FROM widgets", 10)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	want := "Refused (read-only server): statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestExportQueryRefusesWrites(t *testing.T) {
	_, err := exportQuery(nil, "DROP TABLE widgets", t.TempDir()+"/out.csv", "csv", 0)
	if err == nil || err.Error() != "Refused (read-only server): statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExportQueryMissingParentDir(t *testing.T) {
	_, err := exportQuery(nil, "SELECT 1", "/tmp/hq-mcp-does-not-exist/out.csv", "csv", 0)
	if err == nil {
		t.Fatal("expected an error for a missing parent directory")
	}
	if want := "parent directory does not exist: /tmp/hq-mcp-does-not-exist"; err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestBase64PrefixForBinaryValues(t *testing.T) {
	raw := []byte{0x00, 0xff, 0x10}
	got := jsonValue(raw)
	want := "b64:" + base64.StdEncoding.EncodeToString(raw)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRowObjectMapsColumns(t *testing.T) {
	got := rowObject([]string{"a", "b"}, []any{int64(1), "x"})
	want := map[string]any{"a": int64(1), "b": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestPlaceholders(t *testing.T) {
	if got := placeholders(3); got != "?, ?, ?" {
		t.Errorf("got %q", got)
	}
	if got := placeholders(1); got != "?" {
		t.Errorf("got %q", got)
	}
}

func TestLikeFilter(t *testing.T) {
	if got := likeFilter(""); got != "%%" {
		t.Errorf("got %q, want %%%%", got)
	}
	if got := likeFilter("wid"); got != "%wid%" {
		t.Errorf("got %q, want %%wid%%", got)
	}
}

func TestColumnsUsageExamples(t *testing.T) {
	if got := columnsUsage("postgres"); got != `["hqdb", "public", "widgets"]` {
		t.Errorf("postgres usage = %q", got)
	}
	if got := columnsUsage("clickhouse"); got != `["hqdb", "widgets"]` {
		t.Errorf("clickhouse usage = %q", got)
	}
}

func TestGetColumnsRejectsRedis(t *testing.T) {
	_, err := getColumns(t.Context(), &redisDB{sep: ":"}, []string{"user"})
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "get_columns is not available for redis; use get_schema"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestErrNotSQLNamesRedisEquivalent(t *testing.T) {
	if got := errNotSQL("redis", "run_query").Error(); got != "run_query is not available for redis; use run_redis" {
		t.Errorf("got %q", got)
	}
	if got := errNotSQL("duckdb", "run_query").Error(); got != "run_query is not available for duckdb" {
		t.Errorf("got %q", got)
	}
}
