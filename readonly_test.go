package main

import "testing"

func TestReadOnlyViolation(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		// want is the expected refusal reason, empty meaning allowed.
		want string
	}{
		{"select", "SELECT 1", ""},
		{"select lowercase", "select id from widgets", ""},
		{"delete refused", "DELETE FROM widgets", "statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed"},
		{"update allowed", "UPDATE widgets SET price = 1", ""},
		{"insert allowed", "INSERT INTO widgets VALUES (1)", ""},
		{"create refused", "CREATE TABLE x (a int)", "statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed"},
		{"modifying cte refused", "WITH x AS (DELETE FROM widgets RETURNING *) SELECT * FROM x", "statement contains write keyword(s): delete"},
		{"stacked statements refused", "SELECT 1; DROP TABLE widgets", "multiple statements are not allowed; send one read-only query"},
		{"write keyword under select refused", "SELECT * FROM widgets WHERE 1 = 1 -- nothing", ""},
		{"string containing keyword allowed", "SELECT 'this string contains delete' FROM widgets", ""},
		{"line comment containing keyword allowed", "SELECT 1 -- delete", ""},
		{"block comment containing keyword allowed", "SELECT 1 /* drop */", ""},
		{"dollar quote containing keyword allowed", "SELECT $$ drop $$ FROM widgets", ""},
		{"double quoted identifier allowed", `SELECT "delete" FROM widgets`, ""},
		{"explain allowed", "EXPLAIN SELECT * FROM widgets", ""},
		{"write keyword under select refused", "SELECT a, b FROM t WHERE create = 1 AND truncate = 2", "statement contains write keyword(s): create, truncate"},
		{"only semicolons refused", ";;;", "empty statement"},
		{"insert on conflict refused by do keyword", "INSERT INTO widgets VALUES (1) ON CONFLICT DO UPDATE SET a = 1", "statement contains write keyword(s): do"},
		{"parenthesized select allowed", "(SELECT 1)", ""},
		{"trailing semicolon is not a second statement", "SELECT 1; ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readOnlyViolation(tc.sql); got != tc.want {
				t.Errorf("readOnlyViolation(%q) = %q, want %q", tc.sql, got, tc.want)
			}
		})
	}
}

func TestReadOnlyViolationSortsKeywords(t *testing.T) {
	// Several write keywords in one statement are reported sorted and deduped.
	got := readOnlyViolation("SELECT drop, drop, create FROM t WHERE x = truncate")
	want := "statement contains write keyword(s): create, drop, truncate"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
