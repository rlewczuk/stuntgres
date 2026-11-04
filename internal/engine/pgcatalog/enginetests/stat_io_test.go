package enginetests_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGStatIOCapabilities(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID:ids.RelationClassID, ObjectID:pgcatalog.StatIOOID}
	in := pgcatalog.Input{Context:context.Background(), Catalog:view, Source:view.MustRelation(address), Ref:e.Catalog.Domain.Ref(view.Epoch(), address), Owner:view.Owner(), Runtime:core.NewCatalogRuntime(core.CatalogRuntimeFields{})}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatIOOID)
	if !ok { t.Fatal("missing IO producer") }
	names := strings.Fields("backend_type object context reads read_bytes read_time writes write_bytes write_time writebacks writeback_time extends extend_bytes extend_time hits evictions reuses fsyncs fsync_time stats_reset")
	types := []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Int8, sqltype.Numeric, sqltype.Float8, sqltype.Int8, sqltype.Numeric, sqltype.Float8, sqltype.Int8, sqltype.Float8, sqltype.Int8, sqltype.Numeric, sqltype.Float8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Float8, sqltype.Timestamptz}
	if len(in.Source.Columns)!=20 || in.Source.RowType!=12303 || !in.Source.VirtualView { t.Fatal("IO descriptor", in.Source) }
	for i, c := range in.Source.Columns {
		owned, ok := view.LookupType(types[i])
		if !ok || c.Usage.Type!=owned || c.Name!=names[i] || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod!=-1 || c.DeclaredDimensions!=0 || c.OriginTable!=12301 || c.OriginAttribute!=int16(i+1) { t.Fatal("owned IO column", i, c) }
	}
	rows, err := produce(in)
	if err!=nil || len(rows)!=79 { t.Fatal("79 capabilities", len(rows), err) }
	seen := map[string]bool{}
	// Literal masks independently cover every emitted key. As in the released
	// regression table, groups share outcomes, not a production support predicate.
	expected := map[string]string{}
	for _, g := range []struct{ names, relations, temp, wal string }{
		{"autovacuum launcher", "bulkread:rwbhuv init:rwbhvf normal:rwbhvf", "", "wf"},
		{"autovacuum worker", "bulkread:rwbhuv init:rwbehvf normal:rwbehvf vacuum:rwbehuv", "", "wf"},
		{"background writer", "init:wbf normal:wbf", "", "wf"},
		{"checkpointer", "init:wbf normal:wbf", "", "rwf"},
		{"background worker|client backend|io worker|slotsync worker|walsender", "bulkread:rwbhuv bulkwrite:rwbehuv init:rwbehvf normal:rwbehvf vacuum:rwbehuv", "rwehv", "rwf"},
		{"standalone backend|startup", "bulkread:rwbhuv bulkwrite:rwbehuv init:rwbehvf normal:rwbehvf vacuum:rwbehuv", "", "rwf"},
		{"walreceiver|walwriter", "", "", "wf"},
		{"walsummarizer", "", "", "rwf"},
	} {
		for _, backend := range strings.Split(g.names, "|") {
			for _, pair := range strings.Fields(g.relations) {
				parts := strings.Split(pair, ":")
				expected[backend+"/relation/"+parts[0]]=parts[1]
			}
			if g.temp!="" { expected[backend+"/temp relation/normal"]=g.temp }
			expected[backend+"/wal/init"]="wf"
			expected[backend+"/wal/normal"]=g.wal
		}
	}
	for _, row := range rows {
		key := ""
		for i := range 3 {
			text, err := row[i].Type().Output(in.Context, row[i])
			if err!=nil { t.Fatal(err) }
			if i>0 { key+="/" }
			key+=text
		}
		mask, ok := expected[key]
		if !ok || seen[key] { t.Fatal("duplicate/fabricated key", key) }
		seen[key]=true
		for j, operation := range "rrrwwwbbeeehvuff" {
			i:=j+3
			if row[i].Type()!=in.Source.Columns[i].Usage.Type || row[i].IsNull()==strings.ContainsRune(mask, operation) { t.Fatal("typed capability mask", key, names[i], row[i]) }
			if !row[i].IsNull() {
				text, err := row[i].Type().Output(in.Context, row[i])
				if err!=nil || text!="0" { t.Fatal("typed absent-facility zero", key, names[i], text, err) }
			}
		}
		if !row[19].IsNull() || row[19].Type()!=in.Source.Columns[19].Usage.Type { t.Fatal("absent reset must be typed NULL", row[19]) }
	}
	if len(seen)!=len(expected) || len(expected)!=79 { t.Fatal("complete capabilities", len(seen), len(expected)) }
	ctx, cancel := context.WithCancel(in.Context)
	cancel()
	in.Context=ctx
	if rows, err:=produce(in); rows!=nil || !errors.Is(err, context.Canceled) { t.Fatal("canceled IO capture published rows", rows, err) }
}
