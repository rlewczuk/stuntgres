package stuntgres_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestNumericDiagnosticsAndExecutionSizing(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "numeric-diagnostics")
	execSQL(t, db, "CREATE TABLE n(v NUMERIC(3,1))")
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT v FROM n WHERE v=TRUE", stuntgres.Error{Code: "42883", Message: "operator does not exist: numeric = boolean", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"}},
		{"CREATE TABLE bad(v NUMERIC(0))", stuntgres.Error{Code: "22023", Message: "NUMERIC precision 0 must be between 1 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(1001))", stuntgres.Error{Code: "22023", Message: "NUMERIC precision 1001 must be between 1 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,1001))", stuntgres.Error{Code: "22023", Message: "NUMERIC scale 1001 must be between -1000 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,-1001))", stuntgres.Error{Code: "22023", Message: "NUMERIC scale -1001 must be between -1000 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,1,0))", stuntgres.Error{Code: "22023", Message: "invalid NUMERIC type modifier", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(+2))", stuntgres.Error{Code: "42601", Message: "type modifiers must be simple constants or identifiers", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2147483648))", stuntgres.Error{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "20"}},
		{"INSERT INTO n VALUES ('oops')", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "23"}},
		{"SELECT v FROM n WHERE v='oops'", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "25"}},
		{"SELECT 1e131072 FROM n", stuntgres.Error{Code: "22003", Message: "value overflows numeric format", Position: "8"}},
		{"SELECT 1e-16384 FROM n", stuntgres.Error{Code: "22003", Message: "value overflows numeric format", Position: "8"}},
		{"SELECT 1e+ FROM n", stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "1e+"`, Position: "8"}},
		{"SELECT 1.2__3 FROM n", stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "1.2__3"`, Position: "8"}},
		{"SELECT 0x FROM n", stuntgres.Error{Code: "42601", Message: `invalid hexadecimal integer at or near "0x"`, Position: "8"}},
		{"SELECT 1..2 FROM n", stuntgres.Error{Code: "42601", Message: `syntax error at or near ".."`, Position: "9"}},
		{`CREATE TABLE bad(v "decimal")`, stuntgres.Error{Code: "42704", Message: `type "decimal" does not exist`, Position: "20"}},
	} {
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if strings.HasPrefix(tc.q, "CREATE TABLE") {
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.Exec()
			stmt.Close()
		} else if stmt != nil {
			stmt.Close()
			t.Fatal("failed Prepare exposed handle", tc.q)
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
	for _, literal := range []string{"'99.95'", "99.95", "'Infinity'", "'-Infinity'"} {
		q := "INSERT INTO n VALUES (" + literal + ")"
		detail := "A field with precision 3, scale 1 must round to an absolute value less than 10^2."
		if strings.Contains(literal, "Infinity") {
			detail = "A field with precision 3, scale 1 cannot hold an infinite value."
		}
		want := stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: detail}
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal("sizing happened in Prepare", err)
		}
		_, err = stmt.Exec()
		stmt.Close()
		typeCoreDiagnostic(t, err, want)
	}
	for _, literal := range []string{"'+NaN'", "'-NaN'", "'Infinityx'"} {
		q := "INSERT INTO n VALUES (" + literal + ")"
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type numeric: "` + literal[1:len(literal)-1] + `"`, Position: "23"})
	}
	for _, arg := range []string{"oops", "1e-16384"} {
		code, message := "22P02", `invalid input syntax for type numeric: "oops"`
		if arg != "oops" {
			code, message = "22003", "value overflows numeric format"
		}
		_, err := db.Exec("INSERT INTO n VALUES($1)", arg)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: code, Message: message,Context:"unnamed portal parameter $1 = '...'"})
	}
	q := "/*😀é*/ SELECT 1.2__3 FROM n"
	_, err := db.Query(q)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "1.2__3"`, Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "1.2")]) + 1)})
	execSQL(t, db, "SET application_name TO 'before'")
	for _, literal := range []string{"1.5", ".5", "1e2"} {
		_, err := db.Exec("SET application_name TO " + literal)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "non-decimal-integer literal is unsupported", Position: "25"})
		wantTuples(t, db, "SHOW application_name", [][]any{{"before"}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.QueryContext(ctx, "SELECT v FROM n"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestNumericStatementAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "numeric-atomicity")
	execSQL(t, db, "CREATE TABLE n(id INT NOT NULL,v NUMERIC(3,1) NOT NULL,b BIGINT)")
	execSQL(t, db, "INSERT INTO n VALUES(99,9.9,99)")
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"INSERT INTO n VALUES(1,1.2,1),(2,'oops',2)", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "34"}},
		{"INSERT INTO n VALUES(1,1.2,1),(2,99.95,2)", stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}},
		{"INSERT INTO n VALUES(1,1.2,1),(2,1.2,9223372036854775808)", stuntgres.Error{Code: "22003", Message: "bigint out of range"}},
		{"INSERT INTO n VALUES(1,1.2,1),(2,NULL,2)", stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "n" violates not-null constraint`, Schema: "public", Table: "n", Column: "v", Detail: "Failing row contains (2, null, 2)."}},
	} {
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, tc.want)
		wantTuples(t, db, "SELECT * FROM n", [][]any{{int64(99), "9.9", int64(99)}})
	}
}
