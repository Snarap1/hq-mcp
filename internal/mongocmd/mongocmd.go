// Package mongocmd runs one read-only MongoDB operation: find, aggregate,
// count, or distinct.
package mongocmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"hq-mcp/internal/adapters"
)

// Operations is every operation run_mongo accepts, in the order used by
// error messages.
const Operations = "find, aggregate, count, distinct"

// writeStages are the aggregation stages that write to a collection. They
// are refused wherever they appear, including inside $facet, $lookup, and
// $unionWith sub-pipelines.
var writeStages = map[string]bool{"$out": true, "$merge": true}

// Request is one run_mongo call. Filter, Projection, Sort, and Pipeline are
// MongoDB Extended JSON strings, so key order (which matters for sort) and
// typed values such as {"$oid": "..."} or {"$date": "..."} survive.
type Request struct {
	Database   string
	Collection string
	Operation  string
	Filter     string
	Projection string
	Sort       string
	Pipeline   string
	Field      string
	Limit      int
}

// Result is the JSON-safe outcome of one operation. Documents holds find and
// aggregate documents or distinct values; Count holds the count result or the
// number of returned documents.
type Result struct {
	Documents []any
	Count     int64
	Truncated bool
}

// PipelineViolation returns a reason the pipeline is refused, or "" if it
// contains no write stage.
func PipelineViolation(pipeline bson.A) string {
	if stage := findWriteStage(pipeline); stage != "" {
		return fmt.Sprintf("aggregation stage %q writes data; this server permits reads only", stage)
	}
	return ""
}

func findWriteStage(v any) string {
	switch t := v.(type) {
	case bson.D:
		for _, e := range t {
			if writeStages[e.Key] {
				return e.Key
			}
			if s := findWriteStage(e.Value); s != "" {
				return s
			}
		}
	case bson.A:
		for _, e := range t {
			if s := findWriteStage(e); s != "" {
				return s
			}
		}
	}
	return ""
}

// parseDoc parses an Extended JSON object; "" is an empty document.
func parseDoc(name, s string) (bson.D, error) {
	if strings.TrimSpace(s) == "" {
		return bson.D{}, nil
	}
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(s), false, &d); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %v", name, err)
	}
	return d, nil
}

// parsePipeline parses an Extended JSON array of stage documents.
func parsePipeline(s string) (bson.A, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("aggregate requires pipeline")
	}
	// UnmarshalExtJSON only decodes documents, so wrap the array in one.
	var wrapper struct {
		Pipeline []bson.D `bson:"p"`
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"p":`+s+`}`), false, &wrapper); err != nil {
		return nil, fmt.Errorf("pipeline must be a JSON array of stage objects: %v", err)
	}
	out := make(bson.A, len(wrapper.Pipeline))
	for i, d := range wrapper.Pipeline {
		out[i] = d
	}
	return out, nil
}

// JSON converts a BSON value into a JSON-safe value via relaxed Extended
// JSON: ObjectIDs become {"$oid": ...}, dates {"$date": ...}, and plain
// numbers stay numbers.
func JSON(v any) (any, error) {
	b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		return nil, err
	}
	var out struct {
		V any `json:"v"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out.V, nil
}

// Run executes one read-only operation against the request's collection.
// Limit bounds find, aggregate, and distinct results; 0 means unlimited.
func Run(ctx context.Context, c *adapters.Mongo, req Request) (*Result, error) {
	database := req.Database
	if database == "" {
		database = c.Database()
	}
	if database == "" {
		return nil, fmt.Errorf("no database given and the profile has no database")
	}
	if req.Collection == "" {
		return nil, fmt.Errorf("collection is required")
	}
	coll := c.Client().Database(database).Collection(req.Collection)
	filter, err := parseDoc("filter", req.Filter)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(req.Operation)) {
	case "find", "":
		return find(ctx, coll, filter, req)
	case "aggregate":
		pipeline, err := parsePipeline(req.Pipeline)
		if err != nil {
			return nil, err
		}
		if violation := PipelineViolation(pipeline); violation != "" {
			return nil, fmt.Errorf("Refused (read-only server): %s", violation)
		}
		cur, err := coll.Aggregate(ctx, pipeline)
		if err != nil {
			return nil, err
		}
		return drain(ctx, cur, req.Limit)
	case "count":
		n, err := coll.CountDocuments(ctx, filter)
		if err != nil {
			return nil, err
		}
		return &Result{Documents: []any{}, Count: n}, nil
	case "distinct":
		if req.Field == "" {
			return nil, fmt.Errorf("distinct requires field")
		}
		var vals bson.A
		if err := coll.Distinct(ctx, req.Field, filter).Decode(&vals); err != nil {
			return nil, err
		}
		res := &Result{Documents: []any{}}
		for i, v := range vals {
			if req.Limit > 0 && i >= req.Limit {
				res.Truncated = true
				break
			}
			j, err := JSON(v)
			if err != nil {
				return nil, err
			}
			res.Documents = append(res.Documents, j)
		}
		res.Count = int64(len(res.Documents))
		return res, nil
	default:
		return nil, fmt.Errorf("operation %q is not supported; supported: %s", req.Operation, Operations)
	}
}

func find(ctx context.Context, coll *mongo.Collection, filter bson.D, req Request) (*Result, error) {
	opts := options.Find()
	if req.Projection != "" {
		proj, err := parseDoc("projection", req.Projection)
		if err != nil {
			return nil, err
		}
		opts.SetProjection(proj)
	}
	if req.Sort != "" {
		sort, err := parseDoc("sort", req.Sort)
		if err != nil {
			return nil, err
		}
		opts.SetSort(sort)
	}
	if req.Limit > 0 {
		// One extra document tells whether the result was truncated.
		opts.SetLimit(int64(req.Limit) + 1)
	}
	cur, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	return drain(ctx, cur, req.Limit)
}

// drain reads up to limit documents (0 means all) and closes the cursor.
func drain(ctx context.Context, cur *mongo.Cursor, limit int) (*Result, error) {
	defer cur.Close(ctx)
	res := &Result{Documents: []any{}}
	for cur.Next(ctx) {
		if limit > 0 && len(res.Documents) >= limit {
			res.Truncated = true
			break
		}
		j, err := JSON(cur.Current)
		if err != nil {
			return nil, err
		}
		res.Documents = append(res.Documents, j)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	res.Count = int64(len(res.Documents))
	return res, nil
}
