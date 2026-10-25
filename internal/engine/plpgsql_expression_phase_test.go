package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLExceptionExpressionProvenance(t *testing.T) {
	e, s := plExceptionUnitScope(t)
	integer, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing integer")
	}
	zero, err := sqltype.Integer(integer, 0)
	if err != nil {
		t.Fatal(err)
	}
	boolean, ok := e.CatalogRead().LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing boolean")
	}
	program := &proc.PLProgram{FoundSlot: 0, Variables: []proc.PLVariable{
		{Name: "found", Usage: boolean.Usage(), Role: proc.PLFoundSlot},
		{Name: "x", Usage: integer.Usage()},
	}}
	frame := proc.NewPLFrame(program, &proc.Definition{}, &proc.Frame{}, sqltype.CollationRef{})
	defer frame.Close()
	frame.Values[1] = zero
	frame.Enter(&proc.PLBlock{Bindings: map[string]int{"x": 1}})
	for _, tc := range []struct {
		expression string
		planning   bool
	}{
		{"12/x", false},
		{"1/0", true},
		{"(1+2)/(2-2)", true},
		{"12/(x+0)", false},
		{"(12/x)::text", false},
		{"(12/(x+0))::text", false},
		{"(1/0)::text", true},
		{"12/x", false},
	} {
		syntax, err := parser.ParsePLpgSQL("BEGIN RETURN "+tc.expression+"; END", parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		raw := syntax.Statements[0].Expression
		query := &parser.Select{Targets: []parser.Expr{raw}, Pos: raw.SourcePosition()}
		_, _, _, err = ScopeEvaluatePLExpression(s, frame, query, e.CatalogRead(), nil, NewPLNoticeOrigin(PLNoticeOriginFields{}))
		var diagnostic *sqlstate.Error
		var planning *proc.PLExpressionPlanningError
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
			t.Fatalf("expression %s: %v", tc.expression, err)
		}
		if errors.As(err, &planning) != tc.planning {
			t.Fatalf("expression %s planning provenance got %t want %t", tc.expression, planning != nil, tc.planning)
		}
		if RoutineDemandLexical(ScopeSQLRoutineRef(s)) != nil || RoutineDemandPLPlanning(ScopeSQLRoutineRef(s)) {
			t.Fatal("expression provenance escaped lexical activation")
		}
	}
}
