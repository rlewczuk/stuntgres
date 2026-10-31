package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestObjectACLExpressionProperties(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"SELECT pg_get_acl($1,$2,$3)", "SELECT pg_get_acl(objsubid => $1,classid => $2,objid => $3)", "SELECT pg_get_acl(NULL,NULL,NULL)"} {
		q := enginetest.BindSelect(t, e, op, query)
		n, ok := q.Root.(*plan.ProjectNode).Expressions[0].(functions.ObjectACL)
		if !ok || n.ResultUsage().Type.ID() != sqltype.ACLItemArray || n.Properties().Volatility != expr.Stable || n.Validate() != nil {
			t.Fatal("typed STABLE recipe", n)
		}
		for i, arg := range n.Args {
			if arg.ResultUsage().Type.ID() != [3]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4}[n.Slots[i]] {
				t.Fatal("wrong formal argument type", i, arg.ResultUsage())
			}
		}
		children := 0
		plan.ExpressionChildren(n, func(expr.Expr) { children++ })
		if children != 3 {
			t.Fatal("lexical children", children)
		}
		bad := n
		bad.Slots[1] = bad.Slots[0]
		if bad.Validate() == nil {
			t.Fatal("accepted duplicate formal slot")
		}
		bad = n
		bad.Property.Volatility = expr.Immutable
		if bad.Validate() == nil {
			t.Fatal("accepted immutable inquiry")
		}
		if query == "SELECT pg_get_acl(NULL,NULL,NULL)" {
			value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			if err != nil || !value.IsNull() || value.Type().ID() != sqltype.ACLItemArray {
				t.Fatal("strict typed NULL without authority", value, err)
			}
		}
	}
	// A stored recipe has no result/catalog cache; remint every child's carrier.
	view := e.CatalogRead()
	array, _ := view.LookupType(sqltype.ACLItemArray)
	oid, _ := view.LookupType(sqltype.Oid)
	integer, _ := view.LookupType(sqltype.Int4)
	n := functions.ObjectACL{Info: expr.Info{Usage: array.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Slots: [3]uint8{0, 1, 2}}
	for i, typ := range []*sqltype.Descriptor{oid, oid, integer} {
		n.Args[i] = plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}
	}
	destination := engine.New().CatalogRead()
	mapped, changed, err := stored.RetagTypes(destination, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		typ, _ := destination.LookupType(d.ID())
		return typ
	})
	if err != nil || !changed {
		t.Fatal("retag recipe", changed, err)
	}
	fresh := mapped.(functions.ObjectACL)
	if fresh.ResultUsage().Type == n.ResultUsage().Type || fresh.Validate() != nil {
		t.Fatal("foreign result carrier retained", fresh)
	}
	for i, arg := range fresh.Args {
		if arg.ResultUsage().Type == n.Args[i].ResultUsage().Type {
			t.Fatal("foreign argument carrier retained", i)
		}
	}
	remapped, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || remapped.(functions.ObjectACL).Validate() != nil {
		t.Fatal("stored remap", err)
	}
	rewritten, changed, err := stored.MapExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, func(child expr.Expr) (expr.Expr, bool, error) {
		if literal, ok := child.(plan.LiteralExpr); ok && literal.Value.Type().ID() == sqltype.Oid {
			literal.Value, _ = sqltype.OIDValue(oid, 42)
			return literal, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed || rewritten.(functions.ObjectACL).Args[0].(plan.LiteralExpr).Value.IsNull() || !n.Args[0].(plan.LiteralExpr).Value.IsNull() {
		t.Fatal("leaf rewrite mutated original recipe or skipped children", changed, err)
	}
	// Named recipes must render their NULL children in written order.
	n.Slots, n.Labels = [3]uint8{2, 0, 1}, [3]string{"objsubid", "classid", "objid"}
	n.Args = [3]expr.Expr{n.Args[2], n.Args[0], n.Args[1]}
	spec, err := n.Render()
	if err != nil || spec.Name != "pg_get_acl" || len(spec.Args) != 3 {
		t.Fatal("named written-order render", spec, err)
	}
	for i, label := range []string{"objsubid", "classid", "objid"} {
		if spec.Args[i].Label != label || spec.Args[i].Expr != n.Args[i] || !spec.Args[i].Expr.(plan.LiteralExpr).Value.IsNull() {
			t.Fatal("named render changed written order or a NULL child", i, spec.Args[i])
		}
	}
}
