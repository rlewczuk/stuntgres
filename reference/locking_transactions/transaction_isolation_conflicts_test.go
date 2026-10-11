package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceIsolationQueuedConflicts(t *testing.T) {
	t.Helper()
	for _, peer := range []string{"UPDATE t SET v=20", "DELETE FROM t", "UPDATE then DELETE"} {
		for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
			for _, commit := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					name := peer + "/" + own + map[bool]string{false: "/rollback", true: "/commit"}[commit] + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
					t.Run(name, func(t *testing.T) {
						p := fixture.NewScenario(t)
						ctx := p.Ctx
						a, b := fixture.LockConnections(t, ctx, p.Postgres)
						fixture.LockExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
						fixture.LockExec(t, ctx, b, "INSERT INTO t VALUES(10)", 1)
						fixture.LockExec(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
						fixture.IsolationReferenceSelect(t, ctx, a, true, "SELECT v FROM t", false, 10)
						var held *sql.Stmt
						var err error
						if prepared {
							held, err = a.PrepareContext(ctx, own)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
						}
						fixture.LockExec(t, ctx, b, "BEGIN", 0)
						if peer == "UPDATE then DELETE" {
							fixture.LockExec(t, ctx, b, "UPDATE t SET v=20", 1)
							fixture.LockExec(t, ctx, b, "DELETE FROM t", 1)
						} else {
							fixture.LockExec(t, ctx, b, peer, 1)
						}
						ap, bp := fixture.RowPIDs(t, ctx, a, b)
						await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							var result sql.Result
							var err error
							if held != nil {
								result, err = held.ExecContext(ctx)
							} else {
								result, err = a.ExecContext(ctx, own)
							}
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						fixture.RowWait(t, ctx, ap, bp)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						fixture.LockExec(t, ctx, b, end, 0)
						outcome := await()
						if commit {
							if outcome.Result != nil {
								t.Fatal("serialization failure returned a result")
							}
							word := "update"
							if peer == "DELETE FROM t" {
								word = "delete"
							}
							want := fixture.IsolationReferenceWant("40001", "could not serialize access due to concurrent "+word)
							fixture.AssertDiagnostic(t, outcome.Err, &want)
							_, err := a.ExecContext(ctx, "SELECT num_nonnulls(1)")
							want = fixture.TransactionAborted()
							fixture.AssertDiagnostic(t, err, &want)
						} else {
							if outcome.Err != nil {
								t.Fatal(outcome.Err)
							}
							count, err := outcome.Result.RowsAffected()
							if err != nil || count != 1 {
								t.Fatal(count, err)
							}
						}
						fixture.LockExec(t, ctx, a, "ROLLBACK", 0)
						if commit {
							if peer != "UPDATE t SET v=20" {
								fixture.IsolationReferenceSelect(t, ctx, b, true, "SELECT v FROM t", false)
							} else {
								fixture.IsolationReferenceSelect(t, ctx, b, true, "SELECT v FROM t", false, 20)
							}
						} else {
							fixture.IsolationReferenceSelect(t, ctx, b, true, "SELECT v FROM t", false, 10)
						}
					})
				}
			}
		}
	}
}
func referenceIsolationCatalogLifecycle(t *testing.T, postgres bool) {
	t.Helper()
	for _, recreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres]+map[bool]string{false: "/create", true: "/recreate"}[recreate], func(t *testing.T) {
			_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
			ctx := context.Background()
			if recreate {
				fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO u VALUES(9)", false, 1)
			}
			fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
			var one int
			if err := a.QueryRowContext(ctx, "SELECT num_nonnulls(1)").Scan(&one); err != nil || one != 1 {
				t.Fatal(one, err)
			}
			if recreate {
				fixture.IsolationReferenceAffected(t, ctx, b, "DROP TABLE u", false, 0)
			}
			fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
			fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO u VALUES(20)", false, 1)
			fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM u", false)
			fixture.IsolationReferenceAffected(t, ctx, a, "INSERT INTO u VALUES(1)", false, 1)
			fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM u", false, 1)
			fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
			fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM u", false, 20)
		})
	}
}
