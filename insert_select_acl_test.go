package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestInsertSelectIndependentPublicACL(t *testing.T) {
	d := stuntgres.NewDriver()
	admin := openLocal(t, d, "insert-select-public-acl")
	execSQL(t, admin, "CREATE USER writer")
	execSQL(t, admin, "CREATE TABLE source(i INT)")
	execSQL(t, admin, "CREATE TABLE denied_target(i INT)")
	execSQL(t, admin, "INSERT INTO source VALUES(1)")
	connector, err := d.OpenConnectorAs("insert-select-public-acl", "writer")
	if err != nil {
		t.Fatal(err)
	}
	writer := sql.OpenDB(connector)
	defer writer.Close()
	execSQL(t, writer, "CREATE TABLE target(i INT)")
	execSQL(t, writer, "CREATE TABLE own_source(i INT)")
	execSQL(t, writer, "INSERT INTO own_source VALUES(7)")
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, table string }{
			{"INSERT INTO target SELECT i FROM source", "source"},
			{"INSERT INTO target SELECT 1 FROM source WHERE FALSE", "source"},
			{"INSERT INTO denied_target SELECT i FROM own_source", "denied_target"},
		} {
			var err error
			if prepared {
				s, e := writer.Prepare(tc.q)
				if e != nil {
					t.Fatal("semantic prepare failed", e)
				}
				_, err = s.Exec()
				s.Close()
			} else {
				_, err = writer.Exec(tc.q)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "permission denied for table " + tc.table})
		}
		var result sql.Result
		if prepared {
			s, e := writer.Prepare("INSERT INTO target SELECT i FROM own_source")
			if e != nil {
				t.Fatal(e)
			}
			result, err = s.Exec()
			s.Close()
		} else {
			result, err = writer.Exec("INSERT INTO target SELECT i FROM own_source")
		}
		insertSelectCount(t, result, err, 1)
	}
	wantTuples(t, admin, "SELECT i FROM target", [][]any{{int64(7)}, {int64(7)}})
	// ACL checks include source reads even when projection contains no source slot.
	if _, err = writer.ExecContext(context.Background(), "INSERT INTO target SELECT 1 FROM source"); err == nil {
		t.Fatal("literal source bypassed ACL")
	}
}
