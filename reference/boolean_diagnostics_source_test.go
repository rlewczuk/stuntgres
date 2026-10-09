package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceBooleanDiagnosticsSourceLiterals(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
			literal := func(q, message, position string) {
				want := controlsReferenceDiagnostic("22P02", message)
				want.Position = position
				for _, query := range []bool{false, true} {
					var err error
					if query {
						rows, e := backend.db.Query(q)
						err = e
						if rows != nil {
							rows.Close()
							t.Fatal("failed Query exposed rows")
						}
					} else {
						result, e := backend.db.Exec(q)
						err = e
						if result != nil {
							t.Fatal("failed Exec exposed result")
						}
					}
					booleanWrappedDiagnostic(t, err, want)
					run(referenceSelect("SELECT id,flag FROM b", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}}))
				}
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("invalid literal exposed a statement")
				}
				booleanWrappedDiagnostic(t, err, want)
				run(referenceSelect("SELECT id,flag FROM b", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}}))
			}
			// Empty-table binding is independent of row evaluation.
			rows, err := backend.db.Query("SELECT id FROM b WHERE flag='o'")
			if rows != nil {
				rows.Close()
				t.Fatal("empty-table failure exposed rows")
			}
			want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
			want.Position = "29"
			booleanWrappedDiagnostic(t, err, want)
			run(referenceExec("INSERT INTO b VALUES (99,TRUE)", 1))
			for _, input := range booleanInvalidInputs {
				literal("INSERT INTO b VALUES (1,"+input.source+")", input.message, "25")
				run(referenceSelect("SELECT id,flag FROM b", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}}))
				literal("SELECT id FROM b WHERE flag="+input.source, input.message, "29")
				run(referenceSelect("SELECT id,flag FROM b", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}}))
			}
			for _, tc := range []struct{ query, message, position string }{
				{"/*é*/ INSERT INTO b VALUES (1,'o')", `invalid input syntax for type boolean: "o"`, "31"},
				{"/*é*/ SELECT id FROM b WHERE flag='o'", `invalid input syntax for type boolean: "o"`, "35"},
				{`SELECT id FROM b WHERE flag=E'\x6f'`, `invalid input syntax for type boolean: "o"`, "29"},
				{"SELECT id FROM b WHERE flag=$tag$o$tag$", `invalid input syntax for type boolean: "o"`, "29"},
				{"SELECT id FROM b WHERE flag='tr'\n'uth'", `invalid input syntax for type boolean: "truth"`, "29"},
			} {
				literal(tc.query, tc.message, tc.position)
			}
		})
	}
}

func TestReferenceBooleanDiagnosticsSourceParameters(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
			run(referenceExec("INSERT INTO b VALUES (99,TRUE)", 1))
			parameter := func(q string, want referenceDiagnostic, directQuery bool, args ...any) {
				queries := []bool{false}
				if directQuery {
					queries = append(queries, true)
				}
				for _, query := range queries {
					var err error
					if query {
						rows, e := backend.db.Query(q, args...)
						err = e
						if rows != nil {
							rows.Close()
							t.Fatal("failed Query exposed rows")
						}
					} else {
						result, e := backend.db.Exec(q, args...)
						err = e
						if result != nil {
							t.Fatal("failed Exec exposed result")
						}
					}
					booleanWrappedDiagnostic(t, err, want)
					run(referenceSelect("SELECT id,flag FROM b", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}}))
				}
				// Original booleanFailure prepared with Context, then consumed
				// through actual non-Context statement methods.
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				if strings.HasPrefix(q, "SELECT") {
					rows, e := stmt.Query(args...)
					err = e
					if rows != nil {
						rows.Close()
						t.Fatal("failed held Query exposed rows")
					}
				} else {
					result, e := stmt.Exec(args...)
					err = e
					if result != nil {
						t.Fatal("failed held Exec exposed result")
					}
				}
				booleanWrappedDiagnostic(t, err, want)
				run(referenceSelect("SELECT id,flag FROM b", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}}))
			}
			for _, input := range booleanInvalidInputs {
				quoted := "'...'"
				if input.value == "" {
					quoted = "''"
				}
				for _, value := range []any{input.value, []byte(input.value)} {
					want := controlsReferenceDiagnostic("22P02", input.message)
					want.Context = "unnamed portal parameter $2 = " + quoted
					parameter("INSERT INTO b VALUES ($1,$2)", want, true, 1, value)
					want.Context = "unnamed portal parameter $1 = " + quoted
					parameter("SELECT id FROM b WHERE flag=$1", want, true, value)
				}
			}
			run(referenceExec("CREATE TABLE repeated(flag BOOL)", 0))
			run(referenceExec("CREATE TABLE precedence(a BOOL,b BOOL)", 0))
			want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
			want.Context = "unnamed portal parameter $1 = '...'"
			parameter("INSERT INTO repeated VALUES ($1),($1)", want, false, "o")
			want.Message = `invalid input syntax for type boolean: "truth"`
			parameter("INSERT INTO precedence VALUES ($2,$1)", want, false, "truth", "o")
			run(referenceSelect("SELECT flag FROM repeated", []string{"flag"}, []string{"BOOL"}, nil))
			run(referenceSelect("SELECT a,b FROM precedence", []string{"a", "b"}, []string{"BOOL", "BOOL"}, nil))
		})
	}
}
