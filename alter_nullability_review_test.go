package stuntgres_test

import (
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityReviewProjectedUpdate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			ctx := t.Context()
			execSQL(t, db, "CREATE TABLE t(a INT,b INT,c INT)")
			execSQL(t, db, "INSERT INTO t VALUES(7,9,11)")
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			update, err := tx.PrepareContext(ctx, "UPDATE t SET c=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer update.Close()
			run := func(q string, n int64) { transactionAffected(t, ctx, tx, q, prepared, n) }
			change := func(n int) {
				t.Helper()
				if prepared {
					r, e := update.ExecContext(ctx, n)
					if e != nil {
						t.Fatal(e)
					}
					count, e := r.RowsAffected()
					if e != nil || count != 1 {
						t.Fatal(count, e)
					}
				} else {
					transactionAffected(t, ctx, tx, "UPDATE t SET c=$1", false, 1, n)
				}
			}
			run("SAVEPOINT before_shape", 0)
			run("ALTER TABLE t DROP b, ALTER c SET NOT NULL", 0)
			change(12)
			run("SAVEPOINT middle", 0)
			run("ALTER TABLE t DROP a", 0)
			change(13)
			var value int64
			if err = tx.QueryRowContext(ctx, "SELECT c FROM t").Scan(&value); err != nil || value != 13 {
				t.Fatal(value, err)
			}
			run("ROLLBACK TO middle", 0)
			change(14)
			run("ROLLBACK TO before_shape", 0)
			change(15)
			run("ALTER TABLE t DROP a, DROP b, ALTER c SET NOT NULL", 0)
			change(16)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(16)}})
		})
	}
}

func TestAlterNullabilityReviewSuccessiveDependencies(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT,b INT,c INT)")
			execSQL(t, db, "INSERT INTO t VALUES(7,9,11)")
			execSQL(t, db, "CREATE SEQUENCE owned OWNED BY t.b")
			execSQL(t, db, "CREATE VIEW v AS SELECT b,c FROM t")
			execSQL(t, db, "CREATE VIEW w AS SELECT * FROM v")
			if reverse {
				_, err := db.Exec("ALTER TABLE t DROP c RESTRICT, DROP b CASCADE, ALTER a SET NOT NULL")
				notNullDiagnostic(t, err, stuntgres.Error{Code: "2BP01", Message: "cannot drop column c of table t because other objects depend on it", Detail: "view v depends on column c of table t\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				wantTuples(t, db, "SELECT * FROM w", [][]any{{int64(9), int64(11)}})
			} else {
				execSQL(t, db, "ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL")
				wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
			}
		})
	}
}

func TestAlterNullabilityReviewFirstRRSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT,b TEXT,c INT)")
			execSQL(t, db, "INSERT INTO t VALUES(7,'keep',9)")
			ctx := t.Context()
			tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run := func(q string, n int64) { transactionAffected(t, ctx, tx, q, prepared, n) }
			run("SAVEPOINT before_shape", 0)
			run("ALTER TABLE t DROP a, ALTER c SET NOT NULL", 0)
			rows, closeRows, err := transactionQuery(ctx, tx, "SELECT b,c FROM t", prepared)
			if err != nil {
				t.Fatal(err)
			}
			var b string
			var c int64
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			if err = rows.Scan(&b, &c); err != nil || b != "keep" || c != 9 {
				t.Fatal(b, c, err)
			}
			rows.Close()
			closeRows()
			run("INSERT INTO t VALUES('own',10)", 1)
			run("ALTER TABLE t DROP b", 0)
			var total int64
			rows, closeRows, err = transactionQuery(ctx, tx, "SELECT c FROM t ORDER BY c", prepared)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var v int64
				if err = rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				total += v
			}
			if err = rows.Err(); err != nil || total != 19 {
				t.Fatal(total, err)
			}
			rows.Close()
			closeRows()
			run("ROLLBACK TO before_shape", 0)
			run("INSERT INTO t VALUES(8,'restored',11)", 1)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(7), "keep", int64(9)}, {int64(8), "restored", int64(11)}})
		})
	}
}
