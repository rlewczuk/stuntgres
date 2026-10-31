package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

// Result carriers retain diagnostic and traversal authority independently of
// whether the same physical object has already been removed from the stage.
func TestDropClosureRemovedIndexCarrier(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE TABLE closure_index_parent(i integer PRIMARY KEY)",
		"CREATE TABLE closure_index_child(i integer REFERENCES closure_index_parent(i))",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	parent := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_index_parent").Address)
	child := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_index_child").Address)
	fk := child.Constraints.ForeignKeys[0]
	index := base.MustRelation(fk.Index)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), index.Address())); err != nil {
		t.Fatal(err)
	}
	before := stage.RemovalCount()
	edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), []ids.ObjectAddress{parent.Address()})
	if err != nil {
		t.Fatal(err)
	}
	indexes, constraints := 0, 0
	for _, edge := range edges {
		if edge.Address == index.Address() {
			indexes++
			if !edge.Automatic || edge.Relation != index || edge.Target != parent.Address() {
				t.Fatal("removed index lost its actual AUTO carrier", edge)
			}
		}
		if edge.Address == (ids.ObjectAddress{ClassID: 2606, ObjectID: fk.OID}) {
			constraints++
			if edge.Automatic || !edge.Normal || edge.Relation != child {
				t.Fatal("index consumer lost independent dependency", edge)
			}
		}
	}
	if indexes != 1 || constraints != 1 || stage.RemovalCount() != before {
		t.Fatal("missing/duplicate carrier or speculative removal", indexes, constraints)
	}
}
