package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIndexPropertyNames(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"CaN_OrDeR", "can_order"}, {"DESC", "desc"}, {" can_order", ""}, {"can_order ", ""}, {"ſearch_nulls", ""}, {"bogus", ""}, {"", ""}} {
		if got := indexPropertyName(tc.input); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
func TestIndexPropertyExpressionGuardsAndNull(t *testing.T) {
	types := formatUnitTypes()
	boolType := types[sqltype.Bool]
	oidType := types[sqltype.Oid]
	textType := types[sqltype.Text]
	regType := types[sqltype.Regclass]
	literal := func(v sqltype.Value) expr.Expr {
		return temporalTestLiteral(v)
	}
	oid, _ := sqltype.OIDValue(oidType, 403)
	n := IndexProperty{Info: expr.Info{Usage: boolType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Kind: AccessMethodProperty, Args: [3]expr.Expr{literal(oid), literal(sqltype.String(textType, "can_order"))}}
	value, err := n.Eval(&temporalTestContext{Context: context.Background()})
	if err == nil {
		t.Fatal("method inquiry without a catalog runtime succeeded", value)
	}
	for i := range 2 {
		null := n
		null.Args[i] = literal(sqltype.Null(n.Args[i].ResultUsage().Type))
		value, err := null.Eval(&temporalTestContext{Context: context.Background()})
		if err != nil || !value.IsNull() || value.Type() != boolType {
			t.Fatal(i, value, err)
		}
	}
	for _, change := range []func(*IndexProperty){
		func(n *IndexProperty) { n.Kind = 255 }, func(n *IndexProperty) { n.Args[0] = nil },
		func(n *IndexProperty) { n.Args[2] = literal(oid) }, func(n *IndexProperty) { n.Usage = textType.Usage() },
		func(n *IndexProperty) { n.Args[0] = literal(sqltype.String(textType, "403")) }, func(n *IndexProperty) { n.Property.Volatility = expr.Immutable },
		func(n *IndexProperty) { n.oidUsage = oidType.Usage() },
	} {
		broken := n
		change(&broken)
		if validateStoredIndexPropertyExpression(broken) == nil {
			t.Fatal("accepted corrupt index property node", broken)
		}
	}
	index := IndexProperty{Info: n.Info, Kind: WholeIndexProperty, Args: [3]expr.Expr{literal(sqltype.String(regType, "0")), literal(sqltype.String(textType, "index_scan"))}, oidUsage: oidType.Usage()}
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
	index.Args[0] = literal(sqltype.Null(regType))
	value, err = index.Eval(&temporalTestContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != boolType {
		t.Fatal(value, err)
	}
}
