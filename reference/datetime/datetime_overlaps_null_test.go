package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeOverlapsNulls(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		null := "NULL::timestamp"
		left := [4][2]string{{datetimeOverlapsPoint("TIMESTAMP", 1), datetimeOverlapsPoint("TIMESTAMP", 4)}, {datetimeOverlapsPoint("TIMESTAMP", 1), null}, {null, datetimeOverlapsPoint("TIMESTAMP", 4)}, {null, null}}
		right := [4][2]string{{datetimeOverlapsPoint("TIMESTAMP", 2), datetimeOverlapsPoint("TIMESTAMP", 5)}, {datetimeOverlapsPoint("TIMESTAMP", 2), null}, {null, datetimeOverlapsPoint("TIMESTAMP", 5)}, {null, null}}
		expected := [4][4]any{{true, true, nil, nil}, {nil, nil, nil, nil}, {true, nil, nil, nil}, {nil, nil, nil, nil}}
		for i, l := range left {
			for j, r := range right {
				t.Run(fmt.Sprintf("mask/%d/%d", i, j), func(t *testing.T) {
					datetimeOverlapsValue(t, p, b, c, datetimeOverlapsPair(l[0], l[1], r[0], r[1]), expected[i][j])
				})
			}
		}
		for _, typ := range []string{"TIME", "TIMESTAMP", "TIMESTAMPTZ", "TIMETZ"} {
			for _, n := range []int{0, 1, 2, 4, 5} {
				// Use hours on a fixed day to include the exterior point zero.
				point := fmt.Sprintf("%s '2001-01-01 %02d:00'", typ, n)
				lo, hi := typ+" '2001-01-01 01:00'", typ+" '2001-01-01 04:00'"
				if typ == "TIMESTAMPTZ" {
					point = fmt.Sprintf("TIMESTAMPTZ '2001-01-01 %02d:00+00'", n)
					lo = "TIMESTAMPTZ '2001-01-01 01:00+00'"
					hi = "TIMESTAMPTZ '2001-01-01 04:00+00'"
				}
				if typ == "TIME" || typ == "TIMETZ" {
					zone := ""
					if typ == "TIMETZ" {
						zone = "+00"
					}
					point = fmt.Sprintf("%s '%02d:00%s'", typ, n, zone)
					lo = typ + " '01:00" + zone + "'"
					hi = typ + " '04:00" + zone + "'"
				}
				var want any
				if n == 2 {
					want = true
				}
				for orientation := range 4 {
					partial := [2]string{point, "NULL::" + typ}
					if orientation&1 != 0 {
						partial[0], partial[1] = partial[1], partial[0]
					}
					x := [4]string{lo, hi, partial[0], partial[1]}
					if orientation&2 != 0 {
						x = [4]string{partial[0], partial[1], lo, hi}
					}
					t.Run(fmt.Sprintf("partial/%s/%d/%d", typ, n, orientation), func(t *testing.T) {
						datetimeOverlapsValue(t, p, b, c, datetimeOverlapsPair(x[0], x[1], x[2], x[3]), want)
					})
				}
			}
			datetimeOverlapsValue(t, p, b, c, datetimeOverlapsPair(datetimeOverlapsPoint(typ, 2), datetimeOverlapsPoint(typ, 2), datetimeOverlapsPoint(typ, 2), "NULL::"+typ), nil)
		}
		for _, typ := range []string{"TIME", "TIMESTAMP", "TIMESTAMPTZ"} {
			datetimeOverlapsValue(t, p, b, c, datetimeOverlapsPair(datetimeOverlapsPoint(typ, 2), "NULL::interval", datetimeOverlapsPoint(typ, 1), datetimeOverlapsPoint(typ, 4)), true)
			datetimeOverlapsValue(t, p, b, c, datetimeOverlapsPair("NULL::"+typ, "INTERVAL '1 hour'", datetimeOverlapsPoint(typ, 1), datetimeOverlapsPoint(typ, 4)), nil)
		}
		d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		datetimeOverlapsFailure(t, p, b, c, "SELECT (NULL::timestamp,TIMESTAMP '2001-01-01'+INTERVAL '1 day'*(1/0)) OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04')", d)
		datetimeOverlapsBoth(t, p, b, c, fixture.Select("SELECT CASE WHEN false THEN (NULL::timestamp,TIMESTAMP '2001-01-01'+INTERVAL '1 day'*(1/0)) OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04') ELSE true END AS overlaps", []string{"overlaps"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
