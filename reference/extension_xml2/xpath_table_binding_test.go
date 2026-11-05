package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableBinding(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "xt space")
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xtSeed(t, p, b, c)
		xtExec(t, p, b, c,
			`CREATE SCHEMA "xt space"`,
			`CREATE TABLE "xt space"."source 雪"(id integer,doc xml)`,
			"CREATE VIEW xt_view AS SELECT id,doc FROM xt_src WHERE id=1")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO "xt space"."source 雪" VALUES(1,'<doc><a>10</a><a>20</a></doc>')`, 1))
		for _, tc := range []struct{ name, relation, key, doc, criteria string }{
			{"expressions", "xt_src", "id + 8", "doc", "id=1 AND xpath_bool(doc,'/doc/a')"},
			{"qualified_quoted_xml", `"xt space"."source 雪"`, "id + 8", "doc", "true"},
			{"view", "xt_view", "id + 8", "doc", "true"},
			{"subquery", "(SELECT id,doc FROM xt_src WHERE id=1) AS s", "id + 8", "doc", "true"},
			{"join", "xt_view s JOIN xt_src t ON s.id=t.id", "s.id + 8", "s.doc", "true"},
		} {
			s := fixture.Select(xtQuery(tc.key, tc.doc, tc.relation, "/doc/a", tc.criteria, "k text,a integer"), []string{"k", "a"}, []string{"TEXT", "INT4"}, [][]any{{"9", int64(10)}, {"9", int64(20)}})
			s.Bag = true
			xtBoth(t, p, b, c, tc.name, s)
		}
		xtExec(t, p, b, c,
			"CREATE TYPE xt_color AS ENUM('red','blue')",
			"CREATE TYPE xt_pair AS (n integer,s text)")
		typed := "SELECT id,e::text AS e,a::text AS a,c::text AS c,v FROM xpath_table('id'," +
			xtQuote("'<doc><e>red</e><a>{1,2}</a><c>(3,hi)</c><v>xy</v></doc>'") +
			",'xt_src','/doc/e|/doc/a|/doc/c|/doc/v','id=1') AS x(id text,e xt_color,a integer[],c xt_pair,v varchar(3))"
		xtBoth(t, p, b, c, "declared_text_io_enum_array_composite_typmod", fixture.Select(typed,
			[]string{"id", "e", "a", "c", "v"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "VARCHAR"},
			[][]any{{"1", "red", "{1,2}", "(3,hi)", "xy"}}))
		for i := range 5 {
			args := []any{"missing_xt_key", "missing_xt_doc", "xt_absent", "[", "id ="}
			args[i] = nil
			xtBoth(t, p, b, c, fmt.Sprintf("strict_argument_%d", i+1), fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, nil, args...))
		}
		// Actual held execution resolves source text afresh, not during Prepare.
		t.Run("held_dynamic_source", func(t *testing.T) {
			xml2Held(t, p, b, c,
				fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "10"}, {int64(1), "20"}}, "id", "doc", "xt_view", "/doc/a", "true"),
				fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "7"}}, "id", "doc", "xt_src", "/doc/b", "id=1"),
				fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, nil, "id", "doc", nil, "[", "true"))
		})

	})
}
