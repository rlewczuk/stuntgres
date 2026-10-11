package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b INT,n SMALLINT NOT NULL)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2,1),(40000,3,1)", 2))
		}
		expected := [][]any{}
		if populated {
			expected = [][]any{{int64(1), int64(2), int64(1)}, {int64(40000), int64(3), int64(1)}}
		}
		sentinel := fixture.Select("SELECT a,b,n FROM t ORDER BY b", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT2"}, expected)
		for _, tc := range []struct {
			q string
			d fixture.Diagnostic
		}{
			{"UPDATE missing SET a=1", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "8"}},
			{"UPDATE /*é*/ missing SET a=1", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "14"}},
			{"UPDATE t SET missing=1", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "14"}},
			{"UPDATE t SET a=missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "16"}},
			{"UPDATE t SET a=TRUE", fixture.Diagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Position: "16", Hint: "You will need to rewrite or cast the expression."}},
			{"UPDATE t SET a='bad'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "16"}},
			{"UPDATE t SET a=1,a=2", fixture.Diagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
			{"UPDATE t SET ctid=1", fixture.Diagnostic{Code: "0A000", Message: `cannot assign to system column "ctid"`, Position: "14"}},
			{"UPDATE t SET a=1 WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
			{"UPDATE t SET a=1 WHERE", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
			{"UPDATE t SET missing=1 WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "30"}},
			{"UPDATE t SET a=TRUE,b=missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
		} {
			p.Step(t, fixture.ImplicitError(tc.q, tc.d))
			p.Step(t, sentinel)
			query := fixture.ImplicitError(tc.q, tc.d)
			query.SelectRows = true
			p.Step(t, query)
			p.Step(t, sentinel)
			step := fixture.Prepared(fixture.ImplicitError(tc.q, tc.d))
			step.ErrorStage = "prepare"
			p.Step(t, step)
			p.Step(t, sentinel)
		}
	}
	p.Step(t, fixture.Prepared(fixture.ImplicitError("UPDATE t SET n=$1 WHERE b=2", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, 2, null).", Schema: "public", Table: "t", Column: "n"}, nil)))
	p.Step(t, fixture.Select("SELECT a,b,n FROM t ORDER BY b", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT2"}, [][]any{{int64(1), int64(2), int64(1)}, {int64(40000), int64(3), int64(1)}}))
}

func TestReferenceUpdateCoreArithmeticAssignmentDiagnostic(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step) }
		run(fixture.Exec("CREATE TABLE t(f BOOL)", 0))
		for _, populated := range []bool{false, true} {
			wantRows := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO t VALUES(TRUE),(FALSE)", 2))
				wantRows = [][]any{{false}, {true}}
			}
			for _, tc := range []struct {
				name, q, position, typ string
			}{
				{"plain", "UPDATE t SET f=1+2", "16", "integer"},
				{"grouped", "UPDATE t SET f=(1+2)", "17", "integer"},
				{"utf8_grouped", "/*é*/ UPDATE t SET f=(1+2)", "23", "integer"},
				{"postfix", "UPDATE t SET f=(1+2)::bigint", "17", "bigint"},
			} {
				for _, protocol := range []string{"exec", "query", "prepare"} {
					t.Run(map[bool]string{false: "empty", true: "populated"}[populated]+"/"+tc.name+"/"+protocol, func(t *testing.T) {
						defer func() {
							rows, err := conn.QueryContext(p.Ctx, "SELECT f FROM t ORDER BY f")
							if err != nil {
								t.Fatal(err)
							}
							defer rows.Close()
							metadata, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							if len(metadata) != 1 || metadata[0].ScanType() != reflect.TypeFor[bool]() {
								t.Fatal("BOOL ScanType", metadata)
							}
							fixture.UpdateCoreRetained(t, backend.PG, rows, []string{"f"}, []string{"BOOL"}, wantRows, false)
							run(fixture.Select("SELECT f FROM t ORDER BY f", []string{"f"}, []string{"BOOL"}, wantRows))
						}()
						want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "f" is of type boolean but expression is of type ` + tc.typ, Hint: "You will need to rewrite or cast the expression.", Position: tc.position}
						switch protocol {
						case "exec":
							result, err := conn.ExecContext(p.Ctx, tc.q)
							if result != nil {
								t.Fatal("invalid assignment returned a result")
							}
							fixture.AssertDiagnostic(t, err, &want)
						case "query":
							rows, err := conn.QueryContext(p.Ctx, tc.q)
							if rows != nil {
								rows.Close()
								t.Fatal("invalid assignment exposed rows")
							}
							fixture.AssertDiagnostic(t, err, &want)
						case "prepare":
							stmt, err := conn.PrepareContext(p.Ctx, tc.q)
							if stmt != nil {
								stmt.Close()
								t.Fatal("invalid assignment prepared")
							}
							fixture.AssertDiagnostic(t, err, &want)
						}
					})
				}
			}
		}
	}
}
