package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONObjectUniqueAggregateSlotsAndRemap(t *testing.T) {
	source, destination := New(), New()
	ctx := context.Background()
	key, _ := source.CatalogRead().LookupType(sqltype.Text)
	input, _ := source.CatalogRead().LookupType(sqltype.Int4)
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	b.Params.Parameters[1], b.Params.Parameters[2] = key.Usage(), input.Usage()
	names := []string{"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict", "json_object_agg_unique", "jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			raw := parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}}, Pos: parser.Position{Character: 8}}
			first, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			second, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext})
			if err != nil || first.(plan.AggregateExpr).Slot != second.(plan.AggregateExpr).Slot {
				t.Fatal("identical call did not reuse slot", err)
			}
			call := b.Grouping.Aggregates[first.(plan.AggregateExpr).Slot]
			p := call.Provider.(functions.JSONObjectAggregate)
			id := sqltype.JSON
			if strings.HasPrefix(name, "jsonb_") {
				id = sqltype.JSONB
			}
			result, _ := source.CatalogRead().LookupType(id)
			strict, unique := strings.HasSuffix(name, "_strict"), strings.Contains(name, "_unique")
			operation := functions.AggregateJSONObject
			if strict {
				operation = functions.AggregateJSONObjectStrict
			}
			if p.Strict() != strict || p.Unique() != unique || p.ResultUsage().Type != result || p.ResultUsage().Typmod != -1 || p.KeyUsage().Type != key || p.InputUsage().Type != input || !call.IncludeNulls || call.Operation != operation || !first.Properties().StageDependent {
				t.Fatal("fixed descriptor or policy lost", call, p)
			}
			if !unique {
				return
			}
			root := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: p.ResultUsage()}}, Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{{Provider: p, Operation: call.Operation, IncludeNulls: call.IncludeNulls}}}
			mapped, err := remapViewQuery(source.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				next, _ := destination.CatalogRead().LookupType(old.ID())
				return next
			})
			if err != nil {
				t.Fatal(err)
			}
			q := mapped.(*plan.AggregateNode).Calls[0].Provider.(functions.JSONObjectAggregate)
			newKey, _ := destination.CatalogRead().LookupType(sqltype.Text)
			newInput, _ := destination.CatalogRead().LookupType(sqltype.Int4)
			newResult, _ := destination.CatalogRead().LookupType(id)
			if q.KeyUsage().Type != newKey || q.InputUsage().Type != newInput || q.ResultUsage().Type != newResult || !q.Unique() || q.Strict() != strict || p.KeyUsage().Type != key {
				t.Fatal("remap lost fresh owners or policy")
			}
			state, err := q.Init(ctx)
			if err != nil {
				t.Fatal(err)
			}
			k, v := sqltype.String(newKey, "a"), sqltype.Null(newInput)
			if err = q.Advance(ctx, state, k, v); err != nil {
				t.Fatal(err)
			}
			err = q.Advance(ctx, state, k, v)
			if id == sqltype.JSONB {
				if err != nil {
					t.Fatal("binary duplicate became eager", err)
				}
				_, err = q.Finalize(ctx, state)
			}
			var diagnostic *sqlstate.Error
			message := "duplicate JSON object key value"
			if id == sqltype.JSON {
				message += `: "a"`
			}
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22030" || diagnostic.Message != message || diagnostic.Position != "" {
				t.Fatal("remap lost duplicate timing/diagnostic", err)
			}
		})
	}
	if len(b.Grouping.Aggregates) != len(names) {
		t.Fatal("different object policies share slots", len(b.Grouping.Aggregates))
	}
}
