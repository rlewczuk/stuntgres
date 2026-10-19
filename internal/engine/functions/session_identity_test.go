package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDeparseConstraintsReviewIdentityMetadata(t *testing.T) {
	types := formatUnitTypes()
	name, text := types[sqltype.Name], types[sqltype.Text]
	valid := Identity{Info: expr.Info{Usage: name.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Kind: IdentityCurrentUser}
	for _, tc := range []struct {
		name   string
		mutate func(*Identity)
	}{
		{"kind", func(n *Identity) { n.Kind = IdentityKind(255) }},
		{"database keyword", func(n *Identity) { n.Kind = IdentityDatabase }},
		{"catalog call", func(n *Identity) { n.Kind = IdentityCatalog; n.OrdinaryCall = true }},
		{"role call", func(n *Identity) { n.Kind = IdentityRole; n.OrdinaryCall = true }},
		{"user call", func(n *Identity) { n.Kind = IdentityUser; n.OrdinaryCall = true }},
		{"name type", func(n *Identity) { n.Usage = text.Usage() }},
		{"system type", func(n *Identity) { n.Kind = IdentitySystemUser }},
		{"missing type", func(n *Identity) { n.Usage.Type = nil }},
		{"typmod", func(n *Identity) { n.Usage.Typmod = 3 }},
		{"immutable", func(n *Identity) { n.Property.Volatility = expr.Immutable }},
		{"volatile", func(n *Identity) { n.Property.Volatility = expr.Volatile }},
		{"row", func(n *Identity) { n.Property.RowDependent = true }},
		{"parameter", func(n *Identity) { n.Property.ParameterDependent = true }},
		{"stage", func(n *Identity) { n.Property.StageDependent = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if value := recover(); value != nil {
					t.Fatal("malformed leaf panicked", value)
				}
			}()
			node := valid
			tc.mutate(&node)
			if node.Validate() == nil {
				t.Fatal("malformed leaf accepted")
			}
			if _, err := node.Render(); err == nil {
				t.Fatal("malformed SQL/structural leaf accepted")
			}
		})
	}
}

func TestResultNullabilityIdentity(t *testing.T) {
	for _, tc := range []struct {
		name            string
		node            Identity
		nullable, known bool
	}{
		{"current_schema", Identity{Kind: IdentityCurrentSchema}, true, true},
		{"current_schema_call", Identity{Kind: IdentityCurrentSchema, OrdinaryCall: true}, true, true},
		{"catalog", Identity{Kind: IdentityCatalog}, false, false},
		{"database", Identity{Kind: IdentityDatabase}, false, false},
		{"role", Identity{Kind: IdentityRole}, false, false},
		{"current_user", Identity{Kind: IdentityCurrentUser}, false, false},
		{"session_user", Identity{Kind: IdentitySessionUser}, false, false},
		{"user", Identity{Kind: IdentityUser}, false, false},
		{"system_user", Identity{Kind: IdentitySystemUser}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.node
			for _, node := range []expr.ResultNullability{tc.node, &tc.node} {
				for _, site := range []expr.ResultSite{expr.ResultReturning, expr.ResultSelect, expr.ResultReturning} {
					if nullable, known := node.ResultNullability(site); nullable != tc.nullable || known != tc.known {
						t.Fatalf("site %v: got (%v,%v), want (%v,%v)", site, nullable, known, tc.nullable, tc.known)
					}
				}
			}
			if tc.node != before {
				t.Fatal("metadata lookup changed recipe")
			}
		})
	}
}
