package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Pinned PostgreSQL18.6 exercised these character positions and complete logical
// diagnostics. Compute positions from independently specified source tokens.
func updateRowPosition(q, token string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
}

func TestReferenceUpdateRowDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, source, code, message, origin, hint string
	}{
		{"implicit_short", "(1,2)", "42601", "number of columns does not match number of values", "(1,2)", ""},
		{"explicit_short", "ROW(1)", "42601", "number of columns does not match number of values", "ROW", ""},
		{"implicit_long", "(1,2,3,4)", "42601", "number of columns does not match number of values", "(1,2,3,4)", ""},
		{"select_short", "(SELECT 1,2 WHERE FALSE)", "42601", "number of columns does not match number of values", "(SELECT", ""},
		{"select_long", "(SELECT 1,2,3,4)", "42601", "number of columns does not match number of values", "(SELECT", ""},
		{"one_record_output", "(SELECT ROW(1,2,3))", "42601", "number of columns does not match number of values", "(SELECT", ""},
		{"empty_row", "ROW()", "42601", "number of columns does not match number of values", "ROW", ""},
		{"bool_constructor", "ROW(TRUE,2,3)", "42804", `column "id" is of type integer but expression is of type boolean`, "TRUE", "You will need to rewrite or cast the expression."},
		{"bool_select", "(SELECT TRUE,2,3)", "42804", `column "id" is of type integer but expression is of type boolean`, "TRUE", "You will need to rewrite or cast the expression."},
		{"bad_literal", "ROW('bad',2,3)", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'", ""},
		{"text_select", "(SELECT '7','8','9')", "42804", `column "id" is of type integer but expression is of type text`, "'7'", "You will need to rewrite or cast the expression."},
		{"arbitrary_record", "t.*", "0A000", "source for a multiple-column UPDATE item must be a sub-SELECT or ROW() expression", "t.*", ""},
		{"cast_row", "ROW(1,2,3)::triple", "0A000", "source for a multiple-column UPDATE item must be a sub-SELECT or ROW() expression", "ROW", ""},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, route := range []string{"exec", "query", "prepare"} {
				t.Run(tc.name+"/"+strconv.Itoa(len(prefix))+"/"+route, func(t *testing.T) {
					updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						updateRowSeed(t, p, b)
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TYPE triple AS (id INT,a INT,b INT)", 0))
						q := prefix + "UPDATE t SET (id,a,b)=" + tc.source + " WHERE FALSE"
						want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: updateRowPosition(q, tc.origin)}
						s := fixture.ImplicitError(q, want)
						s.SelectRows = route == "query"
						if route == "prepare" {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
					})
				})
			}
		}
	}
	for _, set := range []string{"(a,a)=(1,2)", "(a,b)=(1,2),a=3", "a=3,(a,b)=(1,2)", "(a,b)=(1,2),(b,id)=(3,4)"} {
		t.Run("duplicate/"+set, func(t *testing.T) {
			updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				updateRowSeed(t, p, b)
				column := "a"
				if strings.Contains(set, "(b,id)") {
					column = "b"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("UPDATE t SET "+set, fixture.Diagnostic{Code: "42601", Message: `multiple assignments to same column "` + column + `"`})
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
			})
		})
	}
}

func TestReferenceUpdateRowTyping(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		updateRowSeed(t, p, b)
		run(fixture.Exec("UPDATE t SET (a,b)=('7','8')", 2))
		run(updateRowSnapshot([][]any{{int64(1), int64(7), int64(8)}, {int64(2), int64(7), int64(8)}}))
		run(fixture.Exec("UPDATE t SET (a,b)=(SELECT '9'::int,'10'::int)", 2))
		run(updateRowSnapshot([][]any{{int64(1), int64(9), int64(10)}, {int64(2), int64(9), int64(10)}}))
		run(fixture.Exec("CREATE DOMAIN positive AS INT CHECK(VALUE>0)", 0))
		run(fixture.Exec("CREATE TABLE typed(id INT,n NUMERIC(3,1),v VARCHAR(3),d positive)", 0))
		run(fixture.Exec("INSERT INTO typed VALUES(1,1,'old',1)", 1))
		run(fixture.Exec("UPDATE typed SET (n,v,d)=ROW(1.25,'abc',2)", 1))
		run(fixture.Select("SELECT n,v,d FROM typed", []string{"n", "v", "d"}, []string{"NUMERIC", "VARCHAR", "INT4"}, [][]any{{"1.3", "abc", int64(2)}}))
		run(fixture.Exec("CREATE TABLE src(n NUMERIC,v TEXT,d INT)", 0))
		run(fixture.Exec("INSERT INTO src VALUES(99.95,'abc',2)", 1))
		run(fixture.ImplicitError("UPDATE typed SET (n,v,d)=(SELECT n,v,d FROM src)", fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}))
		run(fixture.Select("SELECT n,v,d FROM typed", []string{"n", "v", "d"}, []string{"NUMERIC", "VARCHAR", "INT4"}, [][]any{{"1.3", "abc", int64(2)}}))
	})
	// Independent SELECT unknown outputs are TEXT, unlike destination-context
	// constructor parameters. Each route is isolated so all phases are discovered.
	for _, source := range []string{"($1,$2)", "(SELECT $1,$2)", "(SELECT $1::int,$2::int)"} {
		for _, route := range []string{"direct", "held", "sql_prepare"} {
			t.Run(source+"/"+route, func(t *testing.T) {
				updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					updateRowSeed(t, p, b)
					q := "UPDATE t SET (a,b)=" + source + " WHERE id=1"
					bad := source == "(SELECT $1,$2)"
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "a" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: updateRowPosition(q, "$1")}
					if route == "sql_prepare" {
						q = "PREPARE rowargs AS " + q
						if bad {
							want.Position = updateRowPosition(q, "$1")
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, want))
							return
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("EXECUTE rowargs(7,8)", 1))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("EXECUTE rowargs(NULL,9)", 1))
					} else if route == "held" {
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if bad {
							if stmt != nil {
								stmt.Close()
								t.Fatal("unexpected successful prepare")
							}
							fixture.AssertDiagnostic(t, err, &want)
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for _, args := range [][]any{{7, 8}, {nil, 9}} {
							result, err := stmt.ExecContext(p.Ctx, args...)
							if err != nil {
								t.Fatal(err)
							}
							fixture.LockAffected(t, result, 1)
						}
					} else {
						if bad {
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, want, 7, 8))
							return
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 1, 7, 8))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 1, nil, 9))
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, updateRowSnapshot([][]any{{int64(1), nil, int64(9)}, {int64(2), int64(30), int64(40)}}))
				})
			})
		}
	}
}
