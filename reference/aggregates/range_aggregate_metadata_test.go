package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeAggregateMetadata(t *testing.T) {
	for _, family := range []struct {
		r, m       string
		roid, moid int
	}{{"INT4RANGE", "INT4MULTIRANGE", 3904, 4451}, {"INT8RANGE", "INT8MULTIRANGE", 3926, 4536}, {"NUMRANGE", "NUMMULTIRANGE", 3906, 4532}} {
		for _, multi := range []bool{false, true} {
			for _, depth := range []int{0, 1, 2} {
				t.Run(fmt.Sprintf("%s/multi=%v/domain=%d", family.r, multi, depth), func(t *testing.T) {
					rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						input, intersection, oid := family.r, family.r, family.roid
						nonempty, empty, intersectValue := "[1,4)", "empty", "[1,4)"
						if multi {
							input, intersection, oid = family.m, family.m, family.moid
							nonempty, empty, intersectValue = "{[1,4)}", "{}", "{[1,4)}"
						}
						for d := 1; d <= depth; d++ {
							name := fmt.Sprintf("ra_domain%d", d)
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN "+name+" AS "+input, 0))
							input = name
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k INT,v "+input+")", 0))
						families := []string{family.m, intersection}
						check := func(q string, columns []string, rows [][]any) {
							s := rangeAggregateStep(b.PG, q, columns, families, rows)
							s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rangeAggregateMetadata(t, rows, b.PG, families) }
							rangeAggregateRoutes(t, p, b, s)
						}
						q := "SELECT range_agg(v),range_intersect_agg(v) FROM r"
						check(q, []string{"range_agg", "range_intersect_agg"}, [][]any{{nil, nil}})
						check(q+" GROUP BY k", []string{"range_agg", "range_intersect_agg"}, nil)
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES(1,NULL),(2,NULL)", 2))
						check(q, []string{"range_agg", "range_intersect_agg"}, [][]any{{nil, nil}})
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES(3,'"+empty+"')", 1))
						check(q, []string{"range_agg", "range_intersect_agg"}, [][]any{{"{}", empty}})
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM r", 3))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES(1,'"+nonempty+"')", 1))
						check(q, []string{"range_agg", "range_intersect_agg"}, [][]any{{"{[1,4)}", intersectValue}})
						for _, predicate := range []string{"FALSE", "NULL"} {
							check("SELECT range_agg(v) FILTER(WHERE "+predicate+"),range_intersect_agg(v) FILTER(WHERE "+predicate+") FROM r", []string{"range_agg", "range_intersect_agg"}, [][]any{{nil, nil}})
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW ra_metadata AS SELECT range_agg(v) AS u,range_intersect_agg(v) AS i FROM r", 0))
						if b.PG {
							// Physical result identities are base overloads, never domain OIDs.
							s := fixture.Select("SELECT attname,atttypid::text,atttypmod,attcollation::text FROM pg_catalog.pg_attribute WHERE attrelid='ra_metadata'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation"}, []string{"NAME", "TEXT", "INT4", "TEXT"}, [][]any{{"u", fmt.Sprint(family.moid), int64(-1), "0"}, {"i", fmt.Sprint(oid), int64(-1), "0"}})
							rangeAggregateRoutes(t, p, b, s)
						}
					})
				})
			}
		}
	}
}
