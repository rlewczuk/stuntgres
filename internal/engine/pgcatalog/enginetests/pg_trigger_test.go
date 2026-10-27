package enginetests_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Synthetic constraint descriptors isolate projection from SQL and allocation.
// The public dropped-object scenarios capture allocator identities through DDL.
func TestPGTriggerForeignKeyCapture(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	source := base.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2620})
	produce, ok := pgcatalog.ProducerFor(2620)
	if !ok {
		t.Fatal("missing trigger producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: base, Source: source, Owner: base.Owner(), Runtime: core.EngineCatalogRuntime{}}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty trigger catalog", rows, err)
	}
	parent := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41001}
	child := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41002}
	constraint := catalog.ForeignKeyConstraint{OID: 41003, Name: "unit_fk", Target: parent, TriggerOIDs: [4]ids.OID{41004, 41005, 41006, 41007}}
	state := *base.State()
	state.SetDescriptor(parent, &catalog.Relation{ObjectAddress: parent, ObjectName: "unit_parent"})
	state.SetDescriptor(child, &catalog.Relation{ObjectAddress: child, ObjectName: "unit_child", Constraints: catalog.RelationConstraints{ForeignKeys: []catalog.ForeignKeyConstraint{constraint}}})
	retained := e.Catalog.Read(&state)
	input.Catalog = retained
	rows, err := produce(input)
	if err != nil || len(rows) != 4 {
		t.Fatal("four persistent triggers", rows, err)
	}
	seen := make(map[uint32]bool)
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatal("trigger row width", row)
		}
		for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Name, sqltype.Bool, sqltype.Oid} {
			owned, _ := retained.LookupType(id)
			if row[i].Type() != owned || row[i].IsNull() {
				t.Fatal("owned nonnull trigger cell", i, row[i])
			}
		}
		oid, err := row[0].OID()
		if err != nil || oid < 41004 || oid > 41007 || seen[oid] {
			t.Fatal("persistent trigger identity", oid, err)
		}
		seen[oid] = true
		wantRelation, kind := uint32(child.ObjectID), "c"
		if oid < 41006 {
			wantRelation, kind = uint32(parent.ObjectID), "a"
		}
		relation, err := row[1].OID()
		if err != nil || relation != wantRelation {
			t.Fatal("action/check relation", relation, err)
		}
		name, err := row[2].Text()
		if err != nil || name != "RI_ConstraintTrigger_"+kind+"_"+strconv.FormatUint(uint64(oid), 10) {
			t.Fatal("allocator-derived trigger name", row[2])
		}
		internal, err := row[3].Boolean()
		if err != nil || !internal {
			t.Fatal("RI internal flag", internal, err)
		}
		owner, err := row[4].OID()
		if err != nil || owner != uint32(constraint.OID) {
			t.Fatal("constraint identity", owner, err)
		}
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), retained, core.EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	dependencies := make(map[ids.OID]int)
	for _, edge := range edges {
		if edge.Dependent.ClassID == 2620 {
			if edge.Referenced != (ids.ObjectAddress{ClassID: 2606, ObjectID: constraint.OID}) || edge.DepType != 'i' {
				t.Fatal("RI constraint ownership", edge)
			}
			dependencies[edge.Dependent.ObjectID]++
		}
	}
	for _, oid := range constraint.TriggerOIDs {
		if dependencies[oid] != 1 {
			t.Fatal("one internal ownership edge", oid, dependencies)
		}
	}
	next := state
	owner := *next.MustRelation(child)
	owner.Constraints.ForeignKeys = nil
	next.SetDescriptor(child, &owner)
	input.Catalog = e.Catalog.Read(&next)
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("removed constraint retained live triggers", rows, err)
	}
	input.Catalog = retained
	if rows, err := produce(input); err != nil || len(rows) != 4 {
		t.Fatal("retained snapshot lost identities", rows, err)
	}
	invalid := *source
	invalid.Columns = source.Columns[:4]
	input.Source = &invalid
	if rows, err := produce(input); err == nil || rows != nil {
		t.Fatal("invalid trigger shape admitted", rows, err)
	}
	input.Source = source
	foreign := engine.New().CatalogRead()
	invalid = *source
	invalid.Columns = append([]catalog.Column(nil), source.Columns...)
	invalid.Columns[0].Usage.Type, _ = foreign.LookupType(sqltype.Oid)
	input.Source = &invalid
	if rows, err := produce(input); err == nil || rows != nil {
		t.Fatal("foreign trigger carrier admitted", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	if rows, err := produce(input); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("canceled projection", rows, err)
	}
}
