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
	candidate, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, raw)
	if !ok || !slices.Equal(candidate.Inputs, []sqltype.Usage{types[sqltype.Int4].Usage(), types[sqltype.Int8].Usage()}) || !slices.Equal(candidate.InputMap, []plan.RoutineInput{{Start: 1, Count: 1}, {Start: -1}, {Start: 0, Count: 1}}) {
		t.Fatal("written/default-gap projection", candidate, ok)
	}
	callSyntax := raw
	callSyntax.ProcedureCall = true
	if _, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, callSyntax); ok {
		t.Fatal("CALL reused SELECT's input-only projection and omitted required OUT")
	}
	candidate.InputMap[0].Start = 99
	again, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, raw)
	if !ok || again.InputMap[0].Start != 1 || raw.ArgNames[0].Text != "c" || d.SQL.Names[0] != "a" {
		t.Fatal("candidate mutation escaped", again)
	}
	for _, labels := range [][]parser.Identifier{{{Text: "out"}, {Text: "a"}}, {{}, {Text: "a"}}, {{Text: "unknown"}, {Text: "a"}}} {
		raw.ArgNames = labels
		if _, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, raw); ok {
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
	if _, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{}); ok {
		t.Fatal("zero expanded tail accepted without default")
	}
	expanded, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{Args: []parser.Expr{nil, nil}})
	if !ok || !expanded.Variadic || expanded.InputMap[0] != (plan.RoutineInput{Start: 0, Count: 2, Expanded: true}) {
		t.Fatal("expanded map", expanded)
	}
	if _, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{Args: []parser.Expr{nil}, ArgNames: []parser.Identifier{{Text: "xs"}}}); ok {
		t.Fatal("named expanded variadic accepted")
	}
	explicit, ok := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{Args: []parser.Expr{nil}, ArgNames: []parser.Identifier{{Text: "xs"}}, Variadic: true})
	if !ok || explicit.Inputs[0] != array.Usage() || explicit.InputMap[0].Expanded {
		t.Fatal("explicit array map", explicit)
	}
}

func TestSQLCallableProcedureProjection(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	for _, kind := range []catalog.RoutineKind{catalog.FunctionRoutine, catalog.ProcedureRoutine} {
		d := &catalog.Procedure{RoutineKind: kind, SQL: &proc.Definition{Procedure: kind == catalog.ProcedureRoutine}}
		if err := d.SQL.SetFormals([]proc.Formal{
			{Mode: "o", Name: "answer", Usage: types[sqltype.Int4].Usage()},
			{Mode: "i", Name: "x", Usage: types[sqltype.Int4].Usage()},
			{Mode: "b", Name: "total", Usage: types[sqltype.Int8].Usage(), HasDefault: true},
		}); err != nil {
			t.Fatal(err)
		}
		raw := parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 2), ArgNames: []parser.Identifier{{Text: "x"}, {Text: "answer"}}}
		candidate, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, raw)
		if !valid || !slices.Equal(candidate.Inputs, []sqltype.Usage{types[sqltype.Int4].Usage(), types[sqltype.Int4].Usage()}) || !slices.Equal(candidate.InputMap, []plan.RoutineInput{{Start: 0, Count: 1}, {Start: -1}}) {
			t.Fatal("all-kind CALL projection", kind, candidate, valid)
		}
		candidate.InputMap[0].Start = 99
		next, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, raw)
		if !valid || next.InputMap[0].Start != 0 || d.SQL.Formals[0].Name != "answer" || raw.ArgNames[0].Text != "x" {
			t.Fatal("candidate map mutated source")
		}
		if _, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 1), ArgNames: []parser.Identifier{{Text: "x"}}}); valid {
			t.Fatal("CALL omitted mandatory OUT")
		}
		if _, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{Args: make([]parser.Expr, 1)}); !valid {
			t.Fatal("SELECT no longer projects input-only positions")
		}
	}
	array, err := sqltype.Array(1007, "_int4", types[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{SQL: &proc.Definition{Procedure: true}}
	if err := d.SQL.SetFormals([]proc.Formal{{Mode: "o", Name: "answer", Usage: array.Usage()}, {Mode: "v", Name: "xs", Usage: array.Usage()}}); err != nil {
		t.Fatal(err)
	}
	expanded, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 3)})
	if !valid || expanded.InputMap[0] != (plan.RoutineInput{Start: 1, Count: 2, Expanded: true}) || expanded.Inputs[0] != array.Usage() || expanded.Inputs[2] != types[sqltype.Int4].Usage() {
		t.Fatal("CALL variadic projection", expanded, valid)
	}
	explicit, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 2), Variadic: true, ArgNames: []parser.Identifier{{Text: "answer"}, {Text: "xs"}}})
	if !valid || explicit.InputMap[0] != (plan.RoutineInput{Start: 1, Count: 1}) || explicit.Inputs[1] != array.Usage() {
		t.Fatal("CALL explicit array projection", explicit, valid)
	}
	if err := d.SQL.SetFormals([]proc.Formal{{Mode: "b", Name: "xs", Usage: array.Usage()}}); err != nil {
		t.Fatal(err)
	}
	nonvariadic, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 1), Variadic: true})
	if !valid || nonvariadic.Variadic || nonvariadic.Inputs[0] != array.Usage() {
		t.Fatal("historically accepted nonvariadic decoration rejected", nonvariadic, valid)
	}
}

func TestSQLCallableProcedureFunctionVariadicProjection(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	array, err := sqltype.Array(1007, "_int4", integer)
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{RoutineKind: catalog.FunctionRoutine, SQL: &proc.Definition{}}
	if err := d.SQL.SetFormals([]proc.Formal{{Mode: "v", Name: "xs", Usage: array.Usage()}, {Mode: "o", Name: "answer", Usage: integer.Usage()}}); err != nil {
		t.Fatal(err)
	}
	candidate, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 3)})
	if !valid || !candidate.Variadic || !slices.Equal(candidate.Inputs, []sqltype.Usage{array.Usage(), integer.Usage(), integer.Usage()}) {
		t.Fatal("CALL did not expand final full formal using declared variadic element", candidate, valid)
	}
	explicit, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 2), Variadic: true})
	if !valid || explicit.Variadic || !slices.Equal(explicit.Inputs, []sqltype.Usage{array.Usage(), integer.Usage()}) {
		t.Fatal("explicit CALL did not retain full declaration", explicit, valid)
	}
	if _, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 2), ArgNames: []parser.Identifier{{Text: "xs"}, {Text: "answer"}}}); valid {
		t.Fatal("named expansion admitted function variadic")
	}
	function, valid := sqlRoutineParameterShape(catalog.Read{}, nil, d, parser.FunctionExpr{Args: make([]parser.Expr, 2)})
	if !valid || !slices.Equal(function.Inputs, []sqltype.Usage{integer.Usage(), integer.Usage()}) || function.InputMap[0] != (plan.RoutineInput{Start: 0, Count: 2, Expanded: true}) {
		t.Fatal("function input-only expansion changed")
	}
}
