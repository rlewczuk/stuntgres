package stuntgres_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestBinaryBasicDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA,s BYTEA)")
	f := "No function matches the given name and argument types. You might need to add explicit type casts."
	o := "No operator matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct{ q, code, msg, pos, hint string }{
		{"SELECT length(1)", "42883", "function length(integer) does not exist", "8", f},
		{"SELECT length()", "42883", "function length() does not exist", "8", f},
		{"SELECT bit_length(1)", "42883", "function bit_length(integer) does not exist", "8", f},
		{"SELECT octet_length(TRUE)", "42883", "function octet_length(boolean) does not exist", "8", f},
		{"SELECT length(v,1) FROM t", "42883", "function length(bytea, integer) does not exist", "8", f},
		{"SELECT v || 1 FROM t", "42883", "operator does not exist: bytea || integer", "10", o},
		{"SELECT 1 || v FROM t", "42883", "operator does not exist: integer || bytea", "10", o},
		{`SELECT btrim(v,'\q') FROM t`, "22P02", "invalid input syntax for type bytea", "16", ""},
		{`SELECT btrim(v,'\x0') FROM t`, "22023", "invalid hexadecimal data: odd number of digits", "16", ""},
		{`SELECT btrim(v,'\xGG') FROM t`, "22023", `invalid hexadecimal digit: "G"`, "16", ""},
		{"SELECT public.length(v) FROM t", "42883", "function public.length(bytea) does not exist", "8", f},
		{"SELECT missing.length(v) FROM t", "3F000", `schema "missing" does not exist`, "8", ""},
	}
	for _, name := range []string{"btrim", "ltrim", "rtrim"} {
		cases = append(cases, struct{ q, code, msg, pos, hint string }{"SELECT " + name + "(v) FROM t", "42883", "function " + name + "(bytea) does not exist", "8", f}, struct{ q, code, msg, pos, hint string }{"SELECT " + name + "(v,1) FROM t", "42883", "function " + name + "(bytea, integer) does not exist", "8", f}, struct{ q, code, msg, pos, hint string }{"SELECT " + name + "(v,s,s) FROM t", "42883", "function " + name + "(bytea, bytea, bytea) does not exist", "8", f})
	}
	for _, populated := range []bool{false, true} {
		if populated {
			execSQL(t, db, "INSERT INTO t VALUES(NULL,NULL)")
		}
		for _, tc := range cases {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + tc.q
				pos, _ := strconv.Atoi(tc.pos)
				if prefix != "" {
					pos += 6
				}
				want := stuntgres.Error{Code: tc.code, Message: tc.msg, Position: strconv.Itoa(pos), Hint: tc.hint}
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, want)
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
					t.Fatal("failed query exposed cursor", q)
				}
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed prepare exposed statement", q)
				}
				typeCoreDiagnostic(t, err, want)
			}
		}
	}
}

func TestBinaryBasicAuthorization(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE ROLE binary_reader", "CREATE TABLE t(v BYTEA,s BYTEA)", `INSERT INTO t VALUES('\xff','\x00')`, "CREATE SCHEMA binary_private", "CREATE TABLE binary_private.t(v BYTEA,s BYTEA)", "SET ROLE binary_reader"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, private := range []bool{false, true} {
		q := "SELECT length(v),btrim(v,s),v||s FROM public.t"
		want := stuntgres.Error{Code: "42501", Message: "permission denied for table t"}
		if private {
			q = "SELECT length(v),btrim(v,s),v||s FROM binary_private.t"
			want = stuntgres.Error{Code: "42501", Message: "permission denied for schema binary_private", Position: strconv.Itoa(strings.Index(q, "binary_private") + 1)}
		}
		rows, err := conn.QueryContext(ctx, q)
		if rows != nil {
			rows.Close()
			t.Fatal("denied cursor")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := conn.PrepareContext(ctx, q)
		if private {
			if stmt != nil {
				stmt.Close()
				t.Fatal("denied statement")
			}
			typeCoreDiagnostic(t, err, want)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			rows, err = stmt.QueryContext(ctx)
			stmt.Close()
			if rows != nil {
				rows.Close()
				t.Fatal("denied prepared cursor")
			}
			typeCoreDiagnostic(t, err, want)
		}
	}
	if _, err := conn.ExecContext(ctx, "RESET ROLE"); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := conn.QueryRowContext(ctx, "SELECT length(v) FROM t").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
