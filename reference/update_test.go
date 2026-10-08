package reference_test

import "testing"

func TestReferenceUpdateCoreRegression(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT,n SMALLINT NOT NULL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,10,20,1),(2,30,40,1),(2,30,40,1)", 3))
	p.step(t, referenceExec("UPDATE t SET a=b,b=a", 3))
	s := referenceSelect("SELECT id,a,b,n FROM t", []string{"id", "a", "b", "n"}, []string{"INT4", "INT4", "INT4", "INT2"}, [][]any{{int64(1), int64(20), int64(10), int64(1)}, {int64(2), int64(40), int64(30), int64(1)}, {int64(2), int64(40), int64(30), int64(1)}})
	s.bag = true
	p.step(t, s)
	p.step(t, referenceExec("UPDATE t SET a=a", 3))
	p.step(t, referenceExec("UPDATE t SET a=7 WHERE id=99", 0))
	for _, backend := range p.backends() {
		updateCoreExec(t, p.ctx, backend.db, "UPDATE t SET a=$1,b=a WHERE id=$2", true, 1, int64(7), int64(1))
	}
	for _, backend := range p.backends() {
		updateCoreExec(t, p.ctx, backend.db, "UPDATE t SET a=$1,b=a WHERE id=$2", true, 2, int64(40000), int64(2))
	}
	s.rows = [][]any{{int64(1), int64(7), int64(20), int64(1)}, {int64(2), int64(40000), int64(40), int64(1)}, {int64(2), int64(40000), int64(40), int64(1)}}
	p.step(t, s)
	p.step(t, referenceImplicitError("UPDATE t SET n=a", referenceDiagnostic{Code: "22003", Message: "smallint out of range"}))
	p.step(t, s)
	p.step(t, referenceExec("UPDATE t SET n=a WHERE FALSE", 0))
	p.step(t, referenceImplicitError("UPDATE t SET n=NULL WHERE id=2", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (2, 40000, 40, null).", Schema: "public", Table: "t", Column: "n"}))
	p.step(t, s)
}
