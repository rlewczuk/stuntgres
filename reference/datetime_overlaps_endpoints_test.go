package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDatetimeOverlapsEndpoints(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, typ := range []string{"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "TIMETZ"} {
			for _, tc := range []struct {
				points [4]int
				want   bool
			}{
				{[4]int{1, 3, 2, 4}, true}, {[4]int{1, 2, 3, 4}, false}, {[4]int{1, 4, 2, 3}, true},
				{[4]int{1, 2, 2, 3}, false}, {[4]int{2, 2, 1, 3}, true}, {[4]int{1, 1, 1, 3}, true},
				{[4]int{3, 3, 1, 3}, false}, {[4]int{2, 2, 2, 2}, true}, {[4]int{1, 1, 2, 2}, false},
				{[4]int{1, 3, 2, 2}, true}, {[4]int{1, 3, 1, 1}, true}, {[4]int{1, 3, 3, 3}, false},
				{[4]int{3, 1, 2, 4}, true}, {[4]int{1, 3, 4, 2}, true}, {[4]int{2, 1, 2, 3}, false}, {[4]int{1, 2, 3, 2}, false},
			} {
				t.Run(fmt.Sprintf("%s/%v", typ, tc.points), func(t *testing.T) {
					var x [4]string
					for i, n := range tc.points {
						x[i] = datetimeOverlapsPoint(typ, n)
					}
					datetimeOverlapsValue(t, p, b, c, datetimeOverlapsPair(x[0], x[1], x[2], x[3]), tc.want)
				})
			}
		}
		for _, tc := range []struct {
			expression string
			want       bool
		}{
			{"(DATE '2001-02-16',DATE '2001-12-21') OVERLAPS (DATE '2001-10-30',DATE '2002-10-30')", true},
			{"(DATE '2001-02-16',INTERVAL '100 days') OVERLAPS (DATE '2001-10-30',DATE '2002-10-30')", false},
			{"(DATE '2001-10-29',DATE '2001-10-30') OVERLAPS (DATE '2001-10-30',DATE '2001-10-31')", false},
			{"(DATE '2001-10-30',DATE '2001-10-30') OVERLAPS (DATE '2001-10-30',DATE '2001-10-31')", true},
			{"(TIME '00:00',TIME '00:00') OVERLAPS (TIME '24:00',TIME '24:00')", false},
			{"(TIMETZ '09:00+01',TIMETZ '09:00+01') OVERLAPS (TIMETZ '08:00+00',TIMETZ '08:00+00')", false},
			{"(TIMESTAMP '-infinity',TIMESTAMP 'infinity') OVERLAPS (TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01')", true},
			{"(TIMESTAMP 'infinity',TIMESTAMP 'infinity') OVERLAPS (TIMESTAMP '-infinity',TIMESTAMP 'infinity')", false},
			{"(TIMESTAMPTZ 'infinity',TIMESTAMPTZ 'infinity') OVERLAPS (TIMESTAMPTZ 'infinity',TIMESTAMPTZ 'infinity')", true},
			{"overlaps(TIME '01:00',TIME '03:00',TIME '02:00',TIME '04:00')", true},
			{"pg_catalog.overlaps(TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03',TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')", true},
			{"\"overlaps\"(TIME '01:00',TIME '02:00',TIME '02:00',TIME '03:00')", false},
		} {
			t.Run(tc.expression, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, tc.expression, tc.want) })
		}
	})
}
