package reference_test

import (
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceConditionalAggregateBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	q := "SELECT COALESCE(count(*),nextval('s'))"
	for _, backend := range p.backends() {
		d := referenceDiagnostic{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"}
		if backend.pg {
			d = referenceDiagnostic{Code: "42P01", Message: `relation "s" does not exist`, Position: "34"}
		}
		step := referenceImplicitError(q, d)
		step.selectRows = true
		step.errorStage = "execute"
		scalarRunStep(t, p.ctx, backend.db, backend.recorder, step)
		step.errorStage = "prepare"
		scalarRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
	}
}

func TestReferenceConditionalQualifiedConsumerBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,v INT,b BOOL)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(2,20,FALSE),(3,30,NULL)", 2))
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			step := referenceExec("UPDATE t SET v=COALESCE(t.v,1)", 2)
			if prepared {
				step = referencePrepared(step)
			}
			scalarRunStep(t, p.ctx, backend.db, backend.recorder, step)
			scalarRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}, {int64(30)}}))
		}
		for _, q := range []string{"DELETE FROM t WHERE CAST(COALESCE(t.v,1) AS int4)=1"} {
			if backend.pg {
				scalarRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			} else {
				_, err := backend.db.ExecContext(p.ctx, q)
				var d *stuntgres.Error
				if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "qualified column references are unsupported" || d.Position == "" {
					t.Fatal(q, err)
				}
			}
			scalarRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}, {int64(30)}}))
		}
	}
}
