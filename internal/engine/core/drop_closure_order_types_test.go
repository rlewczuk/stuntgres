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
