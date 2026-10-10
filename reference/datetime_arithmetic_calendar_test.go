package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func datetimeArithmeticText(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, expr, want string) {
	t.Helper()
	datetimeArithmeticBoth(t, p, b, x, referenceSelect("SELECT ("+expr+")::TEXT AS result", []string{"result"}, []string{"TEXT"}, [][]any{{want}}))
}

func TestReferenceDatetimeArithmeticCalendarBoundaries(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{"DATE '2024-02-28'+2", "2024-03-01"},
			{"DATE '0001-12-31 BC'+1", "0001-01-01"},
			{"DATE '0001-01-01'-1", "0001-12-31 BC"},
			{"DATE '5874897-12-31'-1", "5874897-12-30"},
			{"TIMESTAMP '2024-01-31 12:34:56.123456'+INTERVAL '1 month'", "2024-02-29 12:34:56.123456"},
			{"TIMESTAMP '2023-01-31 12:00'+INTERVAL '1 month'", "2023-02-28 12:00:00"},
			{"TIMESTAMP '2024-03-31'+INTERVAL '1 month'", "2024-04-30 00:00:00"},
			{"TIMESTAMP '2024-03-31'+INTERVAL '2 months'", "2024-05-31 00:00:00"},
			{"TIMESTAMP '2024-01-31'+INTERVAL '1 month 1 day 1 microsecond'", "2024-03-01 00:00:00.000001"},
			{"TIMESTAMP '0001-12-31 BC'+INTERVAL '1 day'", "0001-01-01 00:00:00"},
			{"TIMESTAMP '2024-01-01 01:00'-TIMESTAMP '2024-01-03 03:00'", "-2 days -02:00:00"},
			{"TIMESTAMP '2024-01-02'-TIMESTAMP '2024-01-01'", "1 day"},
			{"TIME '01:00'+INTERVAL '2 months 3 days 2 hours'", "03:00:00"},
			{"TIME '01:00'-INTERVAL '2 months 3 days 2 hours'", "23:00:00"},
			{"TIME '24:00'+INTERVAL '0'", "00:00:00"},
			{"TIME '24:00'-INTERVAL '0'", "00:00:00"},
			{"TIME '24:00'-TIME '00:00'", "24:00:00"},
			{"TIMETZ '24:00-05:30:15'+INTERVAL '0'", "00:00:00-05:30:15"},
			{"TIMETZ '01:00+02:03:04'+INTERVAL '2 months 3 days 2 hours'", "03:00:00+02:03:04"},
		} {
			t.Run(tc.expr, func(t *testing.T) { datetimeArithmeticText(t, p, b, c, tc.expr, tc.want) })
		}
		datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT DATE '5874897-12-31'-DATE '4714-11-24 BC' AS result", []string{"result"}, []string{"INT4"}, [][]any{{int64(2147483493)}}))
	})
}

func datetimeArithmeticDST(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ zone, stamp, duration, plus, minus string }{
		{"America/Denver", "2005-04-02 12:00-07", "1 day", "2005-04-03 12:00:00-06", "2005-04-02 12:00:00-07"},
		{"America/Denver", "2005-04-02 12:00-07", "24 hours", "2005-04-03 13:00:00-06", "2005-04-02 12:00:00-07"},
		{"America/New_York", "2024-11-02 12:00-04", "1 day", "2024-11-03 12:00:00-05", "2024-11-02 12:00:00-04"},
		{"America/New_York", "2024-11-02 12:00-04", "24 hours", "2024-11-03 11:00:00-05", "2024-11-02 12:00:00-04"},
		{"America/New_York", "2018-03-10 02:30-05", "1 day", "2018-03-11 03:30:00-04", "2018-03-10 03:30:00-05"},
		{"America/New_York", "2018-11-03 01:30-04", "1 day", "2018-11-04 01:30:00-05", "2018-11-03 01:30:00-04"},
	} {
		t.Run(tc.zone+"_"+tc.stamp+"_"+tc.duration, func(t *testing.T) {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			stamp, interval := "TIMESTAMPTZ '"+tc.stamp+"'", "INTERVAL '"+tc.duration+"'"
			datetimeArithmeticText(t, p, b, c, stamp+"+"+interval, tc.plus)
			datetimeArithmeticText(t, p, b, c, interval+"+"+stamp, tc.plus)
			datetimeArithmeticText(t, p, b, c, "TIMESTAMPTZ '"+tc.plus+"'-"+interval, tc.minus)
		})
	}
}

