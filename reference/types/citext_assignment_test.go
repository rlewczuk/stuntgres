package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextAssignment(t *testing.T) {
	citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE citext_assignment(id integer,v citext)", 0))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("DELETE FROM citext_assignment", 0))
			q := "INSERT INTO citext_assignment VALUES (1,'MiXeD  '::text),(2,'MiXeD  '::varchar),(3,'MiX'::char(8)),(4,true),(5,false),(6,'192.0.2.1/32'::inet),(7,'192.0.2.129/24'::inet),(8,'2001:db8::1/128'::inet),(9,42::integer),(10,'Same  '::citext),(11,NULL::text),(12,NULL::citext)"
			s := fixture.Exec(q, 12)
			s.Prepared = prepared
			run(s)
			s = citextSelect(b, "SELECT id,v FROM citext_assignment ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "MiXeD  "}, {int64(2), "MiXeD  "}, {int64(3), "MiX"}, {int64(4), "true"}, {int64(5), "false"}, {int64(6), "192.0.2.1/32"}, {int64(7), "192.0.2.129/24"}, {int64(8), "2001:db8::1/128"}, {int64(9), "42"}, {int64(10), "Same  "}, {int64(11), nil}, {int64(12), nil}})
			s.Prepared = prepared
			run(s)
			for _, tc := range []struct{ expression, value string }{
				{"'MiXeD  '::text::citext", "MiXeD  "},
				{"'MiXeD  '::varchar::citext", "MiXeD  "},
				{"'MiX'::char(8)::citext", "MiX"},
				{"true::citext", "true"}, {"false::citext", "false"},
				{"'192.0.2.1/32'::inet::citext", "192.0.2.1/32"},
				{"'192.0.2.129/24'::inet::citext", "192.0.2.129/24"},
				{"'2001:db8::1/128'::inet::citext", "2001:db8::1/128"},
				{"42::integer::citext", "42"},
			} {
				s := citextSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, [][]any{{tc.value}})
				s.Prepared = prepared
				run(s)
			}
			s = fixture.Select("SELECT '42'::citext::integer AS n,'MiXeD  '::citext::text AS t,'MiXeD  '::citext::varchar AS v,'MiX'::citext::char(5) AS c,'Longer'::citext::varchar(3) AS sized", []string{"n", "t", "v", "c", "sized"}, []string{"INT4", "TEXT", "VARCHAR", "BPCHAR", "VARCHAR"}, [][]any{{int64(42), "MiXeD  ", "MiXeD  ", "MiX  ", "Lon"}})
			s.Prepared = prepared
			run(s)
			// Empty the table for the next execution route with an exact count.
			run(fixture.Exec("DELETE FROM citext_assignment", 12))
		}
		run(fixture.Exec("CREATE TABLE citext_outgoing(t text,v varchar,c char(5))", 0))
		run(fixture.Exec("INSERT INTO citext_outgoing VALUES ('MiXeD  '::citext,'MiXeD  '::citext,'MiX'::citext)", 1))
		run(fixture.Select("SELECT t,v,c FROM citext_outgoing", []string{"t", "v", "c"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"MiXeD  ", "MiXeD  ", "MiX  "}}))
		// lower(text) requires the real implicit outgoing edge; citext input
		// itself remains byte-preserving, not lowercased storage.
		run(fixture.Select("SELECT lower('MiXeD'::citext) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"mixed"}}))
	})
}
