package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBlockingPidsTraversalReplay(t *testing.T) {
	ctx := context.Background()
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE blocking_render_owner(v INT)")
	catalog := e.CatalogRead()
	ref := catalogRef(t, e, catalog, "blocking_render_owner")
	owner := catalog.MustRelation(ref.Address)
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"pg_blocking_pids", "pg_safe_snapshot_blocking_pids"} {
		raw := datetimeMakeRaw(name, datetimeMakeInteger(0))
		bound, err := b.bindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := bound.(functions.BlockingInquiry)
		if err := b.captureViewLiterals(raw, n, viewTargetLiteral, 0); err != nil {
			t.Fatal("literal recipe pairing", err)
		}
		if got, err := (scalarRenderer{owner: owner}).sql(ctx, catalog, n); err != nil || !strings.Contains(got, "pg_catalog."+name+"(") {
			t.Fatal("stored recipe", got, err)
		}
		if _, err := defaultSQL(ctx, catalog, n); err == nil {
			t.Fatal("volatile ownerless default rendering widened")
		}
		if got, err := (pgRenderer{view: e.CatalogRead()}).sql(ctx, n, pgRenderParent{}); err != nil || !strings.Contains(got, name+"(") {
			t.Fatal("typed deparse", got, err)
		}
		if got, err := (boundTree{n}).StructuralOutput(ctx); err != nil || !strings.Contains(got, "BlockingInquiry") {
			t.Fatal("structural recipe", got, err)
		}
		if !b.sameGroupExpression(n, n) || generationImmutable(n) || PlannedVolatility(n) != expr.Volatile {
			t.Fatal("lost intrinsic live volatility")
		}
		other := n
		other.Kind = functions.BlockingKind(1 - int(n.Kind))
		if b.sameGroupExpression(n, other) {
			t.Fatal("different inquiries collapsed")
		}
		for _, rewrite := range []func(*EvalContext, expr.Expr, bool) (expr.Expr, bool, error){simplifyConditional, preflightCoercionsWithPolicy} {
			next, _, err := rewrite(&EvalContext{evalStatement: evalStatement{Context: ctx}}, n, false)
			if err != nil || next.Properties().Volatility != expr.Volatile {
				t.Fatal("live recipe precomputed", next, err)
			}
			if _, ok := next.(functions.BlockingInquiry); !ok {
				t.Fatal("replaced live carrier", next)
			}
		}
		destination := New().CatalogRead()
		mapped, changed, err := retagBoundTypes(destination, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == nil {
				return nil
			}
			typ, _ := destination.LookupType(d.ID())
			return typ
		})
		if err != nil || !changed {
			t.Fatal("descriptor replay", mapped, changed, err)
		}
		fresh := mapped.(functions.BlockingInquiry)
		if fresh.Validate() != nil || fresh.ResultUsage().Type == n.ResultUsage().Type || fresh.Arg.ResultUsage().Type == n.Arg.ResultUsage().Type {
			t.Fatal("foreign recipe generations", fresh)
		}
		if n.Validate() != nil {
			t.Fatal("retag mutated original")
		}
		column := plan.ColumnExpr{Info: b.info(b.typ(sqltype.Int4).Usage(), parser.Position{}, expr.Properties{RowDependent: true}), Origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 123, SubID: 1}}, Slot: 0}
		n.Arg = column
		n.Property.RowDependent = true
		visited := 0
		plan.WalkExpression(n, func(child expr.Expr) {
			if _, ok := child.(plan.ColumnExpr); ok {
				visited++
			}
		})
		if visited != 1 {
			t.Fatal("dependency walk lost argument", visited)
		}
		grouped, err := b.rewriteGroup(n, []expr.Expr{column})
		if err != nil || !grouped.Properties().StageDependent || grouped.Properties().Volatility != expr.Volatile {
			t.Fatal("aggregate rewrite", grouped, err)
		}
		if _, ok := grouped.(functions.BlockingInquiry).Arg.(plan.GroupExpr); !ok {
			t.Fatal("aggregate child not reconstructed")
		}
		remapped, err := remapBoundExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID = 456; return ref }, map[int32]int{1: 2})
		if err != nil {
			t.Fatal(err)
		}
		child := remapped.(functions.BlockingInquiry).Arg.(plan.ColumnExpr)
		if child.Origin.Address.ObjectID != 456 || child.Slot != 2 || remapped.Properties().Volatility != expr.Volatile {
			t.Fatal("view slot/reference remap", child)
		}
		bad := n
		bad.Arg = nil
		if bad.Validate() == nil {
			t.Fatal("missing mandatory child accepted")
		}
		bad = n
		bad.Property.Volatility = expr.Stable
		if bad.Validate() == nil {
			t.Fatal("lost intrinsic volatility accepted")
		}
		bad = n
		bad.Usage = b.typ(sqltype.Int4).Usage()
		if bad.Validate() == nil {
			t.Fatal("scalar result accepted")
		}
	}
}
