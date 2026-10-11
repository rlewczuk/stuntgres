package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityAuthorizationAndPhase(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
			run(fixture.Exec("CREATE USER reader", 0))
			if b.PG {
				// Fixture recreation removes public's default USAGE; reach owner-only ALTER.
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO reader", 0))
			}
			for _, q := range []string{"ALTER TABLE absent ALTER missing SET NOT NULL, DROP COLUMN b", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing, ALTER absent SET NOT NULL"} {
				stmt, err := conn.PrepareContext(p.Ctx, q)
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
			run(fixture.Exec("SET SESSION AUTHORIZATION reader", 0))
			s := fixture.ImplicitError("ALTER TABLE t ALTER a DROP NOT NULL, DROP IF EXISTS missing", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"})
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			q := "ALTER TABLE absent ALTER missing SET NOT NULL, DROP b"
			stmt, err := tx.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("effect-free read-only Prepare", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
			s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, recorder, s)
			s = fixture.ImplicitError("ALTER TABLE t ALTER a DROP NOT NULL", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, tx, recorder, s)
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
