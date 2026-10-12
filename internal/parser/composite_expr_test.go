package parser

import "testing"

func TestCompositeExprParser(t *testing.T) {
	for _, query := range []string{
		`SELECT ROW(),ROW(1),ROW(1,'x'),(1,'x'),(ROW(1,'x')).f1,(ROW(1,'x')).*,($1).i`,
		`SELECT (COALESCE(v,ROW(1,'x')::pair)).*,(v).p.i FROM t`,
		`VALUES ((ROW(1,'x')).*)`,
		`INSERT INTO t(k,v.i,v.t) VALUES(1,2,'x')`,
		`INSERT INTO t SELECT (ROW(1,'x')).*`,
		`UPDATE t SET v.p.i=1,v.arr[2].t='x',arr[1:2].p.i=3 RETURNING (v).*`,
		`MERGE INTO t USING s ON t.k=s.k WHEN MATCHED THEN UPDATE SET v.p.i=s.i`,
		`SELECT i(v),public.i(v),i(value=>v),i(VARIADIC v),i(v) FILTER (WHERE true),i(v) OVER () FROM t`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatalf("%s: %v %#v", query, err, statements)
		}
	}
	statements, err := Parse(`UPDATE t SET v.p[2].i=1`)
	if err != nil {
		t.Fatal(err)
	}
	assignment := statements[0].(*Update).Assignments[0]
	if assignment.Target.Text != "v" || len(assignment.Steps) != 3 || assignment.Steps[0].Field == nil || assignment.Steps[0].Field.Text != "p" || assignment.Steps[1].Subscript == nil || assignment.Steps[2].Field == nil || assignment.Steps[2].Field.Text != "i" {
		t.Fatal("lost ordered mixed destination", assignment)
	}
	statements, err = Parse(`SELECT /*é*/ (ROW(7,'x')).f1,(ROW()).*`)
	if err != nil {
		t.Fatal(err)
	}
	targets := statements[0].(*Select).Targets
	selected, ok := targets[0].(FieldExpr)
	if !ok || selected.Expansion || selected.Field.Text != "f1" || selected.OpenPos.Character != 14 {
		t.Fatal("field syntax provenance", targets[0])
	}
	expanded, ok := targets[1].(FieldExpr)
	if !ok || !expanded.Expansion {
		t.Fatal("zero-width expansion lost", targets[1])
	}
	statements, err = Parse(`SELECT i(v),i(value=>v),i(v) FILTER (WHERE true),i(v) OVER () FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	targets = statements[0].(*Select).Targets
	ordinary := targets[0].(FunctionExpr)
	named := targets[1].(FunctionExpr)
	filtered := targets[2].(FunctionExpr)
	windowed := targets[3].(FunctionExpr)
	if ordinary.Syntax != OrdinaryFunctionSyntax || len(ordinary.Args) != 1 || len(named.ArgNames) != 1 || named.ArgNames[0].Text != "value" || filtered.Filter == nil || windowed.Window == nil {
		t.Fatal("function fallback eligibility syntax lost", targets)
	}
	statements, err = Parse(`SELECT int4('7'),bool('true'),int8(1),now() FILTER (WHERE true),format('%s',missing(1))`)
	if err != nil {
		t.Fatal(err)
	}
	targets = statements[0].(*Select).Targets
	for i, name := range []string{"int4", "bool", "int8"} {
		cast, ok := targets[i].(CastExpr)
		if !ok || cast.Syntax != FunctionCast || cast.Callee != name {
			t.Fatal("lost supported cast identity", targets[i])
		}
	}
	if call, ok := targets[3].(FunctionExpr); !ok || call.Filter == nil || call.CompositeCandidate {
		t.Fatal("lost dedicated time-call admission", targets[3])
	}
	child := targets[4].(FunctionExpr).Args[1].(FunctionExpr)
	if !child.ResolutionOnly || child.CompositeCandidate {
		t.Fatal("lost format failure-only context", child)
	}
}