func TestReferenceDatetimeArithmeticDSTAndExecutionSettings(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		datetimeArithmeticDST(t, p, b, c)
		datetimeArithmeticText(t, p, b, c, "TIMESTAMPTZ '2013-07-01 12:00-04'-TIMESTAMPTZ '2013-03-01 12:00-05'", "121 days 23:00:00")
		for _, interval := range []string{"1 day", "24 hours"} {
			datetimeArithmeticValue(t, p, b, c, "TIMESTAMP '2005-04-02 12:00'+INTERVAL '"+interval+"'", "TIMESTAMP", "2005-04-03 12:00:00")
		}
	})
}

func TestReferenceDatetimeArithmeticHeldParametersAndLiteralFreeze(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE 'America/Denver'", 0))
		q := "SELECT (TIMESTAMPTZ '2005-04-02 12:00'+INTERVAL '1 day')::TEXT AS result"
		held := timezoneHeld(t, p, c, q)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"2005-04-03 12:00:00-06"}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE 'UTC'", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"2005-04-03 19:00:00+00"}}))
		parameters := make(map[string]referenceHeldStatement)
		for _, tc := range []struct {
			query, typ string
			args       []any
			want       any
		}{
			{"SELECT $1::DATE+$2::INTEGER AS result", "DATE", []any{"2024-02-28", 1}, datetimeArithmeticRaw(t, b.pg, "DATE", "2024-02-29")},
			{"SELECT $1::DATE+$2::INTEGER AS result", "DATE", []any{nil, 1}, nil},
			{"SELECT $1::INTERVAL*$2 AS result", "INTERVAL", []any{"1 hour", 3.5}, "03:30:00"},
			{"SELECT $1::INTERVAL*$2 AS result", "INTERVAL", []any{nil, 0}, nil},
			{"SELECT $1::TIMESTAMP+$2::INTERVAL AS result", "TIMESTAMP", []any{time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC), "1 month"}, time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)},
		} {
			h, ok := parameters[tc.query]
			if !ok {
				h = timezoneHeld(t, p, c, tc.query)
				parameters[tc.query] = h
			}
			s := referenceSelect(tc.query, []string{"result"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...)
			referenceRunStep(t, p.ctx, h, b.recorder, s)
		}
		q = "SELECT ($1::TIMESTAMPTZ+INTERVAL '1 day')::TEXT AS result"
		h := timezoneHeld(t, p, c, q)
		for _, tc := range []struct{ zone, want string }{{"America/Denver", "2005-04-03 12:00:00-06"}, {"UTC", "2005-04-03 19:00:00+00"}} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			referenceRunStep(t, p.ctx, h, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{tc.want}}, "2005-04-02 12:00-07"))
		}
	})
}

func TestReferenceDatetimeArithmeticComparisons(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		ops := []string{"=", "<>", "<", "<=", ">", ">="}
		families := []struct{ types, equal, low, high []string }{
			{[]string{"DATE", "TIMESTAMP", "TIMESTAMPTZ"}, []string{"DATE '2024-01-01'", "TIMESTAMP '2024-01-01'", "TIMESTAMPTZ '2024-01-01+00'"}, []string{"DATE '2023-12-31'", "TIMESTAMP '2023-12-31'", "TIMESTAMPTZ '2023-12-31+00'"}, []string{"DATE '2024-01-02'", "TIMESTAMP '2024-01-02'", "TIMESTAMPTZ '2024-01-02+00'"}},
			{[]string{"TIME"}, []string{"TIME '12:00'"}, []string{"TIME '11:00'"}, []string{"TIME '13:00'"}},
			{[]string{"TIMETZ"}, []string{"TIMETZ '08:00+00'"}, []string{"TIMETZ '09:00+01'"}, []string{"TIMETZ '09:00+00'"}},
			{[]string{"INTERVAL"}, []string{"INTERVAL '30 days'"}, []string{"INTERVAL '29 days'"}, []string{"INTERVAL '31 days'"}},
		}
		for _, f := range families {
			for i, lt := range f.types {
				for j, rt := range f.types {
					for k, op := range ops {
						for _, pair := range []struct {
							left, right string
							want        bool
						}{
							{f.equal[i], f.equal[j], []bool{true, false, false, true, false, true}[k]},
							{f.low[i], f.equal[j], []bool{false, true, true, true, false, false}[k]},
							{f.high[i], f.equal[j], []bool{false, true, false, false, true, true}[k]},
						} {
							datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT "+pair.left+op+pair.right+" AS result", []string{"result"}, []string{"BOOL"}, [][]any{{pair.want}}))
						}
						for _, expr := range []string{"NULL::" + lt + op + f.equal[j], f.equal[i] + op + "NULL::" + rt, "NULL::" + lt + op + "NULL::" + rt} {
							datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT "+expr+" AS result", []string{"result"}, []string{"BOOL"}, [][]any{{nil}}))
						}
					}
				}
			}
		}
		for _, lt := range []string{"DATE", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL"} {
			rightTypes := []string{lt}
			if lt != "INTERVAL" {
				rightTypes = []string{"DATE", "TIMESTAMP", "TIMESTAMPTZ"}
			}
			for _, rt := range rightTypes {
				for k, op := range ops {
					for _, pair := range []struct {
						a, z string
						want bool
					}{
						{"-infinity", "infinity", []bool{false, true, true, true, false, false}[k]},
						{"infinity", "-infinity", []bool{false, true, false, false, true, true}[k]},
						{"infinity", "infinity", []bool{true, false, false, true, false, true}[k]},
					} {
						q := "SELECT '" + pair.a + "'::" + lt + op + "'" + pair.z + "'::" + rt + " AS result"
						datetimeArithmeticBoth(t, p, b, c, referenceSelect(q, []string{"result"}, []string{"BOOL"}, [][]any{{pair.want}}))
					}
				}
			}
		}
		for _, expr := range []string{"INTERVAL '1 month'=INTERVAL '30 days'", "INTERVAL '1 month'=INTERVAL '720 hours'", "INTERVAL '1 day'=INTERVAL '24 hours'", "DATE '5874897-12-31'>TIMESTAMPTZ '294276-12-31+00'", "DATE '-infinity'<TIMESTAMP '2024-01-01'", "TIMESTAMPTZ 'infinity'>DATE '5874897-12-31'"} {
			datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT "+expr+" AS result", []string{"result"}, []string{"BOOL"}, [][]any{{true}}))
		}
		datetimeArithmeticZoneComparison(t, p, b, c)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE dt_compare(id INTEGER,d DATE)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dt_compare VALUES(1,'2024-01-01'),(2,'2024-01-01'),(3,'2024-01-02')", 3))
		s := referenceSelect("SELECT d+1 AS result FROM dt_compare WHERE d+1=DATE '2024-01-02'", []string{"result"}, []string{"DATE"}, [][]any{{datetimeArithmeticRaw(t, b.pg, "DATE", "2024-01-02")}, {datetimeArithmeticRaw(t, b.pg, "DATE", "2024-01-02")}})
		s.bag = true
		datetimeArithmeticBoth(t, p, b, c, s)
		datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT id FROM dt_compare ORDER BY d+1,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	})
}

