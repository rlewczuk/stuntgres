//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Independent release-source expectations confirmed by root's pinned18 review
// capture before production fixes, not derived from native output.
func characterParameterReviewValues(t *testing.T, p *referencePair, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct {
		source, target string
		outputs        []any
	}{
		{"bpchar", "text", []any{"a", "é🙂", "", nil, "z"}},
		{"bpchar", "varchar(2)", []any{"a", "é🙂", "", nil, "z"}},
		{"text", "bpchar(3)", []any{"a  ", "é🙂 ", "   ", nil, "z  "}},
		{"varchar", "bpchar(3)", []any{"a  ", "é🙂 ", "   ", nil, "z  "}},
		{"text", "varchar(2)", []any{"a ", "é🙂", "", nil, "z "}},
		{"varchar", "text", []any{"a  ", "é🙂  ", "", nil, "z "}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.source+"/"+tc.target+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				q := "SELECT CAST($1 AS " + tc.source + "),CAST($1 AS " + tc.target + ") FROM seed"
				targetName := strings.Split(tc.target, "(")[0]
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = db.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(q, "prepare", err)
					}
					defer stmt.Close()
				}
				for i, input := range []any{"a  ", "é🙂  ", "", nil, "z "} {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.QueryContext(p.ctx, input)
					} else {
						rows, err = db.QueryContext(p.ctx, q, input)
					}
					if err != nil {
						t.Fatal(q, "execute", input, err)
					}
					characterParameterReviewRows(t, rows, []string{tc.source, targetName}, []string{strings.ToUpper(tc.source), strings.ToUpper(targetName)}, []any{input, tc.outputs[i]})
					t.Logf("query=%q stage=execute prepared=%t held-input=%#v expected=%#v", q, prepared, input, []any{input, tc.outputs[i]})
				}
			})
		}
	}
	for _, tc := range []struct {
		query        string
		names, types []string
		values       [][]any
	}{
		{"SELECT CAST($1 AS bpchar)='a',CAST($1 AS text)='a ' FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {true, false}, {true, false}, {nil, nil}}},
		{"SELECT CAST($1 AS text)='a ',CAST($1 AS bpchar)='a' FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}, {true, true}, {false, true}, {nil, nil}}},
		{"SELECT c=$1,CAST($1 AS text) FROM t", []string{"?column?", "text"}, []string{"BOOL", "TEXT"}, [][]any{{true, "a"}, {true, "a"}, {true, "a"}, {nil, nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.query+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = db.PrepareContext(p.ctx, tc.query)
					if err != nil {
						t.Fatal(tc.query, "prepare", err)
					}
					defer stmt.Close()
				}
				for i, input := range []any{"a  ", "a ", "a", nil} {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.QueryContext(p.ctx, input)
					} else {
						rows, err = db.QueryContext(p.ctx, tc.query, input)
					}
					if err != nil {
						t.Fatal(tc.query, "execute", input, err)
					}
					characterParameterReviewRows(t, rows, tc.names, tc.types, tc.values[i])
					t.Logf("query=%q stage=execute prepared=%t held-input=%#v expected=%#v", tc.query, prepared, input, tc.values[i])
				}
			})
		}
	}
}

func characterParameterReviewRows(t *testing.T, rows *sql.Rows, names, types []string, want []any) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, names, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, column := range metadata {
		if column.DatabaseTypeName() != types[i] {
			t.Fatal(i, column.DatabaseTypeName(), types[i])
		}
	}
	if !rows.Next() {
		t.Fatal("missing parameter row", rows.Err())
	}
	var a, b any
	if err := rows.Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]any{a, b}, want) {
		t.Fatal([]any{a, b}, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected parameter row/error", rows.Err())
	}
}

