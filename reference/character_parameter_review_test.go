package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceCharacterKnownParameterCasts(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE seed(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
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
		q := "SELECT CAST($1 AS " + tc.source + "),CAST($1 AS " + tc.target + ") FROM seed"
		targetName := strings.Split(tc.target, "(")[0]
		for _, backend := range p.backends() {
			func() {
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
				for i, input := range []any{"a  ", "é🙂  ", "", nil, "z "} {
					s := referenceSelect(q, []string{tc.source, targetName}, []string{strings.ToUpper(tc.source), strings.ToUpper(targetName)}, [][]any{{input, tc.outputs[i]}}, input)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					referenceRunStep(t, p.ctx, held, backend.recorder, s)
				}
			}()
		}
	}
}

func TestReferenceCharacterParameterComparisonComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(c CHAR(2),v VARCHAR(2))", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('a','a ')", 1))
	for _, tc := range []struct {
		query        string
		names, types []string
		values       [][]any
	}{
		{"SELECT CAST($1 AS bpchar)='a',CAST($1 AS text)='a ' FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {true, false}, {true, false}, {nil, nil}}},
		{"SELECT CAST($1 AS text)='a ',CAST($1 AS bpchar)='a' FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}, {true, true}, {false, true}, {nil, nil}}},
		{"SELECT c=$1,CAST($1 AS text) FROM t", []string{"?column?", "text"}, []string{"BOOL", "TEXT"}, [][]any{{true, "a"}, {true, "a"}, {true, "a"}, {nil, nil}}},
	} {
		for _, backend := range p.backends() {
			func() {
				stmt, err := backend.db.PrepareContext(p.ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: tc.query}
				for i, input := range []any{"a  ", "a ", "a", nil} {
					s := referenceSelect(tc.query, tc.names, tc.types, [][]any{tc.values[i]}, input)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					referenceRunStep(t, p.ctx, held, backend.recorder, s)
				}
			}()
		}
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

func TestReferenceCharacterParameterReviewDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(c CHAR(2),v VARCHAR(2))", 0))
	p.step(t, referenceExec("CREATE TABLE empty(c CHAR(2),v VARCHAR(2))", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES('a','a ')", 1))
		}
		for _, table := range []string{"t", "empty"} {
			for _, tc := range characterParameterReviewDiagnostics() {
				q := strings.Replace(tc.query, " FROM t", " FROM "+table, 1)
				d := tc.diagnostic
				offset := strings.Index(q, tc.token)
				if strings.HasPrefix(tc.token, "WHERE ") {
					offset += len("WHERE ")
				}
				d.Position = strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)
				exec := referenceImplicitError(q, d, tc.args...)
				exec.errorStage = "execute"
				p.step(t, exec)
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(q, d, tc.args...)
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					p.step(t, s)
				}
			}
		}
	}
}

func TestReferenceCharacterInsertCastDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE dst(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO dst VALUES(9)", 1))
	p.step(t, referenceExec("CREATE TABLE src(c CHAR(2),v VARCHAR(2))", 0))
	p.step(t, referenceExec("INSERT INTO src VALUES('a','a')", 1))
	for _, tc := range []struct{ cast, diagnostic, column string }{{"bpchar(2)", "character", "c"}, {"char(2)", "character", "c"}, {"varchar(2)", "character varying", "v"}} {
		for _, source := range []bool{false, true} {
			q := "/*é🙂*/ INSERT INTO dst VALUES(CAST('a' AS " + tc.cast + "))"
			offset := strings.Index(q, "CAST(")
			if source {
				q = "/*é🙂*/ INSERT INTO dst SELECT " + tc.column + " FROM src"
				offset = strings.Index(q, " SELECT ") + len(" SELECT ")
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "i" is of type integer but expression is of type ` + tc.diagnostic, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)}
			for _, backend := range p.backends() {
				for _, prepared := range []bool{false, true} {
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
					var err error
					if prepared {
						stmt, e := backend.db.PrepareContext(ctx, q)
						err = e
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid assignment exposed statement")
						}
					} else {
						var result sql.Result
						result, err = backend.db.ExecContext(ctx, q)
						if result != nil {
							t.Fatal("invalid assignment exposed result")
						}
					}
					referenceState(t, err, "42804")
					referenceAssertDiagnostic(t, err, &want)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT i FROM dst", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
				}
			}
		}
	}
}
