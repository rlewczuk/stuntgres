package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryOuterJoinsNegatedKeys(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	info := expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true, ParameterDependent: true, StageDependent: true}}
	columnInfo := expr.Info{Usage: integer.Usage(), Property: info.Property}
	left := plan.ColumnExpr{Info: columnInfo, Slot: 0}
	right := plan.ColumnExpr{Info: columnInfo, Slot: 1}
	comparison := func(op string, a, b expr.Expr) plan.ComparisonExpr {
		t.Helper()
		operator := map[string]sqltype.ComparisonOperator{
			"=": sqltype.CompareEqual, "<>": sqltype.CompareNotEqual,
			"<": sqltype.CompareLess, "<=": sqltype.CompareLessEqual,
			">": sqltype.CompareGreater, ">=": sqltype.CompareGreaterEqual,
		}[op]
		provider, err := sqltype.BindComparisonPredicate(operator, a.ResultUsage().Type, b.ResultUsage().Type, boolean, a.ResultUsage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		return plan.ComparisonExpr{Info: info, Left: a, Right: b, Operator: op, Predicate: provider}
	}
	logical := func(op parser.LogicalOperator, a, b expr.Expr) plan.LogicalExpr {
		return plan.LogicalExpr{Info: info, Op: op, Left: a, Right: b}
	}
	not := func(a expr.Expr) expr.Expr { return logical(parser.LogicalNot, a, nil) }
	equal := comparison("=", left, right)
	unequal := comparison("<>", left, right)
	unknown := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Null(boolean)}
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	constant := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: one}
	volatile := left
	volatile.Property.Volatility = expr.Volatile
	stable := left
	stable.Property.Volatility = expr.Stable
	cases := []struct {
		name string
		expr expr.Expr
		keys int
	}{
		{"plain_equality", equal, 1},
		{"negated_inequality", not(unequal), 1},
		{"reversed_negated_inequality", not(comparison("<>", right, left)), 1},
		{"triple_negation", not(not(not(unequal))), 1},
		{"bare_inequality", unequal, 0},
		{"negated_equality", not(equal), 0},
		{"double_negated_inequality", not(not(unequal)), 0},
		{"common_effective_key", logical(parser.LogicalOr, equal, not(unequal)), 1},
		{"reversed_OR_identity", logical(parser.LogicalOr, equal, not(comparison("<>", right, left))), 0},
		{"equality_OR_unknown", logical(parser.LogicalOr, equal, unknown), 1},
		{"unknown_OR_equality", logical(parser.LogicalOr, unknown, equal), 1},
		{"negated_inequality_AND_unknown", not(logical(parser.LogicalAnd, unequal, unknown)), 1},
		{"negated_inequality_OR_unknown", not(logical(parser.LogicalOr, unequal, unknown)), 1},
		{"same_side", not(comparison("<>", left, left)), 0},
		{"constant_side", not(comparison("<>", left, constant)), 0},
		{"volatile_operand", not(comparison("<>", volatile, right)), 0},
		{"stable_operand", not(comparison("<>", stable, right)), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			join := &plan.JoinNode{Kind: parser.JoinFull, LeftWidth: 1, RightWidth: 1, Predicate: tc.expr}
			before := join.Predicate
			ctx := &EvalContext{EvalStatement: evalStatement{Context: context.Background()}}
			facts, err := fullJoinCondition(ctx, join, join.Predicate, false)
			if err != nil || facts.constant || len(facts.keys) != tc.keys {
				t.Fatalf("facts=%+v error=%v; want %d keys", facts, err, tc.keys)
			}
			if !reflect.DeepEqual(join.Predicate, before) || join.Predicate.Properties() != info.Property {
				t.Fatal("planning changed the runtime predicate or analyzed dependencies")
			}
			// Planning facts must not replace runtime FALSE/UNKNOWN results,
			// even when they are equivalent at the join match boundary.
			for _, row := range [][]sqltype.Value{{one, one}, {one, two}, {sqltype.Null(integer), one}, {one, sqltype.Null(integer)}} {
				ctx.Row = row
				want, wantErr := before.Eval(ctx)
				got, gotErr := join.Predicate.Eval(ctx)
				if wantErr != nil || gotErr != nil || !sqltype.SameDatum(want, got) {
					t.Fatalf("runtime predicate changed: want=%v/%v got=%v/%v", want, wantErr, got, gotErr)
				}
			}
		})
	}
	// Assert UNKNOWN remains observable despite match-level OR reduction.
	ctx := &EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Row: []sqltype.Value{one, two}}}
	value, err := logical(parser.LogicalOr, equal, unknown).Eval(ctx)
	if err != nil || !value.IsNull() {
		t.Fatalf("equality OR NULL must remain UNKNOWN at runtime: %v/%v", value, err)
	}
}

func TestQueryOuterJoinsComparisonNegators(t *testing.T) {
	for _, tc := range []struct{ operator, negator string }{
		{"=", "<>"}, {"<>", "="}, {"<", ">="}, {"<=", ">"}, {">", "<="}, {">=", "<"},
	} {
		t.Run(tc.operator, func(t *testing.T) {
			if got := fullJoinComparisonOperator(tc.operator, false); got != tc.operator {
				t.Fatalf("nonnegated operator=%q", got)
			}
			if got := fullJoinComparisonOperator(tc.operator, true); got != tc.negator {
				t.Fatalf("negator=%q; want %q", got, tc.negator)
			}
		})
	}
	if got := fullJoinComparisonOperator("unsupported", true); got != "" {
		t.Fatalf("unsupported comparison acquired negator %q", got)
	}
}
