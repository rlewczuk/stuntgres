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

func TestDropClosureNestedColumnDiagnostics(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE SCHEMA closure_nested",
		"CREATE DOMAIN closure_nested.d AS integer",
		"CREATE TABLE closure_nested_holder(v closure_nested.d)",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	holder := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_nested_holder").Address)
	domain := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(holder.Columns[0].Usage.Type.ID())})
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), []ids.ObjectAddress{domain.Namespace()})
	if err != nil {
		t.Fatal(err)
	}
	column := holder.Address()
	column.SubID = holder.Attributes[0]
	visible := 0
	for _, edge := range edges {
		if edge.Automatic {
			continue
		}
		switch visible {
		case 0:
			if edge.Address != domain.Address() || edge.Target != domain.Namespace() {
				t.Fatal("schema diagnostic parent/order changed", edge)
			}
		case 1:
			if edge.Address != column || edge.Target != domain.Address() || edge.Relation != holder || !edge.Normal {
				t.Fatal("live owner consumed nested column diagnostic", edge)
			}
		default:
			t.Fatal("unexpected diagnostic carrier", edge)
		}
		visible++
	}
	if visible != 2 || stage.RemovalCount() != 0 {
		t.Fatal("lost nested RESTRICT detail or mutated stage", visible)
	}
}
