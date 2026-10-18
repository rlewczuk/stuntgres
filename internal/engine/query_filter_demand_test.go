package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryFilterProvenEmptyDemand(t *testing.T) {
	for _, name := range []string{"literalFalse", "literalNull", "parameterFalse", "parameterNull", "literalTrue", "parameterTrue", "semanticFalse", "identityFalse", "contextFalse"} {
		t.Run(name, func(t *testing.T) {
			e, query, input := queryCoreFixture(t, "SELECT id FROM t")
			boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
			mood, err := sqltype.EnumWithMembers(45000, "mood", "mood", []sqltype.EnumMember{{ID: 101, Label: "old", SortOrder: 1}, {ID: 102, Label: "pending", SortOrder: 2}})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := sqltype.BindEnumSupport("enum_last", mood.Usage(), mood.Usage())
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			producer := functions.Enum{Info: functionInfo(expressionInfo{Usage: mood.Usage()}), Plan: plan, Args: []BoundExpr{enumCapturedArgument{expressionInfo: expressionInfo{Usage: mood.Usage()}, value: sqltype.Null(mood), calls: &calls}}}
			project := *query.Root.(*projectNode)
			project.querySchema = []ColumnDescriptor{{Name: "v", Usage: mood.Usage()}}
			project.expressions = []BoundExpr{producer}
			bridge := &querySource{ref: input.ref, nestedRoot: &project, nestedInputs: input, materialized: true, flatWidth: 1}
			if name == "semanticFalse" || name == "identityFalse" {
				// A real captured occurrence below a consuming owner, not merely
				// a semantic context shared with another query branch.
				input.locks = &queryLockContext{identityOnly: name == "identityFalse", leaves: []queryLockLeaf{{source: input, mark: queryRowMark{mode: rowUpdate}}}}
				bridge.locks = input.locks
				project.child = &aggregateNode{child: project.child}
			} else if name == "contextFalse" {
				bridge.locks = &queryLockContext{leaves: []queryLockLeaf{{mark: queryRowMark{mode: rowUpdate}}}}
			}
			t.Cleanup(bridge.close)
			state := &queryState{source: bridge, input: sqltype.InputEnvironment{EnumMemberSafe: enumSafety(map[int64]bool{102: true})}}
			value := sqltype.Boolean(boolean, false)
			live := name == "literalTrue" || name == "parameterTrue"
			if live {
				value = sqltype.Boolean(boolean, true)
			} else if name == "literalNull" || name == "parameterNull" {
				value = sqltype.Null(boolean)
			}
			var predicate BoundExpr = literalExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, value: value}
			if name == "parameterFalse" || name == "parameterNull" || name == "parameterTrue" {
				predicate = parameterExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, number: 1}
				state.parameters = []sqltype.Value{value}
			}
			node := &filterNode{querySchema: project.Schema(), child: &sourceNode{querySchema: project.Schema(), ref: input.ref}, predicate: predicate}
			stage, err := openQueryNode(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if len(stage.schema) != 1 || stage.schema[0].Name != "v" || stage.schema[0].Usage != mood.Usage() {
				t.Fatal("filter lost result schema", stage.schema)
			}
			if calls != 0 {
				t.Fatal("opening filter demanded nested projection", calls)
			}
			_, present, err := stage.nextOccurrence(context.Background())
			if live || name == "semanticFalse" {
				d, ok := err.(*sqlstate.Error)
				if present || !ok || d.Code != "55P04" || calls != 1 {
					t.Fatal("live or semantic-marked input suppressed actual producer", present, calls, err)
				}
			} else if present || err != nil || calls != 0 {
				t.Fatal("proven-empty unmarked input returned rows or demanded producer", present, calls, err)
			}
		})
	}
}

func TestQueryFilterPredicateProofBoundaries(t *testing.T) {
	c := &EvalContext{Context: context.Background()}
	for _, expression := range []BoundExpr{columnExpression{}, &windowExpression{}, subqueryExpression{}} {
		if rejects, err := queryPredicateRejectsAll(c, expression); rejects || err != nil {
			t.Fatal("row/stage/subquery expression used as empty-input proof", expression, rejects, err)
		}
	}
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	calls := 0
	dynamic := queryProbe(boolean.Usage(), func(*EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Boolean(boolean, false), nil
	})
	if rejects, err := queryPredicateRejectsAll(c, dynamic); rejects || err != nil || calls != 0 {
		t.Fatal("arbitrary expression evaluated as proof", rejects, calls, err)
	}
	parameter := parameterExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, number: 1}
	if rejects, err := queryPredicateRejectsAll(c, parameter); rejects || err == nil {
		t.Fatal("invalid imported frame was suppressed", rejects, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := openQueryNode(ctx, &queryState{}, &filterNode{predicate: literalExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, false)}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("empty-input gate erased cancellation", err)
	}
}

func TestViewQualificationProvenEmptyDemand(t *testing.T) {
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	for _, value := range []sqltype.Value{sqltype.Boolean(boolean, false), sqltype.Null(boolean), sqltype.Boolean(boolean, true)} {
		for _, parameter := range []bool{false, true} {
			c := &EvalContext{Context: context.Background(), Parameters: []sqltype.Value{value}}
			var user BoundExpr = literalExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, value: value}
			if parameter {
				user = parameterExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, number: 1}
			}
			var order []string
			outer := queryProbe(boolean.Usage(), func(*EvalContext) (sqltype.Value, error) {
				order = append(order, "outer")
				return sqltype.Boolean(boolean, true), nil
			})
			inner := queryProbe(boolean.Usage(), func(c *EvalContext) (sqltype.Value, error) {
				order = append(order, "inner")
				// Detect a second parameter read after the qualification. The
				// admitted physical value must be returned unchanged, not reread.
				c.Parameters[0] = sqltype.Boolean(boolean, false)
				return sqltype.Boolean(boolean, true), nil
			})
			expression := viewQualificationExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, predicates: []BoundExpr{outer, inner}, user: user}
			got, err := expression.Eval(c)
			truth, truthErr := TruthOf(value)
			if err != nil || truthErr != nil || !sqltype.SameDatum(got, value) {
				t.Fatal("qualification lost original physical value", parameter, got, value, err)
			}
			if truth != True {
				if len(order) != 0 {
					t.Fatal("FALSE/NULL demanded qualification", parameter, order)
				}
			} else if len(order) != 2 || order[0] != "inner" || order[1] != "outer" {
				t.Fatal("TRUE changed qualification security order", parameter, order)
			}
		}
	}
	var order []string
	hidden := queryProbe(boolean.Usage(), func(*EvalContext) (sqltype.Value, error) {
		order = append(order, "hidden")
		return sqltype.Boolean(boolean, false), nil
	})
	user := queryProbe(boolean.Usage(), func(*EvalContext) (sqltype.Value, error) {
		order = append(order, "user")
		return sqltype.Boolean(boolean, true), nil
	})
	expression := viewQualificationExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, predicates: []BoundExpr{hidden}, user: user}
	got, err := expression.Eval(&EvalContext{Context: context.Background()})
	truth, truthErr := TruthOf(got)
	if err != nil || truthErr != nil || truth != False || len(order) != 1 || order[0] != "hidden" {
		t.Fatal("arbitrary user work ran ahead of hidden-row qualification", order, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	expression.user = literalExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, false)}
	if _, err := expression.Eval(&EvalContext{Context: ctx}); !errors.Is(err, context.Canceled) || len(order) != 1 {
		t.Fatal("empty user input erased cancellation", order, err)
	}
}
