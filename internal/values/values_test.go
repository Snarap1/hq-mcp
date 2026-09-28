package values

import (
	"encoding/base64"
	"reflect"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

func TestJSON(t *testing.T) {
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
			got := JSON(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("JSON(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestJSONBase64PrefixForBinaryValues(t *testing.T) {
	raw := []byte{0x00, 0xff, 0x10}
	got := JSON(raw)
	want := "b64:" + base64.StdEncoding.EncodeToString(raw)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
