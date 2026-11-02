package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsFingerprintCall(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE PROCEDURE pgss_call_owned(IN x integer,OUT answer integer) LANGUAGE SQL AS 'SELECT x+1'")
	bound := func(t *testing.T, source string) analyze.StatementJumble {
		t.Helper()
		ast, binding := pgssAnalyzedBinding(t, e, source)
		call, ok := binding.Expressions[0][0].(plan.SQLRoutineExpr)
		if !ok || len(call.Args) != 1 || call.WrittenCount != 1 {
			t.Fatal("pure OUT acquired an executable/fold child", binding.Expressions)
		}
		jumble, _ := analyze.JumbleUtilityBound(ast, binding)
		return jumble
	}
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"CALL pgss_call_owned(1,NULL)", "CALL pgss_call_owned(2,99)", true},
		{"CALL pgss_call_owned(1,NULL)", "CALL public.pgss_call_owned(2,NULL)", true},
		{"CALL pgss_call_owned(1,NULL)", "CALL pgss_call_owned(2,abs(3))", false},
		{"CALL pgss_call_owned(1,abs(3))", "CALL pgss_call_owned(2,abs(4))", true},
		{"CALL pgss_call_owned(1,abs(3))", "CALL pgss_call_owned(2,1/0)", false},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, right := bound(t, tc.left), bound(t, tc.right)
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected CALL relationship: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
	source := "CALL pgss_call_owned(1,abs(-2))"
	jumble := bound(t, source)
	normalized, _ := parser.NormalizeStatementText(source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
	if normalized != "CALL pgss_call_owned($1,abs($2))" {
		t.Fatal("lost OUT source normalization", normalized)
	}
	before := bound(t, "CALL pgss_call_owned(1,NULL)")
	enginetest.Run(t, e, "ALTER PROCEDURE pgss_call_owned(integer) RENAME TO pgss_call_renamed")
	after := bound(t, "CALL pgss_call_renamed(2,NULL)")
	if before.QueryID() != after.QueryID() {
		t.Fatal("raw procedure spelling replaced authentic selected identity")
	}
}
