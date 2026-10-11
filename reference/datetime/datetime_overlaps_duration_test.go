package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeOverlapsDurations(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Four signatures per family, plus the TIMETZ endpoint witness in Endpoints.
		for _, typ := range []string{"TIME", "TIMESTAMP", "TIMESTAMPTZ"} {
			duration := "INTERVAL '2 days'"
			if typ == "TIME" {
				duration = "INTERVAL '2 hours'"
			}
			for mask := range 4 {
				t.Run(fmt.Sprintf("signature/%s/%d", typ, mask), func(t *testing.T) {
					a, end, cstart, d := datetimeOverlapsPoint(typ, 1), datetimeOverlapsPoint(typ, 3), datetimeOverlapsPoint(typ, 2), datetimeOverlapsPoint(typ, 4)
					if mask&1 != 0 {
						end = duration
					}
					if mask&2 != 0 {
						d = duration
					}
					datetimeOverlapsValue(t, p, b, c, datetimeOverlapsPair(a, end, cstart, d), true)
				})
			}
			for _, tc := range []struct {
				start    int
				duration string
				want     bool
			}{
				{3, "-2 days", true}, {1, "0", true}, {3, "0", false},
			} {
				duration := tc.duration
				if typ == "TIME" && duration == "-2 days" {
					duration = "-2 hours"
				}
				expression := datetimeOverlapsPair(datetimeOverlapsPoint(typ, tc.start), "INTERVAL '"+duration+"'", datetimeOverlapsPoint(typ, 1), datetimeOverlapsPoint(typ, 3))
				t.Run(expression, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, expression, tc.want) })
			}
			negative := "INTERVAL '-2 days'"
			if typ == "TIME" {
				negative = "INTERVAL '-2 hours'"
			}
			for _, expression := range []string{
				datetimeOverlapsPair(datetimeOverlapsPoint(typ, 2), datetimeOverlapsPoint(typ, 2), datetimeOverlapsPoint(typ, 3), negative),
				datetimeOverlapsPair(datetimeOverlapsPoint(typ, 3), negative, datetimeOverlapsPoint(typ, 3), negative),
			} {
				t.Run(expression, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, expression, true) })
			}
		}
		for _, tc := range []struct {
			expression string
			want       any
		}{
			{"(TIME '23:00',INTERVAL '2 hours') OVERLAPS (TIME '12:00',TIME '12:00')", true},
			{"(TIME '23:00',INTERVAL '2 hours') OVERLAPS (TIME '00:00',TIME '00:00')", false},
			{"(TIME '12:00',INTERVAL '2 months 3 days') OVERLAPS (TIME '12:00',TIME '13:00')", true},
			{"(TIMESTAMP '2024-01-31',INTERVAL '1 month') OVERLAPS (TIMESTAMP '2024-02-29',TIMESTAMP '2024-02-29')", false},
			{"(TIMESTAMP '2024-01-31',INTERVAL '1 month') OVERLAPS (TIMESTAMP '2024-02-28',TIMESTAMP '2024-02-28')", true},
			{"(TIMESTAMP 'infinity',INTERVAL '1 day') OVERLAPS (TIMESTAMP 'infinity',TIMESTAMP 'infinity')", true},
			{"(TIMESTAMP '2001-01-01',INTERVAL 'infinity') OVERLAPS (TIMESTAMP 'infinity',TIMESTAMP 'infinity')", false},
			{"(TIMESTAMPTZ '2001-01-01+00',INTERVAL '-infinity') OVERLAPS (TIMESTAMPTZ '-infinity',TIMESTAMPTZ '-infinity')", true},
		} {
			t.Run(tc.expression, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, tc.expression, tc.want) })
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/Denver'", 0))
		for _, duration := range []string{"1 day", "24 hours"} {
			expression := "(TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '" + duration + "') OVERLAPS (TIMESTAMPTZ '2005-04-03 12:30-06',TIMESTAMPTZ '2005-04-03 12:30-06')"
			t.Run(duration, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, expression, duration == "24 hours") })
		}
	})
}
