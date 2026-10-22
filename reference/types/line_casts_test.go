package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceLineCharacterCasts(t *testing.T) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		s := fixture.Select("SELECT line '{1,2,3}'::text AS t,line '{1,2,3}'::varchar(4) AS v,line '{1,2,3}'::char(9) AS c", []string{"t", "v", "c"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"{1,2,3}", "{1,2", "{1,2,3}  "}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Exec("CREATE TABLE line_chars(id integer,v varchar(4),c char(4))", 0))
		for _, column := range []string{"v", "c"} {
			q := "INSERT INTO line_chars(id," + column + ") VALUES(1,'ok'),(2,$1::line::text)"
			message := "value too long for type character varying(4)"
			if column == "c" {
				message = "value too long for type character(4)"
			}
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "22001", message, "", "", prepared, true, "{1,2,3}"))
			}
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, boxError(q, "22001", message, "", "", false, true, "{1,2,3}"))
			stmt.Close()
		}
		run(fixture.Select("SELECT count(*) AS n FROM line_chars", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, tc := range []struct{ source, target, expr string }{{"integer", "line", "1"}, {"line", "integer", "(line '{1,2,3}')"}} {
			for _, cast := range []struct{ q, token string }{{"SELECT " + tc.expr + "::" + tc.target, "::"}, {"SELECT CAST(" + tc.expr + " AS " + tc.target + ")", "CAST"}} {
				pos := fixture.Position(utf8.RuneCountInString(cast.q[:strings.Index(cast.q, cast.token)]) + 1)
				for _, prepared := range []bool{false, true} {
					run(boxError(cast.q, "42846", "cannot cast type "+tc.source+" to "+tc.target, pos, "", prepared, false))
				}
			}
		}
	})
}
