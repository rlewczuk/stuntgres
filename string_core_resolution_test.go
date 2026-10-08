package stuntgres_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestStringCoreReviewResolution(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE sc_resolution(v TEXT,n INT)")
	execSQL(t, db, "INSERT INTO sc_resolution VALUES('sentinel',7)")
	char := "CASE WHEN false THEN relkind ELSE $1 END"
	for _, tc := range []struct{ left, right, leftType, rightType string }{
		{"'x'", char, "unknown", `"char"`}, {char, "'x'", `"char"`, "unknown"},
		{"NULL", char, "unknown", `"char"`}, {char, "NULL", `"char"`, "unknown"},
		{"$2", char, "unknown", `"char"`}, {char, "$2", `"char"`, "unknown"},
		{char, "relkind", `"char"`, `"char"`}, {"relkind", char, `"char"`, `"char"`},
		{"'x'::name", char, "name", `"char"`}, {char, "'x'::name", `"char"`, "name"},
		{"'x'::varchar", char, "character varying", `"char"`}, {char, "'x'::varchar", `"char"`, "character varying"},
		{"'x'::char(3)", char, "character", `"char"`}, {char, "'x'::char(3)", `"char"`, "character"},
	} {
		q := "SELECT " + tc.left + "||" + tc.right + " AS v FROM pg_catalog.pg_class WHERE relname='sc_resolution'"
		position := strconv.Itoa(len([]rune("SELECT "+tc.left)) + 1)
		want := stuntgres.Error{Code: "42725", Message: "operator is not unique: " + tc.leftType + " || " + tc.rightType, Position: position, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}
		for _, value := range []any{"A", nil} {
			t.Run(fmt.Sprintf("%s/value=%v", tc.left+"||"+tc.right, value), func(t *testing.T) {
				args := []any{value}
				if strings.Contains(q, "$2") {
					args = append(args, "x")
				}
				result, err := db.Exec(q, args...)
				if result != nil {
					t.Fatal("analysis failure exposed result")
				}
				typeCoreDiagnostic(t, err, want)
				s, err := db.Prepare(q)
				if s != nil {
					s.Close()
					t.Fatal("ambiguity exposed prepared statement")
				}
				typeCoreDiagnostic(t, err, want)
			})
		}
	}
	// Exact root pinned18 records (artifact110); an explicit cast occurrence is
	// not automatically an inconsistent-parameter error.
	for _, tc := range []struct {
		q    string
		args []any
		want string
	}{
		{"SELECT $1||$2 AS v", []any{"x", "y"}, "xy"},
		{"SELECT $1||42 AS v", []any{"x"}, "x42"},
		{"SELECT $1::integer||$1::text AS v", []any{42}, "4242"},
		{"SELECT $1::text||$1::integer AS v", []any{"42"}, "4242"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("known/%s/prepared=%t", tc.q, prepared), func(t *testing.T) {
				if prepared {
					s, err := db.Prepare(tc.q)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					rows, err := s.Query(tc.args...)
					queryCoreRows(t, rows, err, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, false)
				} else {
					rows, err := db.Query(tc.q, tc.args...)
					queryCoreRows(t, rows, err, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, false)
				}
			})
		}
	}
	for _, tc := range []struct {
		q    string
		args []any
		want stuntgres.Error
	}{
		// Stringless assignment inference retains the existing native boundary.
		{"INSERT INTO sc_resolution(v,n) VALUES($1,$1)", []any{"42"}, stuntgres.Error{Code: "0A000", Message: "parameter reused with different destination types"}},
		{"INSERT INTO sc_resolution(n,v) VALUES($1,$1)", []any{42}, stuntgres.Error{Code: "0A000", Message: "parameter reused with different destination types"}},
		{"INSERT INTO sc_resolution(v,n) VALUES($1||'x',$1)", []any{"42"}, stuntgres.Error{Code: "42804", Message: `column "n" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "47"}},
		{"INSERT INTO sc_resolution(n,v) VALUES($1,$1||'x')", []any{42}, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "text versus integer", Position: "39"}},
		{"SELECT $1||'x' AS v FROM sc_resolution WHERE n=$1", []any{"42"}, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "47"}},
		{"SELECT btrim($1)||'x' AS v FROM sc_resolution WHERE n=$1", []any{"42"}, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "54"}},
		{"SELECT btrim($1,$1::varchar) AS v", []any{"x"}, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text", Position: "14"}},
		{"SELECT pg_catalog.normalize($1,$1::varchar) AS v", []any{"NFC"}, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text", Position: "29"}},
		{"SELECT CASE WHEN false THEN $1::integer ELSE 42 END||$1 AS v", []any{42}, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer || integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "52"}},
	} {
		t.Run("parameter/"+tc.q, func(t *testing.T) {
			execSQL(t, db, "DROP TABLE sc_resolution")
			execSQL(t, db, "CREATE TABLE sc_resolution(v TEXT,n INT)")
			execSQL(t, db, "INSERT INTO sc_resolution VALUES('sentinel',7)")
			result, err := db.Exec(tc.q, tc.args...)
			if result != nil {
				t.Fatal("analysis failure exposed result")
			}
			typeCoreDiagnostic(t, err, tc.want)
			s, err := db.Prepare(tc.q)
			if s != nil {
				s.Close()
				t.Fatal("conflicting occurrence exposed prepared statement")
			}
			typeCoreDiagnostic(t, err, tc.want)
			wantTuples(t, db, "SELECT v,n FROM sc_resolution", [][]any{{"sentinel", int64(7)}})
		})
	}
}
