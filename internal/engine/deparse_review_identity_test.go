package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDeparseConstraintsReviewIdentityLeaf(t *testing.T) {
	for _, tc := range []struct {
		source, golden string
		kind           sessionIdentityKind
		call           bool
	}{
		{"CURRENT_CATALOG", "CURRENT_CATALOG", identityCatalog, false},
		{"CURRENT_ROLE", "CURRENT_ROLE", identityRole, false},
		{"CURRENT_USER", "CURRENT_USER", identityCurrentUser, false},
		{"SESSION_USER", "SESSION_USER", identitySessionUser, false},
		{"USER", "USER", identityUser, false},
		{"SYSTEM_USER", "SYSTEM_USER", identitySystemUser, false},
		{"CURRENT_SCHEMA", "CURRENT_SCHEMA", identityCurrentSchema, false},
		{"current_database()", "current_database()", identityDatabase, true},
		{"current_schema()", `"current_schema"()`, identityCurrentSchema, true},
		{"pg_catalog.current_user()", `"current_user"()`, identityCurrentUser, true},
		{"pg_catalog.session_user()", `"session_user"()`, identitySessionUser, true},
		{"pg_catalog.system_user()", `"system_user"()`, identitySystemUser, true},
	} {
		t.Run(tc.source, func(t *testing.T) {
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := statement(t, "SELECT "+tc.source+" FROM t").(*parser.Select).Targets[0]
			bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			node, ok := bound.(identityExpression)
			if !ok || node.kind != tc.kind || node.ordinaryCall != tc.call || node.Properties() != (ExprProperties{Volatility: Stable}) {
				t.Fatal(bound)
			}
			id := sqltype.Name
			if tc.kind == identitySystemUser {
				id = sqltype.Text
			}
			if node.ResultUsage().Type.ID() != id || node.ResultUsage().Typmod != -1 {
				t.Fatal(node.ResultUsage())
			}
			// Valid runtime evaluation requires statement authority. Printing must not.
			if _, err := node.Eval((*EvalContext)(nil)); err == nil {
				t.Fatal("identity unexpectedly evaluates without authority")
			}
			original := node
			t.Run("SQL printer", func(t *testing.T) {
				for _, pretty := range []bool{false, true} {
					got, err := (pgRenderer{view: e.CatalogRead(), pretty: pretty}).sql(context.Background(), node, pgRenderParent{})
					if err != nil || got != tc.golden {
						t.Fatalf("pretty=%t got %q, %v; want %q", pretty, got, err, tc.golden)
					}
				}
			})
			u := node.ResultUsage()
			wantTree := fmt.Sprintf("{type %d typmod %d collation %d derivation %d node Identity kind %d ordinaryCall %t}", u.Type.ID(), u.Typmod, u.Collation.ID, u.Collation.Derivation, tc.kind, tc.call)
			treeType, _ := e.CatalogRead().LookupType(sqltype.PgNodeTree)
			tree, err := catalogTree(treeType, node)
			if err != nil {
				t.Fatal(err)
			}
			serialized, err := treeType.Output(context.Background(), tree)
			if err != nil || serialized != wantTree || strings.Contains(serialized, "Const") {
				t.Fatal(serialized, wantTree, err)
			}
			target := New()
			retagged, changed, err := retagBoundTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
				typ, _ := target.CatalogRead().LookupType(d.ID())
				return typ
			})
			if err != nil || !changed {
				t.Fatal(changed, err)
			}
			remapped, err := remapBoundExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef {
				t.Fatal("identity leaf invented a catalog reference", ref)
				return ref
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			copy := remapped.(identityExpression)
			if copy.kind != node.kind || copy.ordinaryCall != node.ordinaryCall || copy.Pos != node.Pos || copy.Properties() != node.Properties() || copy.ResultUsage().Type == node.ResultUsage().Type {
				t.Fatal(copy, node)
			}
			got, err := (pgRenderer{view: target.CatalogRead()}).sql(context.Background(), copy, pgRenderParent{})
			if err != nil || got != tc.golden {
				t.Fatal(got, err)
			}
			copiedTree, err := (boundTree{expression: copy}).StructuralOutput(context.Background())
			if err != nil || copiedTree != wantTree {
				t.Fatal(copiedTree, err)
			}
			run(t, e, "CREATE TABLE identity_owner(v TEXT DEFAULT "+tc.source+")")
			run(t, e, "DROP TABLE identity_owner")
			after, err := treeType.Output(context.Background(), tree)
			if err != nil || after != serialized || !reflect.DeepEqual(node, original) {
				t.Fatal(after, err, node, original)
			}
		})
	}
	t.Run("malformed metadata", func(t *testing.T) {
		e := New()
		name, _ := e.CatalogRead().LookupType(sqltype.Name)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		valid := identityExpression{expressionInfo: expressionInfo{Usage: name.Usage(), Property: ExprProperties{Volatility: Stable}}, kind: identityCurrentUser}
		for _, tc := range []struct {
			name   string
			mutate func(*identityExpression)
		}{
			{"kind", func(n *identityExpression) { n.kind = sessionIdentityKind(255) }},
			{"database keyword", func(n *identityExpression) { n.kind = identityDatabase }},
			{"catalog call", func(n *identityExpression) { n.kind = identityCatalog; n.ordinaryCall = true }},
			{"role call", func(n *identityExpression) { n.kind = identityRole; n.ordinaryCall = true }},
			{"user call", func(n *identityExpression) { n.kind = identityUser; n.ordinaryCall = true }},
			{"name type", func(n *identityExpression) { n.Usage = text.Usage() }},
			{"system type", func(n *identityExpression) { n.kind = identitySystemUser }},
			{"missing type", func(n *identityExpression) { n.Usage.Type = nil }},
			{"typmod", func(n *identityExpression) { n.Usage.Typmod = 3 }},
			{"immutable", func(n *identityExpression) { n.Property.Volatility = Immutable }},
			{"volatile", func(n *identityExpression) { n.Property.Volatility = Volatile }},
			{"row", func(n *identityExpression) { n.Property.RowDependent = true }},
			{"parameter", func(n *identityExpression) { n.Property.ParameterDependent = true }},
			{"stage", func(n *identityExpression) { n.Property.StageDependent = true }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				defer func() {
					if panicValue := recover(); panicValue != nil {
						t.Fatal("malformed leaf panicked", panicValue)
					}
				}()
				node := valid
				tc.mutate(&node)
				if _, err := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), node, pgRenderParent{}); err == nil {
					t.Fatal("malformed SQL leaf accepted")
				}
				if _, err := (boundTree{expression: node}).StructuralOutput(context.Background()); err == nil {
					t.Fatal("malformed structural leaf accepted")
				}
			})
		}
	})
}
