package extension_test

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
