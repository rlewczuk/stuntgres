package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceRelationLockLifecycleAndDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			pair := newReferenceScenario(t)
			for _, backend := range pair.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx := pair.ctx
					a, b := referenceLockConnections(t, ctx, backend.db)
					recorder := backend.recorder
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT NOT NULL)", 0)
					referenceRelationRowsCloseHold(t, pair, backend, a, b)
					// Utility preparation succeeds even for absent targets/outside a block.
					stmt, err := a.PrepareContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
					if err != nil {
						t.Fatal("LOCK preparation unexpectedly executed", err)
					}
					_, err = stmt.ExecContext(ctx)
					stmt.Close()
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P01", Message: "LOCK TABLE can only be used in transaction blocks"}
					if got := referenceExtractDiagnostic(t, err); got != want {
						t.Fatal(got, want)
					}
					referenceRunStep(t, ctx, a, recorder, referenceTransactionError(prepared, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", want))
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "absent" does not exist`}
					referenceRunStep(t, ctx, tx, recorder, referenceTransactionError(prepared, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", want))
					referenceRunStep(t, ctx, tx, recorder, referenceTransactionError(prepared, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", referenceTransactionAborted()))
					syntax := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"}
					referenceRunStep(t, ctx, tx, recorder, referenceTransactionError(prepared, "SELECT * FROM t WHERE v = )", syntax))
					tx.Rollback()
					// Statement and actual Prepare failures release earlier AX before client end.
					for _, failure := range []string{"constraint", "prepare"} {
						owner, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						referenceRunStep(t, ctx, owner, recorder, referenceStep{query: "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", prepared: prepared, affected: 0})
						referenceLockExec(t, ctx, owner, "INSERT INTO t VALUES(1)", 1)
						if failure == "constraint" {
							wantNull := referenceTransactionNull()
							referenceAssertDiagnostic(t, transactionConstraintExec(t, ctx, owner, "INSERT INTO t VALUES(NULL)", prepared), &wantNull)
						} else {
							stmt, err := owner.PrepareContext(ctx, "SELECT * FROM t WHERE v = )")
							if stmt != nil {
								stmt.Close()
								t.Fatal("failed Prepare exposed handle")
							}
							if got := referenceExtractDiagnostic(t, err); got != syntax {
								t.Fatal(got)
							}
						}
						rows, err := b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal("abort did not release AX", err)
						}
						referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg)
						referenceRunStep(t, ctx, owner, recorder, referenceTransactionError(false, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", referenceTransactionAborted()))
						if err = owner.Commit(); err == nil {
							t.Fatal("failed transaction committed")
						}
						rows, err = b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg)
					}
				})
			}
		})
	}
}
func TestReferenceRelationLockOrdinaryAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"lock_core_reader"})
	pair := newReferenceScenario(t)
	pair.step(t, referenceExec("CREATE USER lock_core_reader", 0))
	pair.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
	// The fixture recreates public without PUBLIC's schema USAGE privilege.
	// Make the reader's lookup authority explicit before testing its table rights.
	if pair.postgres != nil {
		if _, err := referenceAdmin.ExecContext(pair.ctx, "GRANT USAGE ON SCHEMA public TO lock_core_reader"); err != nil {
			t.Fatal(err)
		}
	}
	pair.step(t, referenceExec("GRANT SELECT, INSERT ON t TO lock_core_reader", 0))
	for _, backend := range pair.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := pair.ctx
			a, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			referenceLockExec(t, ctx, a, "SET SESSION AUTHORIZATION lock_core_reader", 0)
			defer a.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			rows, err := a.QueryContext(ctx, "SELECT v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			referenceTransactionValues(t, rows)
			referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
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
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
				if got := referenceExtractDiagnostic(t, err); got != want {
					tx.Rollback()
					t.Fatal(got, want)
				}
				tx.Rollback()
			}
		})
	}
}
