package stuntgres_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestAlterTypePreparedParameterGeneration(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := t.Context()
	execSQL(t, db, "CREATE TABLE t(c INT)")
	held, err := db.Prepare("INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	execSQL(t, db, "ALTER TABLE t ALTER c TYPE BIGINT")
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	result, err := held.Exec(int64(7))
	if err != nil {
		t.Fatal("retained input type used stale generation", err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	alterTypePublicRows(t, db, "SELECT c FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}})
}
