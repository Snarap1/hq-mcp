package main

import "testing"

func TestRedisCommandViolation(t *testing.T) {
	allowed := []string{
		"GET user:1000", "get user:1000", "SCAN 0 MATCH user:* COUNT 100",
		"HGETALL user:1000", "SMEMBERS user:1000:roles", "TTL user:1000",
		"INFO", "PING", "ZRANGE z 0 -1", "XINFO STREAM s", "GEOSEARCH g FROMMEMBER m BYRADIUS 1 m ASC",
	}
	for _, cmd := range allowed {
		if v := redisCommandViolation(cmd); v != "" {
			t.Errorf("redisCommandViolation(%q) = %q, want allowed", cmd, v)
		}
	}
	refused := []string{
		"DEL user:1000", "SET k v", "EXPIRE k 10", "EVAL \"return 1\" 0",
		"CONFIG SET maxmemory 1mb", "SUBSCRIBE ch", "MONITOR", "FLUSHALL",
		"SHUTDOWN NOSAVE", "BLPOP q 0", "AUTH pw", "SELECT 1", "MULTI",
		"", "   ",
	}
	for _, cmd := range refused {
		if v := redisCommandViolation(cmd); v == "" {
			t.Errorf("redisCommandViolation(%q) = \"\", want refused", cmd)
		}
	}
}

func TestRedisRefusalMessage(t *testing.T) {
	got := redisCommandViolation("DEL user:1000")
	want := `command "DEL" is not allowed; this server permits read-only Redis commands only`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRedisEmptyCommandRefused(t *testing.T) {
	if got := redisCommandViolation("   "); got != "empty command" {
		t.Errorf("got %q, want %q", got, "empty command")
	}
}

func TestJSONValueConversion(t *testing.T) {
	now := mustTime(t, "2026-09-28T10:11:12Z")
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"bool", true, true},
		{"int64", int64(42), int64(42)},
		{"int widens to int64", 7, int64(7)},
		{"float", 1.5, 1.5},
		{"string", "x", "x"},
		{"utf8 bytes", []byte("hello"), "hello"},
		{"invalid utf8 bytes", []byte{0xff, 0xfe}, "b64://4="},
		{"time", now, "2026-09-28T10:11:12Z"},
		{"slice", []any{[]byte("a"), int64(1)}, []any{"a", int64(1)}},
		{"map", map[string]any{"k": []byte("v")}, map[string]any{"k": "v"}},
		{"nested", []any{map[string]any{"a": []any{int64(2)}}}, []any{map[string]any{"a": []any{int64(2)}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsonValue(tc.in)
			if !equalJSON(t, got, tc.want) {
				t.Errorf("jsonValue(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
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
