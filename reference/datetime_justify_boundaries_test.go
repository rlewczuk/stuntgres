package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDatetimeJustifyBoundaries(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ f, input, want string }{
			{"justify_days", "29 days", "29 days"}, {"justify_days", "30 days", "1 mon"}, {"justify_days", "31 days", "1 mon 1 day"},
			{"justify_days", "-29 days", "-29 days"}, {"justify_days", "-30 days", "-1 mons"}, {"justify_days", "-31 days", "-1 mons -1 days"},
			{"justify_hours", "23:59:59.999999", "23:59:59.999999"}, {"justify_hours", "24 hours", "1 day"}, {"justify_hours", "24 hours 1 microsecond", "1 day 00:00:00.000001"},
			{"justify_hours", "-23:59:59.999999", "-23:59:59.999999"}, {"justify_hours", "-24 hours", "-1 days"}, {"justify_hours", "-24 hours -1 microsecond", "-1 days -00:00:00.000001"},
			{"justify_days", "1 year 65 days", "1 year 2 mons 5 days"}, {"justify_hours", "50 hours 10 minutes", "2 days 02:10:00"}, {"justify_interval", "1 mon -1 hour", "29 days 23:00:00"},
			{"justify_hours", "30 days", "30 days"}, {"justify_days", "24 hours", "24:00:00"}, {"justify_days", "1 mon -1 hour", "1 mon -01:00:00"},
			{"justify_hours", "6 months 3 days 52 hours 3 minutes 2 seconds", "6 mons 5 days 04:03:02"}, {"justify_days", "6 months 36 days 5 hours 4 minutes 3 seconds", "7 mons 6 days 05:04:03"},
			{"justify_days", "1 mon -1 day 50 hours", "29 days 50:00:00"}, {"justify_days", "-1 mon 1 day -50 hours", "-29 days -50:00:00"},
			{"justify_hours", "6 mons 1 day -1 hour", "6 mons 23:00:00"}, {"justify_hours", "-6 mons -1 day 1 hour", "-6 mons -23:00:00"},
			{"justify_interval", "-1 mon 1 hour", "-29 days -23:00:00"}, {"justify_interval", "1 mon -1 day -1 hour", "28 days 23:00:00"},
			{"justify_interval", "-1 mon 1 day 1 hour", "-28 days -23:00:00"}, {"justify_interval", "1 mon -30 days 1 microsecond", "00:00:00.000001"},
			{"justify_interval", "1 mon 1 day -25 hours", "29 days 23:00:00"}, {"justify_interval", "1 mon -30 days", "00:00:00"},
		} {
			t.Run(tc.f+"/"+tc.input, func(t *testing.T) {
				for _, cast := range []string{"", "INTERVAL "} {
					datetimeJustifyValue(t, p, b, c, tc.f+"("+cast+"'"+tc.input+"')", tc.want)
				}
			})
		}
		for _, f := range []string{"justify_days", "justify_hours", "justify_interval"} {
			for _, tc := range []struct{ input, want string }{{"0", "00:00:00"}, {"1 microsecond", "00:00:00.000001"}, {"-1 microsecond", "-00:00:00.000001"}, {"infinity", "infinity"}, {"-infinity", "-infinity"}} {
				datetimeJustifyValue(t, p, b, c, f+"(INTERVAL '"+tc.input+"')", tc.want)
			}
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE j_boundary_"+f+"(id integer,span interval)", 0))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO j_boundary_"+f+" VALUES(1,INTERVAL '30 days')", 1))
			want := "1 mon"
			if f == "justify_hours" {
				want = "30 days"
			}
			datetimeJustifyBoth(t, p, b, c, referenceSelect("SELECT "+f+"(span) AS v FROM j_boundary_"+f+" ORDER BY id", []string{"v"}, []string{"INTERVAL"}, [][]any{{want}}))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT span FROM j_boundary_"+f+" ORDER BY id", []string{"span"}, []string{"INTERVAL"}, [][]any{{"30 days"}}))
		}
	})
}

func TestReferenceDatetimeJustifyExtrema(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ input, want string }{
			{"2147483647 days 24 hours", "5965232 years 4 mons 8 days"}, {"-2147483648 days -24 hours", "-5965232 years -4 mons -9 days"},
			{"2147483647 months 30 days -24 hours", "178956970 years 7 mons 29 days"}, {"-2147483648 months -30 days 24 hours", "-178956970 years -8 mons -29 days"},
			{"9223372036854775807 microseconds", "296533 years 3 mons 21 days 04:00:54.775807"}, {"-9223372036854775808 microseconds", "-296533 years -3 mons -21 days -04:00:54.775808"},
		} {
			datetimeJustifyValue(t, p, b, c, "justify_interval(INTERVAL '"+tc.input+"')", tc.want)
		}
		for _, tc := range []struct{ input, want string }{
			{"9223372036854775807 microseconds", "106751991 days 04:00:54.775807"}, {"-9223372036854775808 microseconds", "-106751991 days -04:00:54.775808"},
		} {
			datetimeJustifyValue(t, p, b, c, "justify_hours(INTERVAL '"+tc.input+"')", tc.want)
		}
		for _, tc := range []struct{ f, input string }{
			{"justify_hours", "2147483647 days 24 hours"}, {"justify_hours", "-2147483648 days -24 hours"},
			{"justify_days", "2147483647 months 30 days"}, {"justify_days", "-2147483648 months -30 days"},
			{"justify_interval", "2147483647 months 30 days"}, {"justify_interval", "-2147483648 months -30 days"},
			{"justify_interval", "2147483647 months -30 days 1440 hours"}, {"justify_interval", "-2147483648 months 30 days -1440 hours"},
		} {
			t.Run(fmt.Sprintf("%s/%s", tc.f, tc.input), func(t *testing.T) {
				datetimeJustifyFailure(t, p, b, c, "SELECT "+tc.f+"(INTERVAL '"+tc.input+"')", "22008", "interval out of range", "", 0)
			})
		}
	})
}
