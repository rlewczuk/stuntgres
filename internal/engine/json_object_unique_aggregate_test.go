package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/ids"
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
	b.aggregateClause = "SELECT"
	b.parameters[1], b.parameters[2] = key.Usage(), input.Usage()
	names := []string{"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict", "json_object_agg_unique", "jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			raw := parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}}, Pos: parser.Position{Character: 8}}
			first, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			second, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil || first.(aggregateExpression).slot != second.(aggregateExpression).slot {
				t.Fatal("identical call did not reuse slot", err)
			}
			call := b.aggregates[first.(aggregateExpression).slot]
			p := call.provider.(jsonObjectAggregateProvider)
			id := sqltype.JSON
			if strings.HasPrefix(name, "jsonb_") {
				id = sqltype.JSONB
			}
			result, _ := source.CatalogRead().LookupType(id)
			strict, unique := strings.HasSuffix(name, "_strict"), strings.Contains(name, "_unique")
			operation := aggregateJSONObject
			if strict {
				operation = aggregateJSONObjectStrict
			}
			if p.strict != strict || p.unique != unique || p.usage.Type != result || p.usage.Typmod != -1 || p.key.Type != key || p.input.Type != input || !call.includeNulls || call.operation != operation || !first.Properties().StageDependent {
				t.Fatal("fixed descriptor or policy lost", call, p)
			}
			if !unique {
				return
			}
			root := &aggregateNode{querySchema: querySchema{{Name: "v", Usage: p.usage}}, child: &valuesNode{}, calls: []aggregateCall{{provider: p, operation: call.operation, includeNulls: call.includeNulls}}}
			mapped, err := remapViewQuery(source.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				next, _ := destination.CatalogRead().LookupType(old.ID())
				return next
			})
			if err != nil {
				t.Fatal(err)
			}
			q := mapped.(*aggregateNode).calls[0].provider.(jsonObjectAggregateProvider)
			newKey, _ := destination.CatalogRead().LookupType(sqltype.Text)
			newInput, _ := destination.CatalogRead().LookupType(sqltype.Int4)
			newResult, _ := destination.CatalogRead().LookupType(id)
			if q.key.Type != newKey || q.input.Type != newInput || q.usage.Type != newResult || !q.unique || q.strict != strict || p.key.Type != key {
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
	if len(b.aggregates) != len(names) {
		t.Fatal("different object policies share slots", len(b.aggregates))
	}
}
