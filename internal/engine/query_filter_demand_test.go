package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
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
			enumSupportPlan, err := sqltype.BindEnumSupport("enum_last", mood.Usage(), mood.Usage())
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			producer := functions.Enum{Info: expr.Info{Usage: mood.Usage()}, Plan: enumSupportPlan, Args: []expr.Expr{enumCapturedArgument{Info: expr.Info{Usage: mood.Usage()}, value: sqltype.Null(mood), calls: &calls}}}
			project := *query.Root.(*plan.ProjectNode)
			project.QuerySchema = []catalog.Column{{Name: "v", Usage: mood.Usage()}}
			project.Expressions = []expr.Expr{producer}
			bridge := &querySource{Ref: input.Ref, NestedRoot: &project, NestedInputs: input, Materialized: true, FlatWidth: 1}
			if name == "semanticFalse" || name == "identityFalse" {
				// A real captured occurrence below a consuming owner, not merely
				// a semantic context shared with another query branch.
				input.Locks = &queryLockContext{IdentityOnly: name == "identityFalse", Leaves: []queryLockLeaf{{Source: input, Mark: plan.RowMark{Mode: lock.ForUpdate}}}}
				bridge.Locks = input.Locks
				project.Child = &plan.AggregateNode{Child: project.Child}
			} else if name == "contextFalse" {
				bridge.Locks = &queryLockContext{Leaves: []queryLockLeaf{{Mark: plan.RowMark{Mode: lock.ForUpdate}}}}
			}
			t.Cleanup(bridge.Close)
			state := &queryState{Source: bridge, Input: sqltype.InputEnvironment{EnumMemberSafe: typedef.EnumSafety(map[int64]bool{102: true})}}
			value := sqltype.Boolean(boolean, false)
			live := name == "literalTrue" || name == "parameterTrue"
			if live {
				value = sqltype.Boolean(boolean, true)
			} else if name == "literalNull" || name == "parameterNull" {
				value = sqltype.Null(boolean)
			}
			var predicate expr.Expr = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: value}
			if name == "parameterFalse" || name == "parameterNull" || name == "parameterTrue" {
				predicate = plan.ParameterExpr{Info: expr.Info{Usage: boolean.Usage()}, Number: 1}
				state.Parameters = []sqltype.Value{value}
			}
			node := &plan.FilterNode{QuerySchema: project.Schema(), Child: &plan.SourceNode{QuerySchema: project.Schema(), Ref: input.Ref}, Predicate: predicate}
			stage, err := OpenNode(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if len(stage.Columns) != 1 || stage.Columns[0].Name != "v" || stage.Columns[0].Usage != mood.Usage() {
				t.Fatal("filter lost result schema", stage.Columns)
			}
			if calls != 0 {
				t.Fatal("opening filter demanded nested projection", calls)
			}
			_, present, err := stage.NextOccurrence(context.Background())
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
	c := &EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	for _, expression := range []expr.Expr{plan.ColumnExpr{}, &plan.WindowExpr{}, plan.SubqueryExpr{}} {
		if rejects, err := QueryPredicateRejectsAll(c, expression); rejects || err != nil {
			t.Fatal("row/stage/subquery expression used as empty-input proof", expression, rejects, err)
		}
	}
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	calls := 0
	dynamic := queryProbe(boolean.Usage(), func(*EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Boolean(boolean, false), nil
	})
	if rejects, err := QueryPredicateRejectsAll(c, dynamic); rejects || err != nil || calls != 0 {
		t.Fatal("arbitrary expression evaluated as proof", rejects, calls, err)
	}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: boolean.Usage()}, Number: 1}
	if rejects, err := QueryPredicateRejectsAll(c, parameter); rejects || err == nil {
		t.Fatal("invalid imported frame was suppressed", rejects, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenNode(ctx, &queryState{}, &plan.FilterNode{Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("empty-input gate erased cancellation", err)
	}
}

func TestViewQualificationProvenEmptyDemand(t *testing.T) {
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	for _, value := range []sqltype.Value{sqltype.Boolean(boolean, false), sqltype.Null(boolean), sqltype.Boolean(boolean, true)} {
		for _, parameter := range []bool{false, true} {
			c := &EvalContext{evalStatement: evalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}}}
			var user expr.Expr = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: value}
			if parameter {
				user = plan.ParameterExpr{Info: expr.Info{Usage: boolean.Usage()}, Number: 1}
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
			expression := plan.ViewQualificationExpr{Info: expr.Info{Usage: boolean.Usage()}, Predicates: []expr.Expr{outer, inner}, User: user}
			got, err := expression.Eval(c)
			truth, truthErr := plan.TruthOf(value)
			if err != nil || truthErr != nil || !sqltype.SameDatum(got, value) {
				t.Fatal("qualification lost original physical value", parameter, got, value, err)
			}
			if truth != plan.True {
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
	expression := plan.ViewQualificationExpr{Info: expr.Info{Usage: boolean.Usage()}, Predicates: []expr.Expr{hidden}, User: user}
	got, err := expression.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	truth, truthErr := plan.TruthOf(got)
	if err != nil || truthErr != nil || truth != plan.False || len(order) != 1 || order[0] != "hidden" {
		t.Fatal("arbitrary user work ran ahead of hidden-row qualification", order, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	expression.User = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}
	if _, err := expression.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) || len(order) != 1 {
		t.Fatal("empty user input erased cancellation", order, err)
	}
}
