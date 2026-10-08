package stuntgres_test

import (
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinarySliceSemanticSpelling(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA,s BYTEA,i INTEGER,n INTEGER)")
	execSQL(t, db, "INSERT INTO t VALUES('abcdef','cd',3,2),('abcdef','cd',3,2)")
	for _, tc := range []struct {
		query string
		want  [][]any
	}{
		{"SELECT overlay(v PLACING s FROM i FOR n),count(*) FROM t GROUP BY overlay(v,s,i,n) HAVING overlay(v PLACING s FROM i FOR n)='abcdef'::bytea", [][]any{{[]byte("abcdef"), int64(2)}}},
		{"SELECT position(s IN v),count(*) FROM t GROUP BY pg_catalog.position(v,s) HAVING position(s IN v)=3", [][]any{{int64(3), int64(2)}}},
		{"SELECT overlay(v PLACING s FROM i FOR n) AS same,overlay(v,s,i,n) AS same FROM t ORDER BY same", [][]any{{[]byte("abcdef"), []byte("abcdef")}, {[]byte("abcdef"), []byte("abcdef")}}},
		{"SELECT position(s IN v) AS same,pg_catalog.position(v,s) AS same FROM t ORDER BY same", [][]any{{int64(3), int64(3)}, {int64(3), int64(3)}}},
	} {
		wantTuples(t, db, tc.query, tc.want)
		stmt, err := db.Prepare(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		rows, err := stmt.Query()
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		var got [][]any
		for rows.Next() {
			values := make([]any, len(tc.want[0]))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			got = append(got, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		stmt.Close()
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.query, got, tc.want)
		}
	}
}

func TestBinarySliceUnaryMissingSignatures(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ query, name, typ string }{
		{"SELECT substr('abc'::text)", "substr", "text"},
		{"SELECT substr('abc')", "substr", "unknown"},
		{"SELECT substring('abc'::text)", "substring", "text"},
		{"SELECT substring('abc')", "substring", "unknown"},
		{"SELECT pg_catalog.position('abc'::text)", "pg_catalog.position", "text"},
		{"SELECT pg_catalog.position('abc')", "pg_catalog.position", "unknown"},
		{`SELECT "position"('abc'::text)`, "position", "text"},
		{`SELECT "position"('abc')`, "position", "unknown"},
	} {
		want := stuntgres.Error{Code: "42883", Message: "function " + tc.name + "(" + tc.typ + ") does not exist", Position: "8", Hint: hint}
		rows, err := db.Query(tc.query)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(tc.query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed statement")
		}
		typeCoreDiagnostic(t, err, want)
	}
}