func datetimeArithmeticZoneComparison(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	q := "SELECT DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00' AS d,TIMESTAMP '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00' AS s"
	h := timezoneHeld(t, p, c, q)
	for _, tc := range []struct {
		zone string
		want bool
	}{{"America/New_York", true}, {"UTC", false}} {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
		s := referenceSelect(q, []string{"d", "s"}, []string{"BOOL", "BOOL"}, [][]any{{tc.want, tc.want}})
		referenceRunStep(t, p.ctx, h, b.recorder, s)
		datetimeArithmeticBoth(t, p, b, c, s)
	}
}

// TIME borrows the current execution offset through the existing implicit cast;
// the explicit TIMETZ datum keeps its written offset, including tie-breaking.
func TestReferenceDatetimeArithmeticClockComparisonPromotion(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
			want := map[string]bool{"=": true, "<>": false, "<": false, "<=": true, ">": false, ">=": true}[op]
			for _, expr := range []string{"TIME '12:00'" + op + "TIMETZ '12:00+00'", "TIMETZ '12:00+00'" + op + "TIME '12:00'"} {
				datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT "+expr+" AS result", []string{"result"}, []string{"BOOL"}, [][]any{{want}}))
			}
			for _, expr := range []string{"NULL::TIME" + op + "TIMETZ '12:00+00'", "TIME '12:00'" + op + "NULL::TIMETZ", "NULL::TIMETZ" + op + "TIME '12:00'", "TIMETZ '12:00+00'" + op + "NULL::TIME", "NULL::TIME" + op + "NULL::TIMETZ", "NULL::TIMETZ" + op + "NULL::TIME"} {
				datetimeArithmeticBoth(t, p, b, c, referenceSelect("SELECT "+expr+" AS result", []string{"result"}, []string{"BOOL"}, [][]any{{nil}}))
			}
		}
		q := "SELECT $1::TIME=TIMETZ '12:00+00' AS forward,TIMETZ '12:00+00'=$1::TIME AS reverse"
		h := timezoneHeld(t, p, c, q)
		for _, tc := range []struct {
			zone string
			want bool
		}{{"UTC", true}, {"Etc/GMT-2", false}, {"UTC", true}} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			s := referenceSelect(q, []string{"forward", "reverse"}, []string{"BOOL", "BOOL"}, [][]any{{tc.want, tc.want}}, "12:00")
			referenceRunStep(t, p.ctx, h, b.recorder, s)
			datetimeArithmeticBoth(t, p, b, c, s)
		}
	})
}
