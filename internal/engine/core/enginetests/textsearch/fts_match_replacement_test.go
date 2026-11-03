package textsearch_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Replacement into a destination with retained TEMP tsvector state keeps the
// exact TEMP descriptors and roots, and publishes one builtin generation for
// every descriptor they link to, so match authority holds without relaxing it.
func TestFTSMatchTemporaryReplacementGeneration(t *testing.T) {
	ctx := context.Background()
	target := engine.New()
	session := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, session, "CREATE TEMP TABLE kept(id INT, v TSVECTOR)")
	enginetest.SessionSQL(t, session, "INSERT INTO kept VALUES (73,'retained:1')")
	enginetest.SessionSQL(t, session, "CREATE INDEX kept_idx ON kept USING gin(v)")
	lookup := func(name string) ids.ObjectAddress {
		t.Helper()
		matches := target.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: session.TemporaryNamespace, Name: name})
		if len(matches) != 1 {
			t.Fatal("missing temporary relation", name, matches)
		}
		return matches[0]
	}
	keptAddress, indexAddress := lookup("kept"), lookup("kept_idx")
	before := target.CatalogRead()
	kept, index := before.MustRelation(keptAddress), before.Get(indexAddress)
	store := target.Storage.Relations[keptAddress]
	if store == nil || len(store.Versions) != 1 {
		t.Fatal("missing retained TEMP rows")
	}
	row := store.Versions[0]
	vector := kept.Columns[1].Usage.Type

	source := engine.New()
	peer := enginetest.StartSession(t, source, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE gin_control_heap(id INT, a INT[], v TSVECTOR)")
	enginetest.SessionSQL(t, peer, "INSERT INTO gin_control_heap VALUES (1,'{1,2,2}','cat:1 rat:2')")
	enginetest.SessionSQL(t, peer, "CREATE INDEX gin_control_heap_idx ON gin_control_heap USING gin(a, v)")
	sourceRef := enginetest.CatalogRef(t, source, source.CatalogRead(), "gin_control_heap")
	sourceHeap := source.Catalog.State.Get(sourceRef.Address)
	sourceVector, _ := source.CatalogRead().LookupType(sqltype.TSVector)
	if sourceVector == vector {
		t.Fatal("fixture lacks distinct builtin generations")
	}
	foreignQuery, _ := source.CatalogRead().LookupType(sqltype.TSQuery)

	for i, replace := range []func() error{
		func() error { _, err := target.ReplaceCatalog(ctx, source, source.CatalogRead()); return err },
		func() error { _, err := target.ReplaceCatalog(ctx, source, source.CatalogRead()); return err },
		func() error {
			// The range check binds int4range @> int4 against the retained bool.
			return engine.Restore(ctx, target, strings.NewReader("CREATE TABLE gin_control_heap(id INT, a INT[], v TSVECTOR);"+
				"CREATE TABLE ranged(y INT4RANGE, CONSTRAINT c CHECK (y @> 1))"))
		},
	} {
		if err := replace(); err != nil {
			t.Fatal(i, err)
		}
		current := target.CatalogRead()
		if current.Get(keptAddress) != kept || current.Get(indexAddress) != index || target.Storage.Relations[keptAddress] != store {
			t.Fatal(i, "retained TEMP descriptor or root changed")
		}
		if got := store.Versions[0]; got.Row != row.Row || got.Version != row.Version || got.Born != row.Born || got.Values[1] != row.Values[1] {
			t.Fatal(i, "retained TEMP row lifetime changed")
		}
		if source.Catalog.State.Get(sourceRef.Address) != sourceHeap {
			t.Fatal(i, "replacement mutated source")
		}
		published := func(id sqltype.ID) *sqltype.Descriptor {
			t.Helper()
			d, ok := current.LookupType(id)
			if !ok {
				t.Fatal(i, "missing builtin", id)
			}
			return d
		}
		heap := current.MustRelation(enginetest.CatalogRef(t, target, current, "gin_control_heap").Address)
		if published(sqltype.TSVector) != vector || heap.Columns[2].Usage.Type != vector {
			t.Fatal(i, "imported tsvector left the retained generation")
		}
		boolean, query, text := published(sqltype.Bool).Usage(), published(sqltype.TSQuery).Usage(), published(sqltype.Text).Usage()
		for _, bind := range []struct {
			op   sqltype.FTSOperator
			args [3]sqltype.Usage
		}{
			{sqltype.FTSMatchVQ, [3]sqltype.Usage{vector.Usage(), query}},
			{sqltype.FTSMatchQV, [3]sqltype.Usage{query, vector.Usage()}},
			{sqltype.FTSMatchTQ, [3]sqltype.Usage{text, query}},
			{sqltype.FTSMatchTT, [3]sqltype.Usage{text, text}},
		} {
			if _, err := sqltype.BindFTS(bind.op, boolean, bind.args); err != nil {
				t.Fatal(i, "published match authority", bind.op, err)
			}
		}
		// A reverse peer outside text search: point links to the retained bool.
		point := published(sqltype.Point).Usage()
		if _, err := sqltype.BindPoint(sqltype.PointSame, boolean, point, point); err != nil {
			t.Fatal(i, "published point authority", err)
		}
		// Equal OIDs from another generation remain foreign in both operands.
		if _, err := sqltype.BindFTS(sqltype.FTSMatchVQ, boolean, [3]sqltype.Usage{vector.Usage(), foreignQuery.Usage()}); err == nil || err.Error() != "foreign FTS operand authority" {
			t.Fatal(i, "foreign source query accepted", err)
		}
		if _, err := sqltype.BindFTS(sqltype.FTSMatchVQ, boolean, [3]sqltype.Usage{sourceVector.Usage(), query}); err == nil || err.Error() != "foreign FTS operand authority" {
			t.Fatal(i, "foreign source vector accepted", err)
		}
	}
}
