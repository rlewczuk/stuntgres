package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestBitwiseDefaultsViewsRestoreAndAtomicity(t *testing.T) {
	_, db := arithmeticDB(t)
	ctx := context.Background()
	arithmeticExec(t, db, "CREATE TABLE d(id INTEGER,s SMALLINT DEFAULT (1::smallint << 15),i INTEGER DEFAULT (~0),b BIGINT DEFAULT (1::bigint << 63),n SMALLINT DEFAULT (~NULL::smallint),w BIGINT DEFAULT (32767::smallint << 1::smallint))", 0)
	arithmeticExec(t, db, "INSERT INTO d(id) VALUES(1)", 1)
	arithmeticExec(t, db, "INSERT INTO d VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
	arithmeticExec(t, db, "INSERT INTO d VALUES(3,NULL,NULL,NULL,NULL,NULL)", 1)
	arithmeticExec(t, db, "CREATE VIEW masks AS SELECT id,~s AS ns,s << 32 AS ss,s & b AS mixed FROM d", 0)
	want := [][]any{{int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {nil, nil, nil, nil, nil}}
	arithmeticRows(t, db, "SELECT s,i,b,n,w FROM d ORDER BY id", []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, want)
	held, err := db.Query("SELECT ns,ss,mixed FROM masks WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var ns, ss, mixed int64
	if err := held.Scan(&ns, &ss, &mixed); err != nil || ns != 32767 || ss != -32768 || mixed != -9223372036854775808 {
		t.Fatal(ns, ss, mixed, err)
	}
	held.Close()
	arithmeticExec(t, db, "INSERT INTO d(id) VALUES(4)", 1)
	arithmeticRows(t, db, "SELECT ns,ss,mixed FROM masks WHERE id=4", []string{"INT2", "INT2", "INT8"}, [][]any{{int64(32767), int64(-32768), int64(-9223372036854775808)}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE failed(i INTEGER); INSERT INTO failed VALUES(1 & 1); INSERT INTO failed VALUES(~true)")); err == nil {
		t.Fatal("failed replacement installed")
	}
	arithmeticRows(t, db, "SELECT s FROM d WHERE id=4", []string{"INT2"}, [][]any{{int64(-32768)}})
	arithmeticExec(t, db, "CREATE TABLE source(id INTEGER,x TEXT)", 0)
	arithmeticExec(t, db, "INSERT INTO source VALUES(1,'1'),(2,'bad')", 2)
	arithmeticExec(t, db, "CREATE TABLE dest(id INTEGER,s SMALLINT)", 0)
	arithmeticExec(t, db, "INSERT INTO dest VALUES(99,99)", 1)
	q := "INSERT INTO dest SELECT id,CAST(x AS INTEGER) & 1 FROM source ORDER BY id"
	for _, prepared := range []bool{false, true} {
		var err error
		if prepared {
			s, e := db.Prepare(q)
			if e != nil {
				t.Fatal(e)
			}
			_, err = s.Exec()
			s.Close()
		} else {
			_, err = db.Exec(q)
		}
		arithmeticError(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		arithmeticRows(t, db, "SELECT id,s FROM dest", []string{"INT4", "INT2"}, [][]any{{int64(99), int64(99)}})
	}
	arithmeticExec(t, db, "INSERT INTO dest VALUES(1,1),(2,2)", 2)
	if _, err := db.Exec("UPDATE dest SET s=CASE WHEN id=2 THEN 1::bigint << 16 ELSE s::bigint & 1 END"); err == nil {
		t.Fatal("partial update published")
	}
	arithmeticRows(t, db, "SELECT id,s FROM dest ORDER BY id", []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(99), int64(99)}})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.ExecContext(canceled, "INSERT INTO dest VALUES(4,1::smallint<<2)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	arithmeticExec(t, db, "INSERT INTO dest VALUES(4,1::smallint<<2)", 1)
}
