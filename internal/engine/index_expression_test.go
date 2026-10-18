package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExpressionIndexDerivedRoots(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	u := typ.Usage()
	parent := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9000}, attributes: []int32{1}, columns: []ColumnDescriptor{{Name: "v", Usage: u}}, index: map[string]int{"v": 0}}
	column := columnExpression{expressionInfo: expressionInfo{Usage: u, Property: ExprProperties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
	plan, err := sqltype.BindString("lower", u, u, []sqltype.Usage{u}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	node := functions.String{Info: functionInfo(column.expressionInfo), Plan: plan, Args: []BoundExpr{column}, Textual: [4]bool{true}}
	d := &indexDefinition{parent: parent.address, keys: []indexKey{{derived: &derivedIndexKey{definition: node, execution: node}}}, ready: true, valid: true, live: true}
	values := []sqltype.Value{sqltype.String(typ, "Alpha"), sqltype.String(typ, "ALPHA"), sqltype.String(typ, "Beta"), sqltype.Null(typ), sqltype.String(typ, "alpha")}
	store := &relationStorage{}
	for i, v := range values {
		store.versions = append(store.versions, rowVersion{row: ids.RowID(i + 1), version: ids.VersionID(i + 1), values: []sqltype.Value{v}})
	}
	root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, parent, d, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "alpha", "alpha", "beta", "NULL"}
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	publication := &rowPublication{writes: []rowVersion{{row: 6, version: 6, values: []sqltype.Value{sqltype.String(typ, "AlPhA")}}}}
	next, err := mergeIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, parent, &relationDescriptor{sqlIndex: d}, root.entries, publication, false, true, false, "", sqltype.OutputEnvironment{}, nil)
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
	parent := &relationDescriptor{attributes: []int32{7, 9}, columns: []ColumnDescriptor{{Usage: typ.Usage()}, {Usage: typ.Usage()}}}
	d := &indexDefinition{keys: []indexKey{{attribute: 9}, {attribute: 7}}, unique: true, ready: true, valid: true, live: true}
	ordering, err := bindIndexOrdering(parent, d)
	if err != nil || !reflect.DeepEqual(ordering.slots, []int{1, 0}) || ordering.derived != nil {
		t.Fatal(ordering, err)
	}
	d.keys[0] = indexKey{derived: &derivedIndexKey{execution: literalExpression{expressionInfo: expressionInfo{Usage: typ.Usage()}, value: sqltype.Null(typ)}}}
	if _, err := bindIndexOrdering(parent, d); err == nil {
		t.Fatal("derived UNIQUE sentinel admitted")
	}
}

func TestExpressionIndexPlannedVolatility(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Float8)
	value := literalExpression{expressionInfo: expressionInfo{Usage: typ.Usage(), Property: ExprProperties{Volatility: Volatile}}, value: sqltype.Null(typ)}
	if plannedVolatility(value) != Immutable {
		t.Fatal("conservative literal metadata rejected")
	}
	wrapper := caseExpression{expressionInfo: value.expressionInfo, otherwise: value}
	if plannedVolatility(wrapper) != Immutable || wrapper.Properties().Volatility != Volatile {
		t.Fatal("pruned CASE metadata changed")
	}
}
