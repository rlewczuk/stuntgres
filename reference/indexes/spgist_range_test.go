package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTRangePredicates(t *testing.T) {
	for _, tc := range []struct{ typ, a, z, elem, setup string }{
		{"int4range", "[1,3)", "[3,5)", "2::integer", ""}, {"int8range", "[1,3)", "[3,5)", "2::bigint", ""}, {"numrange", "[1,3)", "[3,5)", "2::numeric", ""},
		{"daterange", "[2020-01-01,2020-01-03)", "[2020-01-03,2020-01-05)", "date '2020-01-02'", ""},
		{"tsrange", "[2020-01-01,2020-01-03)", "[2020-01-03,2020-01-05)", "timestamp '2020-01-02'", ""},
		{"tstzrange", "[2020-01-01,2020-01-03)", "[2020-01-03,2020-01-05)", "timestamptz '2020-01-02'", ""},
		{"spgist_range_domain", "[1,3)", "[3,5)", "2::integer", "CREATE TYPE spgist_custom AS RANGE(subtype=integer); CREATE DOMAIN spgist_range_domain AS spgist_custom"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				run(fixture.Exec("CREATE TABLE spgist_ranges(id integer,v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO spgist_ranges VALUES(1,'"+tc.a+"'),(2,'"+tc.z+"'),(3,'empty'),(4,'(,)'),(5,NULL),(6,'"+tc.a+"')", 6))
				check := func(changed bool) {
					for _, c := range []struct {
						op  string
						ids []int64
					}{
						{"=", []int64{1, 6}}, {"&&", []int64{1, 4, 6}}, {"@>", []int64{1, 4, 6}}, {"<@", []int64{1, 3, 6}}, {"<<", nil}, {">>", []int64{2}}, {"&<", []int64{1, 6}}, {"&>", []int64{1, 2, 6}}, {"-|-", []int64{2}},
					} {
						if changed {
							switch c.op {
							case "=", "&<":
								c.ids = []int64{7}
							case "&&", "@>":
								c.ids = []int64{4, 7}
							case "<@":
								c.ids = []int64{1, 3, 7}
							case "&>":
								c.ids = []int64{2, 7}
							}
						}
						var rows [][]any
						for _, id := range c.ids {
							rows = append(rows, []any{id})
						}
						classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_ranges WHERE v "+c.op+" $1::"+tc.typ+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows, tc.a))
						classRoutes(t, p, b, conn, fixture.Select("SELECT v "+c.op+" $1::"+tc.typ+" AS hit FROM spgist_ranges WHERE id=5", []string{"hit"}, []string{"BOOL"}, [][]any{{nil}}, tc.a))
					}
					members := [][]any{{int64(1)}, {int64(4)}, {int64(6)}}
					if changed {
						members = [][]any{{int64(4)}, {int64(7)}}
					}
					classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_ranges WHERE v @> "+tc.elem+" ORDER BY id", []string{"id"}, []string{"INT4"}, members))
					classRoutes(t, p, b, conn, fixture.Select("SELECT v = 'empty'::"+tc.typ+" AS eq,v @> 'empty'::"+tc.typ+" AS contains,v && 'empty'::"+tc.typ+" AS overlaps FROM spgist_ranges WHERE id IN(3,5) ORDER BY id", []string{"eq", "contains", "overlaps"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}, {nil, nil, nil}}))
				}
				q := "SELECT id FROM spgist_ranges WHERE v && $1::" + tc.typ + " ORDER BY id"
				held := classHeld(t, p, conn, q)
				check(false)
				run(fixture.Exec("CREATE INDEX spgist_ranges_i ON spgist_ranges USING spgist(v)", 0))
				check(false)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(6)}}, tc.a))
				run(fixture.Exec("INSERT INTO spgist_ranges SELECT 7,v FROM spgist_ranges WHERE id=1", 1))
				run(fixture.Exec("UPDATE spgist_ranges SET v='empty' WHERE id=1", 1))
				run(fixture.Exec("DELETE FROM spgist_ranges WHERE id=6", 1))
				check(true)
				classRoutes(t, p, b, conn, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(7)}}, tc.a))
				if tc.typ == "int4range" {
					classRoutes(t, p, b, conn, fixture.Select("SELECT '[1,2]'::int4range = '[1,3)'::int4range AS canonical,'(0,2]'::int4range -|- '[3,5)'::int4range AS adjacent,'[1,3)'::int4range && '[3,5)'::int4range AS overlap", []string{"canonical", "adjacent", "overlap"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}))
				}
			})
		})
	}
}
