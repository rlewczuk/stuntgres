package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func referenceIsolationVisibility(t *testing.T, postgres bool) {
	for _, mode := range []struct {
		name      string
		isolation string
		stable    bool
	}{
		{name: "RC", isolation: "READ COMMITTED"},
		{name: "RU", isolation: "READ UNCOMMITTED"},
		{name: "RR", isolation: "REPEATABLE READ", stable: true},
	} {
		for _, prepared := range []bool{false, true} {
			path := "direct"
			if prepared {
				path = "held_prepared"
			}
			t.Run(mode.name+"/"+path, func(t *testing.T) {
				_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
					defer stop()
					a.ExecContext(cleanup, "ROLLBACK")
					b.ExecContext(cleanup, "ROLLBACK")
				})
				for _, table := range []string{"t", "u", "e", "r", "d"} {
					fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE "+table+"(v INT)", false, 0)
				}
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO u VALUES(10)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO r VALUES(10)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO d VALUES(40)", false, 1)

				// Hold only t's statement before BEGIN. Preparing the other relations
				// in the block would defeat the first-ever relation-read witness.
				var held *sql.Stmt
				if prepared {
					var err error
					held, err = a.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				selectT := func(want ...int64) {
					t.Helper()
					if held == nil {
						fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t ORDER BY v", false, want...)
						return
					}
					rows, err := held.QueryContext(ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, postgres, want...)
				}

				fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL "+mode.isolation, false, 0)
				// BEGIN must not fix RR's snapshot: this earlier peer commit is included.
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(2)", false, 1)
				selectT(1, 2)

				fixture.IsolationReferenceAffected(t, ctx, b, "BEGIN", false, 0)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(3)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO u VALUES(20)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO e VALUES(30)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "UPDATE r SET v=20 WHERE v=10", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "DELETE FROM d WHERE v=40", false, 1)
				selectT(1, 2) // Even RU must not expose uncommitted peer rows.
				fixture.IsolationReferenceAffected(t, ctx, b, "COMMIT", false, 0)

				if mode.stable {
					selectT(1, 2)
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM u ORDER BY v", false, 10)
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM e ORDER BY v", false)
					// r was never read or prepared: retain its replaced old root too.
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM r ORDER BY v", false, 10)
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM d ORDER BY v", false, 40)
				} else {
					selectT(1, 2, 3)
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM u ORDER BY v", false, 10, 20)
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM e ORDER BY v", false, 30)
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM r ORDER BY v", false, 20)
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM d ORDER BY v", false)
				}

				fixture.IsolationReferenceAffected(t, ctx, a, "INSERT INTO t VALUES(4)", false, 1)
				if mode.stable {
					selectT(1, 2, 4)
				} else {
					selectT(1, 2, 3, 4)
				}
				fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
				fixture.IsolationReferenceAffected(t, ctx, a, "COMMIT", false, 0)
				fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t ORDER BY v", false, 1, 2, 3, 4)
			})
		}
	}

	for _, prepared := range []bool{false, true} {
		path := "direct"
		if prepared {
			path = "prepared"
		}
		t.Run("RR/concurrent_update/"+path, func(t *testing.T) {
			_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				a.ExecContext(cleanup, "ROLLBACK")
			})
			fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
			fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(10)", false, 1)
			fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
			fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t ORDER BY v", false, 10)
			fixture.IsolationReferenceAffected(t, ctx, b, "UPDATE t SET v=20 WHERE v=10", false, 1)
			// The existing helper requires successful Prepare before actual Exec.
			err := fixture.TransactionConstraintExec(t, ctx, a, "UPDATE t SET v=30", prepared)
			fixture.IsolationReferenceDiagnostic(t, err, fixture.IsolationReferenceWant("40001", "could not serialize access due to concurrent update"))
			_, err = a.ExecContext(ctx, "SELECT v FROM t ORDER BY v")
			fixture.IsolationReferenceDiagnostic(t, err, fixture.TransactionAborted())
			fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
			fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t ORDER BY v", false, 20)
		})
	}
}

func TestReferenceTransactionIsolationVisibility(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			referenceIsolationVisibility(t, postgres)
			referenceIsolationCatalogLifecycle(t, postgres)
		})
	}
	if *fixture.Mode {
		t.Run("snapshot_admission", referenceIsolationSnapshotPhases)
	}
}
