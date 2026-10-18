package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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
				bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				node := bound.(advisoryExpression)
				if node.lifetime != advisoryTransaction || !node.validStored() || node.Properties().Volatility != expr.Volatile || node.Properties().ParameterDependent != strings.Contains(arguments, "$") {
					t.Fatal("lost transaction identity", node)
				}
				bad := node
				bad.lifetime = advisorySession
				if bad.validStored() {
					t.Fatal("accepted name/lifetime mismatch")
				}
				id := sqltype.Int8
				if len(node.args) == 2 {
					id = sqltype.Int4
				}
				for _, child := range node.args {
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
					last := len(node.args) - 1
					column := columnExpression{Info: expr.Info{Usage: node.args[last].ResultUsage(), Property: expr.Properties{RowDependent: true}}, scope: 1, slot: 1, origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 123, SubID: 1}}}
					stored := node
					stored.args = append([]expr.Expr(nil), node.args...)
					stored.args[last] = column
					remapped, err := remapBoundExpression(stored, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID++; return ref }, map[int32]int{1: 7})
					if err != nil {
						t.Fatal("remapping stored column", err)
					}
					mapped := remapped.(advisoryExpression)
					child := mapped.args[last].(columnExpression)
					if mapped.lifetime != advisoryTransaction || !mapped.validStored() || child.origin.Address.ObjectID != 124 || child.slot != 7 || stored.args[last].(columnExpression).slot != 1 {
						t.Fatal("stored remap lost lifetime/column identity or mutated source", mapped, child)
					}
					// Only stored literal consumers have a catalog deparse
					// contract; runtime parameters are not stored recipes.
					text, err := (pgRenderer{view: e.CatalogRead()}).sql(ctx, node, pgRenderParent{})
					if err != nil {
						t.Fatal(err)
					}
					rebound, err := b.bind(statement(t, "SELECT "+text).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
					if err != nil {
						t.Fatal(err)
					}
					reboundNode := rebound.(advisoryExpression)
					if reboundNode.lifetime != advisoryTransaction || reboundNode.mode != node.mode || reboundNode.action != node.action || reboundNode.ResultUsage() != node.ResultUsage() {
						t.Fatal("deparse changed lock semantics", reboundNode)
					}
				}
			})
		}
	}
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, q := range []string{"SELECT pg_advisory_xact_lock(NULL,CASE WHEN pg_try_advisory_lock(12) THEN 1 ELSE 0 END)", "SELECT pg_try_advisory_xact_lock(NULL,1)"} {
		bound, err := b.bind(statement(t, q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		for _, preflight := range []bool{false, true} {
			var folded expr.Expr
			if preflight {
				folded, _, err = preflightCoercions(&EvalContext{Context: ctx}, bound)
			} else {
				folded, _, err = simplifyConditional(&EvalContext{Context: ctx}, bound, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			value, err := folded.Eval(&EvalContext{Context: ctx})
			wantType := sqltype.Void
			if strings.Contains(q, "pg_try_advisory_xact_lock") {
				wantType = sqltype.Bool
			}
			if err != nil || !value.IsNull() || value.Type().ID() != wantType {
				t.Fatal("constant NULL failed to suppress volatile sibling or lost result type", value, err)
			}
		}
	}
	types := formatUnitTypes()
	var order []int
	value, err := sqltype.Integer(types[sqltype.Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	node := advisoryExpression{Info: expr.Info{Usage: types[sqltype.Bool].Usage()}, name: "pg_try_advisory_xact_lock", lifetime: advisoryTransaction, mode: lock.AdvisoryExclusive, action: advisoryTry, args: []expr.Expr{
		mathObservedArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, value: sqltype.Null(types[sqltype.Int4]), order: &order, index: 0},
		mathObservedArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, value: value, order: &order, index: 1},
	}}
	got, err := node.Eval(&EvalContext{Context: ctx})
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime strict demand", got, order, err)
	}
	failure := errors.New("demanded xact sibling")
	order = nil
	node.args[1] = mathObservedArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, order: &order, index: 1, err: failure}
	if _, err := node.Eval(&EvalContext{Context: ctx}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("suppressed runtime sibling", order, err)
	}
}
