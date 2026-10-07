package mongocmd

import (
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPipelineViolation(t *testing.T) {
	allowed := []string{
		`[{"$match": {"status": "active"}}, {"$group": {"_id": "$kind", "n": {"$sum": 1}}}]`,
		`[{"$lookup": {"from": "b", "pipeline": [{"$match": {}}], "as": "bs"}}]`,
		`[{"$match": {"note": "$out"}}]`,
		`[]`,
	}
	for _, p := range allowed {
		pipeline, err := parsePipeline(p)
		if err != nil {
			t.Fatalf("parsePipeline(%s): %v", p, err)
		}
		if v := PipelineViolation(pipeline); v != "" {
			t.Errorf("PipelineViolation(%s) = %q, want allowed", p, v)
		}
	}
	refused := map[string]string{
		`[{"$match": {}}, {"$out": "copy"}]`:                                       "$out",
		`[{"$merge": {"into": "copy"}}]`:                                           "$merge",
		`[{"$facet": {"a": [{"$match": {}}], "b": [{"$out": "x"}]}}]`:              "$out",
		`[{"$unionWith": {"coll": "b", "pipeline": [{"$merge": {"into": "x"}}]}}]`: "$merge",
	}
	for p, stage := range refused {
		pipeline, err := parsePipeline(p)
		if err != nil {
			t.Fatalf("parsePipeline(%s): %v", p, err)
		}
		want := `aggregation stage "` + stage + `" writes data; this server permits reads only`
		if v := PipelineViolation(pipeline); v != want {
			t.Errorf("PipelineViolation(%s) = %q, want %q", p, v, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := parsePipeline(""); err == nil || err.Error() != "aggregate requires pipeline" {
		t.Errorf("empty pipeline: got %v", err)
	}
	if _, err := parsePipeline(`{"$match": {}}`); err == nil || !strings.HasPrefix(err.Error(), "pipeline must be a JSON array") {
		t.Errorf("object pipeline: got %v", err)
	}
	if _, err := parseDoc("filter", `[1]`); err == nil || !strings.HasPrefix(err.Error(), "filter must be a JSON object") {
		t.Errorf("array filter: got %v", err)
	}
	if d, err := parseDoc("filter", "  "); err != nil || len(d) != 0 {
		t.Errorf("blank filter: got %v, %v", d, err)
	}
}

func TestParseDocKeepsOrderAndTypes(t *testing.T) {
	d, err := parseDoc("sort", `{"b": -1, "a": 1, "_id": {"$oid": "65a1b2c3d4e5f60718293a4b"}}`)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{d[0].Key, d[1].Key, d[2].Key}
	if !reflect.DeepEqual(keys, []string{"b", "a", "_id"}) {
		t.Errorf("keys = %v, want order preserved", keys)
	}
	if _, ok := d[2].Value.(bson.ObjectID); !ok {
		t.Errorf("_id decoded as %T, want bson.ObjectID", d[2].Value)
	}
}

func TestJSON(t *testing.T) {
	oid, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4b")
	got, err := JSON(bson.D{{Key: "_id", Value: oid}, {Key: "n", Value: int32(3)}, {Key: "tags", Value: bson.A{"x"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"_id":  map[string]any{"$oid": "65a1b2c3d4e5f60718293a4b"},
		"n":    float64(3),
		"tags": []any{"x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("JSON = %#v, want %#v", got, want)
	}
}
