//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterNullabilityAuthorizationLifetime(t *testing.T) {
	roleReferenceCleanup(t, []string{"nn_nonowner"})
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
		native bool
	}{{"postgres", p.postgres, p.postgresRecord, false}, {"native", p.memory, p.memoryRecord, true}} {
		t.Run(b.name, func(t *testing.T) {
			b.db.SetMaxOpenConns(2)
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if !b.native {
				defer conn.ExecContext(p.ctx, "RESET ROLE")
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.record, s) }
			run(referenceExec("CREATE ROLE nn_nonowner", 0))
			if b.native {
				run(referenceImplicitError("SET ROLE nn_nonowner", referenceDiagnostic{Code: "0A000", Message: "SET ROLE is unsupported", Position: "5"}))
			}
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7)", 1))
			tx, err := conn.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.record, referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			if b.native {
				referenceRunStep(t, p.ctx, b.db, b.record, referenceExec("CREATE ROLE nn_revision", 0))
			} else {
				referenceRunStep(t, p.ctx, tx, b.record, referenceExec("SET ROLE nn_nonowner", 0))
			}
			if err = tx.Commit(); err != nil {
				t.Fatal("later role change invalidated authorized ALTER", err)
			}
			if !b.native {
				run(referenceExec("RESET ROLE", 0))
			}
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceImplicitError("INSERT INTO t VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}))
		})
	}
}
