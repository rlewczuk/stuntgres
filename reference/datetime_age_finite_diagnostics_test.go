package reference_test

import (
	"database/sql"
	"testing"
)

const datetimeAgeFiniteMissingHint = "No function matches the given name and argument types. You might need to add explicit type casts."
const datetimeAgeFiniteAmbiguousHint = "Could not choose a best candidate function. You might need to add explicit type casts."

func TestReferenceDatetimeAgeFiniteResolutionDiagnostics(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			q, signature string
			position     int
		}{
			{"SELECT age()", "age()", 8}, {"SELECT age(1)", "age(integer)", 8},
			{"SELECT age(TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01')", "age(timestamp without time zone, timestamp without time zone, timestamp without time zone)", 8},
			{"SELECT isfinite()", "isfinite()", 8}, {"SELECT isfinite(1)", "isfinite(integer)", 8},
			{"SELECT isfinite('infinity'::text)", "isfinite(text)", 8},
			{"SELECT isfinite(DATE '2000-01-01',DATE '2000-01-01')", "isfinite(date, date)", 8},
			{"SELECT isfinite(TIMETZ '12:00+00')", "isfinite(time with time zone)", 8},
			{"/*é*/ SELECT age(1)", "age(integer)", 14},
			{"SELECT pg_catalog.age(1)", "pg_catalog.age(integer)", 8},
			{"SELECT \"AGE\"(TIMESTAMP '2000-01-01')", "AGE(timestamp without time zone)", 8},
		} {
			t.Run(tc.q, func(t *testing.T) {
				datetimeAgeFiniteFailure(t, p, b, c, tc.q, "42883", "function "+tc.signature+" does not exist", datetimeAgeFiniteMissingHint, tc.position)
			})
		}
		for _, name := range []string{"age", "isfinite"} {
			for _, argument := range []string{"NULL", "'infinity'", "$1"} {
				q := "SELECT " + name + "(" + argument + ")"
				t.Run(q, func(t *testing.T) {
					var args []any
					if argument == "$1" {
						args = []any{nil}
					}
					datetimeAgeFiniteFailure(t, p, b, c, q, "42725", "function "+name+"(unknown) is not unique", datetimeAgeFiniteAmbiguousHint, 8, args...)
				})
			}
		}
		for _, expression := range []string{
			"age(TIMESTAMP '2004-06-01','2004-04-30')", "age('2004-06-01',TIMESTAMP '2004-04-30')",
			"age(TIMESTAMP '2004-06-01',DATE '2004-04-30')", "age(DATE '2004-06-01',TIMESTAMP '2004-04-30')",
			"pg_catalog.age(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30')", "\"age\"(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30')",
		} {
			t.Run(expression, func(t *testing.T) { datetimeAgeFiniteValue(t, p, b, c, expression, "INTERVAL", "1 mon 1 day") })
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN af_timestamp AS timestamp", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN af_interval AS interval", 0))
		datetimeAgeFiniteValue(t, p, b, c, "age('2004-06-01'::af_timestamp,TIMESTAMP '2004-04-30')", "INTERVAL", "1 mon 1 day")
		datetimeAgeFiniteValue(t, p, b, c, "isfinite('0'::af_interval)", "BOOL", true)
		for _, tc := range []struct {
			expression, boundary string
			want                 any
			typ                  string
		}{
			{"age(NULL,NULL)", "age with time zone is unsupported", nil, "INTERVAL"},
			{"age('2004-06-01','2004-04-30')", "age with time zone is unsupported", "1 mon 1 day", "INTERVAL"},
			{"age(DATE '2004-06-01',DATE '2004-04-30')", "age with time zone is unsupported", "1 mon 1 day", "INTERVAL"},
			{"age(DATE '2004-06-01','2004-04-30')", "age with time zone is unsupported", "1 mon 1 day", "INTERVAL"},
			{"age(TIMESTAMPTZ '2004-06-01+00',TIMESTAMP '2004-04-30')", "age with time zone is unsupported", "1 mon 1 day", "INTERVAL"},
			{"age(TIMESTAMP '2004-06-01',TIMESTAMPTZ '2004-04-30+00')", "age with time zone is unsupported", "1 mon 1 day", "INTERVAL"},
			{"age(CURRENT_DATE)", "age with time zone is unsupported", "00:00:00", "INTERVAL"},
			{"age(CURRENT_DATE::timestamptz)", "age with time zone is unsupported", "00:00:00", "INTERVAL"},
			{"isfinite(TIMESTAMPTZ 'infinity')", "isfinite with time zone is unsupported", false, "BOOL"},
			{"isfinite(TIME '12:00')", "time to interval conversion is unsupported", true, "BOOL"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				if b.pg {
					datetimeAgeFiniteValue(t, p, b, c, tc.expression, tc.typ, tc.want)
				} else {
					datetimeAgeFiniteFailure(t, p, b, c, "SELECT "+tc.expression, "0A000", tc.boundary, "", 8)
				}
			})
		}
		if b.pg {
			// xid age is a transaction counter, not the selected symbolic interval function.
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT pg_typeof(age('0'::xid))::text AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"integer"}}))
		} else {
			datetimeAgeFiniteFailure(t, p, b, c, "SELECT age('0'::xid)", "0A000", "unsupported column type", "", 17)
		}
	})
}
