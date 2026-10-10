package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeOverlapsResolution(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			expression string
			want       any
		}{
			{"(TIMESTAMP '2001-01-01','2001-01-03') OVERLAPS (TIMESTAMP '2001-01-02','2001-01-04')", true},
			{"(TIMESTAMP '2001-01-01',DATE '2001-01-03') OVERLAPS (DATE '2001-01-02',DATE '2001-01-04')", true},
			{"(TIMESTAMP '2001-01-01',TIMESTAMPTZ '2001-01-03+00') OVERLAPS (TIMESTAMPTZ '2001-01-02+00',TIMESTAMP '2001-01-04')", true},
			{"(TIME '01:00',TIME '03:00') OVERLAPS (TIMETZ '02:00+00',TIMETZ '04:00+00')", true},
			{"(DATE '2001-01-01',TIME '01:00') OVERLAPS (DATE '2001-01-01',TIME '02:00')", true},
			{"(TIMESTAMP '2001-01-01',TIME '01:00') OVERLAPS (TIMESTAMP '2001-01-01',TIME '02:00')", true},
			{"(TIMESTAMPTZ '2001-01-01+00',TIME '01:00') OVERLAPS (TIMESTAMPTZ '2001-01-01+00',TIME '02:00')", true},
			{"(TIME '12:00',TIME '24:00') OVERLAPS (TIME '13:00',TIME '14:00')", true},
			{"(TIMESTAMP '2001-01-01',TIME '24:00') OVERLAPS (TIMESTAMP '2001-01-01 12:00',TIMESTAMP '2001-01-01 12:00')", true},
			{"(TIMESTAMP '2001-01-02',NULL::time) OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04')", true},
			{"(TIME '12:00',TIME '24:00') OVERLAPS (TIME '12:00',TIME '12:00')", true},
		} {
			t.Run(tc.expression, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, tc.expression, tc.want) })
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN overlaps_stamp AS timestamp", 0))
		datetimeOverlapsValue(t, p, b, c, "('2001-01-01'::overlaps_stamp,'2001-01-03'::overlaps_stamp) OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')", true)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE 'America/Denver'", 0))
		// DATE must be coerced before duration arithmetic. Midnight Apr 3 to Apr 4
		// spans only 23 hours here. Apr 4 midnight is excluded by one calendar day
		// but included by 24 elapsed hours; 23:30 on Apr 3 lies inside both.
		for _, duration := range []string{"1 day", "24 hours"} {
			expression := "(DATE '2005-04-03',INTERVAL '" + duration + "') OVERLAPS (TIMESTAMPTZ '2005-04-03 23:30-06',TIMESTAMPTZ '2005-04-03 23:30-06')"
			t.Run("DATE/DST/"+duration, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, expression, true) })
			// Both start operands DATE: no known TIMESTAMPTZ forces the selection.
			expression = "(DATE '2005-04-03',INTERVAL '" + duration + "') OVERLAPS (DATE '2005-04-04',INTERVAL '0')"
			t.Run("preferred/"+duration, func(t *testing.T) { datetimeOverlapsValue(t, p, b, c, expression, duration == "24 hours") })
		}
	})
}

func TestReferenceDatetimeOverlapsPrepared(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, suffix := range []string{"", " WHERE false"} {
			var rows [][]any
			if suffix == "" {
				rows = [][]any{{true}}
			}
			datetimeOverlapsBoth(t, p, b, c, referenceSelect("SELECT (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')"+suffix, []string{"overlaps"}, []string{"BOOL"}, rows))
		}
		query := "SELECT ($1,TIMESTAMP '2001-01-03') OVERLAPS ($2,TIMESTAMP '2001-01-04')"
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
		for _, tc := range []struct {
			args []any
			want any
		}{
			{[]any{"2001-01-01", "2001-01-02"}, true}, {[]any{"2001-01-01", "2001-01-05"}, false},
			{[]any{nil, "2001-01-02"}, true}, {[]any{nil, nil}, nil},
		} {
			s := referenceSelect(query, []string{"overlaps"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.args...)
			s.checkMetadata = datetimeOverlapsMetadata
			referenceRunStep(t, p.ctx, x, b.recorder, s)
		}
		d := controlsReferenceDiagnostic("22007", "invalid input syntax for type timestamp: \"bad\"")
		d.Context = "unnamed portal parameter $1 = '...'"
		s := referenceErrorDiagnostic(query, d, "bad", "2001-01-02")
		s.selectRows = true
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, x, b.recorder, s)
		referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(query, []string{"overlaps"}, []string{"BOOL"}, [][]any{{true}}, "2001-01-01", "2001-01-02"))
		zoned := "SELECT (DATE '2005-04-03',INTERVAL '24 hours') OVERLAPS (DATE '2005-04-04',INTERVAL '0')"
		held, err := c.PrepareContext(p.ctx, zoned)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		z := referenceHeldStatement{referenceExecutor: c, statement: held, query: zoned}
		for _, zone := range []string{"UTC", "America/Denver", "UTC"} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+zone+"'", 0))
			referenceRunStep(t, p.ctx, z, b.recorder, referenceSelect(zoned, []string{"overlaps"}, []string{"BOOL"}, [][]any{{zone == "America/Denver"}}))
		}
	})
}
