package reference_test

import (
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceControlTransactionsGoCommitAndRollbackRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "go_commit", true: "rollback_chain"}[rollback], func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.CatalogControlSQLWitness(t, p)
			db, a, b := fixture.CatalogControlConnections(t, p, true)
			ctx := context.Background()
			fixture.CatalogControlAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
			if rollback {
				fixture.CatalogControlAffected(t, ctx, a, "BEGIN", false, 0)
				fixture.CatalogControlAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			} else {
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.CatalogControlAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", false, 1)
				if err = stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				_, err = tx.ExecContext(ctx, "SELECT v FROM t")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				fixture.TransactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
				fixture.CatalogControlAffected(t, ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				fixture.CatalogControlSelect(t, ctx, b, "SELECT v FROM t", false)
				return
			}
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT NOT NULL); INSERT INTO t VALUES(99);")); err != nil {
				t.Fatal(err)
			}
			_, err := a.ExecContext(ctx, "SELECT v FROM t")
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			fixture.CatalogControlAffected(t, ctx, a, "ROLLBACK AND CHAIN", true, 0)
			fixture.CatalogControlAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
			fixture.CatalogControlSelect(t, ctx, b, "SELECT v FROM t", false, 99)
			fixture.CatalogControlAffected(t, ctx, a, "COMMIT", false, 0)
			fixture.CatalogControlSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 2, 99)
		})
	}
}
