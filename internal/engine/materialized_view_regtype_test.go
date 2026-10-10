package engine

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestMaterializedViewRegtypePresentation(t *testing.T) {
	source := New()
	_, sourceDone, sourceStage, original := materializedUnitStage(t, source)
	defer sourceDone()
	regtype, ok := sourceStage.base.LookupType(sqltype.Regtype)
	if !ok {
		t.Fatal("missing typed identity provider")
	}
	builtin, err := sqltype.RegtypeValue(regtype, uint32(sqltype.Int4))
	if err != nil {
		t.Fatal(err)
	}
	renderer := pgRenderer{view: source.stageRead(sourceStage), path: []ObjectAddress{original.namespace}}
	text, err := renderer.literal(context.Background(), literalExpression{expressionInfo: expressionInfo{usage: regtype.Usage()}, value: builtin})
	if err != nil || text != "'integer'::regtype" {
		t.Fatalf("builtin symbolic deparse: %q %v", text, err)
	}
	value, err := sqltype.RegtypeValue(regtype, uint32(original.rowType))
	if err != nil {
		t.Fatal(err)
	}
	destination := New()
	destination.mu.Lock()
	_, err = destination.allocateOIDLocked()
	destination.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, destinationDone, destinationStage, reminted := materializedUnitStage(t, destination)
	defer destinationDone()
	if original.rowType == reminted.rowType {
		t.Fatal("fixture did not remint row-type identity")
	}
	value, err = remapRegtypeDatum(value, func(id OID) OID {
		if id == OID(original.rowType) {
			return OID(reminted.rowType)
		}
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	destinationRegtype, ok := destinationStage.base.LookupType(sqltype.Regtype)
	if !ok {
		t.Fatal("missing destination identity provider")
	}
	value, err = value.Retag(destinationRegtype)
	if err != nil {
		t.Fatal(err)
	}
	renderer = pgRenderer{view: destination.stageRead(destinationStage), path: []ObjectAddress{reminted.namespace}}
	text, err = renderer.literal(context.Background(), literalExpression{expressionInfo: expressionInfo{usage: destinationRegtype.Usage()}, value: value})
	if err != nil || text != "'unit_mv'::regtype" {
		t.Fatalf("reminted symbolic deparse: %q %v", text, err)
	}
}
