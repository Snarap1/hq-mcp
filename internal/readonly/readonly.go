// Package readonly screens SQL statements for a read-only server.
package readonly

import (
	"regexp"
	"sort"
	"strings"
)

// readOnlyPrefixes are the statements even considered read-only.
// INSERT and UPDATE are intentionally allowed (parity with the user's
// modified Python server).
var readOnlyPrefixes = []string{
	"select", "with", "explain", "show", "describe", "desc", "table", "insert", "update",
}

// writeKeywords are refused anywhere in the body (outside strings/comments),
// even when the statement starts with a read-only prefix (e.g. a
// data-modifying CTE: WITH x AS (DELETE ...) SELECT ...).
var writeKeywords = map[string]bool{
	"delete": true, "drop": true, "truncate": true, "alter": true, "create": true,
	"replace": true, "merge": true, "upsert": true, "grant": true, "revoke": true,
	"vacuum": true, "attach": true, "detach": true, "copy": true, "call": true,
	"do": true, "lock": true, "rename": true, "reindex": true, "refresh": true,
	"comment": true, "nextval": true, "setval": true,
}

var (
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reLineComment  = regexp.MustCompile(`--[^\n]*`)
	reDollarQuote  = regexp.MustCompile(`(?s)\$\$.*?\$\$`)
	reSingleQuote  = regexp.MustCompile(`'(?:[^']|'')*'`)
	reDoubleQuote  = regexp.MustCompile(`"(?:[^"]|"")*"`)
	reWord         = regexp.MustCompile(`[a-z_]+`)
)

// stripSQL removes comments and string/dollar-quoted literals so keyword
// scanning of the remaining SQL can't be fooled by text inside strings or
// tripped by it.
func stripSQL(sql string) string {
	sql = reBlockComment.ReplaceAllString(sql, " ") // /* block comments */
	sql = reLineComment.ReplaceAllString(sql, " ")  // -- line comments */
	sql = reDollarQuote.ReplaceAllString(sql, " ")  // $$ dollar-quoted $$
	sql = reSingleQuote.ReplaceAllString(sql, " ")  // 'single quoted'
	sql = reDoubleQuote.ReplaceAllString(sql, " ")  // "quoted identifiers"
	return sql
}

// Violation returns a human-readable reason the SQL is refused, or "" if it
// is read-only.
//
// Defense in depth: requires a read-only opening keyword, rejects stacked
// statements, and rejects any data/DDL-changing keyword anywhere in the body.
func Violation(sql string) string {
	cleaned := stripSQL(sql)
	var statements []string
	for _, s := range strings.Split(cleaned, ";") {
		if strings.TrimSpace(s) != "" {
			statements = append(statements, s)
		}
	}
	if len(statements) > 1 {
		return "multiple statements are not allowed; send one read-only query"
	}
	body := ""
	if len(statements) == 1 {
		body = strings.TrimSpace(statements[0])
	}
	if body == "" {
		return "empty statement"
	}
	lower := strings.ToLower(strings.TrimLeft(body, "("))
	matched := false
	for _, p := range readOnlyPrefixes {
		if strings.HasPrefix(lower, p) {
			matched = true
			break
		}
	}
	if !matched {
		return "statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / " +
			"DESCRIBE / TABLE / INSERT / UPDATE queries are allowed"
	}
	var found []string
	seen := map[string]bool{}
	for _, w := range reWord.FindAllString(lower, -1) {
		if writeKeywords[w] && !seen[w] {
			seen[w] = true
			found = append(found, w)
		}
	}
	if len(found) > 0 {
		sort.Strings(found)
		return "statement contains write keyword(s): " + strings.Join(found, ", ")
	}
	return ""
}
