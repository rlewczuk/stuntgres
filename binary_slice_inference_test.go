package stuntgres_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestBinarySliceInferenceBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA,s BYTEA)")
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT overlay(v,$1,$1) FROM t", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "bytea versus integer", Position: "21"}},
		{"SELECT substr(v) FROM t", stuntgres.Error{Code: "42883", Message: "function substr(bytea) does not exist", Hint: hint, Position: "8"}},
		{"SELECT overlay(v,s,1,2,3) FROM t", stuntgres.Error{Code: "42883", Message: "function overlay(bytea, bytea, integer, integer, integer) does not exist", Hint: hint, Position: "8"}},
		{"SELECT missing.substr(v,1) FROM t", stuntgres.Error{Code: "3F000", Message: `schema "missing" does not exist`, Position: "8"}},
		{"SELECT substr(v," + strings.Repeat("1,", 99) + "1) FROM t", stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{`SELECT substring(NULL::bytea FROM 'bad') FROM t`, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "35"}},
	} {
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("unexpected statement", tc.q)
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
	for _, q := range []string{"CREATE TABLE d(v BYTEA DEFAULT substr('abc'::bytea,1))", "CREATE TABLE d(v BYTEA DEFAULT overlay('abc'::bytea,'x'::bytea,1))"} {
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "functions are unsupported", Position: "32"})
	}
	execSQL(t, db, "INSERT INTO t VALUES('abcdef','XY')")
	wantTuples(t, db, "SELECT substr(v,VARIADIC 1) FROM t", [][]any{{[]byte("abcdef")}})
	stmt, err := db.Prepare("SELECT substr(v,VARIADIC 1) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var raw []byte
	if err := stmt.QueryRow().Scan(&raw); err != nil || string(raw) != "abcdef" {
		t.Fatal(raw, err)
	}
}
