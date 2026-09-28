package catalog

import (
	"testing"
)

// fakeRedis is a redis connection stub: GetColumns and GetSchema reject it on
// the adapter kind alone, before any client call.
type fakeRedis struct{}

func (fakeRedis) Kind() string { return "redis" }
func (fakeRedis) Close()       {}

func TestGetColumnsRejectsRedis(t *testing.T) {
	_, err := GetColumns(t.Context(), fakeRedis{}, []string{"user"})
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "get_columns is not available for redis; use get_schema"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
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

func TestRebindPlaceholdersForPostgres(t *testing.T) {
	// pgx needs $n; the other drivers keep '?'.
	if got := rebindPlaceholders("SELECT a FROM t WHERE b = ? AND c = ?", "postgres"); got != "SELECT a FROM t WHERE b = $1 AND c = $2" {
		t.Errorf("postgres: %q", got)
	}
	if got := rebindPlaceholders("SELECT a FROM t WHERE b = ?", "mysql"); got != "SELECT a FROM t WHERE b = ?" {
		t.Errorf("mysql: %q", got)
	}
}
