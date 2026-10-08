package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityFreshValidationAfterDelete(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	execSQL(t, db, "CREATE TABLE t(a INT)")
	execSQL(t, db, "CREATE TABLE u(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(NULL),(7)")
	execSQL(t, db, "INSERT INTO u VALUES(1)")
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var a int64
	if err = tx.QueryRow("SELECT a FROM u").Scan(&a); err != nil || a != 1 {
		t.Fatal(a, err)
	}
	tableCoreExec(t, db, false, "DELETE FROM t WHERE a IS NULL", 1)
	tableCoreExec(t, db, false, "INSERT INTO u VALUES(2)", 1)
	result, err := tx.Exec("ALTER TABLE t ALTER a SET NOT NULL")
	if err != nil {
		t.Fatal("fresh scan kept ended NULL", err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	rows, err := tx.Query("SELECT a FROM u ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err = rows.Scan(&a); err != nil || a != 1 {
		t.Fatal(a, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("DDL refreshed ordinary RR", rows.Err())
	}
	rows.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
	alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
}
