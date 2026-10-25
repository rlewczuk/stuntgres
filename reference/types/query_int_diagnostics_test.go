package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryIntDiagnostics(t *testing.T) {
	inputs := []string{"", "   ", "\t1", "1\n", "1\r", "\u00a01", "+1", "1 2", "1&&2", "1||2", "1&", "!", "()", "(1", "1)", "1a", "0x10", "2147483648", "-2147483649", strings.Repeat("0", 16), "-" + strings.Repeat("0", 15), strings.Repeat("!", 17) + "1"}
	for i, input := range inputs {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				queryIntInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE qi_diag(id integer NOT NULL,v query_int)", 0))
				run(fixture.Exec("INSERT INTO qi_diag VALUES (1,'1'),(2,'!2')", 2))
				code, message := "42601", "syntax error"
				if i == len(inputs)-1 {
					code, message = "54001", "statement too complex"
				}
				for _, prepared := range []bool{false, true} {
					for _, tc := range []struct {
						q, position string
						selectRows  bool
					}{
						{"/*é*/ SELECT " + queryIntLiteral(input) + "::query_int AS v", "14", true},
						{"/*é*/ SELECT CAST(" + queryIntLiteral(input) + " AS query_int) AS v", "19", true},
						{"/*é*/ INSERT INTO qi_diag VALUES (3," + queryIntLiteral(input) + ")", "37", false},
						{"/*é*/ INSERT INTO qi_diag VALUES (3,'3'),(4," + queryIntLiteral(input) + ")", "45", false},
					} {
						want := fixture.ControlsReferenceDiagnostic(code, message)
						want.Position = tc.position
						queryIntError(t, p, b, c, tc.q, prepared, tc.selectRows, true, want)
					}
					for _, tc := range []struct {
						q         string
						parameter int
						args      []any
					}{
						{"INSERT INTO qi_diag VALUES (3,$1)", 1, []any{input}},
						{"INSERT INTO qi_diag VALUES (3,$1),(4,$2)", 2, []any{"3", input}},
						{"UPDATE qi_diag SET v=CASE WHEN id=1 THEN $1::query_int ELSE $2::query_int END", 2, []any{"3", input}},
					} {
						want := queryIntParameterDiagnostic(code, message, input, tc.parameter)
						queryIntError(t, p, b, c, tc.q, prepared, false, false, want, tc.args...)
						run(fixture.QueryIntSelect(b, "SELECT id,v FROM qi_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1"}, {int64(2), "!2"}}))
					}
					run(fixture.QueryIntSelect(b, "SELECT id,v FROM qi_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1"}, {int64(2), "!2"}}))
				}
			})
		})
	}
	t.Run("declaration_locations", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			queryIntInstall(t, p, b, c)
			for _, tc := range []struct {
				q, message, position       string
				selectRows, prepareFailure bool
			}{
				{"/*é*/ CREATE TABLE qi_bad(v query_int(2))", `type modifier is not allowed for type "query_int"`, "29", false, false},
				{"/*é*/ CREATE TABLE qi_bad(v public.query_int(2))", `type modifier is not allowed for type "public.query_int"`, "29", false, false},
				{"CREATE TABLE qi_bad(v query_int(-1))", `type modifier is not allowed for type "query_int"`, "23", false, false},
				{"CREATE TABLE qi_bad(v query_int(2,3))", `type modifier is not allowed for type "query_int"`, "23", false, false},
				{"CREATE TABLE qi_bad(v query_int('x'))", `type modifier is not allowed for type "query_int"`, "23", false, false},
				{"/*é*/ SELECT CAST('1&&2' AS public.query_int)", "syntax error", "19", true, true},
				{"/*é*/\nSELECT '1&&2'::\"query_int\"", "syntax error", "14", true, true},
			} {
				want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				want.Position = tc.position
				if tc.q == "CREATE TABLE qi_bad(v query_int('x'))" {
					// The existing generic declaration syntax boundary is separate
					// from the query text codec; both complete records are observed.
					stage := "execute"
					if !b.PG {
						want.Message, want.Position = `syntax error at or near "'x'"`, "33"
						stage = "prepare"
					}
					fixture.CompositeReferenceError(t, p, c, tc.q, stage, want)
					continue
				}
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, tc.q, prepared, tc.selectRows, tc.prepareFailure, want)
				}
			}
		})
	})
}
