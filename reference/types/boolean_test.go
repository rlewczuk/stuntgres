package reference_test

import (
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func booleanWrappedDiagnostic(t *testing.T, err error, want fixture.Diagnostic) {
	t.Helper()
	var origin interface{ SQLState() string }
	for depth := range 3 {
		fixture.AssertDiagnostic(t, err, &want)
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != want.Code {
			t.Fatalf("SQLSTATE: %v", err)
		}
		if depth == 0 {
			origin = state
		} else if state != origin {
			t.Fatal("wrapping changed originating diagnostic")
		}
		err = fmt.Errorf("wrapper: %w", err)
	}
}

func TestReferenceBooleanDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			literal := func(q, message, position string) {
				for _, op := range []string{"exec", "query", "prepare"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: message, Position: position})
					s.SelectRows = op == "query"
					s.Prepared = op == "prepare"
					s.ErrorStage = "execute"
					if s.Prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
				_, err := backend.DB.ExecContext(p.Ctx, q)
				booleanWrappedDiagnostic(t, err, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message, Position: position})
			}
			run(fixture.Exec("CREATE TABLE b(id INT,flag BOOL)", 0))
			literal("SELECT id FROM b WHERE flag='o'", `invalid input syntax for type boolean: "o"`, "29")
			run(fixture.Exec("INSERT INTO b VALUES (99,TRUE)", 1))
			sentinel := fixture.Select("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}})
			for _, input := range booleanInvalidInputs {
				literal("INSERT INTO b VALUES (1,"+input.source+")", input.message, "25")
				run(sentinel)
				literal("SELECT id FROM b WHERE flag="+input.source, input.message, "29")
				run(sentinel)
			}
			for _, tc := range []struct{ query, message, position string }{
				{"/*é*/ INSERT INTO b VALUES (1,'o')", `invalid input syntax for type boolean: "o"`, "31"},
				{"/*é*/ SELECT id FROM b WHERE flag='o'", `invalid input syntax for type boolean: "o"`, "35"},
				{`SELECT id FROM b WHERE flag=E'\x6f'`, `invalid input syntax for type boolean: "o"`, "29"},
				{"SELECT id FROM b WHERE flag=$tag$o$tag$", `invalid input syntax for type boolean: "o"`, "29"},
				{"SELECT id FROM b WHERE flag='tr'\n'uth'", `invalid input syntax for type boolean: "truth"`, "29"},
			} {
				literal(tc.query, tc.message, tc.position)
				run(sentinel)
			}
		})
	}
}

func TestReferenceBooleanDiagnosticsParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			parameter := func(q, message, portal string, args ...any) {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message, Context: portal}
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, want, args...)
						s.Prepared = prepared
						s.SelectRows = query
						s.ErrorStage = "execute"
						run(s)
					}
				}
				_, err := backend.DB.ExecContext(p.Ctx, q, args...)
				booleanWrappedDiagnostic(t, err, want)
			}
			run(fixture.Exec("CREATE TABLE b(id INT,flag BOOL)", 0))
			run(fixture.Exec("INSERT INTO b VALUES (99,TRUE)", 1))
			sentinel := fixture.Select("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}})
			for _, input := range booleanInvalidInputs {
				quoted := "'...'"
				if input.value == "" {
					quoted = "''"
				}
				for _, value := range []any{input.value, []byte(input.value)} {
					parameter("INSERT INTO b VALUES ($1,$2)", input.message, "unnamed portal parameter $2 = "+quoted, 1, value)
					parameter("SELECT id FROM b WHERE flag=$1", input.message, "unnamed portal parameter $1 = "+quoted, value)
					run(sentinel)
				}
			}
			run(fixture.Exec("CREATE TABLE repeated(flag BOOL)", 0))
			run(fixture.Exec("CREATE TABLE precedence(a BOOL,b BOOL)", 0))
			parameter("INSERT INTO repeated VALUES ($1),($1)", `invalid input syntax for type boolean: "o"`, "unnamed portal parameter $1 = '...'", "o")
			parameter("INSERT INTO precedence VALUES ($2,$1)", `invalid input syntax for type boolean: "truth"`, "unnamed portal parameter $1 = '...'", "truth", "o")
			run(fixture.Select("SELECT flag FROM repeated", []string{"flag"}, []string{"BOOL"}, nil))
			run(fixture.Select("SELECT a,b FROM precedence", []string{"a", "b"}, []string{"BOOL", "BOOL"}, nil))
		})
	}
}
