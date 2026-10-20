package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDeparseConstraintsReviewIdentityLeaf(t *testing.T) {
	for _, tc := range []struct {
		source, golden string
		kind           functions.IdentityKind
		call           bool
	}{
		{"CURRENT_CATALOG", "CURRENT_CATALOG", functions.IdentityCatalog, false},
		{"CURRENT_ROLE", "CURRENT_ROLE", functions.IdentityRole, false},
		{"CURRENT_USER", "CURRENT_USER", functions.IdentityCurrentUser, false},
		{"SESSION_USER", "SESSION_USER", functions.IdentitySessionUser, false},
		{"USER", "USER", functions.IdentityUser, false},
		{"SYSTEM_USER", "SYSTEM_USER", functions.IdentitySystemUser, false},
		{"CURRENT_SCHEMA", "CURRENT_SCHEMA", functions.IdentityCurrentSchema, false},
		{"current_database()", "current_database()", functions.IdentityDatabase, true},
		{"current_schema()", `"current_schema"()`, functions.IdentityCurrentSchema, true},
		{"pg_catalog.current_user()", `"current_user"()`, functions.IdentityCurrentUser, true},
		{"pg_catalog.session_user()", `"session_user"()`, functions.IdentitySessionUser, true},
		{"pg_catalog.system_user()", `"system_user"()`, functions.IdentitySystemUser, true},
	} {
		t.Run(tc.source, func(t *testing.T) {
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := statement(t, "SELECT "+tc.source+" FROM t").(*parser.Select).Targets[0]
			bound, err := b.Bind(raw, expressionPolicy{Context: ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			node, ok := bound.(functions.Identity)
			if !ok || node.Kind != tc.kind || node.OrdinaryCall != tc.call || node.Properties() != (expr.Properties{Volatility: expr.Stable}) {
				t.Fatal(bound)
			}
			id := sqltype.Name
			if tc.kind == functions.IdentitySystemUser {
				id = sqltype.Text
			}
			if node.ResultUsage().Type.ID() != id || node.ResultUsage().Typmod != -1 {
				t.Fatal(node.ResultUsage())
			}
			// Valid runtime evaluation requires statement authority. Printing must not.
			if _, err := node.Eval((*exec.Context)(nil)); err == nil {
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
			copy := remapped.(functions.Identity)
			if copy.Kind != node.Kind || copy.OrdinaryCall != node.OrdinaryCall || copy.Pos != node.Pos || copy.Properties() != node.Properties() || copy.ResultUsage().Type == node.ResultUsage().Type {
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
}
