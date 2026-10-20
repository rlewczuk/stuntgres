package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAdvisoryXactBindingTraversal(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, name := range []string{"pg_advisory_xact_lock", "pg_advisory_xact_lock_shared", "pg_try_advisory_xact_lock", "pg_try_advisory_xact_lock_shared"} {
		for _, arguments := range []string{"$1", "$1,$2", "1", "1,2"} {
			t.Run(name+"/"+arguments, func(t *testing.T) {
				b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
				raw := statement(t, "SELECT "+name+"("+arguments+")").(*parser.Select).Targets[0]
				bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				node := bound.(functions.Advisory)
				if node.Lifetime != functions.AdvisoryTransaction || !node.ValidStored() || node.Properties().Volatility != expr.Volatile || node.Properties().ParameterDependent != strings.Contains(arguments, "$") {
					t.Fatal("lost transaction identity", node)
				}
				bad := node
				bad.Lifetime = functions.AdvisorySession
				if bad.ValidStored() {
					t.Fatal("accepted name/lifetime mismatch")
				}
				id := sqltype.Int8
				if len(node.Args) == 2 {
					id = sqltype.Int4
				}
				for _, child := range node.Args {
					if child.ResultUsage().Type.ID() != id {
						t.Fatal("wrong formal", child.ResultUsage())
					}
				}
				if strings.Contains(arguments, "$") {
					// Prepared parameters are runtime inputs, not persistable view
					// children. Lifetime support must not admit them to storage.
					if _, err := remapBoundExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil); err == nil {
						t.Fatal("persisted a runtime parameter")
					}
				} else {
					last := len(node.Args) - 1
					column := plan.ColumnExpr{Info: expr.Info{Usage: node.Args[last].ResultUsage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Slot: 1, Origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 123, SubID: 1}}}
					stored := node
					stored.Args = append([]expr.Expr(nil), node.Args...)
					stored.Args[last] = column
					remapped, err := remapBoundExpression(stored, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID++; return ref }, map[int32]int{1: 7})
					if err != nil {
						t.Fatal("remapping stored column", err)
					}
					mapped := remapped.(functions.Advisory)
					child := mapped.Args[last].(plan.ColumnExpr)
					if mapped.Lifetime != functions.AdvisoryTransaction || !mapped.ValidStored() || child.Origin.Address.ObjectID != 124 || child.Slot != 7 || stored.Args[last].(plan.ColumnExpr).Slot != 1 {
						t.Fatal("stored remap lost lifetime/column identity or mutated source", mapped, child)
					}
					// Only stored literal consumers have a catalog deparse
					// contract; runtime parameters are not stored recipes.
					text, err := (pgRenderer{view: e.CatalogRead()}).sql(ctx, node, pgRenderParent{})
					if err != nil {
						t.Fatal(err)
					}
					rebound, err := b.Bind(statement(t, "SELECT "+text).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
					if err != nil {
						t.Fatal(err)
					}
					reboundNode := rebound.(functions.Advisory)
					if reboundNode.Lifetime != functions.AdvisoryTransaction || reboundNode.Mode != node.Mode || reboundNode.Action != node.Action || reboundNode.ResultUsage() != node.ResultUsage() {
						t.Fatal("deparse changed lock semantics", reboundNode)
					}
				}
			})
		}
	}
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, q := range []string{"SELECT pg_advisory_xact_lock(NULL,CASE WHEN pg_try_advisory_lock(12) THEN 1 ELSE 0 END)", "SELECT pg_try_advisory_xact_lock(NULL,1)"} {
		bound, err := b.Bind(statement(t, q).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		for _, preflight := range []bool{false, true} {
			var folded expr.Expr
			if preflight {
				folded, _, err = preflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, bound)
			} else {
				folded, _, err = simplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, bound, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			value, err := folded.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			wantType := sqltype.Void
			if strings.Contains(q, "pg_try_advisory_xact_lock") {
				wantType = sqltype.Bool
			}
			if err != nil || !value.IsNull() || value.Type().ID() != wantType {
				t.Fatal("constant NULL failed to suppress volatile sibling or lost result type", value, err)
			}
		}
	}
}
