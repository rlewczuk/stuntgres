package stuntgres_test

import (
	"strconv"
	"stuntgres"
	"testing"
)

func TestBinarySliceDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA,s BYTEA)")
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, populated := range []bool{false, true} {
		if populated {
			execSQL(t, db, "INSERT INTO t VALUES(NULL,NULL)")
		}
		for _, tc := range []struct {
			q, code, msg string
			pos          int
			hint         string
		}{
			{"SELECT substring(v FROM TRUE) FROM t", "42883", "function pg_catalog.substring(bytea, boolean) does not exist", 8, hint},
			{"SELECT substring(v,TRUE) FROM t", "42883", "function substring(bytea, boolean) does not exist", 8, hint},
			{"SELECT trim(v) FROM t", "42883", "function pg_catalog.btrim(bytea) does not exist", 8, hint},
			{"SELECT substr(v,'bad') FROM t", "22P02", `invalid input syntax for type integer: "bad"`, 17, ""},
			{"SELECT substring(v FROM 'bad') FROM t", "22P02", `invalid input syntax for type integer: "bad"`, 25, ""},
			{`SELECT overlay(v PLACING '\q' FROM 1) FROM t`, "22P02", "invalid input syntax for type bytea", 26, ""},
			{"SELECT position(v,s) FROM t", "42601", `syntax error at or near ","`, 18, ""},
			{"SELECT substr(v,1::bigint) FROM t", "42883", "function substr(bytea, bigint) does not exist", 8, hint},
			{"SELECT pg_catalog.trim(v,s) FROM t", "42883", "function pg_catalog.trim(bytea, bytea) does not exist", 8, hint},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				pos := tc.pos
				if prefix != "" {
					pos += 6
				}
				want := stuntgres.Error{Code: tc.code, Message: tc.msg, Position: strconv.Itoa(pos), Hint: tc.hint}
				_, err := db.Exec(prefix + tc.q)
				typeCoreDiagnostic(t, err, want)
				rows, err := db.Query(prefix + tc.q)
				if rows != nil {
					rows.Close()
					t.Fatal("error exposed cursor")
				}
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(prefix + tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("error exposed statement")
				}
				typeCoreDiagnostic(t, err, want)
			}
		}
	}
}

func TestBinarySliceDemandAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)")
	for _, tc := range []struct{ q, code, msg string }{
		{"SELECT substr(v,i,n) FROM t", "22011", "negative substring length not allowed"},
		{"SELECT overlay(v PLACING s FROM i FOR n) FROM t", "22003", "integer out of range"},
	} {
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || types[0].DatabaseTypeName() != "BYTEA" {
			t.Fatal(types, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
		stmt.Close()
	}
	execSQL(t, db, "INSERT INTO t VALUES(1,'abcdef','XY',1,2),(2,'abcdef','XY',2,-1)")
	execSQL(t, db, "CREATE TABLE dst(v BYTEA)")
	execSQL(t, db, "INSERT INTO dst VALUES('sentinel')")
	for _, q := range []string{"INSERT INTO dst SELECT substr(v,i,n) FROM t ORDER BY id", "UPDATE t SET v=substring(v FROM i FOR n) RETURNING v", "DELETE FROM t RETURNING substr(v,i,n)"} {
		want := stuntgres.Error{Code: "22011", Message: "negative substring length not allowed"}
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("published failed cursor")
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
			t.Fatal("published prepared failed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		wantTuples(t, db, "SELECT v FROM dst", [][]any{{[]byte("sentinel")}})
		wantTuples(t, db, "SELECT id,v FROM t ORDER BY id", [][]any{{int64(1), []byte("abcdef")}, {int64(2), []byte("abcdef")}})
	}
	for _, tc := range []struct{ q, code, msg string }{{"SELECT overlay(v PLACING s FROM 0) FROM t", "22011", "negative substring length not allowed"}, {"SELECT overlay(v,s,2147483647,1) FROM t", "22003", "integer out of range"}} {
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		stmt.Close()
		if rows != nil {
			rows.Close()
			t.Fatal("failed cursor")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.msg})
	}
}
