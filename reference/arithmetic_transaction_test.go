package reference_test

import "testing"

func TestReferenceArithmeticTransactions(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(99,99)", 1))
	for _, tc := range []struct{ q, code, primary string }{{"INSERT INTO t VALUES(1,1),(7/0,1)", "22012", "division by zero"}, {"INSERT INTO t VALUES(1,1),(2147483647+1,1)", "22003", "integer out of range"}, {"INSERT INTO t VALUES(1,1),($1+7,1)", "22P02", `invalid input syntax for type integer: "oops"`}} {
		p.step(t, referenceExec("BEGIN", 0))
		var args []any
		if tc.code == "22P02" {
			args = []any{"oops"}
		}
		failed := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.primary}, args...)
		failed.errorStage = "execute"
		if tc.code == "22P02" {
			pg := *failed.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			failed.diagnostic = &pg
		}
		referenceArithmeticFailure(t, p, failed)
		aborted := referenceImplicitError("INSERT INTO t VALUES(2,2)", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.errorStage = "execute"
		referenceArithmeticFailure(t, p, aborted)
		p.step(t, referenceExec("ROLLBACK", 0))
		p.step(t, referenceSelect("SELECT i,b FROM t", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(99), int64(99)}}))
	}
}
