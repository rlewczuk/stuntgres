package reference_test

import "testing"

func TestReferenceCompareOpsTemporalOperatorBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(a SMALLINT,z BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,m NUMERIC,u UUID,v UUID,c TIME,d TIME,x TIMESTAMP,y TIMESTAMP)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(-32768,2147483648,'NaN','Infinity',9007199254740993,9007199254740992,'7fffffffffffffffffffffffffffffff','80000000000000000000000000000000','01:00','02:00','2000-01-01','2000-01-02')", 1))
	// Original SQL/operator location, not the capture's renamed tm column/pos10.
	step := referenceImplicitError("SELECT c<x FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: time without time zone < timestamp without time zone", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	step.selectRows = true
	step.errorStage = "execute"
	scalarStep(t, p, step)
	step.errorStage = "prepare"
	scalarStep(t, p, referencePrepared(step))
}
