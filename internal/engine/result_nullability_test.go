package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
)

func TestResultNullabilityEngine(t *testing.T) {
	// Deliberately omit children, types and execution authority: metadata depends
	// only on the immutable discriminator, never on evaluating a valid call.
	cases := []struct {
		name                              string
		node                              expr.ResultNullability
		selectNullable, selectKnown       bool
		returningNullable, returningKnown bool
	}{
		{"current_schemas", currentSchemasExpression{}, true, true, true, true},
		{"my_temp", namespaceInquiryExpression{kind: myTempNamespace}, false, true, false, true},
		{"other_temp", namespaceInquiryExpression{kind: otherTempNamespace}, true, true, true, true},
		{"blocking", blockingInquiryExpression{kind: ordinaryBlockingInquiry}, true, true, true, true},
		{"safe_snapshot", blockingInquiryExpression{kind: safeSnapshotBlockingInquiry}, true, true, true, true},
		{"current_schema", identityExpression{kind: identityCurrentSchema}, true, true, true, true},
		{"current_schema_call", identityExpression{kind: identityCurrentSchema, ordinaryCall: true}, true, true, true, true},
		{"catalog", identityExpression{kind: identityCatalog}, false, false, false, false},
		{"database", identityExpression{kind: identityDatabase}, false, false, false, false},
		{"role", identityExpression{kind: identityRole}, false, false, false, false},
		{"current_user", identityExpression{kind: identityCurrentUser}, false, false, false, false},
		{"session_user", identityExpression{kind: identitySessionUser}, false, false, false, false},
		{"user", identityExpression{kind: identityUser}, false, false, false, false},
		{"system_user", identityExpression{kind: identitySystemUser}, false, false, false, false},
		{"constraint", deparseExpression{signature: deparseConstraint}, true, true, false, false},
		{"constraint_pretty", deparseExpression{signature: deparseConstraintPretty}, true, true, false, false},
		{"expr", deparseExpression{signature: deparseExpr}, true, true, false, false},
		{"expr_pretty", deparseExpression{signature: deparseExprPretty}, true, true, false, false},
		{"index", deparseExpression{signature: deparseIndex}, true, true, false, false},
		{"index_column", deparseExpression{signature: deparseIndexColumn}, true, true, false, false},
		{"view_text", deparseExpression{signature: deparseViewText}, true, true, false, false},
		{"view_oid", deparseExpression{signature: deparseViewOID}, true, true, false, false},
		{"view_text_pretty", deparseExpression{signature: deparseViewTextPretty}, true, true, false, false},
		{"view_oid_pretty", deparseExpression{signature: deparseViewOIDPretty}, true, true, false, false},
		{"view_wrap", deparseExpression{signature: deparseViewWrap}, true, true, false, false},
		{"serial_sequence", deparseExpression{signature: deparseSerialSequence}, true, true, false, false},
		{"access_method_property", indexPropertyExpression{kind: accessMethodProperty}, true, true, false, false},
		{"index_property", indexPropertyExpression{kind: wholeIndexProperty}, true, true, false, false},
		{"index_column_property", indexPropertyExpression{kind: indexColumnProperty}, true, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, site := range []expr.ResultSite{expr.ResultReturning, expr.ResultSelect, expr.ResultReturning} {
				wantNullable, wantKnown := tc.selectNullable, tc.selectKnown
				if site == expr.ResultReturning {
					wantNullable, wantKnown = tc.returningNullable, tc.returningKnown
				}
				if nullable, known := tc.node.ResultNullability(site); nullable != wantNullable || known != wantKnown {
					t.Fatalf("site %v: got (%v,%v), want (%v,%v)", site, nullable, known, wantNullable, wantKnown)
				}
			}
		})
	}
	t.Run("pointer_site_changes_leave_recipe_immutable", func(t *testing.T) {
		node := deparseExpression{signature: deparseViewWrap, args: []expr.Expr{identityExpression{kind: identityDatabase}}}
		before := deparseExpression{signature: deparseViewWrap, args: append([]expr.Expr(nil), node.args...)}
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
