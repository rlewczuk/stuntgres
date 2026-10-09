package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterNullabilityAuthorizationAndPhase(t *testing.T) {
	roleReferenceCleanup(t, []string{"reader"})
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, recorder := noticeReferencePool(t, p, b.pg, &notices)
			defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
			run(referenceExec("CREATE USER reader", 0))
			if b.pg {
				// Fixture recreation removes public's default USAGE; reach owner-only ALTER.
				run(referenceExec("GRANT USAGE ON SCHEMA public TO reader", 0))
			}
			for _, q := range []string{"ALTER TABLE absent ALTER missing SET NOT NULL, DROP COLUMN b", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing, ALTER absent SET NOT NULL"} {
				stmt, err := conn.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal("effect-free metadata prepare", err)
				}
				if err = stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if len(notices) != 0 {
				t.Fatalf("effect-free Prepare emitted notices: %#v", notices)
			}
			run(referenceExec("SET SESSION AUTHORIZATION reader", 0))
			s := referenceImplicitError("ALTER TABLE t ALTER a DROP NOT NULL, DROP IF EXISTS missing", referenceDiagnostic{Code: "42501", Message: "must be owner of table t"})
			s.errorStage = "execute"
			run(s)
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			tx, err := conn.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			q := "ALTER TABLE absent ALTER missing SET NOT NULL, DROP b"
			stmt, err := tx.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal("effect-free read-only Prepare", err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: tx, statement: stmt, query: q}
			s = referenceImplicitError(q, referenceDiagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"})
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, recorder, s)
			s = referenceImplicitError("ALTER TABLE t ALTER a DROP NOT NULL", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, tx, recorder, s)
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
