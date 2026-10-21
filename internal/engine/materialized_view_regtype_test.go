package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func TestMaterializedViewRegtypePresentation(t *testing.T) {
	source := New()
	_, sourceDone, sourceStage, original := materializedUnitStage(t, source)
	defer sourceDone()
	regtype, ok := sourceStage.Base.LookupType(sqltype.Regtype)
	if !ok {
		t.Fatal("missing typed identity provider")
	}
	builtin, err := sqltype.RegtypeValue(regtype, uint32(sqltype.Int4))
	if err != nil {
		t.Fatal(err)
	}
	renderer := pgRenderer{view: source.stageRead(sourceStage), path: []ids.ObjectAddress{original.Namespace()}}
	text, err := renderer.literal(context.Background(), plan.LiteralExpr{Info: expr.Info{Usage: regtype.Usage()}, Value: builtin})
	if err != nil || text != "'integer'::regtype" {
		t.Fatalf("builtin symbolic deparse: %q %v", text, err)
	}
	value, err := sqltype.RegtypeValue(regtype, uint32(original.RowType))
	if err != nil {
		t.Fatal(err)
	}
	destination := New()
	destination.mu.Lock()
	_, err = destination.catalog.allocateOIDLocked()
	destination.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, destinationDone, destinationStage, reminted := materializedUnitStage(t, destination)
	defer destinationDone()
	if original.RowType == reminted.RowType {
		t.Fatal("fixture did not remint row-type identity")
	}
	value, err = regobj.RemapAliasDatum(value, func(address ids.ObjectAddress) ids.ObjectAddress {
		if address == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(original.RowType)}) {
			return ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(reminted.RowType)}
		}
		return address
	})
	if err != nil {
		t.Fatal(err)
	}
	destinationRegtype, ok := destinationStage.Base.LookupType(sqltype.Regtype)
	if !ok {
		t.Fatal("missing destination identity provider")
	}
	value, err = value.Retag(destinationRegtype)
	if err != nil {
		t.Fatal(err)
	}
	renderer = pgRenderer{view: destination.stageRead(destinationStage), path: []ids.ObjectAddress{reminted.Namespace()}}
	text, err = renderer.literal(context.Background(), plan.LiteralExpr{Info: expr.Info{Usage: destinationRegtype.Usage()}, Value: value})
	if err != nil || text != "'unit_mv'::regtype" {
		t.Fatalf("reminted symbolic deparse: %q %v", text, err)
	}
}
