package stuntgres_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestDeleteReturningMutableOutputOwnership(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(b BYTEA)")
	execSQL(t, db, "INSERT INTO t VALUES('\\x0102'),('\\x0102')")
	rows, err := db.Query("DELETE FROM t RETURNING old.b,old.b,new.b")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for range 2 {
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var first, second []byte
		var newValue any
		if err = rows.Scan(&first, &second, &newValue); err != nil || !bytes.Equal(first, []byte{1, 2}) || !bytes.Equal(second, []byte{1, 2}) || newValue != nil {
			t.Fatal(first, second, newValue, err)
		}
		first[0] = 99
		if !bytes.Equal(second, []byte{1, 2}) {
			t.Fatal("duplicate output exported shared mutable bytes", second)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
}
