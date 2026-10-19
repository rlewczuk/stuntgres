package functions

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
)

func TestResultNullabilityFunctions(t *testing.T) {
	// Nil arguments intentionally cannot be evaluated. Even ACLExplode's answer
	// belongs to its recipe, not its separately owned set-returning admission.
	cases := []struct {
		name string
		node expr.ResultNullability
	}{
		{"has_role_implicit", AccessInquiry{Count: 2}},
		{"has_role_explicit", AccessInquiry{Count: 3}},
		{"row_security", AccessInquiry{RowSecurity: true, Count: 1}},
		{"object_acl", ObjectACL{}},
		{"acl_default", ACLValue{Kind: ACLDefault}},
		{"acl_make", ACLValue{Kind: ACLMake}},
		{"acl_explode", ACLValue{Kind: ACLExplode}},
		{"acl_contains", ACLValue{Kind: ACLContains}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, site := range []expr.ResultSite{expr.ResultSelect, expr.ResultReturning} {
				if nullable, known := tc.node.ResultNullability(site); !nullable || !known {
					t.Fatalf("site %v: got (%v,%v), want (true,true)", site, nullable, known)
				}
			}
		})
	}
	t.Run("pointer_written_recipe_immutable", func(t *testing.T) {
		node := ObjectACL{Slots: [3]uint8{2, 0, 1}, Labels: [3]string{"objsubid", "classid", "objid"}}
		before := node
		var metadata expr.ResultNullability = &node
		for _, site := range []expr.ResultSite{expr.ResultReturning, expr.ResultSelect, expr.ResultReturning} {
			if nullable, known := metadata.ResultNullability(site); !nullable || !known {
				t.Fatalf("site %v: got (%v,%v), want (true,true)", site, nullable, known)
			}
			if !reflect.DeepEqual(node, before) {
				t.Fatal("metadata lookup changed written argument provenance")
			}
		}
	})
}
