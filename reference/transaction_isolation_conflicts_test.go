//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceTransactionIsolationQueuedConflicts(t *testing.T) {
	for _, peer := range []string{"UPDATE t SET v=20", "DELETE FROM t", "UPDATE then DELETE"} {
		for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
			for _, commit := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					name := peer + "/" + own + map[bool]string{false: "/rollback", true: "/commit"}[commit] + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
					t.Run(name, func(t *testing.T) {
						p := newReferencePair(t)
						ctx := p.ctx
						a, b := referenceLockConnections(t, ctx, p.postgres)
						referenceLockExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
						referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(10)", 1)
						referenceLockExec(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
						isolationReferenceSelect(t, ctx, a, "SELECT v FROM t", false, 10)
						var held *sql.Stmt
						var err error
						if prepared {
							held, err = a.PrepareContext(ctx, own)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
						}
						referenceLockExec(t, ctx, b, "BEGIN", 0)
						if peer == "UPDATE then DELETE" {
							referenceLockExec(t, ctx, b, "UPDATE t SET v=20", 1)
							referenceLockExec(t, ctx, b, "DELETE FROM t", 1)
						} else {
							referenceLockExec(t, ctx, b, peer, 1)
						}
						var pid int
						if err := a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							t.Fatal(err)
						}
						done := make(chan relationOutcome, 1)
						go func() {
							var result sql.Result
							var err error
							if held != nil {
								result, err = held.ExecContext(ctx)
							} else {
								result, err = a.ExecContext(ctx, own)
							}
							done <- relationOutcome{result: result, err: err}
						}()
						referenceUpdateWait(t, ctx, pid)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						referenceLockExec(t, ctx, b, end, 0)
						outcome := referenceLockResult(t, done)
						if commit {
							word := "update"
							if peer == "DELETE FROM t" {
								word = "delete"
							}
							want := isolationReferenceWant("40001", "could not serialize access due to concurrent "+word)
							referenceAssertDiagnostic(t, outcome.err, &want)
							_, err := a.ExecContext(ctx, "SELECT num_nonnulls(1)")
							want = referenceTransactionAborted()
							referenceAssertDiagnostic(t, err, &want)
						} else {
							if outcome.err != nil {
								t.Fatal(outcome.err)
							}
							count, err := outcome.result.RowsAffected()
							if err != nil || count != 1 {
								t.Fatal(count, err)
							}
						}
						referenceLockExec(t, ctx, a, "ROLLBACK", 0)
						if commit {
							if peer != "UPDATE t SET v=20" {
								isolationReferenceSelect(t, ctx, b, "SELECT v FROM t", false)
							} else {
								isolationReferenceSelect(t, ctx, b, "SELECT v FROM t", false, 20)
							}
						} else {
							isolationReferenceSelect(t, ctx, b, "SELECT v FROM t", false, 10)
						}
					})
				}
			}
		}
	}
}
func TestReferenceTransactionIsolationCatalogLifecycle(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		for _, recreate := range []bool{false, true} {
			t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres]+map[bool]string{false: "/create", true: "/recreate"}[recreate], func(t *testing.T) {
				_, a, b := isolationReferenceConnections(t, postgres)
				ctx := context.Background()
				if recreate {
					isolationReferenceAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
					isolationReferenceAffected(t, ctx, b, "INSERT INTO u VALUES(9)", false, 1)
				}
				isolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
				var one int
				if err := a.QueryRowContext(ctx, "SELECT num_nonnulls(1)").Scan(&one); err != nil || one != 1 {
					t.Fatal(one, err)
				}
				if recreate {
					isolationReferenceAffected(t, ctx, b, "DROP TABLE u", false, 0)
				}
				isolationReferenceAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
				isolationReferenceAffected(t, ctx, b, "INSERT INTO u VALUES(20)", false, 1)
				isolationReferenceSelect(t, ctx, a, "SELECT v FROM u", false)
				isolationReferenceAffected(t, ctx, a, "INSERT INTO u VALUES(1)", false, 1)
				isolationReferenceSelect(t, ctx, a, "SELECT v FROM u", false, 1)
				isolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
				isolationReferenceSelect(t, ctx, b, "SELECT v FROM u", false, 20)
			})
		}
	}
}
