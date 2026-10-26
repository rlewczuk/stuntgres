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

func lquerySQLPosition(q, token string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, token)]) + 1)
}

func TestReferenceLqueryDiagnostics(t *testing.T) {
	cases := []struct{ input, code, message, detail string }{
		{"", "42601", "lquery syntax error", "Unexpected end of input."},
		{"a.", "42601", "lquery syntax error", "Unexpected end of input."},
		{"a|", "42601", "lquery syntax error", "Unexpected end of input."},
		{"a{", "42601", "lquery syntax error", "Unexpected end of input."},
		{".a", "42601", "lquery syntax error at character 1", ""},
		{"a..b", "42601", "lquery syntax error at character 3", ""},
		{"a||b", "42601", "lquery syntax error at character 3", ""},
		{"a b", "42601", "lquery syntax error at character 2", ""},
		{" ", "42601", "lquery syntax error at character 1", ""},
		{"\t", "42601", "lquery syntax error at character 1", ""},
		{"\n", "42601", "lquery syntax error at character 1", ""},
		{"foo@bar", "42601", "lquery syntax error at character 5", ""},
		{"a{}", "42601", "lquery syntax error at character 3", ""},
		{"a{2,1}", "42601", "lquery syntax error", "Low limit (2) is greater than high limit (1), at character 5."},
		{"a{65536}", "54000", "lquery syntax error", "Low limit (65536) exceeds the maximum allowed (65535), at character 3."},
		{"a{1,65536}", "54000", "lquery syntax error", "High limit (65536) exceeds the maximum allowed (65535), at character 5."},
		{"!", "42601", "lquery syntax error at character 2", "Empty labels are not allowed."},
		{"a{1}x", "42601", "lquery syntax error at character 5", ""},
		{"|a", "42601", "lquery syntax error at character 1", ""},
		{"@", "42601", "lquery syntax error at character 1", ""},
		{"%", "42601", "lquery syntax error at character 1", ""},
		{"a\t", "42601", "lquery syntax error at character 2", ""},
		// REL_18_6 ts_locale.c uses byte isalnum when database_ctype_is_c.
		{"é", "42601", "lquery syntax error at character 1", ""},
		{"a.é", "42601", "lquery syntax error at character 3", ""},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				lqueryInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE lq_diag(id integer NOT NULL,v lquery)", 0))
				run(fixture.Exec("INSERT INTO lq_diag VALUES (1,'a'),(2,'!b')", 2))
				unchanged := func() { run(lquerySelect(b, "SELECT id,v FROM lq_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "a"}, {int64(2), "!b"}})) }
				for _, prepared := range []bool{false, true} {
					lit := queryIntLiteral(tc.input)
					for _, q := range []string{
						"/*é*/ SELECT " + lit + "::lquery AS v",
						"/*é*/ SELECT CAST(" + lit + " AS lquery) AS v",
						"/*é*/ INSERT INTO lq_diag VALUES (3,'c'),(4," + lit + ")",
					} {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Detail, want.Position = tc.detail, lquerySQLPosition(q, lit)
						lqueryError(t, p, b, c, q, prepared, strings.Contains(q, "SELECT"), true, want)
						unchanged()
					}
					for _, route := range []struct{ q string; parameter int; args []any; query bool }{
						{"SELECT $1::lquery AS v", 1, []any{tc.input}, true},
						{"INSERT INTO lq_diag VALUES (3,$1),(4,$2)", 2, []any{"c", []byte(tc.input)}, false},
						{"UPDATE lq_diag SET v=CASE WHEN id=1 THEN $1::lquery ELSE $2::lquery END", 2, []any{"c", tc.input}, false},
					} {
						want := queryIntParameterDiagnostic(tc.code, tc.message, tc.input, route.parameter)
						want.Detail = tc.detail
						lqueryError(t, p, b, c, route.q, prepared, route.query, false, want, route.args...)
						unchanged()
					}
				}
			})
		})
	}
	t.Run("typmods", func(t *testing.T) {
		lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lqueryInstall(t, p, b, c)
			for _, name := range []string{"lquery", "public.lquery"} {
				for _, modifier := range []string{"(2)", "(-1)", "(2,3)"} {
					q := "/*é*/ CREATE TABLE lq_bad(v " + name + modifier + ")"
					want := fixture.ControlsReferenceDiagnostic("42601", `type modifier is not allowed for type "`+name+`"`)
					want.Position = lquerySQLPosition(q, name)
					for _, prepared := range []bool{false, true} { lqueryError(t, p, b, c, q, prepared, false, false, want) }
				}
			}
		})
	})
}
