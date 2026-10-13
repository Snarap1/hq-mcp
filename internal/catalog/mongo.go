package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"hq-mcp/internal/adapters"
)

// mongoColumnSamples is how many documents get_columns samples to infer a
// collection's fields; MongoDB has no fixed schema to read.
const mongoColumnSamples = 100

// mongoSystemDatabases are the built-in databases hidden from get_schema.
var mongoSystemDatabases = map[string]bool{"admin": true, "config": true, "local": true}

// mongoSchemaNodes walks databases, then collections.
func mongoSchemaNodes(ctx context.Context, conn *adapters.Mongo, path []string, nameFilter string) ([]SchemaNode, error) {
	match := func(name string) bool {
		return nameFilter == "" || strings.Contains(strings.ToLower(name), strings.ToLower(nameFilter))
	}
	var nodes []SchemaNode
	switch len(path) {
	case 0:
		names, err := conn.Client().ListDatabaseNames(ctx, bson.D{})
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if !mongoSystemDatabases[n] && match(n) {
				nodes = append(nodes, SchemaNode{Name: n, Kind: "database"})
			}
		}
	case 1:
		specs, err := conn.Client().Database(path[0]).ListCollectionSpecifications(ctx, bson.D{})
		if err != nil {
			return nil, err
		}
		for _, s := range specs {
			if strings.HasPrefix(s.Name, "system.") || !match(s.Name) {
				continue
			}
			kind := "collection"
			if s.Type == "view" {
				kind = "view"
			}
			nodes = append(nodes, SchemaNode{Name: s.Name, Kind: kind})
		}
	default:
		return nil, fmt.Errorf("path %q is too deep for mongodb; expected [database] or [database, collection]",
			strings.Join(path, "."))
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes, nil
}

// mongoColumns infers top-level fields from a sample of documents: the type
// lists every BSON type seen, and a field is nullable when it is null or
// missing in some sampled document.
func mongoColumns(ctx context.Context, conn *adapters.Mongo, database, collection string) (*ColumnsOut, error) {
	cur, err := conn.Client().Database(database).Collection(collection).
		Find(ctx, bson.D{}, options.Find().SetLimit(mongoColumnSamples))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	type field struct {
		types   []string
		seen    int
		hasNull bool
	}
	fields := map[string]*field{}
	var order []string
	docs := 0
	for cur.Next(ctx) {
		docs++
		elems, err := cur.Current.Elements()
		if err != nil {
			return nil, err
		}
		for _, e := range elems {
			name := e.Key()
			f := fields[name]
			if f == nil {
				f = &field{}
				fields[name] = f
				order = append(order, name)
			}
			f.seen++
			typ := e.Value().Type
			if typ == bson.TypeNull {
				f.hasNull = true
				continue
			}
			if t := typ.String(); !containsString(f.types, t) {
				f.types = append(f.types, t)
			}
		}
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	out := &ColumnsOut{Table: database + "." + collection, Columns: []Column{}}
	for _, name := range order {
		f := fields[name]
		typ := strings.Join(f.types, "|")
		if typ == "" {
			typ = "null"
		}
		nullable := "NO"
		if f.hasNull || f.seen < docs {
			nullable = "YES"
		}
		out.Columns = append(out.Columns, Column{Name: name, Type: typ, Nullable: nullable})
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
