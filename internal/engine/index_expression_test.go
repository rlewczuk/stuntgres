package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExpressionIndexDerivedRoots(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	u := typ.Usage()
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9000}, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "v", Usage: u}}, Index: map[string]int{"v": 0}}
	column := plan.ColumnExpr{Info: expr.Info{Usage: u, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	plan, err := sqltype.BindString("lower", u, u, []sqltype.Usage{u}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	node := functions.String{Info: column.Info, Plan: plan, Args: []expr.Expr{column}, Textual: [4]bool{true}}
	d := &catalog.IndexDefinition{Parent: parent.Address(), Keys: []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{Definition: node, Execution: node}}}, Ready: true, Valid: true, Live: true}
	values := []sqltype.Value{sqltype.String(typ, "Alpha"), sqltype.String(typ, "ALPHA"), sqltype.String(typ, "Beta"), sqltype.Null(typ), sqltype.String(typ, "alpha")}
	store := &storage.Relation{}
	for i, v := range values {
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Values: []sqltype.Value{v}})
	}
	root, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, d, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "alpha", "alpha", "beta", "NULL"}
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	publication := &rowPublication{writes: []storage.RowVersion{{Row: 6, Version: 6, Values: []sqltype.Value{sqltype.String(typ, "AlPhA")}}}}
	next, err := mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, &catalog.Relation{SQLIndex: d}, root.Entries, publication, false, true, false, "", sqltype.OutputEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := btreeKeys(t, next); !reflect.DeepEqual(got, []string{"alpha", "alpha", "alpha", "alpha", "beta", "NULL"}) {
		t.Fatal(got)
	}
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal("mutated predecessor", got)
	}
	text, err := values[0].Text()
	if err != nil || text != "Alpha" {
		t.Fatal("mutated parent", text, err)
	}
}

func TestExpressionIndexUniqueSlotContract(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	parent := &catalog.Relation{Attributes: []int32{7, 9}, Columns: []catalog.Column{{Usage: typ.Usage()}, {Usage: typ.Usage()}}}
	d := &catalog.IndexDefinition{Keys: []catalog.IndexKey{{Attribute: 9}, {Attribute: 7}}, Unique: true, Ready: true, Valid: true, Live: true}
	ordering, err := bindIndexOrdering(parent, d)
	if err != nil || !reflect.DeepEqual(ordering.slots, []int{1, 0}) || ordering.derived != nil {
		t.Fatal(ordering, err)
	}
	d.Keys[0] = catalog.IndexKey{Derived: &catalog.DerivedIndexKey{Execution: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}}}
	if _, err := bindIndexOrdering(parent, d); err == nil {
		t.Fatal("derived UNIQUE sentinel admitted")
	}
}

func TestExpressionIndexPlannedVolatility(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Float8)
	value := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Value: sqltype.Null(typ)}
	if exec.PlannedVolatility(value) != expr.Immutable {
		t.Fatal("conservative literal metadata rejected")
	}
	wrapper := plan.CaseExpr{Info: value.Info, Otherwise: value}
	if exec.PlannedVolatility(wrapper) != expr.Immutable || wrapper.Properties().Volatility != expr.Volatile {
		t.Fatal("pruned CASE metadata changed")
	}
}
