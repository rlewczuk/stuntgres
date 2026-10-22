package analyze

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLCallableParameterMapping(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	d := &catalog.Procedure{SQL: &proc.Definition{}}
	if err := d.SQL.SetFormals([]proc.Formal{
		{Mode: "o", Name: "out", Usage: types[sqltype.Int4].Usage()},
		{Mode: "i", Name: "a", Usage: types[sqltype.Int8].Usage()},
		{Mode: "i", Name: "b", Usage: types[sqltype.Text].Usage(), HasDefault: true},
		{Mode: "i", Name: "c", Usage: types[sqltype.Int4].Usage(), HasDefault: true},
	}); err != nil {
		t.Fatal(err)
	}
	raw := parser.FunctionExpr{Args: []parser.Expr{nil, nil}, ArgNames: []parser.Identifier{{Text: "c"}, {Text: "a"}}}
	candidate, ok := sqlRoutineParameterShape(d, raw)
	if !ok || !slices.Equal(candidate.Inputs, []sqltype.Usage{types[sqltype.Int4].Usage(), types[sqltype.Int8].Usage()}) || !slices.Equal(candidate.InputMap, []plan.RoutineInput{{Start: 1, Count: 1}, {Start: -1}, {Start: 0, Count: 1}}) {
		t.Fatal("written/default-gap projection", candidate, ok)
	}
	candidate.InputMap[0].Start = 99
	again, ok := sqlRoutineParameterShape(d, raw)
	if !ok || again.InputMap[0].Start != 1 || raw.ArgNames[0].Text != "c" || d.SQL.Names[0] != "a" {
		t.Fatal("candidate mutation escaped", again)
	}
	for _, labels := range [][]parser.Identifier{{{Text: "out"}, {Text: "a"}}, {{}, {Text: "a"}}, {{Text: "unknown"}, {Text: "a"}}} {
		raw.ArgNames = labels
		if _, ok := sqlRoutineParameterShape(d, raw); ok {
			t.Fatal("candidate label collision accepted", labels)
		}
	}
	array, err := sqltype.Array(1007, "_int4", types[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SQL.SetFormals([]proc.Formal{{Mode: "v", Name: "xs", Usage: array.Usage()}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := sqlRoutineParameterShape(d, parser.FunctionExpr{}); ok {
		t.Fatal("zero expanded tail accepted without default")
	}
	expanded, ok := sqlRoutineParameterShape(d, parser.FunctionExpr{Args: []parser.Expr{nil, nil}})
	if !ok || !expanded.Variadic || expanded.InputMap[0] != (plan.RoutineInput{Start: 0, Count: 2, Expanded: true}) {
		t.Fatal("expanded map", expanded)
	}
	if _, ok := sqlRoutineParameterShape(d, parser.FunctionExpr{Args: []parser.Expr{nil}, ArgNames: []parser.Identifier{{Text: "xs"}}}); ok {
		t.Fatal("named expanded variadic accepted")
	}
	explicit, ok := sqlRoutineParameterShape(d, parser.FunctionExpr{Args: []parser.Expr{nil}, ArgNames: []parser.Identifier{{Text: "xs"}}, Variadic: true})
	if !ok || explicit.Inputs[0] != array.Usage() || explicit.InputMap[0].Expanded {
		t.Fatal("explicit array map", explicit)
	}
}
