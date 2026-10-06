//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceInsertSelectTransactionFailure(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE t(i INT)", 0), referenceExec("INSERT INTO t VALUES(9)", 1), referenceExec("CREATE TABLE s(i BIGINT)", 0), referenceExec("INSERT INTO s VALUES(1),(2147483648)", 2)} {
		p.step(t, s)
	}
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		tx, err := backend.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, backend.record, s) }
			run(referenceExec("INSERT INTO t VALUES(7)", 1))
			failure := referencePrepared(referenceImplicitError("INSERT INTO t SELECT i FROM s ORDER BY i", referenceDiagnostic{Code: "22003", Message: "integer out of range"}))
			failure.errorStage = "execute"
			run(failure)
			failedBlock := referenceImplicitError("SELECT i FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			failedBlock.selectRows = true
			failedBlock.errorStage = "execute"
			run(failedBlock)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
		}()
	}
}
