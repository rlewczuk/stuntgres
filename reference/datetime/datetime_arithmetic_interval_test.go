package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeArithmeticIntervalComponentsAndScalars(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{"INTERVAL '24 hours'+INTERVAL '1 hour'", "25:00:00"},
			{"INTERVAL '1 month'+INTERVAL '30 days'", "1 mon 30 days"},
			{"INTERVAL '1 day'-INTERVAL '1 hour'", "1 day -01:00:00"},
			{"INTERVAL '1 month'*0.5", "15 days"},
			{"INTERVAL '1 month'/4", "7 days 12:00:00"},
			{"INTERVAL '-1 month'/4", "-7 days -12:00:00"},
			{"INTERVAL '1 day'*1.5", "1 day 12:00:00"},
			{"INTERVAL '-1 day'*1.5", "-1 days -12:00:00"},
			{"INTERVAL '1 microsecond'*0.5", "00:00:00"},
			{"INTERVAL '3 microseconds'*0.5", "00:00:00.000002"},
			{"INTERVAL '-3 microseconds'*0.5", "-00:00:00.000002"},
			{"INTERVAL '24 hours'*2", "48:00:00"},
			{"INTERVAL '1 month'*0.0000001", "00:00:00.2592"},
			{"INTERVAL '1 day'/7", "03:25:42.857143"},
			{"INTERVAL '1 month'/7", "4 days 06:51:25.6896"},
		} {
			t.Run(tc.expr, func(t *testing.T) { datetimeArithmeticValue(t, p, b, c, tc.expr, "INTERVAL", tc.want) })
		}
		for _, tc := range []struct{ expr, want string }{
			{"INTERVAL '1 month -31 days'*'Infinity'::DOUBLE PRECISION", "-infinity"},
			{"INTERVAL '-1 month 31 days'*'Infinity'::DOUBLE PRECISION", "infinity"},
			{"INTERVAL '1 hour'/'Infinity'::DOUBLE PRECISION", "00:00:00"},
			{"INTERVAL '1 hour'/'-Infinity'::DOUBLE PRECISION", "00:00:00"},
			{"INTERVAL 'infinity'*-2", "-infinity"},
			{"INTERVAL '-infinity'/-2", "infinity"},
		} {
			t.Run(tc.expr, func(t *testing.T) { datetimeArithmeticText(t, p, b, c, tc.expr, tc.want) })
		}
		for _, tc := range []struct {
			query, code, message string
			args                 []any
		}{
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22012", "division by zero", []any{"1 hour", 0}},
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22012", "division by zero", []any{"infinity", "-0"}},
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22012", "division by zero", []any{"1 hour", "-0"}},
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22012", "division by zero", []any{"-infinity", 0}},
			{"SELECT $1::INTERVAL*$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"1 hour", "NaN"}},
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"1 hour", "NaN"}},
			{"SELECT $1::INTERVAL*$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"infinity", 0}},
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"infinity", "Infinity"}},
			{"SELECT $1::INTERVAL*$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"1 month -30 days", "Infinity"}},
			{"SELECT $1::INTERVAL*$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"0", "Infinity"}},
			{"SELECT $1::INTERVAL*$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"infinity", "NaN"}},
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"-infinity", "-Infinity"}},
			{"SELECT $1::INTERVAL+$2::INTERVAL", "22008", "interval out of range", []any{"2147483647 months", "1 month"}},
			{"SELECT $1::INTERVAL+$2::INTERVAL", "22008", "interval out of range", []any{"2147483647 days", "1 day"}},
			{"SELECT $1::INTERVAL+$2::INTERVAL", "22008", "interval out of range", []any{"9223372036854775807 microseconds", "1 microsecond"}},
			{"SELECT $1::INTERVAL-$2::INTERVAL", "22008", "interval out of range", []any{"-2147483648 months", "1 month"}},
			{"SELECT $1::INTERVAL-$2::INTERVAL", "22008", "interval out of range", []any{"-2147483648 days", "1 day"}},
			{"SELECT $1::INTERVAL-$2::INTERVAL", "22008", "interval out of range", []any{"-9223372036854775808 microseconds", "1 microsecond"}},
			{"SELECT -$1::INTERVAL", "22008", "interval out of range", []any{"-2147483648 months"}},
			{"SELECT -$1::INTERVAL", "22008", "interval out of range", []any{"-2147483648 days"}},
			{"SELECT -$1::INTERVAL", "22008", "interval out of range", []any{"-9223372036854775808 microseconds"}},
			{"SELECT $1::INTERVAL+$2::INTERVAL", "22008", "interval out of range", []any{"2147483647 months 2147483647 days", "9223372036854775807 microseconds"}},
			{"SELECT $1::INTERVAL+$2::INTERVAL", "22008", "interval out of range", []any{"-2147483648 months -2147483648 days", "-9223372036854775808 microseconds"}},
			{"SELECT $1::INTERVAL*$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"2147483647 months", 2}},
			{"SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22008", "interval out of range", []any{"1 hour", 1e-300}},
		} {
			t.Run(tc.query+"_"+tc.message+"_"+fmtDatetimeArgs(tc.args), func(t *testing.T) {
				datetimeArithmeticRuntimeError(t, p, b, c, tc.query, tc.code, tc.message, tc.args...)
			})
		}
	})
}

