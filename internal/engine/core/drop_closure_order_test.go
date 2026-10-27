package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
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

func TestDropClosureSeededRootDiagnosticOrder(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE EXTENSION cube",
		"CREATE EXTENSION earthdistance",
		"CREATE TABLE closure_earth(v earth)",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	var cube, earthdistance ids.ObjectAddress
	for address, descriptor := range base.Descriptors() {
		if ext, ok := descriptor.(*catalog.Extension); ok {
			switch ext.Name() {
			case "cube":
				cube = address
			case "earthdistance":
				earthdistance = address
			}
		}
	}
	if cube.ObjectID == 0 || earthdistance.ObjectID == 0 {
		t.Fatal("missing actual extension owners")
	}
	holder := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_earth").Address)
	column := holder.Address()
	column.SubID = holder.Attributes[0]
	earth := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(holder.Columns[0].Usage.Type.ID())}
	roots := extension.RemovalRoots(base, []ids.ObjectAddress{cube})
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, root := range roots {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), root)); err != nil {
			t.Fatal(err)
		}
	}
	before := stage.RemovalCount()
	edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), roots)
	if err != nil {
		t.Fatal(err)
	}
	extensionSlot, columnSlot := -1, -1
	for i, edge := range edges {
		if edge.Address == earthdistance {
			if extensionSlot >= 0 || edge.Target != cube || edge.Automatic || !edge.Normal {
				t.Fatal("prerequisite edge changed", edge)
			}
			extensionSlot = i
		}
		if edge.Address == column {
			if columnSlot >= 0 || edge.Target != earth || edge.Automatic || !edge.Normal {
				t.Fatal("column parent/permission changed", edge)
			}
			columnSlot = i
		}
	}
	if extensionSlot < 0 || columnSlot <= extensionSlot || stage.RemovalCount() != before {
		t.Fatal("seeded provider preempted incumbent diagnostic order", extensionSlot, columnSlot)
	}
}
