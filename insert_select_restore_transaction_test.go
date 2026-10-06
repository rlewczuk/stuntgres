package stuntgres_test

import (
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestInsertSelectFailedRestoreTransactionEligibility(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "insert-select-failed-restore-tx")
	execSQL(t, db, "CREATE TABLE t(i INT)")
	execSQL(t, db, "INSERT INTO t VALUES(9)")
	execSQL(t, db, "CREATE TABLE s(i INT)")
	execSQL(t, db, "INSERT INTO s VALUES(1)")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO t VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	producer, err := tx.Prepare("INSERT INTO t SELECT i FROM s")
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	err = stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE t(i INT); INSERT INTO t SELECT $1"))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "restore INSERT query sources are unsupported"})
	result, err := producer.Exec()
	insertSelectCount(t, result, err, 1)
	wantTuples(t, db, "SELECT i FROM t", [][]any{{int64(9)}})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT i FROM t ORDER BY i", [][]any{{int64(1)}, {int64(7)}, {int64(9)}})
}
