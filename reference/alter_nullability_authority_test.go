package reference_test

import (
	"testing"
)

func TestReferenceAlterNullabilityAuthorizationLifetime(t *testing.T) {
	roleReferenceCleanup(t, []string{"nn_nonowner"})
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			b.db.SetMaxOpenConns(2)
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if b.pg {
				defer conn.ExecContext(p.ctx, "RESET ROLE")
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE ROLE nn_nonowner", 0))
			run(referenceExec("SET ROLE nn_nonowner", 0))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7)", 1))
			tx, err := conn.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			if !b.pg {
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE ROLE nn_revision", 0))
			} else {
				referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SET ROLE nn_nonowner", 0))
			}
			if err = tx.Commit(); err != nil {
				t.Fatal("later role change invalidated authorized ALTER", err)
			}
			if b.pg {
				run(referenceExec("RESET ROLE", 0))
			}
			referenceTableDDLMetadata(t, p.ctx, conn, b.pg, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceImplicitError("INSERT INTO t VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}))
		})
	}
}
