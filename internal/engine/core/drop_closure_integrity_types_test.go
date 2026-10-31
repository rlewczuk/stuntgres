package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

func TestDropClosureRangeNormalPermission(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE TYPE closure_integrity_endpoint AS ENUM('a','b')",
		"CREATE TYPE closure_integrity_range AS RANGE(SUBTYPE=closure_integrity_endpoint)",
		"CREATE TABLE closure_integrity_holder(v closure_integrity_range)",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	holder := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_integrity_holder").Address)
	rangeType := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(holder.Columns[0].Usage.Type.ID())})
	var endpoint ids.ObjectAddress
	for address, descriptor := range base.Descriptors() {
		if typ, ok := descriptor.(*catalog.Type); ok && typ.Name() == "closure_integrity_endpoint" {
			endpoint = address
		}
	}
	if endpoint.ObjectID == 0 {
		t.Fatal("missing actual subtype")
	}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), []ids.ObjectAddress{endpoint})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, edge := range edges {
		if edge.Address == rangeType.Address() {
			found++
			if !edge.Normal || edge.Automatic || edge.Target != endpoint {
				t.Fatal("owned multirange waived its owner's NORMAL dependency", edge)
			}
		}
	}
	if found != 1 || stage.RemovalCount() != 0 {
		t.Fatal("missing/duplicate range carrier or speculative effect", found)
	}
}
