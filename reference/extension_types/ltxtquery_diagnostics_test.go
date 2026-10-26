package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtxtqueryDiagnostics(t *testing.T) {
 cases := []struct{input,code,message,detail string}{
 {"","42601","operand syntax error",""},
 {" ","42601","operand syntax error",""},
 {"!","42601","operand syntax error",""},
 {"a&","42601","operand syntax error",""},
 {"()","42601","operand syntax error",""},
 {"*","42601","operand syntax error",""},
 {"@a","42601","operand syntax error",""},
 {"a&&b","42601","operand syntax error",""},
 {"é","42601","operand syntax error",""},
 {"a b","42601","syntax error",""},
 {"a.b","42601","syntax error",""},
 {"42._","42601","syntax error",""},
 {"a)","42601","syntax error",""},
 {"(a","42601","syntax error",""},
 {"a\t","42601","syntax error",""},
 {"a\n","42601","syntax error",""},
 {"foo@bar","42601","modifiers syntax error",""},
 {"foo*bar","42601","modifiers syntax error",""},
 {"foo%bar","42601","modifiers syntax error",""},
 }
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ltreeInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE lx_diag(id integer NOT NULL,v ltxtquery)", 0))
				run(fixture.Exec("INSERT INTO lx_diag VALUES (1,'a'),(2,'!b')", 2))
				unchanged := func() { run(ltxtquerySelect(b, "SELECT id,v FROM lx_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "a"}, {int64(2), "!b"}})) }
				for _, prepared := range []bool{false, true} {
					lit := queryIntLiteral(tc.input)
					for _, q := range []string{
						"/*é*/ SELECT " + lit + "::ltxtquery AS v",
						"/*é*/ SELECT CAST(" + lit + " AS ltxtquery) AS v",
						"/*é*/ INSERT INTO lx_diag VALUES (3,'c'),(4," + lit + ")",
					} {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Detail, want.Position = tc.detail, ltreeSQLPosition(q, lit)
						ltreeError(t, p, b, c, q, prepared, strings.Contains(q, "SELECT"), true, want)
						unchanged()
					}
					for _, route := range []struct{ q string; parameter int; args []any; query bool }{
						{"SELECT $1::ltxtquery AS v", 1, []any{tc.input}, true},
						{"INSERT INTO lx_diag VALUES (3,$1),(4,$2)", 2, []any{"c", []byte(tc.input)}, false},
						{"UPDATE lx_diag SET v=CASE WHEN id=1 THEN $1::ltxtquery ELSE $2::ltxtquery END", 2, []any{"c", tc.input}, false},
					} {
						want := queryIntParameterDiagnostic(tc.code, tc.message, tc.input, route.parameter)
						want.Detail = tc.detail
						ltreeError(t, p, b, c, route.q, prepared, route.query, false, want, route.args...)
						unchanged()
					}
				}
			})
		})
	}
	t.Run("typmods", func(t *testing.T) {
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			ltreeInstall(t, p, b, c)
			for _, name := range []string{"ltxtquery", "public.ltxtquery"} {
				for _, modifier := range []string{"(2)", "(-1)", "(2,3)"} {
					q := "/*é*/ CREATE TABLE lx_bad(v " + name + modifier + ")"
					want := fixture.ControlsReferenceDiagnostic("42601", `type modifier is not allowed for type "`+name+`"`)
					want.Position = ltreeSQLPosition(q, name)
					for _, prepared := range []bool{false, true} { ltreeError(t, p, b, c, q, prepared, false, false, want) }
				}
			}
		})
	})
}
