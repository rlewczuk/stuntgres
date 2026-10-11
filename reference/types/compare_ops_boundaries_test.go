package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompareOpsTemporalOperatorBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(a SMALLINT,z BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,m NUMERIC,u UUID,v UUID,c TIME,d TIME,x TIMESTAMP,y TIMESTAMP)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(-32768,2147483648,'NaN','Infinity',9007199254740993,9007199254740992,'7fffffffffffffffffffffffffffffff','80000000000000000000000000000000','01:00','02:00','2000-01-01','2000-01-02')", 1))
	// Original SQL/operator location, not the capture's renamed tm column/pos10.
	step := fixture.ImplicitError("SELECT c<x FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: time without time zone < timestamp without time zone", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	step.SelectRows = true
	step.ErrorStage = "execute"
	fixture.ScalarStep(t, p, step)
	step.ErrorStage = "prepare"
	fixture.ScalarStep(t, p, fixture.Prepared(step))
}
