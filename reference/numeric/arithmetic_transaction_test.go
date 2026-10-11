package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(99,99)", 1))
	for _, tc := range []struct{ q, code, primary string }{{"INSERT INTO t VALUES(1,1),(7/0,1)", "22012", "division by zero"}, {"INSERT INTO t VALUES(1,1),(2147483647+1,1)", "22003", "integer out of range"}, {"INSERT INTO t VALUES(1,1),($1+7,1)", "22P02", `invalid input syntax for type integer: "oops"`}} {
		p.Step(t, fixture.Exec("BEGIN", 0))
		var args []any
		if tc.code == "22P02" {
			args = []any{"oops"}
		}
		failed := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.primary}, args...)
		failed.ErrorStage = "execute"
		if tc.code == "22P02" {
			pg := *failed.Diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			failed.Diagnostic = &pg
		}
		referenceArithmeticFailure(t, p, failed)
		aborted := fixture.ImplicitError("INSERT INTO t VALUES(2,2)", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.ErrorStage = "execute"
		referenceArithmeticFailure(t, p, aborted)
		p.Step(t, fixture.Exec("ROLLBACK", 0))
		p.Step(t, fixture.Select("SELECT i,b FROM t", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(99), int64(99)}}))
	}
}
