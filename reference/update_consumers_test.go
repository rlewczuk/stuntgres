//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateCoreExplicitFailure(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"memory", "postgres"}[i], func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, db)
			record := p.memoryRecord
			if i == 1 {
				record = p.postgresRecord
			}
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, record, s) }
			run(b, referenceExec("CREATE TABLE t(id INT,a INT,n SMALLINT NOT NULL)", 0))
			run(b, referenceExec("INSERT INTO t VALUES(1,1,1),(2,40000,1)", 2))
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, referencePrepared(referenceExec("UPDATE t SET a=7 WHERE id=1", 1)))
			run(tx, referenceImplicitError("UPDATE t SET n=a", referenceDiagnostic{Code: "22003", Message: "smallint out of range"}))
			run(tx, referenceImplicitError("UPDATE t SET a=8", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(b, referenceSelect("SELECT id,a,n FROM t ORDER BY id", []string{"id", "a", "n"}, []string{"INT4", "INT4", "INT2"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(40000), int64(1)}}))
			run(b, referenceExec("UPDATE t SET n=a WHERE id=1", 1))
		})
	}
}

func TestReferenceUpdateCoreAlterConsumers(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,a INT,b TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,NULL,'keep'),(2,2,'other'),(2,2,'other')", 3))
	p.step(t, referenceExec("UPDATE t SET a=3 WHERE id=1", 1))
	p.step(t, referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
	p.step(t, referenceExec("UPDATE t SET a=a", 3))
	p.step(t, referenceExec("ALTER TABLE t DROP COLUMN b", 0))
	step := referenceSelect("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(2)}, {int64(2), int64(2)}})
	step.bag = true
	p.step(t, step)
}
