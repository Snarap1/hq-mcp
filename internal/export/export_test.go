package export

import "testing"

func TestQueryRefusesWrites(t *testing.T) {
	_, err := Query(nil, "DROP TABLE widgets", t.TempDir()+"/out.csv", "csv", 0)
	if err == nil || err.Error() != "Refused (read-only server): statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestQueryMissingParentDir(t *testing.T) {
	_, err := Query(nil, "SELECT 1", "/tmp/hq-mcp-does-not-exist/out.csv", "csv", 0)
	if err == nil {
		t.Fatal("expected an error for a missing parent directory")
	}
	if want := "parent directory does not exist: /tmp/hq-mcp-does-not-exist"; err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
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
		got, err := InferFormat(path)
		if err != nil {
			t.Fatalf("InferFormat(%q): %v", path, err)
		}
		if got != want {
			t.Errorf("InferFormat(%q) = %q, want %q", path, got, want)
		}
	}
	if _, err := InferFormat("/tmp/out.txt"); err == nil ||
		err.Error() != `cannot infer format from extension ".txt"; pass format explicitly` {
		t.Errorf("unexpected error for an unknown extension: %v", err)
	}
}

func TestStringify(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"x", "x"},
		{true, "true"},
		{int64(12), "12"},
		{1.5, "1.5"},
		{[]any{"a", int64(1)}, `["a",1]`},
	}
	for _, tc := range cases {
		if got := stringify(tc.in); got != tc.want {
			t.Errorf("stringify(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