func TestReferenceDatetimeArithmeticInfinity(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{"DATE 'infinity'+1", "infinity"}, {"DATE '-infinity'-1", "-infinity"},
			{"DATE 'infinity'+TIME '12:00'", "infinity"}, {"DATE '-infinity'+TIMETZ '12:00+02'", "-infinity"},
			{"TIMESTAMP 'infinity'+INTERVAL '1 month'", "infinity"}, {"TIMESTAMPTZ '-infinity'-INTERVAL '1 day'", "-infinity"},
			{"TIMESTAMP '2024-01-01'+INTERVAL 'infinity'", "infinity"}, {"TIMESTAMPTZ '2024-01-01+00'+INTERVAL '-infinity'", "-infinity"},
			{"TIMESTAMP 'infinity'+INTERVAL 'infinity'", "infinity"}, {"TIMESTAMPTZ '-infinity'+INTERVAL '-infinity'", "-infinity"},
			{"TIMESTAMP 'infinity'-TIMESTAMP '2024-01-01'", "infinity"}, {"TIMESTAMPTZ '2024-01-01+00'-TIMESTAMPTZ 'infinity'", "-infinity"},
			{"TIMESTAMP 'infinity'-TIMESTAMP '-infinity'", "infinity"}, {"TIMESTAMPTZ '-infinity'-TIMESTAMPTZ 'infinity'", "-infinity"},
			{"INTERVAL 'infinity'+INTERVAL '1 day'", "infinity"}, {"INTERVAL 'infinity'-INTERVAL '-infinity'", "infinity"}, {"-INTERVAL 'infinity'", "-infinity"},
		} {
			t.Run(tc.expr, func(t *testing.T) { datetimeArithmeticText(t, p, b, c, tc.expr, tc.want) })
		}
		for _, tc := range []struct {
			q, message string
			args       []any
		}{
			{"SELECT $1::DATE-$2::DATE", "cannot subtract infinite dates", []any{"infinity", "2024-01-01"}},
			{"SELECT $1::DATE-$2::DATE", "cannot subtract infinite dates", []any{"2024-01-01", "-infinity"}},
			{"SELECT $1::TIMESTAMP+$2::INTERVAL", "timestamp out of range", []any{"infinity", "-infinity"}},
			{"SELECT $1::TIMESTAMPTZ-$2::INTERVAL", "timestamp out of range", []any{"infinity", "infinity"}},
			{"SELECT $1::TIMESTAMP-$2::TIMESTAMP", "interval out of range", []any{"infinity", "infinity"}},
			{"SELECT $1::TIMESTAMPTZ-$2::TIMESTAMPTZ", "interval out of range", []any{"-infinity", "-infinity"}},
			{"SELECT $1::INTERVAL+$2::INTERVAL", "interval out of range", []any{"infinity", "-infinity"}},
			{"SELECT $1::INTERVAL-$2::INTERVAL", "interval out of range", []any{"infinity", "infinity"}},
			{"SELECT $1::TIMESTAMP-$2::INTERVAL", "interval out of range", []any{"infinity", "-2147483648 months"}},
			{"SELECT $1::TIME+$2::INTERVAL", "cannot add infinite interval to time", []any{"12:00", "infinity"}},
			{"SELECT $1::TIME-$2::INTERVAL", "cannot subtract infinite interval from time", []any{"12:00", "-infinity"}},
			{"SELECT $1::TIMETZ+$2::INTERVAL", "cannot add infinite interval to time", []any{"12:00+02", "infinity"}},
			{"SELECT $1::TIMETZ-$2::INTERVAL", "cannot subtract infinite interval from time", []any{"12:00+02", "-infinity"}},
		} {
			t.Run(tc.q+fmtDatetimeArgs(tc.args), func(t *testing.T) { datetimeArithmeticRuntimeError(t, p, b, c, tc.q, "22008", tc.message, tc.args...) })
		}
	})
}
