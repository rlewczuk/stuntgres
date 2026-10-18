package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRowComparisonDistinctPairs(t *testing.T) {
	e := New()
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
				row := rowComparisonExpression{Info: expr.Info{Usage: boolean.Usage()}, operator: mode, comparisons: []comparisonExpression{{
					Info: expr.Info{Usage: boolean.Usage()}, left: probe(0, tc.left, nil), right: probe(1, tc.right, nil), predicate: predicate, operator: "=",
				}}}
				value, err := row.Eval(&EvalContext{Context: context.Background()})
				truth, boolErr := value.Boolean()
				want := tc.distinct
				if mode == "IS NOT DISTINCT FROM" {
					want = !want
				}
				if err != nil || boolErr != nil || truth != want || !reflect.DeepEqual(order, []int{0, 1}) {
					t.Fatal("single pair total result/demand", value, err, boolErr, order)
				}
				row.comparisons = append(row.comparisons, comparisonExpression{Info: expr.Info{Usage: boolean.Usage()}, left: probe(2, one, failure), right: probe(3, one, nil), predicate: predicate, operator: "="})
				order = nil
				_, err = row.Eval(&EvalContext{Context: context.Background()})
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
				if _, err := row.Eval(&EvalContext{Context: ctx}); !errors.Is(err, context.Canceled) || len(order) != 0 {
					t.Fatal("cancelled row evaluated operands", order, err)
				}
			})
		}
	}
	for _, mode := range []string{"IS DISTINCT FROM", "IS NOT DISTINCT FROM"} {
		row := rowComparisonExpression{Info: expr.Info{Usage: boolean.Usage()}, operator: mode}
		value, err := row.Eval(&EvalContext{Context: context.Background()})
		truth, boolErr := value.Boolean()
		if err != nil || boolErr != nil || truth != (mode == "IS NOT DISTINCT FROM") {
			t.Fatal(value, err, boolErr)
		}
	}
}

func TestRowComparisonAnalysis(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
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
			_, err := b.bindRowPredicate(raw, sqltype.CompareEqual, tc.left, tc.right, mode)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatal(mode, tc.code, err)
			}
		}
	}
	unknown := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "1"}}
	node, err := b.bindRowPredicate(raw, sqltype.CompareEqual, row(unknown), row(arrayUnitInteger(1)), "IS NOT DISTINCT FROM")
	if err != nil {
		t.Fatal(err)
	}
	pair := node.(rowComparisonExpression).comparisons[0]
	if pair.left.ResultUsage().Type.ID() != sqltype.Int4 || pair.right.ResultUsage().Type.ID() != sqltype.Int4 {
		t.Fatal("unknown finalized before pair inference")
	}
	fields := make([]parser.Expr, 1665)
	for i := range fields {
		fields[i] = arrayUnitInteger(1)
	}
	_, _, err = b.rowComparisonOperands(row(fields...))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54011" || diagnostic.Position != "8" {
		t.Fatal(err)
	}
}

func TestRowComparisonStoredRemap(t *testing.T) {
	source, target := New(), New()
	b := newTestExpressionBinder(t, context.Background(), source, sqltype.InputEnvironment{}, nil)
	raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}}
	for _, width := range []int{0, 1, 2} {
		fields := make([]parser.Expr, width)
		for i := range fields {
			fields[i] = arrayUnitInteger(int64(i + 1))
		}
		row := parser.RowExpr{Fields: fields, Explicit: true}
		node, err := b.bindRowPredicate(raw, sqltype.CompareEqual, row, row, "IS NOT DISTINCT FROM")
		if err != nil {
			t.Fatal(err)
		}
		original := node.(rowComparisonExpression)
		if width != 0 {
			original.expansions[0] = map[int]compositeExpansionBinding{0: {base: original.comparisons[0].left, fields: []expr.Expr{original.comparisons[0].left}}}
		}
		retained := func(old *sqltype.Descriptor) *sqltype.Descriptor { return eType(t, target, old.ID()) }
		mapped, changed, err := retagBoundTypes(target.CatalogRead(), original, retained)
		if err != nil || !changed {
			t.Fatal(width, changed, err)
		}
		next := mapped.(rowComparisonExpression)
		if next.operator != original.operator || len(next.ordering) != 0 {
			t.Fatal("mode/provider changed")
		}
		for i, pair := range next.comparisons {
			if pair.left.ResultUsage().Type != eType(t, target, sqltype.Int4) || original.comparisons[i].left.ResultUsage().Type != eType(t, source, sqltype.Int4) {
				t.Fatal("pair ownership/source mutation")
			}
		}
		if width != 0 {
			if next.expansions[0][0].base.ResultUsage().Type != eType(t, target, sqltype.Int4) || original.expansions[0][0].fields[0].ResultUsage().Type != eType(t, source, sqltype.Int4) {
				t.Fatal("expansion association not reowned immutably")
			}
		}
		value, err := mapped.Eval(&EvalContext{Context: context.Background()})
		truth, boolErr := value.Boolean()
		if err != nil || boolErr != nil || !truth || value.Type() != eType(t, target, sqltype.Bool) {
			t.Fatal(value, err, boolErr)
		}
		text, err := defaultSQL(context.Background(), target.CatalogRead(), mapped)
		if err != nil || !strings.Contains(text, " IS NOT DISTINCT FROM ") || width < 2 && !strings.Contains(text, "ROW(") {
			t.Fatal(text, err)
		}
		_, changed, err = retagBoundTypes(target.CatalogRead(), mapped, retained)
		if err != nil || changed {
			t.Fatal("unchanged mapping copied", width, changed, err)
		}
	}
}
