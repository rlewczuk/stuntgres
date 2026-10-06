package stuntgres_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

// Root's pinned18 parameter review oracle independently confirmed these values.
// A later cast converts a known character occurrence, not its shared slot.
func TestCharacterReviewKnownParameterCasts(t *testing.T) {
	cases := []struct {
		source, target string
		outputs        []any
	}{
		{"bpchar", "text", []any{"a", "é🙂", "", nil, "z"}},
		{"bpchar", "varchar(2)", []any{"a", "é🙂", "", nil, "z"}},
		{"text", "bpchar(3)", []any{"a  ", "é🙂 ", "   ", nil, "z  "}},
		{"varchar", "bpchar(3)", []any{"a  ", "é🙂 ", "   ", nil, "z  "}},
		{"text", "varchar(2)", []any{"a ", "é🙂", "", nil, "z "}},
		{"varchar", "text", []any{"a  ", "é🙂  ", "", nil, "z "}},
	}
	inputs := []any{"a  ", "é🙂  ", "", nil, "z "}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.source+"/"+tc.target+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE seed(i INT)")
				execSQL(t, db, "INSERT INTO seed VALUES(1)")
				q := "SELECT CAST($1 AS " + tc.source + "),CAST($1 AS " + tc.target + ") FROM seed"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				for i, input := range inputs {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.Query(input)
					} else {
						rows, err = db.Query(q, input)
					}
					if err != nil {
						t.Fatal(q, input, err)
					}
					characterReviewParameterRows(t, rows, []any{input, tc.outputs[i]})
				}
			})
		}
	}
}

func characterReviewParameterRows(t *testing.T, rows *sql.Rows, want []any) {
	t.Helper()
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing parameter row", rows.Err())
	}
	var a, b any
	if err := rows.Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]any{a, b}, want) {
		t.Fatal("parameter values", []any{a, b}, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected parameter row/error", rows.Err())
	}
}

func TestCharacterReviewParameterComparisonComposition(t *testing.T) {
	for _, tc := range []struct {
		query  string
		values [][]any
	}{
		{"SELECT CAST($1 AS bpchar)='a',CAST($1 AS text)='a ' FROM t", [][]any{{true, false}, {true, false}, {true, false}, {nil, nil}}},
		{"SELECT CAST($1 AS text)='a ',CAST($1 AS bpchar)='a' FROM t", [][]any{{false, true}, {true, true}, {false, true}, {nil, nil}}},
		{"SELECT c=$1,CAST($1 AS text) FROM t", [][]any{{true, "a"}, {true, "a"}, {true, "a"}, {nil, nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.query+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(c CHAR(2),v VARCHAR(2))")
				execSQL(t, db, "INSERT INTO t VALUES('a','a ')")
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = db.Prepare(tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				for i, input := range []any{"a  ", "a ", "a", nil} {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.Query(input)
					} else {
						rows, err = db.Query(tc.query, input)
					}
					if err != nil {
						t.Fatal(tc.query, input, err)
					}
					characterReviewParameterRows(t, rows, tc.values[i])
				}
			})
		}
	}
}

func TestCharacterReviewDiagnosticNamesAndUnknownOccurrences(t *testing.T) {
	hint := "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, populated := range []bool{false, true} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name()+strconv.FormatBool(populated))
		execSQL(t, db, "CREATE TABLE t(c CHAR(2),v VARCHAR(2))")
		if populated {
			execSQL(t, db, "INSERT INTO t VALUES('a','a ')")
		}
		for _, tc := range []struct {
			query, token string
			want         stuntgres.Error
			args         []any
		}{
			{"SELECT c=1 FROM t", "=", stuntgres.Error{Code: "42883", Message: "operator does not exist: character = integer", Hint: hint}, nil},
			{"SELECT 1=c FROM t", "=", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = character", Hint: hint}, nil},
			{"SELECT v=1 FROM t", "=", stuntgres.Error{Code: "42883", Message: "operator does not exist: character varying = integer", Hint: hint}, nil},
			{"SELECT TRUE=v FROM t", "=", stuntgres.Error{Code: "42883", Message: "operator does not exist: boolean = character varying", Hint: hint}, nil},
			{"/*é🙂*/ SELECT c=1 FROM t", "=", stuntgres.Error{Code: "42883", Message: "operator does not exist: character = integer", Hint: hint}, nil},
			{"SELECT c FROM t WHERE c", "WHERE c", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type character"}, nil},
			{"SELECT v FROM t WHERE v", "WHERE v", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type character varying"}, nil},
			{"SELECT c AND TRUE FROM t", "c AND", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type character"}, nil},
			{"SELECT v AND TRUE FROM t", "v AND", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type character varying"}, nil},
			{"SELECT NOT c FROM t", "c FROM", stuntgres.Error{Code: "42804", Message: "argument of NOT must be type boolean, not type character"}, nil},
			{"SELECT FALSE OR v FROM t", "v FROM", stuntgres.Error{Code: "42804", Message: "argument of OR must be type boolean, not type character varying"}, nil},
			{"SELECT $1,c=$1 FROM t", "$1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character versus text"}, []any{"a"}},
			{"SELECT $1,CAST($1 AS bpchar) FROM t", "$1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character versus text"}, []any{"a"}},
			{"SELECT $1,CAST($1 AS varchar) FROM t", "$1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text"}, []any{"a"}},
			{"SELECT num_nulls($1),CAST($1 AS bpchar) FROM t", "$1", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1"}, []any{"a"}},
			{"SELECT num_nulls($1),CAST($1 AS varchar) FROM t", "$1", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1"}, []any{"a"}},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(strconv.FormatBool(populated)+"/"+tc.query+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
					want := tc.want
					offset := strings.Index(tc.query, tc.token)
					if strings.HasPrefix(tc.token, "WHERE ") {
						offset += len("WHERE ")
					}
					want.Position = strconv.Itoa(utf8.RuneCountInString(tc.query[:offset]) + 1)
					var err error
					if prepared {
						stmt, e := db.Prepare(tc.query)
						err = e
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid analysis exposed handle")
						}
					} else {
						rows, e := db.Query(tc.query, tc.args...)
						err = e
						if rows != nil {
							rows.Close()
							t.Fatal("invalid analysis exposed rows")
						}
					}
					typeCoreDiagnostic(t, err, want)
				})
			}
		}
	}
}
