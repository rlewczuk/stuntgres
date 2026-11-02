package reference_test

import (
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func temporalBoundarySearch(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		values []string
	}{
		{"date", []string{"-infinity", "4714-11-24 BC", "0001-12-31 BC", "0001-01-01", "1900-02-28", "1900-03-01", "1999-12-31", "2000-01-01", "2000-02-29", "2000-03-01", "5874897-12-31", "infinity"}},
		{"time", []string{"00:00", "00:00:00.000001", "11:59:59.999999", "12:00", "23:59:59.999999", "24:00"}},
		{"timetz", []string{"00:00+15:59:59", "00:00+00", "09:00+01", "08:00+00", "24:00+00", "24:00-15:59:59"}},
		{"timestamp", []string{"-infinity", "4714-11-24 00:00 BC", "0001-12-31 23:59:59.999999 BC", "0001-01-01 00:00", "1900-02-28 23:59:59.999999", "1900-03-01", "1999-12-31 23:59:59.999999", "2000-01-01", "2000-02-29 23:59:59.999999", "2000-03-01", "294276-12-31 23:59:59.999999", "infinity"}},
		{"timestamptz", []string{"-infinity", "4714-11-24 00:00+00 BC", "0001-12-31 23:59:59.999999+00 BC", "0001-01-01 00:00+00", "1900-02-28 23:59:59.999999+00", "1900-03-01 00:00+00", "1999-12-31 23:59:59.999999+00", "2000-01-01 00:00+00", "2000-02-29 23:59:59.999999+00", "2000-03-01 00:00+00", "294276-12-31 23:59:59.999999+00", "infinity"}},
		// Exact all-MIN/all-MAX triples are infinity sentinels, not finite keys.
		{"interval", []string{"-infinity", "-2147483648 months -2147483648 days -9223372036854775807 microseconds", "-9223372036854775808 microseconds", "-1 microsecond", "0", "1 microsecond", "9223372036854775807 microseconds", "2147483647 months 2147483647 days 9223372036854775806 microseconds", "infinity"}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("SET TimeZone='UTC'", 0))
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE TABLE temporal_edges(id integer NOT NULL,v "+tc.typ+")", 0))
				var rows []string
				for i, value := range tc.values {
					rows = append(rows, "("+strconv.Itoa(i+1)+","+networkLiteral(value, tc.typ)+")")
				}
				run(fixture.Exec("INSERT INTO temporal_edges VALUES"+strings.Join(rows, ","), int64(len(rows))))
				q := "SELECT id FROM temporal_edges WHERE v=$1::" + tc.typ + " ORDER BY id"
				held := classHeld(t, p, conn, q)
				check := func() {
					if tc.typ == "interval" {
						var finite [][]any
						for i := range tc.values {
							finite = append(finite, []any{int64(i + 1), i != 0 && i != len(tc.values)-1})
						}
						classRoutes(t, p, b, conn, fixture.Select("SELECT id,isfinite(v) AS finite FROM temporal_edges ORDER BY id", []string{"id", "finite"}, []string{"INT4", "BOOL"}, finite))
					}
					for i, value := range tc.values {
						classRoutes(t, p, b, conn, ginIDs(q, []int64{int64(i + 1)}, value))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{int64(i + 1)}, value))
						var below, above []int64
						for j := range tc.values {
							if j < i {
								below = append(below, int64(j+1))
							}
							if j > i {
								above = append(above, int64(j+1))
							}
						}
						classRoutes(t, p, b, conn, ginIDs("SELECT id FROM temporal_edges WHERE v<$1::"+tc.typ+" ORDER BY id", below, value))
						classRoutes(t, p, b, conn, ginIDs("SELECT id FROM temporal_edges WHERE v>$1::"+tc.typ+" ORDER BY id", above, value))
					}
				}
				check()
				run(fixture.Exec("CREATE INDEX temporal_edges_i ON temporal_edges USING gin(v public."+tc.typ+"_ops)", 0))
				check()
				if tc.typ == "timestamp" || tc.typ == "timestamptz" {
					zone := ""
					if tc.typ == "timestamptz" {
						zone = "+00"
					}
					s := fixture.Select("SELECT '1999-12-31 23:59:59.9999999"+zone+"'::"+tc.typ+"='2000-01-01 00:00"+zone+"'::"+tc.typ+" AS carry", []string{"carry"}, []string{"BOOL"}, [][]any{{true}})
					classRoutes(t, p, b, conn, s)
				}
			})
		})
	}
	t.Run("interval_component_equivalence", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
			run(fixture.Exec("CREATE TABLE temporal_equiv(id integer NOT NULL,v interval)", 0))
			run(fixture.Exec("CREATE INDEX temporal_equiv_i ON temporal_equiv USING gin(v interval_ops)", 0))
			run(fixture.Exec("INSERT INTO temporal_equiv VALUES(1,'1 mon'),(2,'30 days'),(3,'720 hours'),(4,'2147483647 months -2147483648 days'),(5,'2147483646 months -2147483618 days')", 5))
			q := "SELECT id FROM temporal_equiv WHERE v=$1::interval ORDER BY id"
			held := classHeld(t, p, conn, q)
			for _, text := range []string{"1 mon", "30 days", "720 hours"} {
				classRoutes(t, p, b, conn, ginIDs(q, []int64{1, 2, 3}, text))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{1, 2, 3}, text))
			}
			classRoutes(t, p, b, conn, ginIDs(q, []int64{4, 5}, "2147483647 months -2147483648 days"))
			classRoutes(t, p, b, conn, fixture.Select("SELECT id,v FROM temporal_equiv WHERE id<=3 ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "1 mon"}, {int64(2), "30 days"}, {int64(3), "720:00:00"}}))
		})
	})
}
