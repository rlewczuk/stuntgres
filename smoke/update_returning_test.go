package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestUpdateReturningSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)", "INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)", "CREATE SEQUENCE s START 101 CACHE 1"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query("UPDATE t SET a=b,b=a WHERE id=2 RETURNING id,a,b,old.a,new.a,new.a-old.a AS delta,v")
	returningSmokeRows(t, rows, err, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}})
	held, err := db.Prepare("UPDATE t SET a=a+$1 WHERE id=$2 RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	rows, err = held.Query(1, 1)
	returningSmokeRows(t, rows, err, []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(11)}})
	rows, err = held.Query(1, 99)
	returningSmokeRows(t, rows, err, []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{})
	result, err := db.Exec("UPDATE t SET a=a RETURNING nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	if n, e := result.RowsAffected(); e != nil || n != 3 {
		t.Fatal(n, e)
	}
	var next int64
	if err = db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 104 {
		t.Fatal(next, err)
	}
	for _, q := range []string{"CREATE TABLE bad(id INT,a INT,s TEXT)", "INSERT INTO bad VALUES(1,10,'7'),(2,20,'bad')"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = db.Query("UPDATE bad SET a=a+1 RETURNING CAST(s AS INT)")
	if rows != nil {
		rows.Close()
		t.Fatal("failed output exposed cursor")
	}
	if err = typeCoreDiagnostic(err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT id,a FROM bad ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
	for _, tc := range []struct {
		q string
		d stuntgres.Error
	}{
		{"UPDATE t SET id=$1 RETURNING $1", stuntgres.Error{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"}},
		{"UPDATE t SET id=1 RETURNING $1,$1::int4", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "29"}},
	} {
		stmt, e := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("invalid prepared handle")
		}
		if e = typeCoreDiagnostic(e, tc.d); e != nil {
			t.Fatal(e)
		}
	}
	for _, q := range []string{"INSERT INTO t VALUES(9,1,2,NULL) RETURNING id", "UPDATE t x SET a=1 RETURNING x.a", "UPDATE t AS x SET a=1 RETURNING x.a"} {
		if _, err = db.Exec(q); err == nil {
			t.Fatal("excluded feature admitted", q)
		}
	}
}

func TestUpdateReturningControlsSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, q := range []string{"CREATE SEQUENCE s START 101 CACHE 1", "CREATE TABLE t(id INT NOT NULL DEFAULT 9 CHECK(id>0),b BYTEA,n NUMERIC(8,2) DEFAULT 12.30,v VARCHAR(7) DEFAULT 'word')", "COMMENT ON TABLE t IS 'retained'", "CREATE VIEW live AS SELECT id,b FROM t", "INSERT INTO t(id,b) VALUES(1,'\\x0102'),(2,'\\x0304'),(2,'\\x0304')"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	exec := func(q string) {
		t.Helper()
		if _, e := raw.(driver.ExecerContext).ExecContext(ctx, q, nil); e != nil {
			t.Fatal(e)
		}
	}
	exec("BEGIN")
	saved, err := raw.(driver.QueryerContext).QueryContext(ctx, "UPDATE t SET b='\\x0506' WHERE id=2 RETURNING old.b,new.b,nextval('s')", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	var private bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &private); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(private.String(), "0506") || !strings.Contains(private.String(), "0304") {
		t.Fatal("private replacement leaked", private.String())
	}
	exec("ROLLBACK")
	rows, err := db.Query("SELECT id,b FROM t ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), []byte{1, 2}}, {int64(2), []byte{3, 4}}, {int64(2), []byte{3, 4}}})
	exec("BEGIN")
	exec("UPDATE t SET b='\\x0708' WHERE id=2 RETURNING old.b,new.b")
	exec("COMMIT")
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"retained", "NOT NULL", "DEFAULT", "CHECK", "live"} {
		if !strings.Contains(dump.String(), text) {
			t.Fatal("missing persistent object", text, dump.String())
		}
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT id,b FROM live ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), []byte{1, 2}}, {int64(2), []byte{7, 8}}, {int64(2), []byte{7, 8}}})
	rows, err = db.Query("UPDATE t SET id=id WHERE id=1 RETURNING old.n,new.n,old.v,new.v")
	returningSmokeRows(t, rows, err, []string{"n", "n", "v", "v"}, []string{"NUMERIC", "NUMERIC", "VARCHAR", "VARCHAR"}, [][]any{{"12.30", "12.30", "word", "word"}})
	rows, err = db.Query("UPDATE t SET id=id WHERE FALSE RETURNING old.n,new.n,old.v,new.v")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 1} {
		if p, s, known := metadata[i].DecimalSize(); !known || p != 8 || s != 2 {
			t.Fatal(i, p, s, known)
		}
	}
	for _, i := range []int{2, 3} {
		if length, known := metadata[i].Length(); !known || length != 7 {
			t.Fatal(i, length, known)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("empty restored metadata query", rows.Err())
	}
	rows.Close()
	if !reflect.DeepEqual(saved.Columns(), []string{"b", "b", "nextval"}) {
		t.Fatal(saved.Columns())
	}
	dst := make([]driver.Value, 3)
	for _, draw := range []int64{101, 102} {
		if err = saved.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{[]byte{3, 4}, []byte{5, 6}, draw}) {
			t.Fatal(dst, err)
		}
		dst[0].([]byte)[0] = 99
		dst[1].([]byte)[0] = 99
	}
	if err = saved.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	var next int64
	if err = db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 103 {
		t.Fatal(next, err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("UPDATE t SET id=1 RETURNING *;")); err == nil {
		t.Fatal("Restore accepted mutation history")
	}
	rows, err = db.Query("SELECT id FROM t ORDER BY id")
	returningSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}})
	if _, err = db.Exec("INSERT INTO t(b) VALUES(NULL)"); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT id FROM t WHERE id=9")
	returningSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
}
