package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineDemandComparison(t *testing.T) {
	e := engine.New()
	usage := enginetest.Usage(e, sqltype.Int4)
	first, err := proc.PlanResult([]sqltype.Usage{usage}, usage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proc.PlanResult([]sqltype.Usage{usage}, usage)
	if err != nil {
		t.Fatal(err)
	}
	lexical := &proc.PLFrame{Program: &proc.PLProgram{}}
	original := core.NewRoutineDemand(core.RoutineDemandFields{Lexical: lexical, Result: usage, ResultPlan: first})
	if !core.SameSQLRoutineDemand(original, original) {
		t.Fatal("identical scratch rejected")
	}
	for _, fields := range []core.RoutineDemandFields{
		{Lexical: &proc.PLFrame{Program: &proc.PLProgram{}}, Result: usage, ResultPlan: first},
		{Lexical: lexical, Result: usage, ResultPlan: second},
		{Lexical: lexical, Result: usage, ResultPlan: first, Scalar: true},
	} {
		if core.SameSQLRoutineDemand(original, core.NewRoutineDemand(fields)) {
			t.Fatal("scratch comparison followed pointees or ignored row-plan identity")
		}
	}
}
