package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceCharacterKnownParameterCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE seed(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO seed VALUES(1)", 1))
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
		for _, backend := range p.Backends() {
			func() {
				stmt, err := backend.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
				for i, input := range []any{"a  ", "é🙂  ", "", nil, "z "} {
					s := fixture.Select(q, []string{tc.source, targetName}, []string{strings.ToUpper(tc.source), strings.ToUpper(targetName)}, [][]any{{input, tc.outputs[i]}}, input)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
				}
			}()
		}
	}
}

func TestReferenceCharacterParameterComparisonComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(c CHAR(2),v VARCHAR(2))", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES('a','a ')", 1))
	for _, tc := range []struct {
		query        string
		names, types []string
		values       [][]any
	}{
		{"SELECT CAST($1 AS bpchar)='a',CAST($1 AS text)='a ' FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {true, false}, {true, false}, {nil, nil}}},
		{"SELECT CAST($1 AS text)='a ',CAST($1 AS bpchar)='a' FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}, {true, true}, {false, true}, {nil, nil}}},
		{"SELECT c=$1,CAST($1 AS text) FROM t", []string{"?column?", "text"}, []string{"BOOL", "TEXT"}, [][]any{{true, "a"}, {true, "a"}, {true, "a"}, {nil, nil}}},
	} {
		for _, backend := range p.Backends() {
			func() {
				stmt, err := backend.DB.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: tc.query}
				for i, input := range []any{"a  ", "a ", "a", nil} {
					s := fixture.Select(tc.query, tc.names, tc.types, [][]any{tc.values[i]}, input)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
				}
			}()
		}
	}
}

func characterParameterReviewDiagnostics() []struct {
	query, token string
	diagnostic   fixture.Diagnostic
	args         []any
} {
	hint := "No operator matches the given name and argument types. You might need to add explicit type casts."
	return []struct {
		query, token string
		diagnostic   fixture.Diagnostic
		args         []any
	}{
		{"SELECT c=1 FROM t", "=", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: character = integer", Hint: hint}, nil},
		{"SELECT 1=c FROM t", "=", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = character", Hint: hint}, nil},
		{"SELECT v=1 FROM t", "=", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: character varying = integer", Hint: hint}, nil},
		{"SELECT TRUE=v FROM t", "=", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: boolean = character varying", Hint: hint}, nil},
		{"/*é🙂*/ SELECT c=1 FROM t", "=", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: character = integer", Hint: hint}, nil},
		{"SELECT c FROM t WHERE c", "WHERE c", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type character"}, nil},
		{"SELECT v FROM t WHERE v", "WHERE v", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type character varying"}, nil},
		{"SELECT c AND TRUE FROM t", "c AND", fixture.Diagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type character"}, nil},
		{"SELECT v AND TRUE FROM t", "v AND", fixture.Diagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type character varying"}, nil},
		{"SELECT NOT c FROM t", "c FROM", fixture.Diagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type character"}, nil},
		{"SELECT FALSE OR v FROM t", "v FROM", fixture.Diagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type character varying"}, nil},
		{"SELECT $1,c=$1 FROM t", "$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character versus text"}, []any{"a"}},
		{"SELECT $1,CAST($1 AS bpchar) FROM t", "$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character versus text"}, []any{"a"}},
		{"SELECT $1,CAST($1 AS varchar) FROM t", "$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text"}, []any{"a"}},
		{"SELECT num_nulls($1),CAST($1 AS bpchar) FROM t", "$1", fixture.Diagnostic{Code: "42P08", Message: "could not determine data type of parameter $1"}, []any{"a"}},
		{"SELECT num_nulls($1),CAST($1 AS varchar) FROM t", "$1", fixture.Diagnostic{Code: "42P08", Message: "could not determine data type of parameter $1"}, []any{"a"}},
	}
}

func TestReferenceCharacterParameterReviewDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(c CHAR(2),v VARCHAR(2))", 0))
	p.Step(t, fixture.Exec("CREATE TABLE empty(c CHAR(2),v VARCHAR(2))", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO t VALUES('a','a ')", 1))
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
				exec := fixture.ImplicitError(q, d, tc.args...)
				exec.ErrorStage = "execute"
				p.Step(t, exec)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, d, tc.args...)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					p.Step(t, s)
				}
			}
		}
	}
}

func TestReferenceCharacterInsertCastDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE dst(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO dst VALUES(9)", 1))
	p.Step(t, fixture.Exec("CREATE TABLE src(c CHAR(2),v VARCHAR(2))", 0))
	p.Step(t, fixture.Exec("INSERT INTO src VALUES('a','a')", 1))
	for _, tc := range []struct{ cast, diagnostic, column string }{{"bpchar(2)", "character", "c"}, {"char(2)", "character", "c"}, {"varchar(2)", "character varying", "v"}} {
		for _, source := range []bool{false, true} {
			q := "/*é🙂*/ INSERT INTO dst VALUES(CAST('a' AS " + tc.cast + "))"
			offset := strings.Index(q, "CAST(")
			if source {
				q = "/*é🙂*/ INSERT INTO dst SELECT " + tc.column + " FROM src"
				offset = strings.Index(q, " SELECT ") + len(" SELECT ")
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "i" is of type integer but expression is of type ` + tc.diagnostic, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)}
			for _, backend := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
					var err error
					if prepared {
						stmt, e := backend.DB.PrepareContext(ctx, q)
						err = e
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid assignment exposed statement")
						}
					} else {
						var result sql.Result
						result, err = backend.DB.ExecContext(ctx, q)
						if result != nil {
							t.Fatal("invalid assignment exposed result")
						}
					}
					fixture.State(t, err, "42804")
					fixture.AssertDiagnostic(t, err, &want)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT i FROM dst", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
				}
			}
		}
	}
}
