package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeAgeFiniteAgeEdges(t *testing.T) {
	cases := []struct{ name, left, right, want, reverse string }{
		{"manual", "2001-04-10", "1957-06-13", "43 years 9 mons 27 days", "-43 years -9 mons -27 days"},
		{"earlier_month", "2004-06-01", "2004-04-30", "1 mon 1 day", "-1 mons -1 days"},
		{"nonleap", "2023-02-28", "2023-01-31", "28 days", "-28 days"},
		{"leap", "2024-02-29", "2024-01-31", "29 days", "-29 days"},
		{"march", "2024-03-31", "2024-02-29", "1 mon 2 days", "-1 mons -2 days"},
		{"time_borrow", "2004-06-01 01:00", "2004-04-30 02:00", "1 mon 23:00:00", "-1 mons -23:00:00"},
		{"fraction", "2000-03-01 00:00:00.000001", "2000-02-29 23:59:59.999999", "00:00:00.000002", "-00:00:00.000002"},
		{"negative_epoch", "1999-12-31 00:00:00.000001", "1999-12-30 23:59:59.999999", "00:00:00.000002", "-00:00:00.000002"},
		{"equal", "2000-01-01", "2000-01-01", "00:00:00", "00:00:00"},
		{"era_year", "0001-01-01 AD", "0001-01-01 BC", "1 year", "-1 years"},
		{"era_day", "0001-01-01 AD", "0001-12-31 BC", "1 day", "-1 days"},
		{"bc_leap", "0001-03-01 BC", "0001-02-28 BC", "2 days", "-2 days"},
		{"full_domain", "294276-12-31 23:59:59.999999", "4714-11-24 BC", "298989 years 1 mon 7 days 23:59:59.999999", "-298989 years -1 mons -7 days -23:59:59.999999"},
	}
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				datetimeAgeFiniteValue(t, p, b, c, "age(TIMESTAMP '"+tc.left+"',TIMESTAMP '"+tc.right+"')", "INTERVAL", tc.want)
				datetimeAgeFiniteValue(t, p, b, c, "age(TIMESTAMP '"+tc.right+"',TIMESTAMP '"+tc.left+"')", "INTERVAL", tc.reverse)
			})
		}
	})
}

func TestReferenceDatetimeAgeFiniteFiniteMatrix(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, typ := range []string{"date", "timestamp", "interval"} {
			t.Run(typ, func(t *testing.T) {
				for _, v := range []string{"infinity", "-infinity"} {
					datetimeAgeFiniteValue(t, p, b, c, "isfinite('"+v+"'::"+typ+")", "BOOL", false)
				}
				datetimeAgeFiniteValue(t, p, b, c, "isfinite(NULL::"+typ+")", "BOOL", nil)
			})
		}
		for _, literal := range []string{
			"DATE '2000-01-01'", "DATE '4714-11-24 BC'", "DATE '5874897-12-31'",
			"TIMESTAMP '2000-01-01'", "TIMESTAMP '4714-11-24 BC'", "TIMESTAMP '294276-12-31 23:59:59.999999'",
			"INTERVAL '0'", "INTERVAL '1 mon -2 days 03:00'", "INTERVAL '2147483647 months'", "INTERVAL '-2147483648 days'", "INTERVAL '9223372036854775806 microseconds'",
		} {
			t.Run(literal, func(t *testing.T) { datetimeAgeFiniteValue(t, p, b, c, "isfinite("+literal+")", "BOOL", true) })
		}
	})
}

func TestReferenceDatetimeAgeFiniteInfinityNullDiagnostics(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			expression string
			want       any
		}{
			{"age(TIMESTAMP 'infinity')", "-infinity"}, {"age(TIMESTAMP '-infinity')", "infinity"},
			{"age(TIMESTAMP 'infinity',TIMESTAMP '2000-01-01')", "infinity"},
			{"age(TIMESTAMP '-infinity',TIMESTAMP '2000-01-01')", "-infinity"},
			{"age(TIMESTAMP '2000-01-01',TIMESTAMP 'infinity')", "-infinity"},
			{"age(TIMESTAMP '2000-01-01',TIMESTAMP '-infinity')", "infinity"},
			{"age(TIMESTAMP 'infinity',TIMESTAMP '-infinity')", "infinity"},
			{"age(TIMESTAMP '-infinity',TIMESTAMP 'infinity')", "-infinity"},
			{"age(NULL::timestamp)", nil}, {"age(NULL::timestamp,TIMESTAMP 'infinity')", nil},
			{"age(TIMESTAMP '-infinity',NULL::timestamp)", nil},
		} {
			t.Run(tc.expression, func(t *testing.T) { datetimeAgeFiniteValue(t, p, b, c, tc.expression, "INTERVAL", tc.want) })
		}
		for _, sign := range []string{"infinity", "-infinity"} {
			t.Run(sign, func(t *testing.T) {
				datetimeAgeFiniteFailure(t, p, b, c, "SELECT age(TIMESTAMP '"+sign+"',TIMESTAMP '"+sign+"')", "22008", "interval out of range", "", 0)
				datetimeAgeFiniteValue(t, p, b, c, "age(TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01')", "INTERVAL", "00:00:00")
			})
		}
	})
}

func TestReferenceDatetimeAgeFiniteFeatureSmoke(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		datetimeAgeFiniteBoth(t, p, b, c, referenceSelect("SELECT age(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30') AS symbolic, age(TIMESTAMP '0001-01-01 AD',TIMESTAMP '0001-01-01 BC') AS era", []string{"symbolic", "era"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"1 mon 1 day", "1 year"}}))
		datetimeAgeFiniteBoth(t, p, b, c, referenceSelect("SELECT isfinite(DATE '2000-01-01') AS d,isfinite(TIMESTAMP 'infinity') AS t,isfinite(INTERVAL '-infinity') AS i,isfinite(NULL::interval) AS n", []string{"d", "t", "i", "n"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false, nil}}))
		datetimeAgeFiniteBoth(t, p, b, c, referenceSelect("SELECT age(CURRENT_DATE::timestamp) AS midnight,age(TIMESTAMP 'infinity') AS inf,age(NULL::timestamp) AS n", []string{"midnight", "inf", "n"}, []string{"INTERVAL", "INTERVAL", "INTERVAL"}, [][]any{{"00:00:00", "-infinity", nil}}))
		datetimeAgeFiniteHeldParameters(t, p, b, c)
		for _, tc := range []struct {
			q, code, message, hint string
			pos                    int
		}{
			{"SELECT age(1)", "42883", "function age(integer) does not exist", datetimeAgeFiniteMissingHint, 8},
			{"SELECT isfinite(NULL)", "42725", "function isfinite(unknown) is not unique", datetimeAgeFiniteAmbiguousHint, 8},
			{"SELECT age(TIMESTAMP 'infinity',TIMESTAMP 'infinity')", "22008", "interval out of range", "", 0},
		} {
			t.Run(tc.q, func(t *testing.T) {
				datetimeAgeFiniteFailure(t, p, b, c, tc.q, tc.code, tc.message, tc.hint, tc.pos)
				datetimeAgeFiniteValue(t, p, b, c, "isfinite(INTERVAL '0')", "BOOL", true)
			})
		}
		if b.pg {
			datetimeAgeFiniteValue(t, p, b, c, "age(CURRENT_DATE::timestamptz)", "INTERVAL", "00:00:00")
		} else {
			datetimeAgeFiniteFailure(t, p, b, c, "SELECT age(TIMESTAMPTZ '2000-01-01+00')", "0A000", "age with time zone is unsupported", "", 8)
		}
	})
}
