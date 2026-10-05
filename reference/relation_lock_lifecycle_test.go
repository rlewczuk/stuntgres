//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceRelationLockLifecycleAndDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			pair := newReferencePair(t)
			for index, db := range []*sql.DB{pair.memory, pair.postgres} {
				t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
					ctx := pair.ctx
					a, b := referenceLockConnections(t, ctx, db)
					recorder := pair.memoryRecord
					if index == 1 {
						recorder = pair.postgresRecord
					}
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT NOT NULL)", 0)
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
						referenceLockExec(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						referenceLockExec(t, ctx, owner, "INSERT INTO t VALUES(1)", 1)
						if failure == "constraint" {
							stmt, err := owner.PrepareContext(ctx, "INSERT INTO t VALUES(NULL)")
							if err != nil {
								t.Fatal("constraint must prepare", err)
							}
							_, err = stmt.ExecContext(ctx)
							stmt.Close()
							if got := referenceExtractDiagnostic(t, err); got != referenceTransactionNull() {
								t.Fatal(got)
							}
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
						referenceTransactionValues(t, rows)
						referenceRunStep(t, ctx, owner, recorder, referenceTransactionError(false, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", referenceTransactionAborted()))
						if err = owner.Commit(); err == nil {
							t.Fatal("failed transaction committed")
						}
						rows, err = b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						referenceTransactionValues(t, rows)
					}
				})
			}
		})
	}
}
func TestReferenceRelationLockOrdinaryAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"lock_core_reader"})
	pair := newReferencePair(t)
	pair.step(t, referenceExec("CREATE USER lock_core_reader", 0))
	pair.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
	// The fixture recreates public without PUBLIC's schema USAGE privilege.
	// Make the reader's lookup authority explicit before testing its table rights.
	if _, err := referenceAdmin.ExecContext(pair.ctx, "GRANT USAGE ON SCHEMA public TO lock_core_reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := referenceAdmin.ExecContext(pair.ctx, "GRANT SELECT, INSERT ON t TO lock_core_reader"); err != nil {
		t.Fatal(err)
	}
	for index, db := range []*sql.DB{pair.memory, pair.postgres} {
		t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
			ctx := pair.ctx
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			referenceLockExec(t, ctx, a, "SET SESSION AUTHORIZATION lock_core_reader", 0)
			defer a.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			if index == 1 {
				rows, err := a.QueryContext(ctx, "SELECT v FROM t")
				if err != nil {
					t.Fatal(err)
				}
				referenceTransactionValues(t, rows)
				referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
			}
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
