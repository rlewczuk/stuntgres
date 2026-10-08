package reference_test

import (
	"testing"
	"time"
)

func TestReferenceUpdateCoreConstantCastPhases(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(n SMALLINT)", 0))
	want := referenceDiagnostic{Code: "22003", Message: "smallint out of range"}
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(1)", 1))
		}
		for _, q := range []string{"UPDATE t SET n=40000", "UPDATE t SET n=40000 WHERE FALSE"} {
			p.step(t, referenceImplicitError(q, want))
			step := referencePrepared(referenceImplicitError(q, want))
			step.errorStage = "execute"
			p.step(t, step)
		}
	}
	p.step(t, referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT2"}, [][]any{{int64(1)}}))
}

func TestReferenceUpdateCoreScalarAssignments(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,a INT,b INT,f BOOL,n NUMERIC(4,1),r DOUBLE PRECISION,s SMALLINT,ts TIMESTAMP(2),tr TIMESTAMP)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,1,2,TRUE,1.0,2.5,1,NULL,TIMESTAMP '2024-01-02 03:04:05.126'),(2,2,2,NULL,2.0,-2.5,1,NULL,TIMESTAMP '2024-01-02 03:04:05.124')", 2))
	p.step(t, referenceExec("UPDATE t SET f=a=b,s=r,n=r,ts=tr", 2))
	p.step(t, referenceSelect("SELECT id,f,s,n,ts FROM t ORDER BY id", []string{"id", "f", "s", "n", "ts"}, []string{"INT4", "BOOL", "INT2", "NUMERIC", "TIMESTAMP"}, [][]any{{int64(1), false, int64(2), "2.5", time.Date(2024, 1, 2, 3, 4, 5, 130000000, time.UTC)}, {int64(2), true, int64(-2), "-2.5", time.Date(2024, 1, 2, 3, 4, 5, 120000000, time.UTC)}}))
	p.step(t, referenceExec("UPDATE t SET f=NOT f WHERE TRUE", 2))
	p.step(t, referenceExec("UPDATE t SET a=num_nulls(a,b) WHERE f", 1))
	p.step(t, referenceExec("UPDATE t SET a=99 WHERE NULL", 0))
	p.step(t, referenceSelect("SELECT id,a,f FROM t ORDER BY id", []string{"id", "a", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(0), true}, {int64(2), int64(2), false}}))
}
