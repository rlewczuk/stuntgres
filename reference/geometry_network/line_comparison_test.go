package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLineComparison(t *testing.T) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct {
			left, right string
			want        any
		}{
			{"{1,2,3}", "{2,4,6}", true}, {"{1,2,3}", "{2,4,7}", false}, {"{NaN,2,3}", "{NaN,2,3}", true}, {"{NaN,2,3}", "{NaN,4,6}", false},
			// Released line_eq first divides A coefficients; the ratio's C
			// product differs by about 1.5e-6, outside geometric tolerance.
			{"{1,2,3}", "{1.0000005,2,3}", false},
			{"{1,2,3}", "{1,2.0000005,3}", true},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT $1::line=$2::line AS eq", []string{"eq"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right)
				s.Prepared = prepared
				run(s)
			}
		}
		for _, q := range []string{"SELECT NULL::line=line '{1,2,3}' AS eq", "SELECT line '{1,2,3}'=NULL::line AS eq", "SELECT NULL::line=NULL::line AS eq"} {
			s := fixture.Select(q, []string{"eq"}, []string{"BOOL"}, [][]any{{nil}})
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("CREATE TABLE line_compare(id integer,l line)", 0))
		run(fixture.Exec("INSERT INTO line_compare VALUES(1,'{1,2,3}'),(2,'{2,4,6}'),(3,'{2,4,7}')", 3))
		q := "SELECT id,l FROM line_compare WHERE l=$1 ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, lineSelect(b, q, []string{"id", "l"}, []string{"INT4", "LINE"}, [][]any{{int64(1), "{1,2,3}"}, {int64(2), "{2,4,6}"}}, "{3,6,9}"))
		for _, op := range []string{"<", ">", "<=", ">=", "<>", "!=", "~="} {
			for _, q := range []string{"SELECT line '{1,2,3}' " + op + " line '{2,4,6}'", "SELECT NULL::line " + op + " NULL::line", "SELECT l " + op + " l FROM line_compare WHERE FALSE"} {
				name := op
				if name == "!=" {
					name = "<>"
				}
				pos := fixture.Position(strings.Index(q, " "+op+" ") + 2)
				for _, prepared := range []bool{false, true} {
					run(boxError(q, "42883", "operator does not exist: line "+name+" line", pos, "No operator matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
				}
			}
		}
		for _, tc := range []struct{ q, code, message, hint, token string }{
			{"SELECT l FROM line_compare ORDER BY l", "42883", "could not identify an ordering operator for type line", "Use an explicit ordering operator or modify the query.", "l"},
			{"SELECT DISTINCT l FROM line_compare", "42883", "could not identify an equality operator for type line", "", "l"},
			{"SELECT l FROM line_compare GROUP BY l", "42883", "could not identify an equality operator for type line", "", "l"},
			{"CREATE INDEX line_btree ON line_compare(l)", "42704", "data type line has no default operator class for access method \"btree\"", "You must specify an operator class for the index or define a default operator class for the data type.", ""},
			{"CREATE INDEX line_hash ON line_compare USING hash(l)", "42704", "data type line has no default operator class for access method \"hash\"", "You must specify an operator class for the index or define a default operator class for the data type.", ""},
		} {
			pos := ""
			if tc.token != "" {
				pos = fixture.Position(strings.LastIndex(tc.q, tc.token) + 1)
				if strings.Contains(tc.q, "DISTINCT") {
					pos = fixture.Position(strings.Index(tc.q, " l ") + 2)
				}
			}
			// Utility opclass resolution occurs at execution, unlike SELECT analysis.
			for _, prepared := range []bool{false, true} {
				run(boxError(tc.q, tc.code, tc.message, pos, tc.hint, prepared, strings.HasPrefix(tc.q, "CREATE INDEX")))
			}
		}
		run(fixture.Select("SELECT oid::bigint,oprleft::bigint,oprright::bigint,oprresult::bigint,oprcode::oid::bigint FROM pg_operator WHERE oid=1616", []string{"oid", "oprleft", "oprright", "oprresult", "oprcode"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(1616), int64(628), int64(628), int64(16), int64(1492)}}))
		run(fixture.Select("SELECT line_eq(line '{1,2,3}',line '{2,4,6}') AS eq", []string{"eq"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
