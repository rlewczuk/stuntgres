package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestUpdateCoreControlsAndAlterConsumers(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,a INT,b TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,NULL,'keep'),(2,2,'other'),(2,2,'other')")
	old, err := db.Query("SELECT id,a,b FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	stmt, err := db.Prepare("UPDATE t SET a=$1 WHERE id=$2")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	execSQL(t, db, "UPDATE t SET a=3 WHERE id=1")
	execSQL(t, db, "ALTER TABLE t ALTER a SET NOT NULL")
	execSQL(t, db, "COMMENT ON COLUMN t.a IS 'current'")
	execSQL(t, db, "UPDATE t SET a=a")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "UPDATE") || strings.Contains(dump.String(), "NULL, 'keep'") {
		t.Fatal("dump serialized history", dump.String())
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE bad(v INT); UPDATE bad SET v=1;")); err == nil {
		t.Fatal("Restore admitted UPDATE")
	}
	wantTuples(t, db, "SELECT id,a,b FROM t ORDER BY id", [][]any{{int64(1), int64(3), "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}})
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	r, err := stmt.Exec(4, 1)
	if err != nil {
		t.Fatal("prepared rebind after replacement", err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	execSQL(t, db, "ALTER TABLE t DROP COLUMN b")
	wantTuples(t, db, "SELECT id,a FROM t ORDER BY id", [][]any{{int64(1), int64(4)}, {int64(2), int64(2)}, {int64(2), int64(2)}})
	execSQL(t, db, "ALTER TABLE t RENAME COLUMN a TO renamed")
	_, err = stmt.Exec(5, 1)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "a" of relation "t" does not exist`, Position: "14"})
	queryCoreRows(t, old, nil, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), nil, "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}, true)
}

func TestUpdateCoreExplicitLateFailure(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,a INT,n SMALLINT NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,1,1),(2,40000,1)")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE t SET a=7 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("UPDATE t SET n=a")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
	_, err = tx.Exec("UPDATE t SET a=8")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT id,a,n FROM t ORDER BY id", [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(40000), int64(1)}})
	execSQL(t, db, "UPDATE t SET a=2 WHERE id=1")
}
