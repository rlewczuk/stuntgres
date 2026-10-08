package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestUpdateReturningCapturedRegclass(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	rows, err := db.Query("UPDATE t SET id=id RETURNING 't'::regclass AS target")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "ALTER TABLE t RENAME TO renamed")
	if err = stuntgres.Reset(context.Background(), db); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	queryCoreRows(t, rows, nil, []string{"target"}, []string{"REGCLASS"}, [][]any{{"t"}, {"t"}}, true)
}
