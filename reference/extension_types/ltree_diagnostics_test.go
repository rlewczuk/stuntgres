package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func ltreeSQLPosition(q, token string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, token)]) + 1)
}

func TestReferenceLtreeDiagnostics(t *testing.T) {
	cases := []struct{ input, code, message, detail string }{
		{"a.", "42601", "ltree syntax error", "Unexpected end of input."},
		{".a", "42601", "ltree syntax error at character 1", ""},
		{".", "42601", "ltree syntax error at character 1", ""},
		{"a..b", "42601", "ltree syntax error at character 3", ""},
		{"a b", "42601", "ltree syntax error at character 2", ""},
		{"a*", "42601", "ltree syntax error at character 2", ""},
		{"a/", "42601", "ltree syntax error at character 2", ""},
		{"a\t", "42601", "ltree syntax error at character 2", ""},
		{"a\n", "42601", "ltree syntax error at character 2", ""},
		{" ", "42601", "ltree syntax error at character 1", ""},
		{"\t", "42601", "ltree syntax error at character 1", ""},
		{"\n", "42601", "ltree syntax error at character 1", ""},
		{"é", "42601", "ltree syntax error at character 1", ""},
		{"雪", "42601", "ltree syntax error at character 1", ""},
		{"١", "42601", "ltree syntax error at character 1", ""},
		{"１", "42601", "ltree syntax error at character 1", ""},
		{"́", "42601", "ltree syntax error at character 1", ""},
		{"a.é", "42601", "ltree syntax error at character 3", ""},
		{"á", "42601", "ltree syntax error at character 2", ""},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ltreeInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE lt_diag(id integer NOT NULL,v ltree)", 0))
				run(fixture.Exec("INSERT INTO lt_diag VALUES (1,'a'),(2,'b')", 2))
				unchanged := func() { run(ltreeSelect(b, "SELECT id,v FROM lt_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "a"}, {int64(2), "b"}})) }
				for _, prepared := range []bool{false, true} {
					lit := queryIntLiteral(tc.input)
					for _, q := range []string{
						"/*é*/ SELECT " + lit + "::ltree AS v",
						"/*é*/ SELECT CAST(" + lit + " AS ltree) AS v",
						"/*é*/ INSERT INTO lt_diag VALUES (3,'c'),(4," + lit + ")",
					} {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Detail, want.Position = tc.detail, ltreeSQLPosition(q, lit)
						ltreeError(t, p, b, c, q, prepared, strings.Contains(q, "SELECT"), true, want)
						unchanged()
					}
					for _, route := range []struct{ q string; parameter int; args []any; query bool }{
						{"SELECT $1::ltree AS v", 1, []any{tc.input}, true},
						{"INSERT INTO lt_diag VALUES (3,$1),(4,$2)", 2, []any{"c", []byte(tc.input)}, false},
						{"UPDATE lt_diag SET v=CASE WHEN id=1 THEN $1::ltree ELSE $2::ltree END", 2, []any{"c", tc.input}, false},
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
			for _, name := range []string{"ltree", "public.ltree"} {
				for _, modifier := range []string{"(2)", "(-1)", "(2,3)"} {
					q := "/*é*/ CREATE TABLE lt_bad(v " + name + modifier + ")"
					want := fixture.ControlsReferenceDiagnostic("42601", `type modifier is not allowed for type "`+name+`"`)
					want.Position = ltreeSQLPosition(q, name)
					for _, prepared := range []bool{false, true} { ltreeError(t, p, b, c, q, prepared, false, false, want) }
				}
			}
		})
	})
}