func characterParameterReviewDiagnostics() []struct {
	query, token string
	diagnostic   referenceDiagnostic
	args         []any
} {
	hint := "No operator matches the given name and argument types. You might need to add explicit type casts."
	return []struct {
		query, token string
		diagnostic   referenceDiagnostic
		args         []any
	}{
		{"SELECT c=1 FROM t", "=", referenceDiagnostic{Code: "42883", Message: "operator does not exist: character = integer", Hint: hint}, nil},
		{"SELECT 1=c FROM t", "=", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = character", Hint: hint}, nil},
		{"SELECT v=1 FROM t", "=", referenceDiagnostic{Code: "42883", Message: "operator does not exist: character varying = integer", Hint: hint}, nil},
		{"SELECT TRUE=v FROM t", "=", referenceDiagnostic{Code: "42883", Message: "operator does not exist: boolean = character varying", Hint: hint}, nil},
		{"/*é🙂*/ SELECT c=1 FROM t", "=", referenceDiagnostic{Code: "42883", Message: "operator does not exist: character = integer", Hint: hint}, nil},
		{"SELECT c FROM t WHERE c", "WHERE c", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type character"}, nil},
		{"SELECT v FROM t WHERE v", "WHERE v", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type character varying"}, nil},
		{"SELECT c AND TRUE FROM t", "c AND", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type character"}, nil},
		{"SELECT v AND TRUE FROM t", "v AND", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type character varying"}, nil},
		{"SELECT NOT c FROM t", "c FROM", referenceDiagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type character"}, nil},
		{"SELECT FALSE OR v FROM t", "v FROM", referenceDiagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type character varying"}, nil},
		{"SELECT $1,c=$1 FROM t", "$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character versus text"}, []any{"a"}},
		{"SELECT $1,CAST($1 AS bpchar) FROM t", "$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character versus text"}, []any{"a"}},
		{"SELECT $1,CAST($1 AS varchar) FROM t", "$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text"}, []any{"a"}},
		{"SELECT num_nulls($1),CAST($1 AS bpchar) FROM t", "$1", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1"}, []any{"a"}},
		{"SELECT num_nulls($1),CAST($1 AS varchar) FROM t", "$1", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1"}, []any{"a"}},
	}
}

func characterParameterReviewSetup(t *testing.T, p *referencePair, postgresOnly bool) {
	t.Helper()
	for _, q := range []string{"CREATE TABLE seed(i INT)", "INSERT INTO seed VALUES(1)", "CREATE TABLE t(c CHAR(2),v VARCHAR(2))", "INSERT INTO t VALUES('a','a ')", "CREATE TABLE empty(c CHAR(2),v VARCHAR(2))"} {
		if postgresOnly {
			if _, err := p.postgres.ExecContext(p.ctx, q); err != nil {
				t.Fatal(err)
			}
		} else {
			affected := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				affected = 1
			}
			p.step(t, referenceExec(q, affected))
		}
	}
}

func characterParameterReviewErrors(t *testing.T, p *referencePair, postgresOnly bool) {
	t.Helper()
	for _, empty := range []bool{false, true} {
		for _, tc := range characterParameterReviewDiagnostics() {
			q := tc.query
			if empty {
				q = strings.Replace(q, " FROM t", " FROM empty", 1)
			}
			d := tc.diagnostic
			offset := strings.Index(q, tc.token)
			if strings.HasPrefix(tc.token, "WHERE ") {
				offset += len("WHERE ")
			}
			d.Position = strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)
			for _, prepared := range []bool{false, true} {
				t.Run(q+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
					if postgresOnly {
						characterOracleExec(t, p, q, prepared, true, d, tc.args...)
						return
					}
					s := referenceImplicitError(q, d, tc.args...)
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					p.step(t, s)
				})
			}
		}
	}
}

// No native SQL is executed by this root capture entrypoint.
func TestReferenceCharacterParameterReviewOracle(t *testing.T) {
	p := newReferencePair(t)
	characterParameterReviewSetup(t, p, true)
	characterParameterReviewValues(t, p, p.postgres)
	characterParameterReviewErrors(t, p, true)
}

func TestReferenceCharacterParameterReview(t *testing.T) {
	p := newReferencePair(t)
	characterParameterReviewSetup(t, p, false)
	t.Run("native", func(t *testing.T) { characterParameterReviewValues(t, p, p.memory) })
	t.Run("postgres", func(t *testing.T) { characterParameterReviewValues(t, p, p.postgres) })
	characterParameterReviewErrors(t, p, false)
}

// Root's independent pinned18 capture confirmed the exact records and stages.
func characterInsertCastDiagnosticWitnesses(t *testing.T, postgresOnly bool) {
	p := newReferencePair(t)
	run := func(s referenceStep) {
		t.Helper()
		if postgresOnly {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	run(referenceExec("CREATE TABLE dst(i INT)", 0))
	run(referenceExec("INSERT INTO dst VALUES(9)", 1))
	run(referenceExec("CREATE TABLE src(c CHAR(2),v VARCHAR(2))", 0))
	run(referenceExec("INSERT INTO src VALUES('a','a')", 1))
	for _, tc := range []struct{ cast, diagnostic, column string }{
		{"bpchar(2)", "character", "c"},
		{"char(2)", "character", "c"},
		{"varchar(2)", "character varying", "v"},
	} {
		for _, sourceQuery := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.cast+"/source="+strconv.FormatBool(sourceQuery)+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
					q := "/*é🙂*/ INSERT INTO dst VALUES(CAST('a' AS " + tc.cast + "))"
					offset := strings.Index(q, "CAST(")
					if sourceQuery {
						q = "/*é🙂*/ INSERT INTO dst SELECT " + tc.column + " FROM src"
						offset = strings.Index(q, " SELECT ") + len(" SELECT ")
					}
					d := referenceDiagnostic{
						Code:     "42804",
						Message:  `column "i" is of type integer but expression is of type ` + tc.diagnostic,
						Hint:     "You will need to rewrite or cast the expression.",
						Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1),
					}
					if postgresOnly {
						characterOracleExec(t, p, q, prepared, true, d)
					} else {
						s := referenceImplicitError(q, d)
						s.errorStage = "execute"
						if prepared {
							s = referencePrepared(s)
							s.errorStage = "prepare"
						}
						run(s)
					}
					run(referenceSelect("SELECT i FROM dst", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
					t.Logf("query=%q prepared=%t source-query=%t expected-42804=%#v unchanged-sentinel=9", q, prepared, sourceQuery, d)
				})
			}
		}
	}
}

// This capture entrypoint executes SQL only on the configured pinned PostgreSQL.
func TestReferenceCharacterInsertCastDiagnosticOracle(t *testing.T) {
	characterInsertCastDiagnosticWitnesses(t, true)
}
func TestReferenceCharacterInsertCastDiagnostics(t *testing.T) {
	characterInsertCastDiagnosticWitnesses(t, false)
}
