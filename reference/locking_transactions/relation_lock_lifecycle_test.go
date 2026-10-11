package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationLockLifecycleAndDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			pair := fixture.NewScenario(t)
			for _, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := pair.Ctx
					a, b := fixture.LockConnections(t, ctx, backend.DB)
					recorder := backend.Recorder
					fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT NOT NULL)", 0)
					referenceRelationRowsCloseHold(t, pair, backend, a, b)
					// Utility preparation succeeds even for absent targets/outside a block.
					stmt, err := a.PrepareContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
					if err != nil {
						t.Fatal("LOCK preparation unexpectedly executed", err)
					}
					_, err = stmt.ExecContext(ctx)
					stmt.Close()
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P01", Message: "LOCK TABLE can only be used in transaction blocks"}
					if got := fixture.ExtractDiagnostic(t, err); got != want {
						t.Fatal(got, want)
					}
					fixture.RunStep(t, ctx, a, recorder, fixture.TransactionError(prepared, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", want))
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "absent" does not exist`}
					fixture.RunStep(t, ctx, tx, recorder, fixture.TransactionError(prepared, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", want))
					fixture.RunStep(t, ctx, tx, recorder, fixture.TransactionError(prepared, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", fixture.TransactionAborted()))
					syntax := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"}
					fixture.RunStep(t, ctx, tx, recorder, fixture.TransactionError(prepared, "SELECT * FROM t WHERE v = )", syntax))
					tx.Rollback()
					// Statement and actual Prepare failures release earlier AX before client end.
					for _, failure := range []string{"constraint", "prepare"} {
						owner, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						fixture.RunStep(t, ctx, owner, recorder, fixture.Step{Query: "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", Prepared: prepared, Affected: 0})
						fixture.LockExec(t, ctx, owner, "INSERT INTO t VALUES(1)", 1)
						if failure == "constraint" {
							wantNull := fixture.TransactionNull()
							fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, ctx, owner, "INSERT INTO t VALUES(NULL)", prepared), &wantNull)
						} else {
							stmt, err := owner.PrepareContext(ctx, "SELECT * FROM t WHERE v = )")
							if stmt != nil {
								stmt.Close()
								t.Fatal("failed Prepare exposed handle")
							}
							if got := fixture.ExtractDiagnostic(t, err); got != syntax {
								t.Fatal(got)
							}
						}
						rows, err := b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal("abort did not release AX", err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG)
						fixture.RunStep(t, ctx, owner, recorder, fixture.TransactionError(false, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", fixture.TransactionAborted()))
						if err = owner.Commit(); err == nil {
							t.Fatal("failed transaction committed")
						}
						rows, err = b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG)
					}
				})
			}
		})
	}
}
func TestReferenceRelationLockOrdinaryAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"lock_core_reader"})
	pair := fixture.NewScenario(t)
	pair.Step(t, fixture.Exec("CREATE USER lock_core_reader", 0))
	pair.Step(t, fixture.Exec("CREATE TABLE t(v INT)", 0))
	// The fixture recreates public without PUBLIC's schema USAGE privilege.
	// Make the reader's lookup authority explicit before testing its table rights.
	if pair.Postgres != nil {
		if _, err := fixture.Admin.ExecContext(pair.Ctx, "GRANT USAGE ON SCHEMA public TO lock_core_reader"); err != nil {
			t.Fatal(err)
		}
	}
	pair.Step(t, fixture.Exec("GRANT SELECT, INSERT ON t TO lock_core_reader", 0))
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := pair.Ctx
			a, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			fixture.LockExec(t, ctx, a, "SET SESSION AUTHORIZATION lock_core_reader", 0)
			defer a.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			rows, err := a.QueryContext(ctx, "SELECT v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			fixture.TransactionValues(t, rows)
			fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
			for _, prepared := range []bool{false, true} {
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				stmt := (*sql.Stmt)(nil)
				if prepared {
					stmt, err = tx.PrepareContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					if err != nil {
						tx.Rollback()
						t.Fatal("utility preparation performed authority", err)
					}
					_, err = stmt.ExecContext(ctx)
					stmt.Close()
				} else {
					_, err = tx.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				}
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
				if got := fixture.ExtractDiagnostic(t, err); got != want {
					tx.Rollback()
					t.Fatal(got, want)
				}
				tx.Rollback()
			}
		})
	}
}
