package stuntgres_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestStringCoreDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		q       string
		want    stuntgres.Error
		prepare bool
	}{
		{"SELECT btrim(1)", stuntgres.Error{Code: "42883", Message: "function btrim(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT trim(1)", stuntgres.Error{Code: "42883", Message: "function pg_catalog.btrim(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT normalize(1)", stuntgres.Error{Code: "42883", Message: "function pg_catalog.normalize(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT 1 IS NORMALIZED", stuntgres.Error{Code: "42883", Message: "function pg_catalog.is_normalized(integer) does not exist", Position: "10", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT 1 || 2", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer || integer", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT normalize('x','NFC')", stuntgres.Error{Code: "42601", Message: "syntax error at or near \"'NFC'\"", Position: "22", Hint: ""}, true},
		{"SELECT normalize('x', BAD)", stuntgres.Error{Code: "42601", Message: "syntax error at or near \"BAD\"", Position: "23", Hint: ""}, true},
		{"SELECT 'x' IS BAD NORMALIZED", stuntgres.Error{Code: "42601", Message: "syntax error at or near \"BAD\"", Position: "15", Hint: ""}, true},
		{"SELECT \"normalize\"('x','bad')", stuntgres.Error{Code: "22023", Message: "invalid normalization form: bad", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x','bad')", stuntgres.Error{Code: "22023", Message: "invalid normalization form: bad", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x',' NFC')", stuntgres.Error{Code: "22023", Message: "invalid normalization form:  NFC", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x','')", stuntgres.Error{Code: "22023", Message: "invalid normalization form: ", Position: "", Hint: ""}, false},
		{"SELECT btrim(NULL,1)", stuntgres.Error{Code: "42883", Message: "function btrim(unknown, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT btrim()", stuntgres.Error{Code: "42883", Message: "function btrim() does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT btrim('x','y','z')", stuntgres.Error{Code: "42883", Message: "function btrim(unknown, unknown, unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT trim('x','y','z')", stuntgres.Error{Code: "42883", Message: "function pg_catalog.btrim(unknown, unknown, unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT pg_catalog.trim('x')", stuntgres.Error{Code: "42883", Message: "function pg_catalog.trim(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT \"trim\"('x')", stuntgres.Error{Code: "42883", Message: "function trim(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
	} {
		t.Run(tc.q, func(t *testing.T) {
			_, err := db.Exec(tc.q)
			typeCoreDiagnostic(t, err, tc.want)
			stmt, err := db.Prepare(tc.q)
			if tc.prepare {
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed Prepare returned statement")
				}
				typeCoreDiagnostic(t, err, tc.want)
				return
			}
			if err != nil {
				t.Fatal("runtime error at Prepare", err)
			}
			defer stmt.Close()
			_, err = stmt.Exec()
			typeCoreDiagnostic(t, err, tc.want)
		})
	}
	_, err := db.Exec("/*é🙂*/ SELECT 1 || 2")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer || integer", Position: "17", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	stmt, err := db.Prepare(`SELECT "normalize"($1,$2)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec("x", "bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "invalid normalization form: bad"})
	for _, tc := range []struct{ input, bytes string }{{string([]byte{0}), "0x00"}, {string([]byte{255}), "0xff"}} {
		_, err = stmt.Exec(tc.input, "NFC")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes, Context: "unnamed portal parameter $1"})
	}
}
func TestStringCoreCancellationAndEncoding(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	var encoding string
	if err := db.QueryRow("SHOW server_encoding").Scan(&encoding); err != nil || encoding != "UTF8" {
		t.Fatal(encoding, err)
	}
	for _, q := range []string{"SET server_encoding='LATIN1'", "RESET server_encoding"} {
		_, err := db.Exec(q)
		sqlState(t, err, "55P02")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := db.QueryContext(ctx, "SELECT normalize('é')")
	if rows != nil {
		rows.Close()
		t.Fatal("cancelled query exposed cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("context error became SQL error", err)
	}
	stmt, err := db.PrepareContext(ctx, "SELECT normalize($1)")
	if stmt != nil {
		stmt.Close()
		t.Fatal("cancelled prepare exposed statement")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStringCoreArgumentLimitAndExcludedArray(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, name := range []string{"btrim", "ltrim", "rtrim", "is_normalized", `"normalize"`} {
		q := "SELECT " + name + "(" + strings.Repeat("'x',", 100) + "'x')"
		want := stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, want)
		s, err := db.Prepare(q)
		if s != nil {
			s.Close()
			t.Fatal("failed analysis exposed statement")
		}
		typeCoreDiagnostic(t, err, want)
	}
	for _, tc := range []struct{ q, message, position string }{
		{"SELECT 'x'::text||current_schemas(false)", "operator does not exist: text || name[]", "17"},
		{"SELECT current_schemas(false)||'x'::text", "operator does not exist: name[] || text", "30"},
	} {
		// Native capability boundary only: PostgreSQL has out-of-scope array operators.
		want := stuntgres.Error{Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, want)
		s, err := db.Prepare(tc.q)
		if s != nil {
			s.Close()
			t.Fatal("array concat exposed statement")
		}
		typeCoreDiagnostic(t, err, want)
	}
	_, err := db.Exec("SELECT 'x'::text||setseed(2)")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "setseed parameter 2 is out of allowed range [-1,1]"})
}
