package query

import "testing"

func TestRunRefusesWrites(t *testing.T) {
	// The screening happens before any connection is used, so a nil handle is fine.
	_, err := Run(nil, "DELETE FROM widgets", 10)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	want := "Refused (read-only server): statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestRunRefusesStackedStatements(t *testing.T) {
	_, err := Run(nil, "SELECT 1; SELECT 2", 10)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	want := "Refused (read-only server): multiple statements are not allowed; send one read-only query"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}
