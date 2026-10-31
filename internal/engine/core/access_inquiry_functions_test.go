package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestAccessInquiryBinding(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	for _, tc := range []struct {
		sql string
		ids []sqltype.ID
		rls bool
	}{
		{"SELECT pg_has_role($1,$2,$3)", []sqltype.ID{sqltype.Name, sqltype.Name, sqltype.Text}, false},
		{"SELECT pg_has_role(10::oid,$1,$2)", []sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Text}, false},
		{"SELECT pg_has_role($1,10::oid,$2)", []sqltype.ID{sqltype.Name, sqltype.Oid, sqltype.Text}, false},
		{"SELECT pg_has_role(10::oid,10::oid,$1)", []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Text}, false},
		{"SELECT pg_has_role($1,$2)", []sqltype.ID{sqltype.Name, sqltype.Text}, false},
		{"SELECT pg_has_role(10::oid,$1)", []sqltype.ID{sqltype.Oid, sqltype.Text}, false},
		{"SELECT row_security_active($1)", []sqltype.ID{sqltype.Text}, true},
		{"SELECT row_security_active(0::oid)", []sqltype.ID{sqltype.Oid}, true},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			q := enginetest.BindSelect(t, e, op, tc.sql)
			n, ok := q.Root.(*plan.ProjectNode).Expressions[0].(functions.AccessInquiry)
			if !ok || n.Count != len(tc.ids) || (n.Kind == functions.AccessRowSecurity) != tc.rls || n.ResultUsage().Type.ID() != sqltype.Bool || n.Properties().Volatility != expr.Stable {
				t.Fatalf("typed signature %#v", n)
			}
			for i, id := range tc.ids {
				if n.Args[i].ResultUsage().Type.ID() != id {
					t.Fatalf("argument %d got %v want %v", i, n.Args[i].ResultUsage(), id)
				}
			}
			if err := n.Validate(); err != nil {
				t.Fatal(err)
			}
			bad := n
			bad.Property.Volatility = expr.Immutable
			if bad.Validate() == nil {
				t.Fatal("accepted immutable inquiry")
			}
			bad = n
			bad.Args[0] = nil
			if bad.Validate() == nil {
				t.Fatal("accepted missing argument")
			}
		})
	}
}

func TestAccessInquiryLifecycle(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	boolean, _ := view.LookupType(sqltype.Bool)
	oid, _ := view.LookupType(sqltype.Oid)
	name, _ := view.LookupType(sqltype.Name)
	text, _ := view.LookupType(sqltype.Text)
	n := functions.AccessInquiry{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Count: 3}
	n.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: name.Usage()}, Value: sqltype.String(name, "postgres")}
	v, _ := sqltype.OIDValue(oid, 10)
	n.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: v}
	n.Args[2] = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "MEMBER")}
	destination := engine.New().CatalogRead()
	mapped, changed, err := stored.RetagTypes(destination, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		typ, _ := destination.LookupType(d.ID())
		return typ
	})
	if err != nil || !changed {
		t.Fatal("did not remint recipe", mapped, changed, err)
	}
	fresh := mapped.(functions.AccessInquiry)
	if fresh.ResultUsage().Type == n.ResultUsage().Type {
		t.Fatal("result retained foreign type")
	}
	for i, arg := range fresh.Children() {
		if arg.ResultUsage().Type == n.Args[i].ResultUsage().Type {
			t.Fatal("child retained foreign type", i)
		}
	}
	if err := fresh.Validate(); err != nil {
		t.Fatal(err)
	}
	if n.Args[0].ResultUsage().Type != name {
		t.Fatal("mutated original children")
	}
	rewritten, changed, err := stored.MapExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, func(child expr.Expr) (expr.Expr, bool, error) {
		if literal, ok := child.(plan.LiteralExpr); ok && literal.Value.Type().ID() == sqltype.Name {
			literal.Value = sqltype.String(name, "replacement")
			return literal, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed {
		t.Fatal("nested leaf rewrite failed", changed, err)
	}
	got, _ := rewritten.(functions.AccessInquiry).Args[0].(plan.LiteralExpr).Value.Text()
	old, _ := n.Args[0].(plan.LiteralExpr).Value.Text()
	if got != "replacement" || old != "postgres" {
		t.Fatal("recipe rewrite aliasing", got, old)
	}
	remapped, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || remapped.(functions.AccessInquiry).Validate() != nil {
		t.Fatal("stored recipe remap", err)
	}
}
