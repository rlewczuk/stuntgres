package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeOverlapsResolution(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN overlaps_stamp AS timestamp", 0))
		datetimeOverlapsValue(t, p, b, c, "('2001-01-01'::overlaps_stamp,'2001-01-03'::overlaps_stamp) OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')", true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/Denver'", 0))
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
	datetimeOverlapsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, suffix := range []string{"", " WHERE false"} {
			var rows [][]any
			if suffix == "" {
				rows = [][]any{{true}}
			}
			datetimeOverlapsBoth(t, p, b, c, fixture.Select("SELECT (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')"+suffix, []string{"overlaps"}, []string{"BOOL"}, rows))
		}
		query := "SELECT ($1,TIMESTAMP '2001-01-03') OVERLAPS ($2,TIMESTAMP '2001-01-04')"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, tc := range []struct {
			args []any
			want any
		}{
			{[]any{"2001-01-01", "2001-01-02"}, true}, {[]any{"2001-01-01", "2001-01-05"}, false},
			{[]any{nil, "2001-01-02"}, true}, {[]any{nil, nil}, nil},
		} {
			s := fixture.Select(query, []string{"overlaps"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.args...)
			s.CheckMetadata = datetimeOverlapsMetadata
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		}
		d := fixture.ControlsReferenceDiagnostic("22007", "invalid input syntax for type timestamp: \"bad\"")
		d.Context = "unnamed portal parameter $1 = '...'"
		s := fixture.ErrorDiagnostic(query, d, "bad", "2001-01-02")
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(query, []string{"overlaps"}, []string{"BOOL"}, [][]any{{true}}, "2001-01-01", "2001-01-02"))
		zoned := "SELECT (DATE '2005-04-03',INTERVAL '24 hours') OVERLAPS (DATE '2005-04-04',INTERVAL '0')"
		held, err := c.PrepareContext(p.Ctx, zoned)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		z := fixture.HeldStatement{Executor: c, Statement: held, SQL: zoned}
		for _, zone := range []string{"UTC", "America/Denver", "UTC"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
			fixture.RunStep(t, p.Ctx, z, b.Recorder, fixture.Select(zoned, []string{"overlaps"}, []string{"BOOL"}, [][]any{{zone == "America/Denver"}}))
		}
	})
}
