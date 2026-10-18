package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIndexPropertyCapabilityMatrix(t *testing.T) {
	props := []string{"can_order", "can_unique", "can_multi_col", "can_exclude", "can_include"}
	for _, tc := range []struct {
		oid  uint32
		want [5]bool
	}{
		{403, [5]bool{true, true, true, true, true}}, {405, [5]bool{false, false, false, true, false}},
		{783, [5]bool{false, false, true, true, true}}, {2742, [5]bool{false, false, true, false, false}},
		{4000, [5]bool{false, false, false, true, true}}, {3580, [5]bool{false, false, true, false, false}},
	} {
		for i, prop := range props {
			got, known := indexAMProperty(tc.oid, prop)
			if !known || got != tc.want[i] {
				t.Fatal(tc.oid, prop, got, known)
			}
		}
	}
	for _, oid := range []uint32{0, 2, 4294967295} {
		if _, known := indexAMProperty(oid, "can_order"); known {
			t.Fatal(oid)
		}
	}
	if _, known := indexAMProperty(403, "asc"); known {
		t.Fatal("wrong property level")
	}
	index := &indexDefinition{keys: []indexKey{{desc: false, nullsFirst: true}, {desc: true, nullsFirst: false}}, include: []int32{3}, ready: true, valid: true, live: true}
	for _, tc := range []struct {
		ordinal     int64
		property    string
		want, known bool
	}{
		{1, "asc", true, true}, {1, "desc", false, true}, {1, "nulls_first", true, true}, {1, "nulls_last", false, true},
		{2, "asc", false, true}, {2, "desc", true, true}, {2, "nulls_first", false, true}, {2, "nulls_last", true, true},
		{1, "orderable", true, true}, {1, "distance_orderable", false, true}, {1, "returnable", true, true}, {1, "search_array", true, true}, {1, "search_nulls", true, true},
		{3, "asc", false, false}, {3, "desc", false, false}, {3, "nulls_first", false, false}, {3, "nulls_last", false, false},
		{3, "orderable", false, true}, {3, "distance_orderable", false, true}, {3, "returnable", true, true}, {3, "search_array", false, false}, {3, "search_nulls", false, false},
		{0, "asc", false, false}, {-1, "asc", false, false}, {4, "asc", false, false}, {2147483647, "asc", false, false},
		{1, "index_scan", false, false}, {1, "can_order", false, false}, {1, "bogus", false, false},
	} {
		got, known := indexColumnCapability(index, tc.ordinal, tc.property)
		if got != tc.want || known != tc.known {
			t.Fatal(tc, got, known)
		}
	}
	for _, tc := range []struct{ input, want string }{{"CaN_OrDeR", "can_order"}, {"DESC", "desc"}, {" can_order", ""}, {"can_order ", ""}, {"ſearch_nulls", ""}, {"bogus", ""}, {"", ""}} {
		if got := indexPropertyName(tc.input); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestIndexPropertyExpressionGuardsAndNull(t *testing.T) {
	view := New().CatalogRead()
	boolType, _ := view.LookupType(sqltype.Bool)
	oidType, _ := view.LookupType(sqltype.Oid)
	textType, _ := view.LookupType(sqltype.Text)
	regType, _ := view.LookupType(sqltype.Regclass)
	literal := func(v sqltype.Value) BoundExpr {
		return literalExpression{Info: expressionInfo{Usage: v.Type().Usage()}, value: v}
	}
	oid, _ := sqltype.OIDValue(oidType, 403)
	n := indexPropertyExpression{Info: expressionInfo{Usage: boolType.Usage(), Property: ExprProperties{Volatility: Stable}}, kind: accessMethodProperty, args: [3]BoundExpr{literal(oid), literal(sqltype.String(textType, "can_order"))}}
	value, err := n.Eval(&EvalContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Boolean()
	if err != nil || !got || value.Type() != boolType {
		t.Fatal(value, err)
	}
	for i := range 2 {
		null := n
		null.args[i] = literal(sqltype.Null(n.args[i].ResultUsage().Type))
		value, err := null.Eval(&EvalContext{Context: context.Background()})
		if err != nil || !value.IsNull() || value.Type() != boolType {
			t.Fatal(i, value, err)
		}
	}
	for _, change := range []func(*indexPropertyExpression){
		func(n *indexPropertyExpression) { n.kind = 255 }, func(n *indexPropertyExpression) { n.args[0] = nil },
		func(n *indexPropertyExpression) { n.args[2] = literal(oid) }, func(n *indexPropertyExpression) { n.Usage = textType.Usage() },
		func(n *indexPropertyExpression) { n.args[0] = literal(sqltype.String(textType, "403")) }, func(n *indexPropertyExpression) { n.Property.Volatility = Immutable },
		func(n *indexPropertyExpression) { n.oidUsage = oidType.Usage() },
	} {
		broken := n
		change(&broken)
		if validateStoredIndexPropertyExpression(broken) == nil {
			t.Fatal("accepted corrupt index property node", broken)
		}
	}
	index := indexPropertyExpression{Info: n.Info, kind: wholeIndexProperty, args: [3]BoundExpr{literal(sqltype.String(regType, "0")), literal(sqltype.String(textType, "index_scan"))}, oidUsage: oidType.Usage()}
	index.oidPlan, err = sqltype.PlanCoercion(regType.Usage(), oidType.Usage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStoredIndexPropertyExpression(index); err != nil {
		t.Fatal(err)
	}
	broken := index
	broken.oidPlan = sqltype.CoercionPlan{}
	if err := validateStoredIndexPropertyExpression(broken); err == nil {
		t.Fatal("accepted missing normalization")
	}
	index.args[0] = literal(sqltype.Null(regType))
	value, err = index.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != boolType {
		t.Fatal(value, err)
	}
}
