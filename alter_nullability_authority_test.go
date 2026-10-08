package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestAlterNullabilityAuthorizationLifetime(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	execSQL(t, db, "CREATE ROLE nn_nonowner")
	_, boundary := db.Exec("SET ROLE nn_nonowner")
	notNullDiagnostic(t, boundary, stuntgres.Error{Code: "0A000", Message: "SET ROLE is unsupported", Position: "5"})
	execSQL(t, db, "CREATE TABLE t(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL"} {
		r, e := tx.Exec(q)
		if e != nil {
			t.Fatal(q, e)
		}
		n, e := r.RowsAffected()
		if e != nil || n != 0 {
			t.Fatal(q, n, e)
		}
	}
	execSQL(t, db, "CREATE ROLE nn_revision")
	if err = tx.Commit(); err != nil {
		t.Fatal("later role change invalidated authorized ALTER", err)
	}
	alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
	_, err = db.Exec("INSERT INTO t VALUES(NULL)")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
}
