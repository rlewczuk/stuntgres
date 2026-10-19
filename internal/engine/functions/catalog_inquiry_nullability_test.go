package functions

import (
	"reflect"
	"stuntgres/internal/engine/expr"
	"testing"
)

func TestResultNullabilityCatalogInquiry(t *testing.T) {
	// Nil descriptors/children intentionally cannot execute: the answers depend
	// only on immutable recipe selectors and the actual result descriptor site.
	cases := []struct {
		name                                                           string
		node                                                           expr.ResultNullability
		selectNullable, selectKnown, returningNullable, returningKnown bool
	}{
		{"current_schemas", CurrentSchemas{}, true, true, true, true},
		{"my_temp", NamespaceInquiry{Kind: MyTempNamespace}, false, true, false, true},
		{"other_temp", NamespaceInquiry{Kind: OtherTempNamespace}, true, true, true, true},
		{"constraint", Deparse{Signature: DeparseConstraint}, true, true, false, false},
		{"constraint_pretty", Deparse{Signature: DeparseConstraintPretty}, true, true, false, false},
		{"expr", Deparse{Signature: DeparseExpr}, true, true, false, false},
		{"expr_pretty", Deparse{Signature: DeparseExprPretty}, true, true, false, false},
		{"index", Deparse{Signature: DeparseIndex}, true, true, false, false},
		{"index_column", Deparse{Signature: DeparseIndexColumn}, true, true, false, false},
		{"view_text", Deparse{Signature: DeparseViewText}, true, true, false, false},
		{"view_oid", Deparse{Signature: DeparseViewOID}, true, true, false, false},
		{"view_text_pretty", Deparse{Signature: DeparseViewTextPretty}, true, true, false, false},
		{"view_oid_pretty", Deparse{Signature: DeparseViewOIDPretty}, true, true, false, false},
		{"view_wrap", Deparse{Signature: DeparseViewWrap}, true, true, false, false},
		{"serial_sequence", Deparse{Signature: DeparseSerialSequence}, true, true, false, false},
		{"access_method_property", IndexProperty{Kind: AccessMethodProperty}, true, true, false, false},
		{"index_property", IndexProperty{Kind: WholeIndexProperty}, true, true, false, false},
		{"index_column_property", IndexProperty{Kind: IndexColumnProperty}, true, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pointer := reflect.New(reflect.TypeOf(tc.node))
			pointer.Elem().Set(reflect.ValueOf(tc.node))
			for _, metadata := range []expr.ResultNullability{tc.node, pointer.Interface().(expr.ResultNullability)} {
				for _, site := range []expr.ResultSite{expr.ResultReturning, expr.ResultSelect, expr.ResultReturning} {
					wantNullable, wantKnown := tc.selectNullable, tc.selectKnown
					if site == expr.ResultReturning {
						wantNullable, wantKnown = tc.returningNullable, tc.returningKnown
					}
					if nullable, known := metadata.ResultNullability(site); nullable != wantNullable || known != wantKnown {
						t.Fatalf("site %v: got (%v,%v), want (%v,%v)", site, nullable, known, wantNullable, wantKnown)
					}
				}
			}
		})
	}
	t.Run("pointer_site_changes_leave_recipe_immutable", func(t *testing.T) {
		node := Deparse{Signature: DeparseViewWrap, Args: []expr.Expr{Identity{Kind: IdentityDatabase}}}
		before := Deparse{Signature: DeparseViewWrap, Args: append([]expr.Expr(nil), node.Args...)}
		var metadata expr.ResultNullability = &node
		for _, site := range []expr.ResultSite{expr.ResultSelect, expr.ResultReturning, expr.ResultSelect} {
			nullable, known := metadata.ResultNullability(site)
			if nullable != (site == expr.ResultSelect) || known != (site == expr.ResultSelect) {
				t.Fatalf("site %v: got (%v,%v)", site, nullable, known)
			}
			if !reflect.DeepEqual(node, before) {
				t.Fatal("metadata lookup changed the recipe")
			}
		}
	})
}
