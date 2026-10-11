package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeArithmeticZonedJulianBoundary(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'Etc/GMT+1'", 0))
		// The zoned local-day allowance must not widen the plain/DATE domains.
		datetimeArithmeticRuntimeError(t, p, b, c, "SELECT $1::TIMESTAMP-INTERVAL '1 day'", "22008", "timestamp out of range", "4714-11-24 00:00:00 BC")
		datetimeArithmeticRuntimeError(t, p, b, c, "SELECT $1::DATE-1", "22008", "date out of range", "4714-11-24 BC")
		for _, expression := range []string{"$1::TIMESTAMPTZ-INTERVAL '1 day'", "$1::TIMESTAMPTZ+INTERVAL '-1 day'", "INTERVAL '-1 day'+$1::TIMESTAMPTZ"} {
			t.Run(expression, func(t *testing.T) {
				q := "SELECT (" + expression + ") AS result,((" + expression + ") AT TIME ZONE 'UTC')::TEXT AS rendered"
				// Released source permits local Julian -1 when rotation yields
				// a valid instant, including the exact timestamp minimum.
				for _, tc := range []struct {
					input, rendered string
					minute          int
				}{
					{"4714-11-25 00:30:00+00 BC", "4714-11-24 00:30:00 BC", 30},
					{"4714-11-25 00:00:00+00 BC", "4714-11-24 00:00:00 BC", 0},
				} {
					want := time.Date(-4713, time.November, 24, 0, tc.minute, 0, 0, time.UTC)
					s := fixture.Select(q, []string{"result", "rendered"}, []string{"TIMESTAMPTZ", "TEXT"}, [][]any{{want, tc.rendered}}, tc.input)
					s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
						var raw time.Time
						var rendered string
						if err := rows.Scan(&raw, &rendered); err != nil {
							t.Fatal(err)
						}
						_, east := raw.Zone()
						if !raw.Equal(want) || east != -3600 || raw.Year() != -4713 || raw.Month() != time.November || raw.Day() != 23 || raw.Hour() != 23 || raw.Minute() != tc.minute || rendered != tc.rendered {
							t.Fatal("raw zoned boundary", raw, east, rendered)
						}
					}
					datetimeArithmeticContractBoth(t, p, b, c, s)
				}
				// One microsecond below the actual instant minimum remains an
				// error even though the destination local day is permitted.
				datetimeArithmeticRuntimeError(t, p, b, c, q, "22008", "timestamp out of range", "4714-11-24 23:59:59.999999+00 BC")
			})
		}
	})
}

func TestReferenceDatetimeArithmeticZonedMonthStages(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		// Source-derived goldens: each month destination resolves first; a day
		// stage then retains that resolved clock and re-resolves its own offset.
		for _, tc := range []struct{ name, input, interval, op, want string }{
			{"gap_month", "2018-02-11 02:30-05", "1 month", "+", "2018-03-11 03:30:00-04"},
			{"fold_month", "2018-10-04 01:30-04", "1 month", "+", "2018-11-04 01:30:00-05"},
			{"gap_month_day", "2018-02-11 02:30-05", "1 month 1 day", "+", "2018-03-12 03:30:00-04"},
			{"fold_month_day", "2018-10-04 01:30-04", "1 month 1 day", "+", "2018-11-05 01:30:00-05"},
			{"subtract_gap_month", "2018-04-11 02:30-04", "1 month", "-", "2018-03-11 03:30:00-04"},
			{"subtract_fold_month", "2018-12-04 01:30-05", "1 month", "-", "2018-11-04 01:30:00-05"},
			{"subtract_gap_month_day", "2018-04-11 02:30-04", "1 month 1 day", "-", "2018-03-10 03:30:00-05"},
			{"subtract_fold_month_day", "2018-12-04 01:30-05", "1 month 1 day", "-", "2018-11-03 01:30:00-04"},
			{"subtract_gap_month_then_forward_day", "2018-04-11 02:30-04", "1 month -1 day", "-", "2018-03-12 03:30:00-04"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				a := "TIMESTAMPTZ '" + tc.input + "'"
				interval := "INTERVAL '" + tc.interval + "'"
				if tc.op == "+" {
					for _, expression := range []string{a + "+" + interval, interval + "+" + a} {
						datetimeArithmeticValue(t, p, b, c, expression, "TIMESTAMPTZ", tc.want)
					}
				} else {
					for _, expression := range []string{a + "-" + interval, a + "+(-" + interval + ")", "(-" + interval + ")+" + a} {
						datetimeArithmeticValue(t, p, b, c, expression, "TIMESTAMPTZ", tc.want)
					}
				}
			})
		}
	})
}
