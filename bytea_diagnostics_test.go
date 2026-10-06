package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestByteaDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA)")
	for _, tc := range []struct{ q, code, message, pos string }{
		{`INSERT INTO t VALUES('\x0')`, "22023", "invalid hexadecimal data: odd number of digits", "22"},
		{`INSERT INTO t VALUES('\xGG')`, "22023", `invalid hexadecimal digit: "G"`, "22"},
		{`INSERT INTO t VALUES('\x0 0')`, "22023", `invalid hexadecimal digit: " "`, "22"},
		{"INSERT INTO t VALUES('\\x\f00')", "22023", "invalid hexadecimal digit: \"\f\"", "22"},
		{"INSERT INTO t VALUES('\\x\v00')", "22023", "invalid hexadecimal digit: \"\v\"", "22"},
		{`INSERT INTO t VALUES('\q')`, "22P02", "invalid input syntax for type bytea", "22"},
		{`/*é*/ INSERT INTO t VALUES('\q')`, "22P02", "invalid input syntax for type bytea", "28"},
		{`INSERT INTO t VALUES('\X41')`, "22P02", "invalid input syntax for type bytea", "22"},
		{`INSERT INTO t VALUES('\400')`, "22P02", "invalid input syntax for type bytea", "22"},
		{`INSERT INTO t VALUES('\08')`, "22P02", "invalid input syntax for type bytea", "22"},
		{`INSERT INTO t VALUES('\00')`, "22P02", "invalid input syntax for type bytea", "22"},
		{`INSERT INTO t VALUES('\')`, "22P02", "invalid input syntax for type bytea", "22"},
		{`CREATE TABLE bad(v BYTEA(1))`, "42601", `type modifier is not allowed for type "bytea"`, "20"},
		{`CREATE TABLE bad(v "bytea"(1))`, "42601", `type modifier is not allowed for type "bytea"`, "20"},
		{`/*é*/ CREATE TABLE bad(v BYTEA(1))`, "42601", `type modifier is not allowed for type "bytea"`, "26"},
		{`SELECT v FROM t WHERE v='\q'`, "22P02", "invalid input syntax for type bytea", "25"},
	} {
		want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.pos}
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, want)
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
		}
		typeCoreDiagnostic(t, err, want)
		s, err := db.Prepare(tc.q)
		if tc.code == "42601" {
			// CREATE Prepare is metadata-only; semantic type checks run at Execute.
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Exec()
		}
		if s != nil {
			s.Close()
		}
		typeCoreDiagnostic(t, err, want)
	}
	want := stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "bytea versus text", Position: "8"}
	rows, err := db.Query("SELECT $1 FROM t WHERE v=$1", []byte{65})
	if rows != nil {
		rows.Close()
	}
	typeCoreDiagnostic(t, err, want)
	s, err := db.Prepare("SELECT $1 FROM t WHERE v=$1")
	if s != nil {
		s.Close()
	}
	typeCoreDiagnostic(t, err, want)
}
