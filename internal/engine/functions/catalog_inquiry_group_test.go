package functions

import (
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCatalogInquiryGroupProperties(t *testing.T) {
	types := formatUnitTypes()
	first := temporalTestLiteral(sqltype.Null(types[sqltype.Text]))
	first.Property = expr.Properties{Volatility: expr.Volatile, RowDependent: true, ParameterDependent: true, DomainDependent: true}
	second := temporalTestLiteral(sqltype.Null(types[sqltype.Text]))
	second.Property = expr.Properties{StageDependent: true, RuntimeDomainConversion: true}
	children := []expr.Expr{first, second}
	want := expr.Properties{Volatility: expr.Volatile, RowDependent: true, ParameterDependent: true, StageDependent: true, DomainDependent: true, RuntimeDomainConversion: true}
	index := IndexProperty{Kind: WholeIndexProperty, Info: expr.Info{Property: expr.Properties{StageDependent: true}}}
	deparse := Deparse{Signature: DeparseViewOID, Info: expr.Info{Property: expr.Properties{StageDependent: true}}}
	originalIndex, originalDeparse := index, deparse
	for _, tc := range []struct {
		name string
		node expr.GroupPropertyRewriter
	}{
		{"index_value", index}, {"index_pointer", &index}, {"deparse_value", deparse}, {"deparse_pointer", &deparse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.GroupProperties(nil); got != (expr.Properties{Volatility: expr.Stable}) {
				t.Fatal("grouping invented stage or retained original dependence", got)
			}
			if got := tc.node.GroupProperties(children); got != want {
				t.Fatal("rewritten child properties lost", got, want)
			}
			if !reflect.DeepEqual(index, originalIndex) || !reflect.DeepEqual(deparse, originalDeparse) {
				t.Fatal("grouping changed immutable recipe")
			}
		})
	}
}
