package tables_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

// These units use the actual catalog-stage reader and admitted descriptors.
// Closure discovery must merge provenance without creating removal effects.
func TestDropClosureRetiredInheritance(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()")
	enginetest.Run(t, e, "CREATE TABLE closure_parent(i integer)")
	enginetest.Run(t, e, "CREATE TABLE closure_child() INHERITS(closure_parent)")
	base := e.CatalogRead()
	parent := enginetest.RoleTableRef(t, e, "closure_parent").Address
	child := enginetest.RoleTableRef(t, e, "closure_child").Address
	for _, roots := range [][]ids.ObjectAddress{{parent, child}, {child, parent}} {
		stage := e.BeginCatalogStage(base)
		reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
		reader.MarkDropRoots(roots)
		for _, address := range roots {
			if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), address)); err != nil {
				t.Fatal(err)
			}
		}
		removals := stage.RemovalCount()
		for range 2 {
			edges, err := view.DropClosure(reader, append(slices.Clone(roots), roots...))
			if err != nil {
				t.Fatal(err)
			}
			if len(edges) != 0 || stage.RemovalCount() != removals {
				t.Fatal("retired roots produced duplicate effects/reporting", edges)
			}
			if got := stage.DropFlags[parent]; !got.Original || got.Normal {
				t.Fatal("parent flags", got)
			}
			if got := stage.DropFlags[child]; !got.Original || !got.Normal {
				t.Fatal("child lost independent NORMAL", got)
			}
			for _, address := range roots {
				relation := base.MustRelation(address)
				row := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
				for _, oid := range []ids.OID{ids.OID(relation.RowType), ids.OID(row.Array)} {
					if got := stage.DropFlags[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: oid}]; got.Original || got.Normal {
						t.Fatal("implicit type inherited flags", oid, got)
					}
				}
			}
		}
		e.DiscardCatalogStage(stage)
	}
	// The same real edge still admits a live dependent for RESTRICT/CASCADE.
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), []ids.ObjectAddress{parent})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range edges {
		if edge.Address == child {
			found = true
			if !edge.Normal || edge.Automatic {
				t.Fatal("live inheritance permission changed", edge)
			}
		}
	}
	if !found {
		t.Fatal("live inheritance dependency disappeared")
	}
}
