package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinarySliceHeterogeneousNullAndOptionalCounts(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		query string
		want  any
	}{
		{"SELECT overlay(NULL::bytea,'X'::bytea,0,1)", nil},
		{"SELECT overlay('abc'::bytea,NULL::bytea,0,1)", nil},
		{"SELECT overlay('abc'::bytea,'X'::bytea,NULL::integer,1)", nil},
		{"SELECT overlay('abc'::bytea,'X'::bytea,0,NULL::integer)", nil},
		{"SELECT substring(NULL::bytea,1,-1)", nil},
		{"SELECT substring('abc'::bytea,NULL::integer,-1)", nil},
		{"SELECT substring('abc'::bytea,1,NULL::integer)", nil},
		{"SELECT substr(NULL::bytea,1,-1)", nil},
		{"SELECT substr('abc'::bytea,NULL::integer,-1)", nil},
		{"SELECT substr('abc'::bytea,1,NULL::integer)", nil},
		{`SELECT overlay('abcdef'::bytea PLACING '\xc3a9'::bytea FROM 3)`, []byte{'a', 'b', 195, 169, 'e', 'f'}},
		{"SELECT overlay('abcdef'::bytea,''::bytea,3)", []byte("abcdef")},
		{"SELECT overlay('abcdef'::bytea,''::bytea,2147483647)", []byte("abcdef")},
		{"SELECT overlay('abcdef'::bytea,'X'::bytea,2147483647,0)", []byte("abcdefX")},
	} {
		wantTuples(t, db, tc.query, [][]any{{tc.want}})
		stmt, err := db.Prepare(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		var got any
		err = stmt.QueryRow().Scan(&got)
		stmt.Close()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.query, got, tc.want, err)
		}
	}
	for _, q := range []string{"SELECT overlay('abcdef'::bytea,'X'::bytea,2147483647)", "SELECT overlay('abcdef'::bytea,''::bytea,2147483647,1)"} {
		want := stuntgres.Error{Code: "22003", Message: "integer out of range"}
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		stmt.Close()
		if rows != nil {
			rows.Close()
			t.Fatal("failed cursor")
		}
		typeCoreDiagnostic(t, err, want)
	}
}

func TestBinarySliceForOnlyInputPhase(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA)")
	for _, populated := range []bool{false, true} {
		if populated {
			execSQL(t, db, "INSERT INTO t VALUES(NULL),('abcdef')")
		}
		q := "SELECT substring(v FOR '1.5') FROM t"
		want := stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "1.5"`, Position: "24"}
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("analysis error exposed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("analysis error exposed statement")
		}
		typeCoreDiagnostic(t, err, want)
	}
}

func TestBinarySliceLateOverlayAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'abcdef','XY',1,2),(2,'abcdef','XY',2147483647,1)")
	execSQL(t, db, "CREATE TABLE dst(v BYTEA)")
	execSQL(t, db, "INSERT INTO dst VALUES('sentinel')")
	for _, q := range []string{"INSERT INTO dst SELECT overlay(v,s,i,n) FROM t ORDER BY id", "UPDATE t SET v=overlay(v PLACING s FROM i FOR n) RETURNING v", "DELETE FROM t RETURNING overlay(v,s,i,n)"} {
		want := stuntgres.Error{Code: "22003", Message: "integer out of range"}
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed write exposed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		stmt.Close()
		if rows != nil {
			rows.Close()
			t.Fatal("failed prepared write exposed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		wantTuples(t, db, "SELECT v FROM dst", [][]any{{[]byte("sentinel")}})
		wantTuples(t, db, "SELECT id,v FROM t ORDER BY id", [][]any{{int64(1), []byte("abcdef")}, {int64(2), []byte("abcdef")}})
	}
}

func TestBinarySliceOrdinaryPositionReplay(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := context.Background()
	for _, tc := range []struct{ name, callee string }{{"quoted", `"position"`}, {"qualified", "pg_catalog.position"}} {
		execSQL(t, db, "CREATE TABLE p_"+tc.name+"(v BYTEA,s BYTEA,CONSTRAINT ck_"+tc.name+" CHECK("+tc.callee+"(v,s)>0))")
		execSQL(t, db, "INSERT INTO p_"+tc.name+" VALUES('abcdef','cd')")
		wantTuples(t, db, "SELECT pg_get_expr(conbin,conrelid),pg_get_expr(conbin,conrelid,true) FROM pg_constraint WHERE conname='ck_"+tc.name+"'", [][]any{{`("position"(v, s) > 0)`, `"position"(v, s) > 0`}})
	}
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
	for _, name := range []string{"quoted", "qualified"} {
		wantTuples(t, db, "SELECT pg_catalog.position(v,s) FROM p_"+name, [][]any{{int64(3)}})
		_, err := db.Exec("INSERT INTO p_" + name + " VALUES('abcdef','zz')")
		var diagnostic *stuntgres.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" {
			t.Fatal(err)
		}
	}
}
