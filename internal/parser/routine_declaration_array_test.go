package parser

import (
	"context"
	"errors"
	"testing"
)

func TestRoutineDeclarationArraySubqueryBoundary(t *testing.T) {
	const source = "SELECT ARRAY(SELECT m.mapdict::regdictionary FROM pg_ts_config_map AS m WHERE m.mapcfg=$1 ORDER BY m.mapseqno) AS dictionaries"
	for _, parse := range []func(string) ([]Statement, error){Parse, func(sql string) ([]Statement, error) {
		return ParseWithOptions(sql, Options{StandardConformingStrings: true})
	}} {
		if statements, err := parse(source); err == nil || len(statements) != 0 {
			t.Fatal("ordinary SQL acquired declaration-only syntax", statements, err)
		}
	}
	statements, err := ParseRoutineDeclarationBody(source)
	if err != nil || len(statements) != 1 {
		t.Fatal("released array subquery rejected", statements, err)
	}
	query, ok := statements[0].(*Select)
	if !ok || len(query.Targets) != 1 {
		t.Fatal("lost outer SELECT", statements)
	}
	array, ok := query.Targets[0].(DeclarationArraySubqueryExpr)
	if !ok || array.Query == nil || len(array.Query.Targets) != 1 || array.Query.From == nil || array.Query.Where == nil || array.Query.Order == nil {
		t.Fatal("lost genuine array query structure", query.Targets[0])
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
	if err := VisitQuerySources(statements[0], func(q *Select) error { queries++; return nil }, nil); err != nil || queries != 2 {
		t.Fatal("array query omitted by readonly syntax traversal", queries, err)
	}
	if again, err := Parse(source); err == nil || len(again) != 0 {
		t.Fatal("readonly mode escaped its invocation", again, err)
	}
	for _, source := range []string{
		"SELECT ARRAY(SELECT 1", "SELECT ARRAY(SELECT 1); unexpected", "SELECT ARRAY(SELECT 1)\x00", "SELECT ARRAY(SELECT 1)\xff",
	} {
		if statements, err := ParseRoutineDeclarationBody(source); err == nil || len(statements) != 0 {
			t.Fatal("readonly array bypassed complete input validation", source, statements, err)
		}
	}
	if statements, err := parseWithOptions("SELECT ARRAY(SELECT $2)", Options{StandardConformingStrings: true, ClosedRoutineFormals: true, FormalCount: 1}, true); err == nil || len(statements) != 0 {
		t.Fatal("array bypassed closed formal bounds", statements, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if statements, err := parseWithOptions(source, Options{Context: ctx, StandardConformingStrings: true}, true); !errors.Is(err, context.Canceled) || len(statements) != 0 {
		t.Fatal("array bypassed cancellation", statements, err)
	}
}
