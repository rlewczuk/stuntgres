package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRowComparisonDistinctPairs(t *testing.T) {
	e := engine.New()
	integer, boolean := eType(t, e, sqltype.Int4), eType(t, e, sqltype.Bool)
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	null := sqltype.Null(integer)
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, integer, integer, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("demanded later row operand")
	for _, tc := range []struct {
		name        string
		left, right sqltype.Value
		distinct    bool
	}{
		{"both_null", null, null, false},
		{"left_null", null, one, true},
		{"right_null", one, null, true},
		{"equal", one, one, false},
		{"unequal", one, two, true},
	} {
		for _, mode := range []string{"IS DISTINCT FROM", "IS NOT DISTINCT FROM"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				var order []int
				probe := func(id int, value sqltype.Value, failure error) expr.Expr {
					return rowOperandEffect{expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, value, &order, id, failure}
				}
				row := plan.RowComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Operator: mode, Comparisons: []plan.ComparisonExpr{{
					Info: expr.Info{Usage: boolean.Usage()}, Left: probe(0, tc.left, nil), Right: probe(1, tc.right, nil), Predicate: predicate, Operator: "=",
				}}}
				value, err := row.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
				truth, boolErr := value.Boolean()
				want := tc.distinct
				if mode == "IS NOT DISTINCT FROM" {
					want = !want
				}
				if err != nil || boolErr != nil || truth != want || !reflect.DeepEqual(order, []int{0, 1}) {
					t.Fatal("single pair total result/demand", value, err, boolErr, order)
				}
				row.Comparisons = append(row.Comparisons, plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: probe(2, one, failure), Right: probe(3, one, nil), Predicate: predicate, Operator: "="})
				order = nil
				_, err = row.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
				if tc.distinct {
					if err != nil || !reflect.DeepEqual(order, []int{0, 1}) {
						t.Fatal("decisive pair demanded sibling", order, err)
					}
				} else if !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2}) {
					t.Fatal("reached error/order lost", order, err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				order = nil
				if _, err := row.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) || len(order) != 0 {
					t.Fatal("cancelled row evaluated operands", order, err)
				}
			})
		}
	}
	for _, mode := range []string{"IS DISTINCT FROM", "IS NOT DISTINCT FROM"} {
		row := plan.RowComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Operator: mode}
		value, err := row.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		truth, boolErr := value.Boolean()
		if err != nil || boolErr != nil || truth != (mode == "IS NOT DISTINCT FROM") {
			t.Fatal(value, err, boolErr)
		}
	}
}

func TestRowComparisonAnalysis(t *testing.T) {
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	pos := parser.Position{Character: 17}
	raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Pos: pos}
	row := func(fields ...parser.Expr) parser.RowExpr {
		return parser.RowExpr{Fields: fields, Explicit: true, Pos: parser.Position{Character: 8}}
	}
	missing := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}}, Pos: parser.Position{Character: 12}}
	for _, mode := range []string{"=", "IS DISTINCT FROM"} {
		for _, tc := range []struct {
			left, right parser.RowExpr
			code        string
		}{
			{row(missing), row(arrayUnitInteger(1), arrayUnitInteger(2)), "42703"},
			{row(arrayUnitInteger(1)), row(arrayUnitInteger(1), missing), "42703"},
			{row(arrayUnitInteger(1)), row(arrayUnitInteger(1), arrayUnitInteger(2)), "42601"},
			{row(arrayUnitInteger(1), arrayUnitInteger(2)), row(arrayUnitInteger(2), parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}), "42883"},
		} {
			_, err := b.BindRowPredicate(raw, sqltype.CompareEqual, tc.left, tc.right, mode)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatal(mode, tc.code, err)
			}
		}
	}
	unknown := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "1"}}
	node, err := b.BindRowPredicate(raw, sqltype.CompareEqual, row(unknown), row(arrayUnitInteger(1)), "IS NOT DISTINCT FROM")
	if err != nil {
		t.Fatal(err)
	}
	pair := node.(plan.RowComparisonExpr).Comparisons[0]
	if pair.Left.ResultUsage().Type.ID() != sqltype.Int4 || pair.Right.ResultUsage().Type.ID() != sqltype.Int4 {
		t.Fatal("unknown finalized before pair inference")
	}
	fields := make([]parser.Expr, 1665)
	for i := range fields {
		fields[i] = arrayUnitInteger(1)
	}
	_, _, err = b.RowComparisonOperands(row(fields...))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54011" || diagnostic.Position != "8" {
		t.Fatal(err)
	}
}

func TestRowComparisonStoredRemap(t *testing.T) {
	source, target := engine.New(), engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), source, sqltype.InputEnvironment{}, nil)
	mapper := stored.ViewQueryRemapper{Catalog: target.CatalogRead(), Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: replacementProcedurePolicyFixture(source, target)}
	raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}}
	for _, width := range []int{0, 1, 2} {
		fields := make([]parser.Expr, width)
		for i := range fields {
			fields[i] = arrayUnitInteger(int64(i + 1))
		}
		row := parser.RowExpr{Fields: fields, Explicit: true}
		node, err := b.BindRowPredicate(raw, sqltype.CompareEqual, row, row, "IS NOT DISTINCT FROM")
		if err != nil {
			t.Fatal(err)
		}
		original := node.(plan.RowComparisonExpr)
		if width != 0 {
			original.Expansions[0] = map[int]plan.CompositeExpansionBinding{0: {Base: original.Comparisons[0].Left, Fields: []expr.Expr{original.Comparisons[0].Left}}}
		}
		retained := func(old *sqltype.Descriptor) *sqltype.Descriptor { return eType(t, target, old.ID()) }
		mapped, changed, err := stored.RetagTypes(target.CatalogRead(), original, retained)
		if err != nil || !changed {
			t.Fatal(width, changed, err)
		}
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{mapped}}})
		if err != nil {
			t.Fatal(err)
		}
		mapped = query.(*plan.ValuesNode).Rows[0][0]
		next := mapped.(plan.RowComparisonExpr)
		if next.Operator != original.Operator || len(next.Ordering) != 0 {
			t.Fatal("mode/provider changed")
		}
		for i, pair := range next.Comparisons {
			if pair.Left.ResultUsage().Type != eType(t, target, sqltype.Int4) || original.Comparisons[i].Left.ResultUsage().Type != eType(t, source, sqltype.Int4) {
				t.Fatal("pair ownership/source mutation")
			}
		}
		if width != 0 {
			if next.Expansions[0][0].Base.ResultUsage().Type != eType(t, target, sqltype.Int4) || original.Expansions[0][0].Fields[0].ResultUsage().Type != eType(t, source, sqltype.Int4) {
				t.Fatal("expansion association not reowned immutably")
			}
		}
		value, err := mapped.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		truth, boolErr := value.Boolean()
		if err != nil || boolErr != nil || !truth || value.Type() != eType(t, target, sqltype.Bool) {
			t.Fatal(value, err, boolErr)
		}
		text, err := stored.DefaultSQL(context.Background(), target.CatalogRead(), mapped)
		if err != nil || !strings.Contains(text, " IS NOT DISTINCT FROM ") || width < 2 && !strings.Contains(text, "ROW(") {
			t.Fatal(text, err)
		}
	}
}
