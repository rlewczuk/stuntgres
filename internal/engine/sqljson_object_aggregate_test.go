package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSQLJSONObjectAggregatePolicySlotsAndReturningOwner(t *testing.T) {
	e := New()
	ctx := context.Background()
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	b.Params.Parameters[1], b.Params.Parameters[2] = text.Usage(), integer.Usage()
	for _, absent := range []bool{false, true} {
		for _, unique := range []bool{false, true} {
			for range 2 {
				target := parser.TypeName{Name: "text"}
				raw := parser.AggregateExpr{Name: "json_objectagg", Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}}, JSON: &parser.JSONAggregateOptions{Absent: absent, Unique: unique, Returning: &target}, Pos: parser.Position{Character: 8}}
				node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				wrapper, ok := node.(plan.CoercionExpr)
				if !ok || node.ResultUsage().Type != text {
					t.Fatal("returning wrapper", node)
				}
				if _, ok = wrapper.Child.(plan.AggregateExpr); !ok {
					t.Fatal("returning did not retain owned aggregate", wrapper)
				}
			}
		}
	}
	if len(b.Grouping.Aggregates) != 4 {
		t.Fatal("policies lost or duplicate ownership", len(b.Grouping.Aggregates))
	}
	for _, call := range b.Grouping.Aggregates {
		if !call.IncludeNulls {
			t.Fatal("executor dropped NULL keys")
		}
	}
}

func TestSQLJSONObjectAggregateUniqueRemap(t *testing.T) {
	source, destination := New(), New()
	ctx := context.Background()
	key, _ := source.CatalogRead().LookupType(sqltype.Text)
	input, _ := source.CatalogRead().LookupType(sqltype.Int4)
	for _, id := range []sqltype.ID{sqltype.JSON, sqltype.JSONB} {
		result, _ := source.CatalogRead().LookupType(id)
		p, err := functions.NewJSONObjectAggregate(result.Usage(), true, true).Complete(key.Usage(), input.Usage())
		if err != nil {
			t.Fatal(err)
		}
		root := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: result.Usage()}}, Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{{Provider: p, Operation: functions.AggregateJSONObjectStrict, IncludeNulls: true}}}
		mapped, err := RemapViewQuery(source.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := destination.CatalogRead().LookupType(old.ID())
			return next
		})
		if err != nil {
			t.Fatal(err)
		}
		q := mapped.(*plan.AggregateNode).Calls[0].Provider.(functions.JSONObjectAggregate)
		if !q.Unique() || !q.Strict() || q.KeyUsage().Type == p.KeyUsage().Type || q.InputUsage().Type == p.InputUsage().Type || q.ResultUsage().Type == p.ResultUsage().Type {
			t.Fatal("remap lost policy or ownership")
		}
		state, err := q.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		keyValue := sqltype.String(q.KeyUsage().Type, "a")
		value := sqltype.Null(q.InputUsage().Type)
		if err = q.Advance(ctx, state, keyValue, value); err != nil {
			t.Fatal(err)
		}
		err = q.Advance(ctx, state, keyValue, value)
		if id == sqltype.JSON {
			if err == nil {
				t.Fatal("remap lost transition uniqueness")
			}
		} else {
			if err != nil {
				t.Fatal("remap changed binary timing", err)
			}
			if _, err = q.Finalize(ctx, state); err == nil {
				t.Fatal("remap lost binary uniqueness")
			}
		}
	}
}
