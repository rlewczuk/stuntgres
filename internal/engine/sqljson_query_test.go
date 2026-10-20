package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLJSONArrayQueryReachedCancellationCleanup(t *testing.T) {
	e, query, captured := queryJoinsFixture(t, "SELECT k FROM a")
	inner := query.Root.(*projectNode)
	inputUsage := inner.Schema()[0].Usage
	jsonType, ok := e.CatalogRead().LookupType(sqltype.JSON)
	if !ok {
		t.Fatal("missing JSON descriptor")
	}
	jsonArrayAggregatePlan, err := sqltype.BindJSONArrayAggregate(inputUsage, jsonType.Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	// A real lazy view producer exposes its actual nested stage. Both levels
	// are ordinary query machinery, not a stand-in cursor or canned result.
	base := inner.Child.(*sourceNode)
	outer := &projectNode{QuerySchema: inner.QuerySchema, Frame: inner.Frame + 1}
	outer.Child = &sourceNode{QuerySchema: inner.QuerySchema, Ref: base.Ref, Occurrence: 0, PresenceSlot: base.PresenceSlot}
	column := columnExpression{Info: expr.Info{Usage: inputUsage}, Scope: 1, Frame: outer.Frame, Slot: 0}
	outer.Expressions = []expr.Expr{column}
	bound := *query
	bound.Root = outer
	expr := subqueryTestExpression(t, e, &bound, parser.SubqueryScalar)
	expr.Usage = jsonType.Usage()
	expr.JSONArray = &sqlJSONArrayQuery{Plan: jsonArrayAggregatePlan, Input: column}
	newCapture := func() *querySource {
		return &querySource{ref: base.Ref, authority: captured.authority, nestedRoot: inner, nestedInputs: captured, flatWidth: 1, materialized: true}
	}
	bridge := newCapture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parent := queryState{source: bridge, executionContext: ctx}
	calls, closes := 0, 0
	secondary := errors.New("nested stage cleanup")
	var reached *indexedStage
	expr.JSONArray.Input = queryProbe(inputUsage, func(c *EvalContext) (sqltype.Value, error) {
		calls++
		value, err := column.Eval(c)
		if err != nil {
			return value, err
		}
		if calls == 2 {
			if bridge.flatCount != 2 || bridge.nestedStage == nil || bridge.nestedStage.closed {
				t.Fatal("real query rows not reached", bridge.flatCount, bridge.nestedStage)
			}
			reached = bridge.nestedStage
			finish := reached.finish
			reached.finish = func() error {
				closes++
				if finish != nil {
					if err := finish(); err != nil {
						return err
					}
				}
				return secondary
			}
			// The first row already passed Advance. Cancel during evaluation of
			// the second row, leaving evalJSONArray itself to observe cancellation.
			cancel()
		}
		return value, nil
	})
	c := parent.evaluation(ctx)
	value, err := expr.Eval(&c)
	if !errors.Is(err, context.Canceled) || value != (sqltype.Value{}) || calls != 2 || closes != 1 {
		t.Fatal("cancellation/primary error lost", value, err, calls, closes)
	}
	if reached == nil || !reached.closed || reached.source != nil || reached.finish != nil || reached.produce != nil {
		t.Fatal("real nested producer retained resources", reached)
	}
	if err := reached.Close(); err != nil || closes != 1 {
		t.Fatal("cleanup was not idempotent", err, closes)
	}
	// Held executions reuse the bound owner, not a closed execution's capture.
	// Reopen the same expression with a fresh capture of the same source rows.
	parent.source = newCapture()
	parent.executionContext = nil
	c = parent.evaluation(context.Background())
	value, err = expr.Eval(&c)
	text, outputErr := jsonType.Output(context.Background(), value)
	if err != nil || outputErr != nil || text != "[1, 1, 2]" || calls != 6 {
		t.Fatal("same query owner failed recovery", text, err, outputErr, calls)
	}
	if stage := parent.source.nestedStage; stage == nil || !stage.closed || stage.source != nil {
		t.Fatal("successful recovery retained producer", stage)
	}
	for _, phase := range []string{"input", "close"} {
		t.Run(phase, func(t *testing.T) {
			execution := newCapture()
			owner := queryState{source: execution}
			primary := errors.New("per-row input")
			inputs, finished := 0, 0
			var nested *indexedStage
			expr.JSONArray.Input = queryProbe(inputUsage, func(c *EvalContext) (sqltype.Value, error) {
				inputs++
				if inputs == 1 {
					nested = execution.nestedStage
					if nested == nil || nested.closed {
						t.Fatal("nested producer not opened")
					}
					finish := nested.finish
					nested.finish = func() error {
						finished++
						if finish != nil {
							if err := finish(); err != nil {
								return err
							}
						}
						return secondary
					}
				}
				if phase == "input" && inputs == 2 {
					return sqltype.Value{}, primary
				}
				return column.Eval(c)
			})
			c := owner.evaluation(context.Background())
			value, err := expr.Eval(&c)
			want := secondary
			if phase == "input" {
				want = primary
				if value != (sqltype.Value{}) || inputs != 2 {
					t.Fatal("failed input published partial array", value, inputs)
				}
			} else if inputs != 4 {
				t.Fatal("successful array did not consume all rows", inputs)
			}
			if err != want || finished != 1 || nested == nil || !nested.closed || nested.source != nil {
				t.Fatal("query-array close precedence/ownership lost", err, finished, nested)
			}
		})
	}
}
