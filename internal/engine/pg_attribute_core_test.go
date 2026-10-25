package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestPGAttributeCarrier(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, _ := pgAttributeUnitSource(t, e, view)
	u := d.Columns[24].Usage
	value := sqltype.CatalogResultNull(u)
	if err := exec.ValidateProducedValue(value, u); err != nil {
		t.Fatal("produced canonical NULL rejected", err)
	}
	result, err := AnonymousResult([]catalog.Column{d.Columns[24]}, [][]sqltype.Value{{value}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Rows.Close()
	dst := make([]sqltype.Value, 1)
	if present, err := result.Rows.Next(context.Background(), dst); !present || err != nil || !dst[0].IsNull() || dst[0].Type() != u.Type {
		t.Fatal("catalog result carrier", dst, present, err)
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: u}, Slot: 0, Scope: 1, Frame: 1}
	got, err := column.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{value}}})
	if err != nil || !got.ValidResult(u) {
		t.Fatal("projection carrier", got, err)
	}
	foreign := sqltype.CatalogResultNull(testUsage(New(), sqltype.ID(2277)))
	if err := exec.ValidateProducedValue(foreign, u); err == nil {
		t.Fatal("foreign produced NULL admitted")
	}
	if _, err := AnonymousResult([]catalog.Column{d.Columns[24]}, [][]sqltype.Value{{foreign}}, 1); err == nil {
		t.Fatal("foreign carrier descriptor admitted")
	}
	// Ordinary storage validation must not admit this pseudo type elsewhere.
	ordinary := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "bad", Owner: e.RoleStore.Domain.Principal(10), Columns: []catalog.Column{{Name: "a", Usage: u}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"a": 0}}
	if err := (RelationProvider).Validate(view, ordinary); err == nil {
		t.Fatal("ANYARRAY stored column admitted")
	}
	_, err = pgcatalog.ClassACL(&e.Catalog.Owner, New().Roles(), d.Columns[21].Usage.Type, authorization.ACLValue{Explicit: true})
	if err == nil {
		t.Fatal("foreign empty ACL authority admitted")
	}
}
