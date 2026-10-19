package functions

import (
	"stuntgres/internal/engine/expr"
	"testing"
)

func TestCatalogInquiryIntrinsicVolatility(t *testing.T) {
	// These seven historically retain analyzed volatility even when physical
	// child simplification removes the volatile arm. Inspection does not validate
	// or execute their recipes; nil children deliberately cannot execute.
	info := expr.Info{Property: expr.Properties{Volatility: expr.Volatile}}
	for _, tc := range []struct {
		name string
		node expr.Intrinsic
		want expr.Volatility
	}{
		{"identity", Identity{Info: info, Kind: IdentityCurrentUser}, expr.Volatile},
		{"schemas", CurrentSchemas{Info: info}, expr.Volatile},
		{"namespace", NamespaceInquiry{Info: info, Kind: OtherTempNamespace}, expr.Volatile},
		{"visibility", VisibilityInquiry{Info: info, Kind: TableVisibility}, expr.Volatile},
		{"index", IndexProperty{Info: info, Kind: WholeIndexProperty}, expr.Volatile},
		{"comment", CommentInquiry{Info: info, Kind: ObjectDescription}, expr.Volatile},
		{"deparse", Deparse{Info: info, Signature: DeparseExpr}, expr.Volatile},
		{"username_physical_children", CatalogUserName{Info: info}, expr.Stable},
		{"linkage", CatalogForeignKeys{Info: info}, expr.Stable},
		{"address_physical_children", ObjectAddress{Info: info}, expr.Stable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.IntrinsicVolatility(); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
