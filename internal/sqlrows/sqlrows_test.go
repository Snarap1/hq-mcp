package sqlrows

import (
	"reflect"
	"testing"
)

func TestObjectMapsColumns(t *testing.T) {
	got := Object([]string{"a", "b"}, []any{int64(1), "x"})
	want := map[string]any{"a": int64(1), "b": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestObjectIgnoresExtraRowValues(t *testing.T) {
	got := Object([]string{"a"}, []any{int64(1), "dropped"})
	want := map[string]any{"a": int64(1)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}
