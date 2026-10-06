package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestInsertSelectCommandMetadataAndRebind(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "insert-select-metadata")
	execSQL(t, db, "CREATE TABLE s(i INT)")
	execSQL(t, db, "CREATE TABLE t(i INT)")
	raw, err := d.Open("insert-select-metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	stmt, err := raw.Prepare("INSERT INTO t SELECT $1 FROM s")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if stmt.NumInput() != 1 {
		t.Fatal(stmt.NumInput())
	}
	result, err := stmt.Exec([]driver.Value{int64(7)})
	insertSelectCount(t, result, err, 0)
	execSQL(t, db, "INSERT INTO s VALUES(1),(2)")
	result, err = stmt.Exec([]driver.Value{int64(8)})
	insertSelectCount(t, result, err, 2)
	// The same prepared producer sees later data, never a cached source snapshot.
	execSQL(t, db, "INSERT INTO s VALUES(3)")
	result, err = stmt.Exec([]driver.Value{int64(9)})
	insertSelectCount(t, result, err, 3)
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			s, e := db.Prepare("INSERT INTO t SELECT i FROM s")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			rows, err = s.Query()
		} else {
			rows, err = db.Query("INSERT INTO t SELECT i FROM s")
		}
		if err != nil {
			t.Fatal(err)
		}
		names, e := rows.Columns()
		types, te := rows.ColumnTypes()
		if e != nil || te != nil || len(names) != 0 || len(types) != 0 || rows.Next() || rows.Err() != nil {
			t.Fatal(names, types, e, te, rows.Err())
		}
		rows.Close()
	}
	// Compatible descriptor evolution must rebind the current source names.
	execSQL(t, db, "ALTER TABLE s RENAME COLUMN i TO renamed")
	result, err = stmt.Exec([]driver.Value{int64(10)})
	insertSelectCount(t, result, err, 3)
	named, err := db.Prepare("INSERT INTO t SELECT renamed FROM s")
	if err != nil {
		t.Fatal(err)
	}
	defer named.Close()
	execSQL(t, db, "ALTER TABLE s RENAME COLUMN renamed TO changed")
	_, err = named.Exec()
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "renamed" does not exist`, Position: "22"})
}

func TestInsertSelectTransactions(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "failure"} {
		t.Run(mode, func(t *testing.T) {
			d := stuntgres.NewDriver()
			db := openLocal(t, d, "insert-select-tx-"+mode)
			other := openLocal(t, d, "insert-select-tx-"+mode)
			execSQL(t, db, "CREATE TABLE t(i INT)")
			execSQL(t, db, "INSERT INTO t VALUES(1),(1),(2)")
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.Exec("INSERT INTO t VALUES(3)"); err != nil {
				t.Fatal(err)
			}
			s, err := tx.Prepare("INSERT INTO t SELECT * FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			result, err := s.Exec()
			insertSelectCount(t, result, err, 4)
			rows, err := tx.Query("SELECT i FROM t ORDER BY i")
			queryCoreRows(t, rows, err, []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}}, false)
			wantTuples(t, other, "SELECT i FROM t ORDER BY i", [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
			if mode == "commit" {
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				wantTuples(t, other, "SELECT i FROM t ORDER BY i", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}})
			} else {
				if mode == "failure" {
					_, err = tx.Exec("INSERT INTO t SELECT 'bad'")
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "22"})
					_, err = tx.Exec("SELECT i FROM t")
					sqlState(t, err, "25P02")
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				wantTuples(t, other, "SELECT i FROM t ORDER BY i", [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
			}
		})
	}
}

func TestInsertSelectControls(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "insert-select-controls")
	execSQL(t, db, "CREATE TABLE s(i INT,v TEXT)")
	execSQL(t, db, "INSERT INTO s VALUES(1,'x'),(1,'x'),(2,NULL)")
	execSQL(t, db, "CREATE TABLE t(v TEXT,i BIGINT)")
	execSQL(t, db, "INSERT INTO t SELECT v,i FROM s")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "SELECT") {
		t.Fatal("dump retained query provenance", dump.String())
	}
	clone := openLocal(t, d, "insert-select-controls-clone")
	if err := stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err := clone.Query("SELECT * FROM t ORDER BY i")
	queryCoreRows(t, rows, err, []string{"v", "i"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(1)}, {"x", int64(1)}, {nil, int64(2)}}, false)
	raw, err := d.Open("insert-select-controls")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT * FROM t ORDER BY i", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, script := range []string{"CREATE TABLE t(i INT); INSERT INTO t SELECT 1", "CREATE TABLE t(i INT); INSERT INTO t SELECT $1", "CREATE TABLE t(i INT); INSERT INTO t VALUES(1) ORDER BY 1"} {
		err := stuntgres.Restore(ctx, db, strings.NewReader(script))
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "restore INSERT query sources are unsupported"})
		wantTuples(t, db, "SELECT * FROM t ORDER BY i", [][]any{{"x", int64(1)}, {"x", int64(1)}, {nil, int64(2)}})
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('new')")); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(held.Columns(), []string{"v", "i"}) || held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "INT8" {
		t.Fatal(held.Columns())
	}
	dst := make([]driver.Value, 2)
	for _, want := range [][]driver.Value{{"x", int64(1)}, {"x", int64(1)}, {nil, int64(2)}} {
		if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, err, want)
		}
	}
	if err := held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
}

func TestInsertSelectAtomicConstraintsAndCancellation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "insert-select-atomic")
	execSQL(t, db, "CREATE TABLE s(i INT)")
	execSQL(t, db, "INSERT INTO s VALUES(1),(NULL)")
	execSQL(t, db, "CREATE TABLE t(i INT NOT NULL)")
	execSQL(t, db, "INSERT INTO t VALUES(9)")
	for _, prepared := range []bool{false, true} {
		var err error
		if prepared {
			s, e := db.Prepare("INSERT INTO t SELECT i FROM s ORDER BY i")
			if e != nil {
				t.Fatal(e)
			}
			_, err = s.Exec()
			s.Close()
		} else {
			_, err = db.Exec("INSERT INTO t SELECT i FROM s ORDER BY i")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "i" of relation "t" violates not-null constraint`, Schema: "public", Table: "t", Column: "i", Detail: "Failing row contains (null)."})
		_, err = db.Exec("INSERT INTO t SELECT i FROM s WHERE i='bad'")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "39"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = db.ExecContext(ctx, "INSERT INTO t SELECT i FROM s")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		wantTuples(t, db, "SELECT i FROM t", [][]any{{int64(9)}})
	}
}
