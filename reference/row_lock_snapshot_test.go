package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRowLockKeyShareSnapshot(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			recorder := backend.recorder
			referenceRunStep(t, p.ctx, a, recorder, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
			referenceRunStep(t, p.ctx, a, recorder, referenceExec("INSERT INTO t VALUES(1,10)", 1))
			reader, err := b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			referenceRunStep(t, p.ctx, reader, recorder, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			referenceRunStep(t, p.ctx, a, recorder, referenceExec("UPDATE t SET v=20 WHERE id=1", 1))
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"SELECT v FROM t WHERE id=1 FOR KEY SHARE", "SELECT v FROM t WHERE v=10 FOR KEY SHARE", "SELECT a.v FROM t a JOIN t b USING(id) WHERE a.id=1 AND b.v=10 FOR KEY SHARE"} {
					step := referenceSelect(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
					if prepared {
						step = referencePrepared(step)
					}
					referenceRunStep(t, p.ctx, reader, recorder, step)
				}
			}
			referenceRunStep(t, p.ctx, a, recorder, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}}))
		})
	}
}

func TestReferenceRowLockKeyShareDeletedReplacement(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					recorder := backend.recorder
					referenceRunStep(t, p.ctx, a, recorder, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					referenceRunStep(t, p.ctx, a, recorder, referenceExec("INSERT INTO t VALUES(1,10)", 1))
					reader, err := b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					referenceRunStep(t, p.ctx, reader, recorder, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					referenceRunStep(t, p.ctx, a, recorder, referenceExec("UPDATE t SET v=20 WHERE id=1", 1))
					referenceRunStep(t, p.ctx, a, recorder, referenceExec("DELETE FROM t WHERE id=1", 1))
					step := referenceImplicitError("SELECT v FROM t WHERE id=1 FOR KEY SHARE", referenceDiagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
					step.selectRows = true
					step.errorStage = "execute"
					if prepared {
						step = referencePrepared(step)
					}
					referenceRunStep(t, p.ctx, reader, recorder, step)
					referenceRunStep(t, p.ctx, reader, recorder, referenceTransactionError(false, "SELECT v FROM t", referenceTransactionAborted()))
				})
			}
		})
	}
}
