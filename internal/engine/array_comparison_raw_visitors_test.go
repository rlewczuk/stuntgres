package engine

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestArrayCompareVisitorsRawEngine(t *testing.T) {
	literal := parser.LiteralExpr{}
	forms := []struct {
		name string
		make func(parser.Expr) parser.Expr
	}{
		{"list left", func(child parser.Expr) parser.Expr {
			return parser.InListExpr{Left: child, Items: []parser.Expr{literal}}
		}},
		{"list late item", func(child parser.Expr) parser.Expr {
			return parser.InListExpr{Left: literal, Items: []parser.Expr{literal, child}}
		}},
		{"array left", func(child parser.Expr) parser.Expr { return parser.ArrayComparisonExpr{Left: child, Right: literal} }},
		{"array right", func(child parser.Expr) parser.Expr { return parser.ArrayComparisonExpr{Left: literal, Right: child} }},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			if !containsValuesAggregateAnalysis(form.make(parser.AggregateExpr{Name: "sum"})) {
				t.Fatal("SUM child lost during failed analysis")
			}
			if containsValuesAggregateAnalysis(form.make(literal)) {
				t.Fatal("spurious SUM analysis")
			}
			for _, stored := range []bool{false, true} {
				if err := restoreScalar(form.make(literal), stored); err != nil {
					t.Fatal(err)
				}
				if err := restoreScalar(form.make(parser.ParameterExpr{Number: 1}), stored); err == nil {
					t.Fatal("restore parameter child lost", stored)
				}
			}
			if err := materializedSnapshotScalar(form.make(literal)); err == nil {
				t.Fatal("closed codec snapshot boundary widened")
			}
		})
	}
}
