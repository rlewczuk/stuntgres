package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterRenameValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT,c BIGINT)")
			execSQL(t, db, "INSERT INTO t VALUES(7,'O''Brien',9),(8,NULL,10)")
			tableCoreExec(t, db, prepared, "ALTER TABLE t RENAME COLUMN a TO renamed", 0)
			tableCoreExec(t, db, prepared, "ALTER TABLE t RENAME TO newtable", 0)
			for _, mode := range []bool{false, true} {
				alterDropMetadata(t, db, mode, "SELECT * FROM newtable", []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{false, true, true}, 2)
			}
			wantTuples(t, db, "SELECT * FROM newtable ORDER BY renamed", [][]any{{int64(7), "O'Brien", int64(9)}, {int64(8), nil, int64(10)}})
			_, err := db.Query("SELECT a FROM newtable")
			alterDropDiagnostic(t, err, "42703", `column "a" does not exist`, "8")
			_, err = db.Query("SELECT * FROM t")
			alterDropDiagnostic(t, err, "42P01", `relation "t" does not exist`, "15")
		})
	}
}

func TestAlterRenamePrepared(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,'keep')")
	prepare := func(q string) *sql.Stmt {
		s, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	star, named, other := prepare("SELECT * FROM t"), prepare("SELECT a FROM t"), prepare("SELECT b FROM t")
	insert, ordered := prepare("INSERT INTO t(a,b) VALUES($1,$2)"), prepare("INSERT INTO t VALUES($1,$2)")
	held, err := db.Query("SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tableCoreExec(t, db, true, "ALTER TABLE t RENAME a TO renamed", 0)
	labels, _ := held.Columns()
	if !reflect.DeepEqual(labels, []string{"a", "b"}) {
		t.Fatal(labels)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var a int64
	var b string
	if err = held.Scan(&a, &b); err != nil || a != 7 || b != "keep" {
		t.Fatal(a, b, err)
	}
	held.Close()
	_, err = star.Query()
	alterDropDiagnostic(t, err, "0A000", "cached plan must not change result type", "")
	_, err = named.Query()
	alterDropDiagnostic(t, err, "42703", `column "a" does not exist`, "8")
	_, err = insert.Exec(8, "bad")
	alterDropDiagnostic(t, err, "42703", `column "a" of relation "t" does not exist`, "15")
	rows, err := other.Query()
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result, err := ordered.Exec(8, "tail")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		t.Fatal(n)
	}
	tableCoreExec(t, db, true, "INSERT INTO t(renamed,b) VALUES($1,$2)", 1, 9, "fresh")
	execSQL(t, db, "ALTER TABLE t RENAME TO newtable")
	_, err = other.Query()
	alterDropDiagnostic(t, err, "42P01", `relation "t" does not exist`, "15")
	wantTuples(t, db, "SELECT * FROM newtable ORDER BY renamed", [][]any{{int64(7), "keep"}, {int64(8), "tail"}, {int64(9), "fresh"}})
}

func TestAlterRenameControls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,'O''Brien'),(8,NULL)")
	execSQL(t, db, "COMMENT ON TABLE t IS 'table comment'")
	execSQL(t, db, "COMMENT ON COLUMN t.a IS 'column comment'")
	execSQL(t, db, "ALTER TABLE t RENAME a TO renamed")
	execSQL(t, db, "ALTER TABLE t RENAME TO newtable")
	var before bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	dump := before.String()
	for _, fragment := range []string{"newtable", "renamed", "nn", "O''Brien", "table comment", "column comment"} {
		if !strings.Contains(dump, fragment) {
			t.Fatal(fragment, dump)
		}
	}
	if strings.Contains(dump, "ALTER TABLE") {
		t.Fatal(dump)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM newtable ORDER BY renamed", [][]any{{int64(7), "O'Brien"}, {int64(8), nil}})
	var after bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &after); err != nil || dump != after.String() {
		t.Fatal(err, after.String(), dump)
	}
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader("ALTER TABLE newtable RENAME TO bad;")); err == nil {
		t.Fatal("Restore accepted ALTER history")
	}
	after.Reset()
	if err := stuntgres.Dump(context.Background(), db, &after); err != nil || dump != after.String() {
		t.Fatal("failed restore changed catalog", err)
	}
}
