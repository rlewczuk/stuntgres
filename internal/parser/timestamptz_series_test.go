package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTimestamptzSeriesParser(t *testing.T) {
	const args = "TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day',$1::text"
	for _, tc := range []struct {
		call                     string
		arity                    int
		distinct, filter, window bool
	}{
		{"generate_series(" + args + ")", 4, false, false, false},
		{"pg_catalog.generate_series(" + args + ")", 4, false, false, false},
		{`"generate_series"(ALL ` + args + ")", 4, false, false, false},
		{"generate_series(DISTINCT " + args + ")", 4, true, false, false},
		{"generate_series(" + args + ") FILTER(WHERE true)", 4, false, true, false},
		{"generate_series(" + args + ") OVER()", 4, false, false, true},
		{"generate_series($1::timestamptz,$2::timestamptz,$3::interval)", 3, false, false, false},
	} {
		q := "/*é🙂*/ SELECT " + tc.call
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || len(call.Args) != tc.arity || call.Name.Parts[len(call.Name.Parts)-1].Text != "generate_series" || call.Distinct != tc.distinct || (call.Filter != nil) != tc.filter || (call.Window != nil) != tc.window || call.Syntax != OrdinaryFunctionSyntax {
			t.Fatal("call shape/decorations", call)
		}
		offset := strings.Index(q, tc.call)
		if call.Pos.Offset != offset || call.Pos.Character != utf8.RuneCountInString(q[:offset])+1 {
			t.Fatal("UTF8 call position", call.Pos)
		}
	}
	for _, q := range []string{
		"SELECT * FROM generate_series(" + args + ") WITH ORDINALITY AS s(v,n)",
		"SELECT * FROM LATERAL ROWS FROM(pg_catalog.generate_series(" + args + ")) WITH ORDINALITY AS s(v,n)",
	} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		alias := statements[0].(*Select).From.(*AliasReference)
		source := alias.Input.(*FunctionTable)
		if !source.Ordinality || len(source.Calls) != 1 || len(source.Calls[0].Args) != 4 || len(alias.Columns) != 2 {
			t.Fatal("source shape", source, alias)
		}
		if strings.Contains(q, "LATERAL") && (!source.Lateral || !source.RowsFrom) {
			t.Fatal("lost lateral/rowsfrom", source)
		}
	}
	q := "SELECT s.id,v FROM (VALUES(1),(2)) s(id) CROSS JOIN LATERAL generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',s.id*INTERVAL '1 day','America/New_York') g(v)"
	if statements, err := Parse(q); err != nil || len(statements) != 1 {
		t.Fatal("correlated four argument source", statements, err)
	}
}
