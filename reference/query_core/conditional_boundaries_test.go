package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConditionalAggregateBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	q := "SELECT COALESCE(count(*),nextval('s'))"
	for _, backend := range p.Backends() {
		d := fixture.Diagnostic{Code: "42P01", Message: `relation "s" does not exist`, Position: "34"}
		step := fixture.ImplicitError(q, d)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		step.ErrorStage = "prepare"
		fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
	}
}

func TestReferenceConditionalQualifiedConsumerBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT,v INT,b BOOL)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(2,20,FALSE),(3,30,NULL)", 2))
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("UPDATE t SET v=COALESCE(t.v,1)", 2)
			if prepared {
				step = fixture.Prepared(step)
			}
			fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}, {int64(30)}}))
		}
		for _, q := range []string{"DELETE FROM t WHERE CAST(COALESCE(t.v,1) AS int4)=1"} {
			for _, prepared := range []bool{false, true} {
				step := fixture.Exec(q, 0)
				step.Prepared = prepared
				fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			}
			fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}, {int64(30)}}))
		}
	}
}
