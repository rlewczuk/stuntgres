package stuntgres_test

import (
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestRowLockReadCommitted(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range rowLockPublicClauses {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
				transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10)", false, 1)
				transactionAffected(t, ctx, a, "UPDATE t SET v=20 WHERE id=1", false, 1)
				transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1 "+clause, prepared, 20)
				transactionSelect(t, ctx, b, "SELECT v FROM t WHERE v=10 "+clause, prepared)
				transactionAffected(t, ctx, a, "DELETE FROM t WHERE id=1", false, 1)
				transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1 "+clause, prepared)
			})
		}
	}
}
func TestRowLockJoins(t *testing.T) {
	db := queryJoinsPublicFixture(t)
	for _, query := range []string{"SELECT a.l,b.r FROM a JOIN b ON a.k=b.k FOR UPDATE", "SELECT a.l,b.r FROM a JOIN b USING(k) FOR NO KEY UPDATE", "SELECT a.l,b.r FROM a NATURAL JOIN b FOR SHARE", "SELECT a.l,b.r FROM a,b WHERE a.k=b.k FOR KEY SHARE"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				rows, closeQuery, err := transactionQuery(context.Background(), db, query, prepared)
				if closeQuery != nil {
					defer closeQuery()
				}
				queryCoreRows(t, rows, err, []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}}, true)
			})
		}
	}
}
func TestRowLockDiagnostics(t *testing.T) {
	for _, clause := range rowLockPublicClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, _ := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, a, "CREATE TABLE t(v INT)", false, 0)
				transactionAffected(t, ctx, a, "CREATE SEQUENCE s", false, 0)
				rows, closeQuery, err := transactionQuery(ctx, a, "SELECT last_value FROM s "+clause, prepared)
				if rows != nil {
					rows.Close()
					t.Fatal("sequence row mark exposed cursor")
				}
				if closeQuery != nil {
					closeQuery()
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42809", Message: `cannot lock rows in sequence "s"`})
				transactionAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
				rows, closeQuery, err = transactionQuery(ctx, a, "SELECT v FROM t "+clause, prepared)
				if rows != nil {
					rows.Close()
					t.Fatal("read-only locker exposed cursor")
				}
				if closeQuery != nil {
					closeQuery()
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25006", Message: "cannot execute SELECT " + clause + " in a read-only transaction"})
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			})
		}
	}
}
func TestRowLockControls(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(v INT)")
			execSQL(t, db, "INSERT INTO t VALUES(10),(20)")
			rows, err := db.Query("SELECT v FROM t ORDER BY v FOR SHARE")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			ctx := context.Background()
			if restore {
				if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(99)")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
			}
			transactionValues(t, rows, 10, 20)
			if restore {
				transactionSelect(t, ctx, db, "SELECT v FROM t FOR UPDATE", true, 99)
			}
		})
	}
}
