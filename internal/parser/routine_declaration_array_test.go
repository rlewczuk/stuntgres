package parser

import (
	"context"
	"errors"
	"testing"
)

func TestArraySubqueryOrdinaryAdmission(t *testing.T) {
	const source = "SELECT ARRAY(SELECT m.mapdict::regdictionary FROM pg_ts_config_map AS m WHERE m.mapcfg=$1 ORDER BY m.mapseqno) AS dictionaries"
	check := func(t *testing.T, statements []Statement) {
		t.Helper()
		if len(statements) != 1 {
			t.Fatal("lost complete statement", statements)
		}
		query, ok := statements[0].(*Select)
		if !ok || len(query.Targets) != 1 {
			t.Fatal("lost outer SELECT", statements)
		}
		array, ok := query.Targets[0].(SubqueryExpr)
		if !ok || array.Kind != SubqueryArray || array.Query == nil {
			t.Fatal("lost ordinary array subquery node", query.Targets[0])
		}
		if array.Pos.Character != 8 {
			t.Fatal("array subquery must be positioned at the ARRAY token", array.Pos)
		}
		if len(array.Query.Targets) != 1 || array.Query.From == nil || array.Query.Where == nil || array.Query.Order == nil {
			t.Fatal("lost genuine array query structure", array.Query)
		}
		cast, ok := array.Query.Targets[0].(CastExpr)
		if !ok || cast.TypeName.Name != "regdictionary" {
			t.Fatal("lost dictionary cast", array.Query.Targets[0])
		}
		column, ok := cast.Child.(ColumnExpr)
		if !ok || len(column.Name.Parts) != 2 || column.Name.Parts[0].Text != "m" || column.Name.Parts[1].Text != "mapdict" {
			t.Fatal("lost original query target", cast.Child)
		}
		queries := 0
		if err := VisitQuerySources(statements[0], func(*Select) error { queries++; return nil }, nil); err != nil || queries != 2 {
			t.Fatal("array query omitted by ordinary syntax traversal", queries, err)
		}
	}
	for _, tc := range []struct {
		name  string
		parse func(string) ([]Statement, error)
	}{
		{"Parse", Parse},
		{"ParseWithOptions", func(sql string) ([]Statement, error) {
			return ParseWithOptions(sql, Options{StandardConformingStrings: true})
		}},
		{"ParseRoutineDeclarationBody", ParseRoutineDeclarationBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := tc.parse(source)
			if err != nil {
				t.Fatalf("released array subquery rejected: %v", err)
			}
			check(t, statements)
		})
	}
	for _, source := range []string{
		"SELECT ARRAY(SELECT 1", "SELECT ARRAY(SELECT 1); unexpected", "SELECT ARRAY(SELECT 1)\x00", "SELECT ARRAY(SELECT 1)\xff",
	} {
		if statements, err := Parse(source); err == nil || len(statements) != 0 {
			t.Fatal("array bypassed complete input validation", source, statements, err)
		}
	}
	if statements, err := parseWithOptions("SELECT ARRAY(SELECT $2)", Options{StandardConformingStrings: true, ClosedRoutineFormals: true, FormalCount: 1}, false); err == nil || len(statements) != 0 {
		t.Fatal("array bypassed closed formal bounds", statements, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if statements, err := parseWithOptions(source, Options{Context: ctx, StandardConformingStrings: true}, false); !errors.Is(err, context.Canceled) || len(statements) != 0 {
		t.Fatal("array bypassed cancellation", statements, err)
	}
}
