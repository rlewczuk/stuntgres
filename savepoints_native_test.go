package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres"
)

func TestSavepointsNativeReplacement(t *testing.T) {
	for _, replacement := range []string{"Reset", "Restore"} {
		t.Run(replacement, func(t *testing.T) {
			db, a, b, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
			rows, err := a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			clone := openLocal(t, stuntgres.NewDriver(), t.Name()+"/clone")
			if err := stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			transactionSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
			if err := stuntgres.Restore(ctx, db, strings.NewReader("SAVEPOINT s;")); err == nil {
				t.Fatal("Restore accepted controls")
			}
			transactionAffected(t, ctx, a, "ROLLBACK TO s", false, 0)
			if replacement == "Reset" {
				err = stuntgres.Reset(ctx, db)
			} else {
				err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);"))
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.ExecContext(ctx, "ROLLBACK TO s")
			transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			transactionValues(t, rows, 1, 77)
			transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			if replacement == "Restore" {
				transactionSelect(t, ctx, b, "SELECT v FROM t", false, 99)
			}
		})
	}
}

func TestSavepointsBorrowerPersistence(t *testing.T) {
	db, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	db.SetMaxOpenConns(2)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
	stmt, err := a.PrepareContext(ctx, "ROLLBACK TO s")
	if err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	borrowed, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer borrowed.Close()
	defer borrowed.ExecContext(ctx, "ROLLBACK")
	transactionAffected(t, ctx, borrowed, "INSERT INTO t VALUES(2)", false, 1)
	transactionAffected(t, ctx, borrowed, "ROLLBACK TO s", false, 0)
	transactionSelect(t, ctx, borrowed, "SELECT v FROM t", false, 1)
	transactionAffected(t, ctx, borrowed, "COMMIT", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t", false, 1)
}
