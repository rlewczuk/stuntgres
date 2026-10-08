package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityTransactionBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			ctx := t.Context()
			transactionAffected(t, ctx, db, "CREATE TABLE t(a INT,b TEXT)", false, 0)
			transactionAffected(t, ctx, db, "INSERT INTO t VALUES(7,'seed')", false, 1)
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			transactionAffected(t, ctx, tx, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", prepared, 0)
			_, err = transactionExec(ctx, tx, "INSERT INTO t VALUES($1,$2)", prepared, nil, "bad")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"})
			_, err = tx.ExecContext(ctx, "SELECT * FROM t")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, 1)
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			transactionAffected(t, ctx, tx, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", prepared, 0)
			transactionAffected(t, ctx, tx, "INSERT INTO t VALUES($1,$2)", prepared, 1, 8, "valid")
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			transactionAffected(t, ctx, tx, "ALTER TABLE t DROP CONSTRAINT nn", prepared, 0)
			transactionAffected(t, ctx, tx, "INSERT INTO t VALUES($1,$2)", prepared, 1, nil, "rolled_back")
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, 2)
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			transactionAffected(t, ctx, tx, "ALTER TABLE t DROP CONSTRAINT nn", prepared, 0)
			transactionAffected(t, ctx, tx, "INSERT INTO t VALUES($1,$2)", prepared, 1, nil, "allowed")
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, "SELECT * FROM t ORDER BY b", [][]any{{nil, "allowed"}, {int64(7), "seed"}, {int64(8), "valid"}})
			alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, false}, 3)
		})
	}
}
